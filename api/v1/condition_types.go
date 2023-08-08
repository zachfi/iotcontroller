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
	Name         string        `json:"name,omitempty"`
	Enabled      bool          `json:"enabled,omitempty"`
	AlertNames   string        `json:"alert_names,omitempty"`
	Remediations []Remediation `json:"remediation,omitempty"`
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
	Zone  string `json:"zone,omitempty"`
	State string `json:"state,omitempty"`
}

func init() {
	SchemeBuilder.Register(&Condition{}, &ConditionList{})
}
