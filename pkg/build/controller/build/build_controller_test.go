package build

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	buildapi "github.com/openshift/origin/pkg/build/api"
)

func TestIsValidTransition(t *testing.T) {
	phases := []buildapi.BuildPhase{
		buildapi.BuildPhaseNew,
		buildapi.BuildPhasePending,
		buildapi.BuildPhaseRunning,
		buildapi.BuildPhaseComplete,
		buildapi.BuildPhaseFailed,
		buildapi.BuildPhaseError,
		buildapi.BuildPhaseCancelled,
	}
	for _, fromPhase := range phases {
		for _, toPhase := range phases {
			if isTerminal(fromPhase) && fromPhase != toPhase {
				if isValidTransition(fromPhase, toPhase) {
					t.Errorf("transition %v -> %v should be invalid", fromPhase, toPhase)
				}
				continue
			}
			if fromPhase == buildapi.BuildPhasePending && toPhase == buildapi.BuildPhaseNew {
				if isValidTransition(fromPhase, toPhase) {
					t.Errorf("transition %v -> %v should be invalid", fromPhase, toPhase)
				}
				continue
			}
			if fromPhase == buildapi.BuildPhaseRunning && (toPhase == buildapi.BuildPhaseNew || toPhase == buildapi.BuildPhasePending) {
				if isValidTransition(fromPhase, toPhase) {
					t.Errorf("transition %v -> %v shluld be invalid", fromPhase, toPhase)
				}
				continue
			}

			if !isValidTransition(fromPhase, toPhase) {
				t.Errorf("transition %v -> %v should be valid", fromPhase, toPhase)
			}
		}
	}
}

func TestIsTerminal(t *testing.T) {
	tests := map[buildapi.BuildPhase]bool{
		buildapi.BuildPhaseNew:       false,
		buildapi.BuildPhasePending:   false,
		buildapi.BuildPhaseRunning:   false,
		buildapi.BuildPhaseComplete:  true,
		buildapi.BuildPhaseFailed:    true,
		buildapi.BuildPhaseError:     true,
		buildapi.BuildPhaseCancelled: true,
	}
	for phase, expected := range tests {
		if actual := isTerminal(phase); actual != expected {
			t.Errorf("unexpected response for %s: %v", phase, actual)
		}
	}
}

func TestSetBuildCompletionTimestampAndDuration(t *testing.T) {
	// set start time to 2 seconds ago to have some significant duration
	startTime := metav1.NewTime(time.Now().Add(time.Second * -2))
	earlierTime := metav1.NewTime(startTime.Add(time.Hour * -1))

	// Marker times used for validation
	afterStartTimeBeforeNow := metav1.NewTime(time.Time{})

	// Marker durations used for validation
	greaterThanZeroLessThanSinceStartTime := time.Duration(0)
	atLeastOneHour := time.Duration(0)
	zeroDuration := time.Duration(0)

	buildWithStartTime := &buildapi.Build{}
	buildWithStartTime.Status.StartTimestamp = &startTime
	buildWithNoStartTime := &buildapi.Build{}
	tests := []struct {
		name         string
		build        *buildapi.Build
		podStartTime *metav1.Time
		expected     *buildUpdate
	}{
		{
			name:         "build with start time",
			build:        buildWithStartTime,
			podStartTime: &earlierTime,
			expected: &buildUpdate{
				completionTime: &afterStartTimeBeforeNow,
				duration:       &greaterThanZeroLessThanSinceStartTime,
			},
		},
		{
			name:         "build with no start time",
			build:        buildWithNoStartTime,
			podStartTime: &earlierTime,
			expected: &buildUpdate{
				startTime:      &earlierTime,
				completionTime: &afterStartTimeBeforeNow,
				duration:       &atLeastOneHour,
			},
		},
		{
			name:         "build with no start time, no pod start time",
			build:        buildWithNoStartTime,
			podStartTime: nil,
			expected: &buildUpdate{
				startTime:      &afterStartTimeBeforeNow,
				completionTime: &afterStartTimeBeforeNow,
				duration:       &zeroDuration,
			},
		},
	}

	for _, test := range tests {
		update := &buildUpdate{}
		setBuildCompletionTimestampAndDuration(test.build, test.podStartTime, update)
		// Ensure that only the fields in the expected update are set
		if test.expected.podNameAnnotation == nil && (test.expected.podNameAnnotation != update.podNameAnnotation) {
			t.Errorf("%s: podNameAnnotation should not be set", test.name)
			continue
		}
		if test.expected.phase == nil && (test.expected.phase != update.phase) {
			t.Errorf("%s: phase should not be set", test.name)
			continue
		}
		if test.expected.reason == nil && (test.expected.reason != update.reason) {
			t.Errorf("%s: reason should not be set", test.name)
			continue
		}
		if test.expected.message == nil && (test.expected.message != update.message) {
			t.Errorf("%s: message should not be set", test.name)
			continue
		}
		if test.expected.startTime == nil && (test.expected.startTime != update.startTime) {
			t.Errorf("%s: startTime should not be set", test.name)
			continue
		}
		if test.expected.completionTime == nil && (test.expected.completionTime != update.completionTime) {
			t.Errorf("%s: completionTime should not be set", test.name)
			continue
		}
		if test.expected.duration == nil && (test.expected.duration != update.duration) {
			t.Errorf("%s: duration should not be set", test.name)
			continue
		}
		if test.expected.outputRef == nil && (test.expected.outputRef != update.outputRef) {
			t.Errorf("%s: outputRef should not be set", test.name)
			continue
		}
		now := metav1.NewTime(time.Now().Add(2 * time.Second))
		if test.expected.startTime != nil {
			if update.startTime == nil {
				t.Errorf("%s: expected startTime to be set", test.name)
				continue
			}
			switch test.expected.startTime {
			case &afterStartTimeBeforeNow:
				if !update.startTime.Time.After(startTime.Time) && !update.startTime.Time.Before(now.Time) {
					t.Errorf("%s: startTime (%v) not within expected range (%v - %v)", test.name, update.startTime, startTime, now)
					continue
				}
			default:
				if !update.startTime.Time.Equal(test.expected.startTime.Time) {
					t.Errorf("%s: startTime (%v) not equal expected time (%v)", test.name, update.startTime, test.expected.startTime)
					continue
				}
			}
		}
		if test.expected.completionTime != nil {
			if update.completionTime == nil {
				t.Errorf("%s: expected completionTime to be set", test.name)
				continue
			}
			switch test.expected.completionTime {
			case &afterStartTimeBeforeNow:
				if !update.completionTime.Time.After(startTime.Time) && !update.completionTime.Time.Before(now.Time) {
					t.Errorf("%s: completionTime (%v) not within expected range (%v - %v)", test.name, update.completionTime, startTime, now)
					continue
				}
			default:
				if !update.completionTime.Time.Equal(test.expected.completionTime.Time) {
					t.Errorf("%s: completionTime (%v) not equal expected time (%v)", test.name, update.completionTime, test.expected.completionTime)
					continue
				}
			}
		}
		if test.expected.duration != nil {
			if update.duration == nil {
				t.Errorf("%s: expected duration to be set", test.name)
				continue
			}
			switch test.expected.duration {
			case &greaterThanZeroLessThanSinceStartTime:
				sinceStart := now.Rfc3339Copy().Time.Sub(startTime.Rfc3339Copy().Time)
				if !(*update.duration > 0) || !(*update.duration <= sinceStart) {
					t.Errorf("%s: duration (%v) not within expected range (%v - %v)", test.name, update.duration, 0, sinceStart)
					continue
				}
			case &atLeastOneHour:
				if !(*update.duration >= time.Hour) {
					t.Errorf("%s: duration (%v) is not at least one hour", test.name, update.duration)
					continue
				}
			default:
				if *update.duration != *test.expected.duration {
					t.Errorf("%s: duration (%v) not equal expected duration (%v)", test.name, update.duration, test.expected.duration)
					continue
				}
			}
		}
	}
}
