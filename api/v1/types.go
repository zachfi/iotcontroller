package v1

import (
	"encoding/json"

	"github.com/prometheus/alertmanager/timeinterval"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Annotation keys recognized by the operator. Annotations are how
// operators ask the controller to do a one-shot action on a CR
// without restructuring its Spec — the controller consumes the
// annotation and clears it on success.
const (
	// AnnotationReinterviewRequested asks the zigbee coordinator to
	// re-run the device interview (node descriptor + active endpoints
	// + simple descriptors) against an already-paired Device. Set its
	// value to anything non-empty (e.g. "true" or a request ID) on the
	// Device CR and the coordinator's reinterview poll will pick it up
	// within the next tick (default 30s).
	//
	// Use case: when a device joined with a partial interview (Tuya
	// firmware that rejects ZDO node descriptor on first ask, devices
	// that flapped during join, etc.) the operator can request a fresh
	// pass without un-pairing and re-pairing.
	//
	// Required for the reinterview to actually fire:
	//   * Device.Spec.NetworkAddress must be set (we don't query the
	//     coordinator's nwk↔ieee map by IEEE address yet — that lookup
	//     would also be valid, but starts costing extra plumbing for
	//     the common case).
	//   * The device must be alive on the mesh; an unreachable device
	//     will simply timeout each interview step and the annotation
	//     stays set so the operator can retry later or remove it.
	//
	// On a successful interview the coordinator clears this annotation
	// via a JSON-merge patch so subsequent reconciler ticks no-op.
	AnnotationReinterviewRequested = "iot.iot/reinterview-requested"
)

// TimeIntervalSpec mirrors the JSON/YAML *input* format expected by Prometheus.
// +kubebuilder:object:generate=true
type TimeIntervalSpec struct {
	// Times is a list of start/end times.
	// We use a local struct here to ensure deepcopy generation works.
	Times []TimePeriod `json:"times,omitempty"`

	// Weekdays accepts strings like "monday", "saturday", "monday:wednesday".
	Weekdays []string `json:"weekdays,omitempty"`

	// DaysOfMonth accepts strings like "1", "-1" (last day), "1:5".
	DaysOfMonth []string `json:"daysOfMonth,omitempty"`

	// Months accepts strings like "january", "march:may".
	Months []string `json:"months,omitempty"`

	// Years accepts strings like "2023", "2023:2025".
	Years []string `json:"years,omitempty"`

	// Location is the IANA Timezone string (e.g. "America/New_York").
	Location string `json:"location,omitempty"`

	// SunRelative defines time windows relative to the current day's
	// solar events at the conditioner's configured (lat, lon). Multiple
	// SunWindow entries OR together with each other and with Times.
	//
	// A SunWindow with Event=sunset, Before=30m, After=12h opens 30m
	// before sunset and stays open for 12.5 h — typically spanning
	// midnight into the next morning. The window is treated as a single
	// continuous [start, end] span (no wrap-around split), so callers
	// observing "is now in the window?" semantics get the intuitive
	// answer for evening-into-morning patterns.
	SunRelative []SunWindow `json:"sun_relative,omitempty"`
}

// SunWindow defines a time span anchored to a daily solar event.
// +kubebuilder:object:generate=true
type SunWindow struct {
	// Event is the anchor: "sunrise", "sunset", "noon", or "midnight".
	// Names match the go-sunrise library convention; "midnight" is solar
	// midnight, not 00:00 local time.
	Event string `json:"event"`

	// Before is how long before the event the window opens. Zero (or
	// unset) means the window opens at the event itself.
	Before metav1.Duration `json:"before,omitempty"`

	// After is how long after the event the window stays open. Zero (or
	// unset) means the window closes at the event itself (i.e. an
	// instantaneous window — generally not useful; set at least one of
	// Before/After to a non-zero value).
	After metav1.Duration `json:"after,omitempty"`
}

// TimePeriod defines a time range within a day. StartTime and EndTime use
// "HH:MM" format (24-hour). Mirrors the upstream Prometheus timeinterval format.
// +kubebuilder:object:generate=true
type TimePeriod struct {
	StartTime string `json:"start_time"`
	EndTime   string `json:"end_time"`
}

// AsPrometheus converts the Kubernetes Spec into the usable upstream TimeInterval type.
// It utilizes a JSON round-trip to trigger the upstream parsing logic (string -> int conversion).
func (in *TimeIntervalSpec) AsPrometheus() (timeinterval.TimeInterval, error) {
	var out timeinterval.TimeInterval

	// Marshal local K8s spec to JSON.
	// This produces standard JSON like {"weekdays": ["monday:friday"], ...}
	data, err := json.Marshal(in)
	if err != nil {
		return out, err
	}

	// Unmarshal the JSON into the Upstream Type.
	// This triggers timeinterval.WeekdayRange.UnmarshalJSON, which parses "monday:friday"
	// and sets the internal integers.
	if err := json.Unmarshal(data, &out); err != nil {
		return out, err
	}

	return out, nil
}
