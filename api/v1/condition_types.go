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

type Remediation struct {
	Zone          string `json:"zone,omitempty"`
	ActiveState   string `json:"active_state,omitempty"`
	InactiveState string `json:"inactive_state,omitempty"`

	ActiveScene   string `json:"active_scene,omitempty"`
	InactiveScene string `json:"inactive_scene,omitempty"`

	// WhenGate is used to create a window for the epoch around which this
	// Remediation is applicable.  When is relative to the "when" label.
	// i.e. If the epoch in the label is at 10am, and the when.start is -30, then
	// the event will fire 30 minutes before, at 9:30am.
	WhenGate When `json:"when_gate,omitempty"`

	// TimeIntervals define the windows during which this remediation is
	// applicable.  If the conditioner receives an event outside of this range,
	// the zone will be set to the inactive state, if defined in the condition spec.
	TimeIntervals []TimeIntervalSpec `json:"time_intervals,omitempty"`
}

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
