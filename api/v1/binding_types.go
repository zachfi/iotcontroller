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

// BindingSpec defines an event → condition mapping. When the trigger event
// fires from a device matching the selector, the named Condition is activated.
type BindingSpec struct {
	// Event matches a normalized device-emitted event by property and value.
	// Both zigbee2mqtt and the native zigbee router normalize their incoming
	// messages into the same Event shape so a single Binding works regardless
	// of transport.
	Event EventTrigger `json:"event"`

	// Condition is the name of the Condition resource to activate when this
	// binding fires. The Condition must exist in the same namespace.
	Condition string `json:"condition"`
}

// EventTrigger matches a normalized device event, optionally scoped to a
// subset of devices via Selector.
type EventTrigger struct {
	// Property is the normalized expose name. Examples: "action",
	// "occupancy", "water_leak", "state", "contact", "tamper".
	Property string `json:"property"`

	// Value is the expected value rendered as a string. For booleans use
	// "true" / "false". For action enums use the action name (e.g. "single",
	// "double", "on"). May be empty to match any value of the property.
	// Ignored when Values is non-empty.
	Value string `json:"value,omitempty"`

	// Values is the list of accepted values for this trigger. Use this
	// when a single Binding should match multiple device-specific
	// action vocabularies for the same intent — e.g. ["single",
	// "1_single", "button_1_press"] all meaning "primary press."
	// When Values is non-empty it takes precedence over Value.
	// Multi-value matching is for *aliases of the same intent*; if you
	// want different actions to trigger different Conditions, write
	// separate Bindings.
	Values []string `json:"values,omitempty"`

	// Selector restricts which devices can fire this binding. All non-empty
	// fields must match the device. An empty selector matches every device
	// that emitted the property.
	Selector EventSelector `json:"selector,omitempty"`
}

// EventSelector restricts a Binding to a subset of devices. Multiple fields
// AND together. The most specific binding wins (IEEE > Device > LabelSelector
// per key > DeviceType > Zone > none); ties are broken by sorted Binding name.
type EventSelector struct {
	// IEEE matches the device's 64-bit IEEE address (e.g. "0xffffb40e06036411").
	IEEE string `json:"ieee,omitempty"`

	// Device matches the device's CR name.
	Device string `json:"device,omitempty"`

	// DeviceType matches Spec.Type (e.g. "DEVICE_TYPE_BUTTON").
	DeviceType string `json:"device_type,omitempty"`

	// Zone matches the device's `iot/zone` label.
	Zone string `json:"zone,omitempty"`

	// LabelSelector is an exact-match label set. Every key/value here must
	// be present on the Device for the binding to fire.
	LabelSelector map[string]string `json:"labels,omitempty"`
}

// BindingStatus defines the observed state of Binding.
type BindingStatus struct{}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// Binding maps a normalized device event to a Condition. When the event
// fires from a device matching the selector, the named Condition's
// remediations are applied to its zones. This replaces hardcoded action
// strings in the router.
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
