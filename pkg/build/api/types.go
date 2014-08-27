package api

import (
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
)

// Build encapsulates the inputs needed to produce a new deployable image, as well as
// the status of the operation and a reference to the Pod which runs the build.
type Build struct {
	api.JSONBase `json:",inline" yaml:",inline"`
	Config       BuildConfig       `json:"config,omitempty" yaml:"config,omitempty"`
	Status       BuildStatus       `json:"status,omitempty" yaml:"status,omitempty"`
	PodID        string            `json:"podID,omitempty" yaml:"podID,omitempty"`
	Labels       map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`
}

// BuildConfig contains the inputs needed to produce a new deployable image
type BuildConfig struct {
	api.JSONBase `json:",inline" yaml:",inline"`
	Type         BuildType         `json:"type,omitempty" yaml:"type,omitempty"`
	SourceURI    string            `json:"sourceUri,omitempty" yaml:"sourceUri,omitempty"`
	ImageTag     string            `json:"imageTag,omitempty" yaml:"imageTag,omitempty"`
	BuilderImage string            `json:"builderImage,omitempty" yaml:"builderImage,omitempty"`
	Labels       map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`
}

// BuildType is a type of build (docker, sti, etc)
type BuildType string

const (
	DockerBuildType BuildType = "docker"
	STIBuildType    BuildType = "sti"
)

// BuildStatus represents the status of a Build at a point in time.
type BuildStatus string

const (
	BuildNew      BuildStatus = "new"
	BuildPending  BuildStatus = "pending"
	BuildRunning  BuildStatus = "running"
	BuildComplete BuildStatus = "complete"
	BuildFailed   BuildStatus = "failed"
	BuildError    BuildStatus = "error"
)

// BuildList is a collection of Builds.
type BuildList struct {
	api.JSONBase `json:",inline" yaml:",inline"`
	Items        []Build `json:"items,omitempty" yaml:"items,omitempty"`
}

// BuildConfigList is a collection of BuildConfigs.
type BuildConfigList struct {
	api.JSONBase `json:",inline" yaml:",inline"`
	Items        []BuildConfig `json:"items,omitempty" yaml:"items,omitempty"`
}
