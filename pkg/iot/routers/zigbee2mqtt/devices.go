package zigbee2mqtt

import (
	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

// Exposes contains a property, but also features which contain a property.
// cat pkg/iot/devices.json| jq -r '.[] | select(. != null).definition | select(. != null).exposes[].features | select(. != null).[].property'
// cat pkg/iot/devices.json| jq -r '.[] | select(. != null).definition | select(. != null).exposes[].property'

// TODO: many of these properties appear unused.  They are intended to match the
// message to ensure we're adhering.
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
	PropertyWaterLeak         = "water_leak"
	PropertyPressure          = "pressure"
	PropertySoilMoisture      = "soil_moisture"
	PropertyState             = "state"
	PropertyTemperature       = "temperature"
	PropertyVOC               = "voc"
	PropertyVoltage           = "voltage"
	PropertyTransmitPower     = "transmit_power"
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
			Bindings             []any `json:"bindings"`
			ConfiguredReportings []any `json:"configured_reportings"`
			Clusters             struct {
				Input  []string `json:"input"`
				Output []any    `json:"output"`
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
	ValueMax    float64  `json:"value_max,omitempty"`
	ValueMin    float64  `json:"value_min,omitempty"`
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

func deviceType(z Device) iotv1proto.DeviceType {
	// Check for colored light using color
	for _, e := range z.Definition.Exposes {
		if len(e.Features) > 0 {
			for _, f := range e.Features {
				if f.Property == PropertyColor {
					return iotv1proto.DeviceType_DEVICE_TYPE_COLOR_LIGHT
				}
			}
		}
	}

	// Check for basic light using brightness
	for _, e := range z.Definition.Exposes {
		if len(e.Features) > 0 {
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

	// Use the switch exposure to identify relay devices. Checked BEFORE
	// action / occupancy / water_leak because some relays expose both
	// a `switch` and ancillary properties — Third Reality smart plugs
	// (3RSP02028BZ) for example expose an onboard button via `action`
	// alongside the relay's `switch`. The primary function of such a
	// device is the relay; the button is a manual override. Without
	// this ordering the pond-pump's plug gets classified BUTTON, the
	// zone's relay control path no-ops, and the leak-driven Condition
	// fires SetState but no Zigbee command goes out.
	for _, e := range z.Definition.Exposes {
		if e.Type == FeatureTypeSwitch {
			return iotv1proto.DeviceType_DEVICE_TYPE_RELAY
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

	for _, e := range z.Definition.Exposes {
		if e.Property == PropertyTransmitPower {
			return iotv1proto.DeviceType_DEVICE_TYPE_ROUTER
		}
	}

	// Fall back to a bare temperature exposure last, so devices that also
	// expose temperature alongside a more specific feature (occupancy, water
	// leak, action, etc.) keep their more specific classification. A pure
	// temperature probe (e.g. SONOFF SNZB-02LD) reaches this point with only
	// temperature/battery/linkquality exposed.
	for _, e := range z.Definition.Exposes {
		if e.Property == PropertyTemperature {
			return iotv1proto.DeviceType_DEVICE_TYPE_TEMPERATURE
		}
	}

	switch z.Type {
	case "Coordinator":
		return iotv1proto.DeviceType_DEVICE_TYPE_COORDINATOR
	case "Router":
		return iotv1proto.DeviceType_DEVICE_TYPE_ROUTER
	}

	return iotv1proto.DeviceType_DEVICE_TYPE_UNSPECIFIED
}
