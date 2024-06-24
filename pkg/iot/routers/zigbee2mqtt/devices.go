package zigbee2mqtt

import (
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
	PropertySoilMoisture      = "soil_moisture"
	PropertyState             = "state"
	PropertyTemperature       = "temperature"
	PropertyVOC               = "voc"
	PropertyVoltage           = "voltage"
	PropertyWaterLeak         = "water_leak"
)

const (
	FeatureTypeSwitch = "switch"
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

	// Check for soil monitor by checking for soil_mosture
	for _, e := range z.Definition.Exposes {
		if e.Property == PropertySoilMoisture {
			return iotv1proto.DeviceType_DEVICE_TYPE_SOIL
		}
	}

	for _, e := range z.Definition.Exposes {
		if e.Property == PropertyVOC {
			return iotv1proto.DeviceType_DEVICE_TYPE_AIR_QUALITY
		}
	}

	// Use humidity to identify a temperature sensor
	for _, e := range z.Definition.Exposes {
		if e.Property == PropertyHumidity {
			return iotv1proto.DeviceType_DEVICE_TYPE_TEMPERATURE
		}
	}

	// Check for button using action
	for _, e := range z.Definition.Exposes {
		if e.Property == PropertyAction {
			return iotv1proto.DeviceType_DEVICE_TYPE_BUTTON
		}
	}

	// Use occupancy to identify a motion device
	for _, e := range z.Definition.Exposes {
		if e.Property == PropertyOccupancy {
			return iotv1proto.DeviceType_DEVICE_TYPE_MOTION
		}
	}

	// Use water_leak to identify a moisture devices
	for _, e := range z.Definition.Exposes {
		if e.Property == PropertyWaterLeak {
			return iotv1proto.DeviceType_DEVICE_TYPE_LEAK
		}
	}

	// Use the switch exposure to identify relay devices
	for _, e := range z.Definition.Exposes {
		if e.Type == FeatureTypeSwitch {
			return iotv1proto.DeviceType_DEVICE_TYPE_RELAY
		}
	}

	switch z.Type {
	case "Coordinator":
		return iotv1proto.DeviceType_DEVICE_TYPE_COORDINATOR
	}

	return iotv1proto.DeviceType_DEVICE_TYPE_UNSPECIFIED
}
