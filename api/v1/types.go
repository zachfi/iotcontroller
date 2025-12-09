package v1

import (
	"encoding/json"

	"github.com/prometheus/alertmanager/timeinterval"
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
}

// TimePeriod mirrors the `yamlTimeRange` struct from the upstream package.
// We use strings here because Prometheus expects "HH:MM".
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
