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

// BindingSpec defines a trigger → condition mapping. Exactly one of ZCL or
// MQTT must be set. When the trigger fires, the named Condition is activated.
type BindingSpec struct {
	// ZCL matches a native Zigbee command by IEEE address, cluster, and command ID.
	ZCL *ZCLTrigger `json:"zcl,omitempty"`

	// MQTT matches a message on an MQTT topic by extracting a JSON field and
	// comparing its string value. Covers zigbee2mqtt, Tasmota, ESPHome, etc.
	MQTT *MQTTTrigger `json:"mqtt,omitempty"`

	// Condition is the name of the Condition resource to activate when this
	// binding fires. The Condition must exist in the same namespace.
	Condition string `json:"condition"`
}

// ZCLTrigger matches a ZCL command from a specific device.
type ZCLTrigger struct {
	// IEEE is the 64-bit IEEE address of the device (e.g. "0xffffb40e06032b2e").
	IEEE string `json:"ieee"`

	// ClusterID is the ZCL cluster identifier (e.g. 6 for On/Off).
	ClusterID uint16 `json:"cluster_id"`

	// CommandID is the ZCL command identifier within the cluster (e.g. 1 for On).
	CommandID uint8 `json:"command_id"`
}

// MQTTTrigger matches an MQTT message by topic, JSON field, and value.
type MQTTTrigger struct {
	// Topic is the exact MQTT topic to match (e.g. "zigbee2mqtt/my-button").
	Topic string `json:"topic"`

	// Field is the top-level JSON key to extract from the payload (e.g. "action").
	Field string `json:"field"`

	// Value is the expected string value of the field (e.g. "single").
	Value string `json:"value"`
}

// BindingStatus defines the observed state of Binding.
type BindingStatus struct{}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// Binding maps a device trigger (ZCL command or MQTT message) to a Condition.
// When the trigger fires, the named Condition's remediations are applied to
// its zones. This replaces hardcoded action strings in the router.
type Binding struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BindingSpec   `json:"spec,omitempty"`
	Status BindingStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// BindingList contains a list of Binding.
type BindingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Binding `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Binding{}, &BindingList{})
}
