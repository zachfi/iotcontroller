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

// ZoneSpec defines the desired state of Zone
type ZoneSpec struct {
	Devices []string `json:"devices,omitempty"`
	Colors  []string `json:"colors,omitempty"`
}

// ZoneStatus defines the observed state of Zone
type ZoneStatus struct {
	State            string `json:"state,omitempty"`
	Brightness       string `json:"brightness,omitempty"`
	Color            string `json:"color,omitempty"`
	ColorTemperature string `json:"color_temperature,omitempty"`
	TimeoutAfter     string `json:"timeout_after,omitempty"`
	TimeoutState     string `json:"timeout_state,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// Zone is the Schema for the zones API
type Zone struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ZoneSpec   `json:"spec,omitempty"`
	Status ZoneStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// ZoneList contains a list of Zone
type ZoneList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Zone `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Zone{}, &ZoneList{})
}
