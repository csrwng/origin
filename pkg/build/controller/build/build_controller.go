package build

import (
	"fmt"
	"time"

	"github.com/golang/glog"
	errors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	v1core "k8s.io/client-go/kubernetes/typed/core/v1"
	clientv1 "k8s.io/client-go/pkg/api/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	kapi "k8s.io/kubernetes/pkg/api"
	kexternalclientset "k8s.io/kubernetes/pkg/client/clientset_generated/clientset"
	kclientset "k8s.io/kubernetes/pkg/client/clientset_generated/internalclientset"
	kcoreclient "k8s.io/kubernetes/pkg/client/clientset_generated/internalclientset/typed/core/internalversion"
	kcoreinformers "k8s.io/kubernetes/pkg/client/informers/informers_generated/internalversion/core/internalversion"
	internalversion "k8s.io/kubernetes/pkg/client/listers/core/internalversion"
	kcontroller "k8s.io/kubernetes/pkg/controller"

	builddefaults "github.com/openshift/origin/pkg/build/admission/defaults"
	buildoverrides "github.com/openshift/origin/pkg/build/admission/overrides"
	buildapi "github.com/openshift/origin/pkg/build/api"
	"github.com/openshift/origin/pkg/build/api/validation"
	buildclient "github.com/openshift/origin/pkg/build/client"
	"github.com/openshift/origin/pkg/build/controller/common"
	"github.com/openshift/origin/pkg/build/controller/policy"
	strategy "github.com/openshift/origin/pkg/build/controller/strategy"
	buildutil "github.com/openshift/origin/pkg/build/util"
	osclient "github.com/openshift/origin/pkg/client"
	oscache "github.com/openshift/origin/pkg/client/cache"
	"github.com/openshift/origin/pkg/controller/shared"
	imageapi "github.com/openshift/origin/pkg/image/api"
)

const (
	maxRetries = 15
)

// BuildController watches builds and synchronizes them with their
// corresponding build pods
type BuildController struct {
	buildPatcher      buildclient.BuildPatcher
	buildLister       buildclient.BuildLister
	buildConfigGetter buildclient.BuildConfigGetter
	buildDeleter      buildclient.BuildDeleter
	podClient         kcoreclient.PodsGetter

	queue workqueue.RateLimitingInterface

	buildStore       *oscache.StoreToBuildLister
	secretStore      internalversion.SecretLister
	podStore         internalversion.PodLister
	imageStreamStore *oscache.StoreToImageStreamLister

	podInformer   cache.SharedIndexInformer
	buildInformer cache.SharedIndexInformer

	buildStoreSynced       func() bool
	podStoreSynced         func() bool
	secretStoreSynced      func() bool
	imageStreamStoreSynced func() bool

	runPolicies    []policy.RunPolicy
	createStrategy buildPodCreationStrategy
	buildDefaults  builddefaults.BuildDefaults
	buildOverrides buildoverrides.BuildOverrides

	recorder record.EventRecorder
}

// BuildControllerParams is the set of parameters needed to
// create a new BuildController
type BuildControllerParams struct {
	BuildInformer       shared.BuildInformer
	ImageStreamInformer shared.ImageStreamInformer
	PodInformer         kcoreinformers.PodInformer
	SecretInformer      kcoreinformers.SecretInformer
	KubeClientInternal  kclientset.Interface
	KubeClientExternal  kexternalclientset.Interface
	OpenshiftClient     osclient.Interface
	DockerBuildStrategy *strategy.DockerBuildStrategy
	SourceBuildStrategy *strategy.SourceBuildStrategy
	CustomBuildStrategy *strategy.CustomBuildStrategy
	BuildDefaults       builddefaults.BuildDefaults
	BuildOverrides      buildoverrides.BuildOverrides
}

type podDeletedKey string

// NewBuildController creates a new BuildController.
func NewBuildController(params *BuildControllerParams) *BuildController {
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartRecordingToSink(&v1core.EventSinkImpl{Interface: v1core.New(params.KubeClientExternal.Core().RESTClient()).Events("")})

	buildClient := buildclient.NewOSClientBuildClient(params.OpenshiftClient)
	buildConfigGetter := buildclient.NewOSClientBuildConfigClient(params.OpenshiftClient)
	c := &BuildController{
		buildPatcher:      buildClient,
		buildLister:       buildClient,
		buildConfigGetter: buildConfigGetter,
		buildDeleter:      buildClient,
		secretStore:       params.SecretInformer.Lister(),
		podClient:         params.KubeClientInternal.Core(),
		podInformer:       params.PodInformer.Informer(),
		podStore:          params.PodInformer.Lister(),
		buildInformer:     params.BuildInformer.Informer(),
		buildStore:        params.BuildInformer.Lister(),
		imageStreamStore:  params.ImageStreamInformer.Lister(),
		createStrategy: &typeBasedFactoryStrategy{
			dockerBuildStrategy: params.DockerBuildStrategy,
			sourceBuildStrategy: params.SourceBuildStrategy,
			customBuildStrategy: params.CustomBuildStrategy,
		},
		buildDefaults:  params.BuildDefaults,
		buildOverrides: params.BuildOverrides,

		queue:       workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter()),
		recorder:    eventBroadcaster.NewRecorder(kapi.Scheme, clientv1.EventSource{Component: "build-controller"}),
		runPolicies: policy.GetAllRunPolicies(buildClient, buildClient),
	}

	c.podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: c.podUpdated,
		DeleteFunc: c.podDeleted,
	})
	c.buildInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    c.buildAdded,
		UpdateFunc: c.buildUpdated,
	})

	c.buildStoreSynced = c.buildInformer.HasSynced
	c.podStoreSynced = c.podInformer.HasSynced
	c.secretStoreSynced = params.SecretInformer.Informer().HasSynced
	c.imageStreamStoreSynced = params.ImageStreamInformer.Informer().HasSynced

	return c
}

// Run begins watching and syncing.
func (c *BuildController) Run(workers int, stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()

	// Wait for the controller stores to sync before starting any work in this controller.
	if !cache.WaitForCacheSync(stopCh, c.buildStoreSynced, c.podStoreSynced, c.secretStoreSynced, c.imageStreamStoreSynced) {
		utilruntime.HandleError(fmt.Errorf("timed out waiting for caches to sync"))
		return
	}

	glog.Infof("Starting build controller")

	for i := 0; i < workers; i++ {
		go wait.Until(c.worker, time.Second, stopCh)
	}

	<-stopCh
	glog.Infof("Shutting down build controller")
}

func (bc *BuildController) worker() {
	for {
		if quit := bc.work(); quit {
			return
		}
	}
}

// work gets the next build from the queue and, depending on
// the type of key, either invokes handleBuild (for a regular build key),
// or handlePodDeleted (for a podDeletedKey which is queued when a pod deleted
// event is received)
func (bc *BuildController) work() bool {
	key, quit := bc.queue.Get()
	if quit {
		return true
	}

	defer bc.queue.Done(key)

	podDeleted := false
	var stringKey string

	switch v := key.(type) {
	case string:
		stringKey = v
	case podDeletedKey:
		stringKey = string(v)
		podDeleted = true
	}

	build, err := bc.getBuildByKey(stringKey)
	if err != nil {
		utilruntime.HandleError(err)
		return false
	}

	if podDeleted {
		err = bc.handlePodDeleted(build)
	} else {
		// Inform the build handler when we're about to give up on the current build
		willBeDropped := bc.queue.NumRequeues(key) >= maxRetries-2
		err = bc.handleBuild(build, willBeDropped)
	}

	bc.handleError(err, key)
	return false
}

// handleBuild retrieves the build's corresponding pod and calls the appropriate
// handle function based on the build's current state. Each handler returns a buildUpdate
// object that includes any updates that need to be made on the build.
func (bc *BuildController) handleBuild(build *buildapi.Build, willBeDropped bool) error {
	if shouldIgnore(build) {
		return nil
	}

	glog.V(4).Infof("Handling build %s", buildDesc(build))

	pod, podErr := bc.podStore.Pods(build.Namespace).Get(buildapi.GetBuildPodName(build))

	var update *buildUpdate
	var err error

	switch {
	case shouldCancel(build):
		update, err = bc.cancelBuild(build)
	case build.Status.Phase == buildapi.BuildPhaseNew:
		update, err = bc.handleNewBuild(build, willBeDropped, pod, podErr)
	case build.Status.Phase == buildapi.BuildPhasePending,
		build.Status.Phase == buildapi.BuildPhaseRunning:
		update, err = bc.handleActiveBuild(build, willBeDropped, pod, podErr)
	case buildutil.IsBuildComplete(build):
		update, err = bc.handleCompletedBuild(build, pod, podErr)
	}
	if update != nil && !update.isEmpty() {
		bc.updateBuild(build, update, pod)
	}
	if err != nil {
		return err
	}
	return nil
}

// handlePodDeleted handles the case of a build that has had its pod deleted
// builds that are in terminal state or that are marked for cancellation should
// be ignored
func (bc *BuildController) handlePodDeleted(build *buildapi.Build) error {
	// only update the build if it's new/active and has not been cancelled
	if shouldIgnore(build) || buildutil.IsBuildComplete(build) || build.Status.Cancelled {
		return nil
	}
	glog.V(4).Infof("Handling deleted pod for build %s", buildDesc(build))
	update := podDeletedUpdate()
	bc.updateBuild(build, update, nil)
	return nil
}

// shouldIgnore returns true if a build should be ignored by the controller.
// These include pipeline builds as well as builds that are in a terminal state.
// However if the build is either complete or failed and its completion timestamp
// has not been set, then it returns false so that the build's completion timestamp
// gets updated.
func shouldIgnore(build *buildapi.Build) bool {
	// If pipeline build, do nothing.
	// These builds are processed/updated/etc by the jenkins sync plugin
	if build.Spec.Strategy.JenkinsPipelineStrategy != nil {
		glog.V(4).Infof("Ignoring build %s with jenkins pipeline strategy", buildDesc(build))
		return true
	}

	// If a build is in a terminal state, ignore it; unless it is in a succeeded or failed
	// state and its completion time is not set, then we should at least attempt to set its
	// completion time if possible.
	if buildutil.IsBuildComplete(build) {
		switch build.Status.Phase {
		case buildapi.BuildPhaseComplete,
			buildapi.BuildPhaseFailed:
			if build.Status.CompletionTimestamp == nil {
				return false
			}
		}
		glog.V(4).Infof("Ignoring build %s in completed state", buildDesc(build))
		return true
	}

	return false
}

// shouldCancel returns true if a build is active and its cancellation flag is set
func shouldCancel(build *buildapi.Build) bool {
	return !buildutil.IsBuildComplete(build) && build.Status.Cancelled
}

// cancelBuild deletes a build pod and returns an update to mark the build as cancelled
func (bc *BuildController) cancelBuild(build *buildapi.Build) (*buildUpdate, error) {
	glog.V(4).Infof("Cancelling build %s", buildDesc(build))

	podName := buildapi.GetBuildPodName(build)
	err := bc.podClient.Pods(build.Namespace).Delete(podName, &metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return nil, fmt.Errorf("could not delete build pod %s/%s to cancel build %s: %v", build.Namespace, podName, buildDesc(build), err)
	}

	update := &buildUpdate{}
	update.setPhase(buildapi.BuildPhaseCancelled)
	update.setReason(buildapi.StatusReasonCancelledBuild)
	update.setMessage(buildapi.StatusMessageCancelledBuild)

	return update, nil
}

// handleNewBuild will check whether policy allows running the new build and if so, creates a pod
// for the build and returns an update to move it to the Pending phase
func (bc *BuildController) handleNewBuild(build *buildapi.Build, willBeDropped bool, pod *kapi.Pod, podErr error) (*buildUpdate, error) {
	// If a pod was found, and it was created after the build was created, it
	// means that the build is active and its status should be updated
	if podErr == nil && pod != nil {
		if !pod.CreationTimestamp.Before(build.CreationTimestamp) {
			return bc.handleActiveBuild(build, willBeDropped, pod, podErr)
		}
	}

	// If there was some other error retrieving the pod, there may be an issue with the API server, better retry
	if !errors.IsNotFound(podErr) {
		return nil, podErr
	}

	runPolicy := policy.ForBuild(build, bc.runPolicies)
	if runPolicy == nil {
		return nil, fmt.Errorf("unable to determine build policy for %s", buildDesc(build))
	}

	// The runPolicy decides whether to execute this build or not.
	if run, err := runPolicy.IsRunnable(build); err != nil || !run {
		return nil, err
	}

	return bc.createBuildPod(build)
}

// createPodSpec creates a pod spec for the given build
func (bc *BuildController) createPodSpec(originalBuild *buildapi.Build, ref string) (*kapi.Pod, error) {
	// TODO(rhcarvalho)
	// The S2I and Docker builders expect build.Spec.Output.To to contain a
	// resolved reference to a Docker image. Since build.Spec is immutable, we
	// change a copy (that is never persisted) and pass it to
	// createStrategy.createBuildPod. We should make the builders use
	// build.Status.OutputDockerImageReference, which will make copying the build
	// unnecessary.
	build, err := buildutil.BuildDeepCopy(originalBuild)
	if err != nil {
		return nil, fmt.Errorf("unable to copy build %s: %v", buildDesc(originalBuild), err)
	}

	build.Status.OutputDockerImageReference = ref
	if build.Spec.Output.To != nil && len(build.Spec.Output.To.Name) != 0 {
		build.Spec.Output.To = &kapi.ObjectReference{
			Kind: "DockerImage",
			Name: ref,
		}
	}

	// Invoke the strategy to create a build pod.
	podSpec, err := bc.createStrategy.CreateBuildPod(build)
	if err != nil {
		if strategy.IsFatal(err) {
			return nil, &strategy.FatalError{Reason: fmt.Sprintf("failed to create a build pod spec for build %s/%s: %v", build.Namespace, build.Name, err)}
		}
		return nil, fmt.Errorf("failed to create a build pod spec for build %s/%s: %v", build.Namespace, build.Name, err)
	}
	if err := bc.buildDefaults.ApplyDefaults(podSpec); err != nil {
		return nil, fmt.Errorf("failed to apply build defaults for build %s/%s: %v", build.Namespace, build.Name, err)
	}
	if err := bc.buildOverrides.ApplyOverrides(podSpec); err != nil {
		return nil, fmt.Errorf("failed to apply build overrides for build %s/%s: %v", build.Namespace, build.Name, err)
	}
	return podSpec, nil
}

// resolveOutputDockerImageReference returns a reference to a Docker image
// computed from the buid.Spec.Output.To reference.
func (bc *BuildController) resolveOutputDockerImageReference(build *buildapi.Build) (string, error) {
	outputTo := build.Spec.Output.To
	if outputTo == nil || outputTo.Name == "" {
		return "", nil
	}
	var ref string
	switch outputTo.Kind {
	case "DockerImage":
		ref = outputTo.Name
	case "ImageStream", "ImageStreamTag":
		// TODO(smarterclayton): security, ensure that the reference image stream is actually visible
		namespace := outputTo.Namespace
		if len(namespace) == 0 {
			namespace = build.Namespace
		}

		var tag string
		streamName := outputTo.Name
		if outputTo.Kind == "ImageStreamTag" {
			var ok bool
			streamName, tag, ok = imageapi.SplitImageStreamTag(streamName)
			if !ok {
				return "", fmt.Errorf("the referenced image stream tag is invalid: %s", outputTo.Name)
			}
			tag = ":" + tag
		}
		stream, err := bc.imageStreamStore.ImageStreams(namespace).Get(streamName, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				return "", fmt.Errorf("the referenced output image stream %s/%s does not exist", namespace, streamName)
			}
			return "", fmt.Errorf("the referenced output image stream %s/%s could not be found by build %s/%s: %v", namespace, streamName, build.Namespace, build.Name, err)
		}
		if len(stream.Status.DockerImageRepository) == 0 {
			e := fmt.Errorf("the image stream %s/%s cannot be used as the output for build %s/%s because the integrated Docker registry is not configured and no external registry was defined", namespace, outputTo.Name, build.Namespace, build.Name)
			bc.recorder.Eventf(build, kapi.EventTypeWarning, "invalidOutput", "Error starting build: %v", e)
			return "", e
		}
		ref = fmt.Sprintf("%s%s", stream.Status.DockerImageRepository, tag)
	}
	return ref, nil
}

// createBuildPod creates a new pod to run a build
func (bc *BuildController) createBuildPod(build *buildapi.Build) (*buildUpdate, error) {

	update := &buildUpdate{}

	// Set the output Docker image reference.
	ref, err := bc.resolveOutputDockerImageReference(build)
	if err != nil {
		update.setReason(buildapi.StatusReasonInvalidOutputReference)
		update.setMessage(buildapi.StatusMessageInvalidOutputRef)
		return update, err
	}

	// Create the build pod spec
	buildPod, err := bc.createPodSpec(build, ref)
	if err != nil {
		update.setReason(buildapi.StatusReasonCannotCreateBuildPodSpec)
		update.setMessage(buildapi.StatusMessageCannotCreateBuildPodSpec)
		return update, err
	}

	glog.V(4).Infof("Pod %s/%s for build %s is about to be created", build.Namespace, buildPod.Name, buildDesc(build))
	if _, err := bc.podClient.Pods(build.Namespace).Create(buildPod); err != nil {
		if errors.IsAlreadyExists(err) {
			bc.recorder.Eventf(build, kapi.EventTypeWarning, "FailedCreate", "Pod already exists: %s/%s", buildPod.Namespace, buildPod.Name)
			glog.V(4).Infof("Build pod %s/%s for build %s already exists", build.Namespace, buildPod.Name, buildDesc(build))

			// If the existing pod was created before this build, switch to the Error state.
			existingPod, err := bc.podClient.Pods(build.Namespace).Get(buildPod.Name, metav1.GetOptions{})
			if err == nil && existingPod.CreationTimestamp.Before(build.CreationTimestamp) {
				update.setPhase(buildapi.BuildPhaseError)
				update.setReason(buildapi.StatusReasonBuildPodExists)
				update.setMessage(buildapi.StatusMessageBuildPodExists)
				return update, nil
			}
			return nil, nil
		}
		// Log an event if the pod is not created (most likely due to quota denial).
		bc.recorder.Eventf(build, kapi.EventTypeWarning, "FailedCreate", "Error creating: %v", err)
		update.setReason(buildapi.StatusReasonCannotCreateBuildPod)
		update.setMessage(buildapi.StatusMessageCannotCreateBuildPod)
		return update, fmt.Errorf("failed to create build pod: %v", err)
	}
	glog.V(4).Infof("Created pod %s/%s for build %s", build.Namespace, buildPod.Name, buildDesc(build))
	update.setPodNameAnnotation(buildPod.Name)
	update.setPhase(buildapi.BuildPhasePending)
	update.setReason("")
	update.setMessage("")
	update.setOutputRef(ref)
	return update, nil
}

// handleActiveBuild handles a build in either pending or running state
func (bc *BuildController) handleActiveBuild(build *buildapi.Build, willBeDropped bool, pod *kapi.Pod, podErr error) (*buildUpdate, error) {
	switch {
	case errors.IsNotFound(podErr):
		// If about to stop retrying the build, fail if necessary
		if willBeDropped {
			return bc.handlePodNotFound(build)
		}
		// Otherwise, keep retrying
		return nil, podErr
	case podErr != nil:
		return nil, fmt.Errorf("could not retrieve pod for build %s: %v", buildDesc(build), podErr)
	}

	update := &buildUpdate{}
	switch pod.Status.Phase {
	case kapi.PodPending:
		if build.Status.Phase != buildapi.BuildPhasePending {
			update.setReason("")
			update.setMessage("")
			update.setPhase(buildapi.BuildPhasePending)
		}
		if secret := build.Spec.Output.PushSecret; secret != nil && build.Status.Reason != buildapi.StatusReasonMissingPushSecret {
			if _, err := bc.secretStore.Secrets(build.Namespace).Get(secret.Name); err != nil && errors.IsNotFound(err) {
				update.setReason(buildapi.StatusReasonMissingPushSecret)
				update.setMessage(buildapi.StatusMessageMissingPushSecret)
				glog.V(4).Infof("Setting reason for pending build to %q due to missing secret for %s", build.Status.Reason, buildDesc(build))
			}
		}
	case kapi.PodRunning:
		if build.Status.Phase != buildapi.BuildPhaseRunning {
			update.setReason("")
			update.setMessage("")
			update.setPhase(buildapi.BuildPhaseRunning)
			if pod.Status.StartTime != nil {
				update.setStartTime(*pod.Status.StartTime)
			}
		}
	case kapi.PodSucceeded:
		if build.Status.Phase != buildapi.BuildPhaseComplete {
			update.setReason("")
			update.setMessage("")
			update.setPhase(buildapi.BuildPhaseComplete)
		}
		if len(pod.Status.ContainerStatuses) == 0 {
			// no containers in the pod means something went terribly wrong, so the build
			// should be set to an error state
			glog.V(2).Infof("Setting build %s to error state because its pod has no containers", buildDesc(build))
			update.setPhase(buildapi.BuildPhaseError)
			if build.Status.Reason == "" {
				update.setReason(buildapi.StatusReasonBuildPodDeleted)
				update.setMessage(buildapi.StatusMessageBuildPodDeleted)
			}
		} else {
			for _, info := range pod.Status.ContainerStatuses {
				if info.State.Terminated != nil && info.State.Terminated.ExitCode != 0 {
					glog.V(2).Infof("Setting build %s to error state because a container in its pod has non-zero exit code", buildDesc(build))
					update.setPhase(buildapi.BuildPhaseError)
					break
				}
			}
		}
	case kapi.PodFailed:
		if build.Status.Phase != buildapi.BuildPhaseFailed {
			update.setPhase(buildapi.BuildPhaseFailed)
			update.setReason(buildapi.StatusReasonGenericBuildFailed)
			update.setMessage(buildapi.StatusMessageGenericBuildFailed)
		}
	}

	if _, requestedPhaseIsSet := build.Annotations[buildapi.BuildPodRequestedPhaseAnnotation]; requestedPhaseIsSet {
		requestedPhase := buildapi.BuildPhase(build.Annotations[buildapi.BuildPodRequestedPhaseAnnotation])

		// Only pay attention to the phase (and other fields) if a terminal phase was requested
		if isTerminal(requestedPhase) {
			if (build.Status.Phase != requestedPhase && update.phase == nil) ||
				(update.phase != nil && *update.phase != requestedPhase) {
				glog.V(4).Infof("overriding build update phase with requested phase %s", requestedPhase)
				update.setPhase(requestedPhase)
			}

			if _, requestedReasonIsSet := build.Annotations[buildapi.BuildPodRequestedReasonAnnotation]; requestedReasonIsSet {
				requestedReason := buildapi.StatusReason(build.Annotations[buildapi.BuildPodRequestedReasonAnnotation])
				if (build.Status.Reason != requestedReason && update.reason == nil) ||
					(update.reason != nil && *update.reason != requestedReason) {
					glog.V(4).Infof("overriding build update reason with requested reason %s", requestedReason)
					update.setReason(requestedReason)
				}
			}
			if _, requestedMessageIsSet := build.Annotations[buildapi.BuildPodRequestedMessageAnnotation]; requestedMessageIsSet {
				requestedMessage := build.Annotations[buildapi.BuildPodRequestedMessageAnnotation]
				if (build.Status.Message != requestedMessage && update.message == nil) ||
					(update.message == nil && *update.message != requestedMessage) {
					glog.V(4).Infof("overriding build update message with requested message %s", requestedMessage)
					update.setMessage(requestedMessage)
				}
			}
		}
	}

	return update, nil
}

// handleCompletedBuild will only be called on builds that are already in a terminal phase however, their completion timestamp
// has not been set.
func (bc *BuildController) handleCompletedBuild(build *buildapi.Build, pod *kapi.Pod, podErr error) (*buildUpdate, error) {
	// Make sure that the completion timestamp has not already been set
	if build.Status.CompletionTimestamp != nil {
		return nil, nil
	}

	update := &buildUpdate{}
	var podStartTime *metav1.Time
	if podErr != nil && pod != nil {
		podStartTime = pod.Status.StartTime
	}
	setBuildCompletionTimestampAndDuration(build, podStartTime, update)

	return update, nil
}

// updateBuild is the single place where any update to a build is done in the build controller.
// It will check that the update is valid, peform any necessary processing such as calling HandleBuildCompletion,
// and apply the buildUpdate object as a patch.
func (bc *BuildController) updateBuild(build *buildapi.Build, update *buildUpdate, pod *kapi.Pod) {

	stateTransition := false
	// Check whether we are transitioning to a different build phase
	if update.phase != nil && (*update.phase) != build.Status.Phase {
		stateTransition = true

		// Make sure that the transition is valid
		if !isValidTransition(build.Status.Phase, *update.phase) {
			err := fmt.Errorf("invalid phase transition %s -> %s", buildDesc(build), *update.phase)
			utilruntime.HandleError(err)
			return
		}

		// Log that we are updating build status
		reasonText := ""
		if update.reason != nil && *update.reason != "" {
			reasonText = fmt.Sprintf(" ( %s )", *update.reason)
		}

		// Update build completion timestamp if transitioning to a terminal phase
		if isTerminal(*update.phase) {
			var podStartTime *metav1.Time
			if pod != nil {
				podStartTime = pod.Status.StartTime
			}
			setBuildCompletionTimestampAndDuration(build, podStartTime, update)
		}

		glog.V(4).Infof("Updating build %s -> %s%s", buildDesc(build), *update.phase, reasonText)
	}

	// Ensure that a pod name annotation has been set on the build if a pod is available
	if update.podNameAnnotation == nil && !common.HasBuildPodNameAnnotation(build) && pod != nil {
		update.setPodNameAnnotation(pod.Name)
	}

	patchedBuild, err := bc.patchBuild(build, update)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("failed to update build %s with %v: %v", buildDesc(build), update, err))
		return
	}

	// Emit events and handle build completion if transitioned to a terminal phase
	if stateTransition {
		switch *update.phase {
		case buildapi.BuildPhaseRunning:
			bc.recorder.Eventf(patchedBuild, kapi.EventTypeNormal, buildapi.BuildStartedEventReason, fmt.Sprintf(buildapi.BuildStartedEventMessage, patchedBuild.Namespace, patchedBuild.Name))
		case buildapi.BuildPhaseCancelled:
			bc.recorder.Eventf(patchedBuild, kapi.EventTypeNormal, buildapi.BuildCancelledEventReason, fmt.Sprintf(buildapi.BuildCancelledEventMessage, patchedBuild.Namespace, patchedBuild.Name))
		case buildapi.BuildPhaseComplete:
			bc.recorder.Eventf(patchedBuild, kapi.EventTypeNormal, buildapi.BuildCompletedEventReason, fmt.Sprintf(buildapi.BuildCompletedEventMessage, patchedBuild.Namespace, patchedBuild.Name))
		case buildapi.BuildPhaseError,
			buildapi.BuildPhaseFailed:
			bc.recorder.Eventf(patchedBuild, kapi.EventTypeNormal, buildapi.BuildFailedEventReason, fmt.Sprintf(buildapi.BuildFailedEventMessage, patchedBuild.Namespace, patchedBuild.Name))
		}
		if isTerminal(*update.phase) {
			common.HandleBuildCompletion(patchedBuild, bc.buildLister, bc.buildConfigGetter, bc.buildDeleter, bc.runPolicies)
		}
	}
}

// patchBuild generates a patch for the given build and buildUpdate
// and applies that patch using the REST client
func (bc *BuildController) patchBuild(build *buildapi.Build, update *buildUpdate) (*buildapi.Build, error) {

	// Create a patch using the buildUpdate object
	updatedBuild, err := buildutil.BuildDeepCopy(build)
	if err != nil {
		return nil, fmt.Errorf("cannot create a deep copy of build %s: %v", buildDesc(build), err)
	}
	update.apply(updatedBuild)
	patch, err := validation.CreateBuildPatch(build, updatedBuild)
	if err != nil {
		return nil, fmt.Errorf("failed to create a build patch: %v", err)
	}

	glog.V(5).Infof("Patching build %s with %v", buildDesc(build), update)
	return bc.buildPatcher.Patch(build.Namespace, build.Name, patch)
}

// handlePodNotFound is called when a we're about to give up on finding a pod for a given
// build. A last attempt is made to fetch with the REST client directly. If still not found,
// it returns an update to mark the build in Error
func (bc *BuildController) handlePodNotFound(build *buildapi.Build) (*buildUpdate, error) {
	// Make one last attempt to fetch the pod using the REST client
	_, err := bc.podClient.Pods(build.Namespace).Get(buildapi.GetBuildPodName(build), metav1.GetOptions{})
	if err == nil {
		glog.V(2).Infof("Found missing pod for build %s by using direct client.", buildDesc(build))
		return nil, nil
	}
	return podDeletedUpdate(), nil
}

// getPodByKey looks up a pod by key in the podInformer cache
func (bc *BuildController) getPodByKey(key string) (*kapi.Pod, error) {
	obj, exists, err := bc.podInformer.GetIndexer().GetByKey(key)
	if err != nil {
		glog.V(2).Infof("Unable to retrieve pod %q from store: %v", key, err)
		bc.queue.AddRateLimited(key)
		return nil, err
	}
	if !exists {
		glog.V(2).Infof("Pod %q has been deleted", key)
		return nil, nil
	}

	return obj.(*kapi.Pod), nil
}

// getBuildByKey looks up a build by key in the buildInformer cache
func (bc *BuildController) getBuildByKey(key string) (*buildapi.Build, error) {
	obj, exists, err := bc.buildInformer.GetIndexer().GetByKey(key)
	if err != nil {
		glog.V(2).Infof("Unable to retrieve build %q from store: %v", key, err)
		bc.queue.AddRateLimited(key)
		return nil, err
	}
	if !exists {
		glog.V(2).Infof("Build %q has been deleted", key)
		return nil, nil
	}

	return obj.(*buildapi.Build), nil
}

// podUpdated gets called by the pod informer event handler whenever a pod
// is updated or there is a relist of pods
func (bc *BuildController) podUpdated(old, cur interface{}) {
	// A periodic relist will send update events for all known pods.
	curPod := cur.(*kapi.Pod)
	oldPod := old.(*kapi.Pod)
	// The old and new ResourceVersion will be the same in a relist of pods.
	// Here we ignore pod relists because we already listen to build relists.
	if curPod.ResourceVersion == oldPod.ResourceVersion {
		return
	}
	if isBuildPod(curPod) {
		bc.enqueueBuildForPod(curPod, false)
	}
}

// podDeleted gets called by the pod informer event handler whenever a pod
// is deleted
func (bc *BuildController) podDeleted(obj interface{}) {
	pod, ok := obj.(*kapi.Pod)
	if !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("couldn't get object from tombstone: %+v", obj))
			return
		}
		pod, ok = tombstone.Obj.(*kapi.Pod)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("tombstone contained object that is not a pod: %+v", obj))
			return
		}
	}
	if isBuildPod(pod) {
		bc.enqueueBuildForPod(pod, true)
	}
}

// buildAdded is called by the build informer event handler whenever a build
// is created
func (bc *BuildController) buildAdded(obj interface{}) {
	build := obj.(*buildapi.Build)
	bc.enqueueBuild(build, false)
}

// buildUpdated is called by the build informer event handler whenever a build
// is updated or there is a relist of builds
func (bc *BuildController) buildUpdated(old, cur interface{}) {
	build := cur.(*buildapi.Build)
	bc.enqueueBuild(build, false)
}

// enqueueBuild adds the given build to the queue. If the podDeleted flag is
// true, then the build key is queued as a podDeletedKey
func (bc *BuildController) enqueueBuild(build *buildapi.Build, podDeleted bool) {
	var key interface{}
	stringKey, err := kcontroller.KeyFunc(build)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("couldn't get key for build %#v: %v", build, err))
		return
	}
	if podDeleted {
		key = podDeletedKey(stringKey)
	} else {
		key = stringKey
	}
	bc.queue.Add(key)
}

// enqueueBuildForPod adds the build corresponding to the given pod to the controller
// queue. If a build is not found for the pod, then an error is logged.
func (bc *BuildController) enqueueBuildForPod(pod *kapi.Pod, podDeleted bool) {
	buildName := buildutil.GetBuildName(pod)
	build, err := bc.buildStore.Builds(pod.Namespace).Get(buildName)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("couldn't retrieve build for pod %s/%s: %v", pod.Namespace, pod.Name, err))
		return
	}
	bc.enqueueBuild(build, podDeleted)
}

// handleError is called by the main work loop to check the return of the handleBuild or handlePodDeleted
// calls. If an error occurred, then the key is re-added to the queue unless it has been retried too many
// times.
func (bc *BuildController) handleError(err error, key interface{}) {
	if err == nil {
		bc.queue.Forget(key)
		return
	}

	if strategy.IsFatal(err) {
		glog.V(2).Infof("Will not retry fatal error for key %v: %v", key, err)
		bc.queue.Forget(key)
		return
	}

	if bc.queue.NumRequeues(key) < maxRetries {
		glog.V(4).Infof("Retrying key %v: %v", key, err)
		bc.queue.AddRateLimited(key)
		return
	}

	glog.V(2).Infof("Giving up retrying %v: %v", key, err)
	bc.queue.Forget(key)
}

// isBuildPod returns true if the given pod is a build pod
func isBuildPod(pod *kapi.Pod) bool {
	return len(buildutil.GetBuildName(pod)) > 0
}

// buildDesc is a utility to format the namespace/name and phase of a build
// for errors and logging
func buildDesc(build *buildapi.Build) string {
	return fmt.Sprintf("%s/%s (%s)", build.Namespace, build.Name, build.Status.Phase)
}

// podDeletedUpdate returns a buildUpdate object to apply to a build that has had
// its pod deleted
func podDeletedUpdate() *buildUpdate {
	update := &buildUpdate{}
	update.setPhase(buildapi.BuildPhaseError)
	update.setReason(buildapi.StatusReasonBuildPodDeleted)
	update.setMessage(buildapi.StatusMessageBuildPodDeleted)
	return update
}

// isValidTransition returns true if the given phase transition is valid
func isValidTransition(from, to buildapi.BuildPhase) bool {
	if from == to {
		return true
	}

	switch {
	case isTerminal(from):
		return false
	case from == buildapi.BuildPhasePending:
		switch to {
		case buildapi.BuildPhaseNew:
			return false
		}
	case from == buildapi.BuildPhaseRunning:
		switch to {
		case buildapi.BuildPhaseNew,
			buildapi.BuildPhasePending:
			return false
		}
	}

	return true
}

// isTerminal returns true if the given build phase is a terminal state
func isTerminal(phase buildapi.BuildPhase) bool {
	switch phase {
	case buildapi.BuildPhaseNew,
		buildapi.BuildPhasePending,
		buildapi.BuildPhaseRunning:
		return false
	}
	return true
}

// setBuildCompletionTimestampAndDuration sets the build completion time and duration as well as the start time
// if not already set on the given buildUpdate object
func setBuildCompletionTimestampAndDuration(build *buildapi.Build, podStartTime *metav1.Time, update *buildUpdate) {
	now := metav1.Now()
	update.setCompletionTime(now)

	startTime := build.Status.StartTimestamp
	if startTime == nil {
		if podStartTime != nil {
			startTime = podStartTime
		} else {
			startTime = &now
		}
		update.setStartTime(*startTime)
	}
	update.setDuration(now.Rfc3339Copy().Time.Sub(startTime.Rfc3339Copy().Time))
}
