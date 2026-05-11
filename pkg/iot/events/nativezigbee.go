package events

import (
	apiv1 "github.com/zachfi/iotcontroller/api/v1"
	zclv1proto "github.com/zachfi/iotcontroller/proto/zcl/v1"
)

// FromZclMessage converts a native-Zigbee ZCL message into a slice of
// normalized events. Today this only emits PropertyAction with the same
// action vocabulary that zigbee2mqtt uses for buttons/dimmers/scenes —
// so a Binding written against `event.property=action,value=on` matches
// regardless of whether the device joined via z2m or the native dongle.
//
// Future additions (IAS Zone status reports for water_leak/contact/tamper,
// On/Off attribute reports for state) belong here so the matcher pipeline
// stays transport-agnostic.
func FromZclMessage(msg *zclv1proto.ZclMessage, d *apiv1.Device) []DeviceEvent {
	if msg == nil {
		return nil
	}
	if action := zclCommandToAction(msg); action != "" {
		return []DeviceEvent{{Property: PropertyAction, Value: action, Device: d}}
	}
	return nil
}

// zclCommandToAction maps a ZclMessage command to the same action string
// vocabulary used by zigbee2mqtt for the corresponding cluster/command
// pairs. Returns "" for non-command messages (sensor reports, attribute
// reads, defaultResponse, etc.).
//
// Kept here (rather than in the nativezigbee router) so the events layer
// owns the canonical ZCL→action vocabulary; the router calls FromZclMessage
// rather than re-implementing it.
func zclCommandToAction(msg *zclv1proto.ZclMessage) string {
	if msg.GetFrame() == nil || msg.GetFrame().GetCommand() == nil {
		return ""
	}

	switch msg.GetFrame().GetCommand().GetCommand().(type) {
	// genOnOff (0x0006)
	case *zclv1proto.ZclCommand_GenOnoffOn:
		return "on"
	case *zclv1proto.ZclCommand_GenOnoffOff:
		return "off"
	case *zclv1proto.ZclCommand_GenOnoffToggle:
		return "toggle"

	// genLevelControl (0x0008)
	case *zclv1proto.ZclCommand_GenLevelcontrolMoveToLevel:
		return "brightness_move_to_level"
	case *zclv1proto.ZclCommand_GenLevelcontrolMove:
		cmd := msg.GetFrame().GetCommand().GetGenLevelcontrolMove()
		if cmd.GetMoveMode() == zclv1proto.ZclMoveMode_ZCL_MOVE_MODE_DOWN {
			return "brightness_move_down"
		}
		return "brightness_move_up"
	case *zclv1proto.ZclCommand_GenLevelcontrolStep:
		cmd := msg.GetFrame().GetCommand().GetGenLevelcontrolStep()
		if cmd.GetStepMode() == zclv1proto.ZclStepMode_ZCL_STEP_MODE_DOWN {
			return "brightness_step_down"
		}
		return "brightness_step_up"
	case *zclv1proto.ZclCommand_GenLevelcontrolStop:
		return "brightness_stop"

	// genColorControl (0x0300)
	case *zclv1proto.ZclCommand_GenColorcontrolMoveToColorTemp:
		return "color_temperature_move"
	case *zclv1proto.ZclCommand_GenColorcontrolMoveColorTemp:
		cmd := msg.GetFrame().GetCommand().GetGenColorcontrolMoveColorTemp()
		if cmd.GetMoveMode() == zclv1proto.ZclMoveMode_ZCL_MOVE_MODE_DOWN {
			return "color_temperature_move_down"
		}
		return "color_temperature_move_up"
	case *zclv1proto.ZclCommand_GenColorcontrolStepColorTemp:
		return "color_temperature_step"
	case *zclv1proto.ZclCommand_GenColorcontrolMoveToHue:
		return "hue_move"
	case *zclv1proto.ZclCommand_GenColorcontrolMoveToSaturation:
		return "saturation_move"
	case *zclv1proto.ZclCommand_GenColorcontrolMoveToHueAndSaturation:
		return "hue_saturation_move"
	}

	return ""
}
