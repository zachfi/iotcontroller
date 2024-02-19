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

// SceneSpec defines the desired state of Scene
type SceneSpec struct {
	Brightness       string `json:"brightness,omitempty"`
	Color            string `json:"color,omitempty"`
	ColorTemperature string `json:"color_temperature,omitempty"`
	/* Colors           []string `json:"colors,omitempty"` */
}

// SceneStatus defines the observed state of Scene
type SceneStatus struct{}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// Scene is the Schema for the scenes API
type Scene struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SceneSpec   `json:"spec,omitempty"`
	Status SceneStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// SceneList contains a list of Scene
type SceneList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Scene `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Scene{}, &SceneList{})
}
