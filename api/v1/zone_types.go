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

	// ReconcilerStack reflects the per-axis Active Computer Stack for
	// reconcile-managed zones. Written by the conditioner's reconciler
	// after each ReconcileZone; empty/absent for zones not in
	// `-conditioner.reconcile-zones`. Co-exists with the State /
	// Brightness / Color / ColorTemperature fields (written by the
	// zonekeeper) via Status().Patch with disjoint field ownership.
	//
	// Short-term: this is how operators inspect "why is foyer asserting
	// dusk-full right now?" via `kubectl get zone foyer -o yaml`. Long
	// term, the same data will live on a dedicated ZonePolicy CRD with
	// its own status sub-resource.
	ReconcilerStack []ReconcilerStackEntry `json:"reconciler_stack,omitempty"`

	// LastReconciledAt is the wall-clock time of the most recent
	// ReconcileZone call that completed (with or without a flush). Lets
	// operators distinguish "stack is genuinely empty" from "reconciler
	// hasn't ticked recently."
	LastReconciledAt *metav1.Time `json:"last_reconciled_at,omitempty"`
}

// ReconcilerStackEntry is one axis's view of the Active Computer Stack
// for a reconcile-managed zone. One entry per axis that has at least
// one Activation pushed; axes with no pushes are absent from the list.
type ReconcilerStackEntry struct {
	// Axis is the canonical AxisKind enum string
	// (e.g. AXIS_KIND_BRIGHTNESS). Stable string form so operators can
	// grep without proto knowledge.
	Axis string `json:"axis"`

	// Depth is the count of non-expired Activations on this axis at
	// reconcile time. Always >= 1 (axes with no pushes are absent).
	Depth int32 `json:"depth"`

	// Top is the highest-priority non-expired Activation — what's
	// currently winning on this axis. nil only in the transient case
	// where Depth was just bumped to 0 by lazy expiration; in steady
	// state Depth >= 1 implies Top != nil.
	Top *ReconcilerStackTop `json:"top,omitempty"`
}

// ReconcilerStackTop is the projection of a runtimeActivation suitable
// for Zone.Status reflection. Stays a flat string-friendly shape so
// `kubectl get zone -o yaml` stays human-readable; the full Activation
// proto lives in the in-process stack and on the wire.
type ReconcilerStackTop struct {
	Computer   string       `json:"computer"`
	SourceKind string       `json:"source_kind"`
	SourceName string       `json:"source_name,omitempty"`
	Priority   int32        `json:"priority"`
	PushedAt   metav1.Time  `json:"pushed_at"`
	ExpiresAt  *metav1.Time `json:"expires_at,omitempty"`
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
