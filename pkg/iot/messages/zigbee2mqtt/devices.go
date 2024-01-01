package zigbee2mqtt

import (
	"regexp"
	"strings"

	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

// Exposes contains a property, but also features which contain a property.
// cat pkg/iot/devices.json| jq -r '.[] | select(. != null).definition | select(. != null).exposes[].features | select(. != null).[].property'
// cat pkg/iot/devices.json| jq -r '.[] | select(. != null).definition | select(. != null).exposes[].property'

const (
	PropertyAction            = "action"
	PropertyBattery           = "battery"
	PropertyBrightness        = "brightness"
	PropertyColor             = "color"
	PropertyColorTemp         = "color_temp"
	PropertyDeviceCo2         = "device_co2"
	PropertyDeviceTemperature = "device_temperature"
	PropertyFormaldehyde      = "formaldehyd"
	PropertyHumidity          = "humidity"
	PropertyIlluminance       = "illuminance"
	PropertyLinkQuality       = "linkquality"
	PropertyOccupancy         = "occupancy"
	PropertyPressure          = "pressure"
	PropertyState             = "state"
	PropertyTemperature       = "temperature"
	PropertyVOC               = "voc"
	PropertyVoltage           = "voltage"
	PropertyWaterLeak         = "water_leak"
)

type Devices []Device

type Device struct {
	IeeeAddress    string `json:"ieee_address"`
	Type           string `json:"type"`
	NetworkAddress int    `json:"network_address"`
	Supported      bool   `json:"supported"`
	FriendlyName   string `json:"friendly_name"`
	Endpoints      struct {
		Num1 struct {
			Bindings             []interface{} `json:"bindings"`
			ConfiguredReportings []interface{} `json:"configured_reportings"`
			Clusters             struct {
				Input  []string      `json:"input"`
				Output []interface{} `json:"output"`
			} `json:"clusters"`
		} `json:"1"`
	} `json:"endpoints"`
	Definition  Definition `json:"definition"`
	PowerSource string     `json:"power_source"`
	DateCode    string     `json:"date_code"`
	ModelID     string     `json:"model_id"`
	Scenes      []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"scenes"`
	Interviewing       bool   `json:"interviewing"`
	InterviewCompleted bool   `json:"interview_completed"`
	SoftwareBuildID    string `json:"software_build_id,omitempty"`
}

type Definition struct {
	Model       string   `json:"model,omitempty"`
	Vendor      string   `json:"vendor,omitempty"`
	Description string   `json:"description,omitempty"`
	Options     []Option `json:"options,omitempty"`
	Exposes     []Expose `json:"exposes,omitempty"`
}

type Option struct {
	Access int8 `json:"access,omitempty"`
}

type exposeCommon struct {
	Access      int      `json:"access,omitempty"`
	Description string   `json:"description,omitempty"`
	Label       string   `json:"label,omitempty"`
	Name        string   `json:"name,omitempty"`
	Property    string   `json:"property,omitempty"`
	Type        string   `json:"type,omitempty"`
	Unit        string   `json:"unit,omitempty"`
	ValueMax    int      `json:"value_max,omitempty"`
	ValueMin    int      `json:"value_min,omitempty"`
	Values      []string `json:"values,omitempty"`
}

type Expose struct {
	exposeCommon
	ValueOff    bool `json:"value_off,omitempty"`
	ValueOn     bool `json:"value_on,omitempty"`
	ValueToggle bool `json:"value_toggle,omitempty"`
	Features    []struct {
		exposeCommon
		ValueOff    string `json:"value_off,omitempty"`
		ValueOn     string `json:"value_on,omitempty"`
		ValueToggle string `json:"value_toggle,omitempty"`
		Presets     []struct {
			Description string `json:"description,omitempty"`
			Name        string `json:"name,omitempty"`
			Value       int64  `json:"value,omitempty"`
		} `json:"presets,omitempty"`
	} `json:"features,omitempty"`
}

func DeviceType(z Device) iotv1proto.DeviceType {
	// Check for colored light using color
	for _, e := range z.Definition.Exposes {
		if e.Features != nil && len(e.Features) > 0 {
			for _, f := range e.Features {
				if f.Property == PropertyColor {
					return iotv1proto.DeviceType_DEVICE_TYPE_COLOR_LIGHT
				}
			}
		}
	}

	// Check for basic light using brightness
	for _, e := range z.Definition.Exposes {
		if e.Features != nil && len(e.Features) > 0 {
			for _, f := range e.Features {
				if f.Property == PropertyBrightness {
					return iotv1proto.DeviceType_DEVICE_TYPE_BASIC_LIGHT
				}
			}
		}
	}

	// Check for button using action
	for _, e := range z.Definition.Exposes {
		if e.Property == PropertyAction {
			return iotv1proto.DeviceType_DEVICE_TYPE_BUTTON
		}
	}

	// TODO: migrate use of these device types to the "exposes" data structure.

	switch z.Type {
	case "Coordinator":
		return iotv1proto.DeviceType_DEVICE_TYPE_COORDINATOR
	}

	switch z.Definition.Vendor {
	case "Philips":
		switch z.ModelID {
		case "LWB014":
			return iotv1proto.DeviceType_DEVICE_TYPE_BASIC_LIGHT
		case "ROM001":
			return iotv1proto.DeviceType_DEVICE_TYPE_BUTTON
		default:
			if strings.HasPrefix(z.ModelID, "LC") {
				return iotv1proto.DeviceType_DEVICE_TYPE_COLOR_LIGHT
			}
		}

		switch z.Definition.Description {
		case "Hue dimmer switch":
			return iotv1proto.DeviceType_DEVICE_TYPE_BUTTON
		case "Hue Tap dial switch":
			return iotv1proto.DeviceType_DEVICE_TYPE_BUTTON
		}

	case "Xiaomi":
		switch z.Definition.Model {
		case "WXKG11LM":
			return iotv1proto.DeviceType_DEVICE_TYPE_BUTTON
		}

		switch z.ModelID {
		case "lumi.sensor_switch":
			return iotv1proto.DeviceType_DEVICE_TYPE_BUTTON
		case "lumi.sensor_motion.aq2":
			return iotv1proto.DeviceType_DEVICE_TYPE_MOTION
		case "lumi.weather":
			return iotv1proto.DeviceType_DEVICE_TYPE_TEMPERATURE
		case "lumi.sensor_cube.aqgl01":
			return iotv1proto.DeviceType_DEVICE_TYPE_BUTTON
		case "lumi.remote.b1acn01":
			return iotv1proto.DeviceType_DEVICE_TYPE_BUTTON
		case "lumi.sensor_wleak.aq1":
			return iotv1proto.DeviceType_DEVICE_TYPE_LEAK
		}

	case "SONOFF":
		relayRegex := regexp.MustCompile(`^S[0-9]{2}ZB.*$`)
		match := relayRegex.FindAllString(z.Definition.Model, -1)
		if len(match) == 1 {
			return iotv1proto.DeviceType_DEVICE_TYPE_RELAY
		}

		buttonRegex := regexp.MustCompile(`^SNZB-[0-9]{2}.*$`)
		match = buttonRegex.FindAllString(z.Definition.Model, -1)
		if len(match) == 1 {
			return iotv1proto.DeviceType_DEVICE_TYPE_BUTTON
		}

	case "Third Reality":
		// Same as the Third Reality below, but on the model, not the description
		switch z.Definition.Model {
		case "3RTHS24BZ":
			return iotv1proto.DeviceType_DEVICE_TYPE_TEMPERATURE
		case "3RSB22BZ":
			return iotv1proto.DeviceType_DEVICE_TYPE_BUTTON
		}
		switch z.Definition.Description {
		case "Temperature and humidity sensor":
			return iotv1proto.DeviceType_DEVICE_TYPE_TEMPERATURE
		case "Smart button":
			return iotv1proto.DeviceType_DEVICE_TYPE_BUTTON
		}

	case "TuYa":
		switch z.Definition.Model {
		case "TS0601_air_quality_sensor":
			return iotv1proto.DeviceType_DEVICE_TYPE_TEMPERATURE
		}

	}

	return iotv1proto.DeviceType_DEVICE_TYPE_UNSPECIFIED
}
