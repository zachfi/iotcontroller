/*
Copyright 2022.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ConditionSpec defines the desired state of Condition
type ConditionSpec struct {
	Name    string `json:"name,omitempty"`
	Enabled bool   `json:"enabled,omitempty"`

	Remediations []Remediation `json:"remediations,omitempty"`
	Matches      []Match       `json:"matches,omitempty"`
	// A cron string: * * * * *
	Schedule string `json:"schedule,omitempty"`
}

// ConditionStatus defines the observed state of Condition
type ConditionStatus struct{}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// Condition is the Schema for the conditions API
type Condition struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ConditionSpec   `json:"spec,omitempty"`
	Status ConditionStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// ConditionList contains a list of Condition
type ConditionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Condition `json:"items"`
}

// Remediation describes what to do to a zone when a condition matches: which
// zone, active/inactive state and scene, optional WhenGate (epoch window), and
// optional TimeIntervals (alert windows).
type Remediation struct {
	Zone string `json:"zone,omitempty"`

	// TODO: add support for action names.
	//
	// Instead of hardcoding the actions, if conditions leveraged an action, we
	// could have free form and allow the user to define what a 4_single or
	// quadrupal meant for what zones.
	//
	// This would allow a condition to specify what to handle.  Perhaps a single
	// condition could have multiple actions that set the scene active, etc so
	// that devices which expose different actions could perform the same zone
	// actions.  Consider conflicting conditions.
	//

	// Activate and deacvate the zones to these states/scenes.

	ActiveState   string `json:"active_state,omitempty"`
	InactiveState string `json:"inactive_state,omitempty"`

	ActiveScene   string `json:"active_scene,omitempty"`
	InactiveScene string `json:"inactive_scene,omitempty"`

	// WhenGate is relative to the Epoch event and is used to create an
	// activation window. When the window opens, the zone is activated; when
	// the window closes, the zone is deactivated.
	WhenGate When `json:"when_gate,omitempty"`

	// TimeIntervals define the windows during which this remediation is
	// applicable for Alerts.  If the conditioner receives an event outside of
	// this range, the zone will be set to the inactive state, if defined in the
	// condition spec.
	TimeIntervals []TimeIntervalSpec `json:"time_intervals,omitempty"`

	// ActiveBrightnessDelta applies a relative brightness change instead
	// of (or in addition to) an absolute ActiveState/ActiveScene. Positive
	// walks the Brightness enum up N steps (toward BRIGHTNESS_FULL);
	// negative walks down. Clamped at the enum boundaries.
	//
	// Bypasses the conditioner's applyDesired cache — each fire takes
	// effect, regardless of whether the previous fire targeted the
	// same value. This is the right semantic for "press to brighten":
	// repeated presses must each bump one step.
	//
	// When set alongside ActiveState=on or unset state, the side effect
	// of the underlying RPC also sets the zone to ON if it was OFF
	// ("press brighter on a dark room → turn on at the new level").
	ActiveBrightnessDelta int `json:"active_brightness_delta,omitempty"`

	// ActiveCompute is the name of a registered Computer whose result is
	// used to drive the zone at evaluation time. On each evaluator tick
	// (cfg.EvaluationInterval) the Conditioner invokes the named computer
	// with `(now, location, lastApplied, args)` and applies the returned
	// ApplyValues tuple via the ZoneKeeper.ApplyValues RPC.
	//
	// Computers are Go code registered at startup — there is no user-
	// authored expression language here. Built-in computers ship with
	// the controller; adding a new one is a code change.
	//
	// Initial set:
	//   "sun_color_temperature" — Brightness/ColorTemperature based on
	//                              the current solar position for the
	//                              configured (lat, lon).
	//
	// When set, the Remediation is evaluated every tick regardless of
	// external triggers (it's "always-on"). ActiveCompute is independent
	// of ActiveState/ActiveScene — set them too if you want the eval
	// loop to apply a Scene first and then layer the computer's output
	// on top (most computers will be partial: e.g. sun_color_temperature
	// only sets ColorTemperature, leaving brightness and state untouched).
	ActiveCompute string `json:"active_compute,omitempty"`

	// ActiveComputeArgs are passed verbatim to the named Computer. The
	// schema is computer-defined; consult the Computer's documentation
	// for the supported keys.
	ActiveComputeArgs map[string]string `json:"active_compute_args,omitempty"`
}

// When defines an activation window relative to an epoch event time.
// Start and Stop are Go duration strings (e.g. "-30m", "1h") applied to the
// event time. Empty Start defaults to event time - 1 minute; empty Stop
// uses the conditioner's EpochTimeWindow.
type When struct {
	Start string `json:"start,omitempty"`
	Stop  string `json:"stop,omitempty"`
}

type Match struct {
	Labels map[string]string `json:"labels,omitempty"`
}

func init() {
	SchemeBuilder.Register(&Condition{}, &ConditionList{})
}
