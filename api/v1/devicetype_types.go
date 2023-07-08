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

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// DeviceTypeSpec defines the desired state of DeviceType
type DeviceTypeSpec struct {
	Name            string `json:"friendly_name,omitempty"`
	Description     string `json:"description,omitempty"`
	Zone            string `json:"zone,omitempty"`
	Type            string `json:"type,omitempty"`
	DateCode        string `json:"date_code,omitempty"`
	Model           string `json:"model,omitempty"`
	Vendor          string `json:"vendor,omitempty"`
	ManufactureName string `json:"manufacture_name,omitempty"`
	PowerSource     string `json:"power_source,omitempty"`
	ModelID         string `json:"model_id,omitempty"`
}

// DeviceTypeStatus defines the observed state of DeviceType
type DeviceTypeStatus struct {
	LastSeen        uint64 `json:"last_seen,omitempty"`
	SoftwareBuildID string `json:"software_build_id,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// DeviceType is the Schema for the devicetypes API
type DeviceType struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DeviceTypeSpec   `json:"spec,omitempty"`
	Status DeviceTypeStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// DeviceTypeList contains a list of DeviceType
type DeviceTypeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DeviceType `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DeviceType{}, &DeviceTypeList{})
}
