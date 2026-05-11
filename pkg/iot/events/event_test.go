package events

import (
	"testing"

	"github.com/stretchr/testify/require"

	apiv1 "github.com/zachfi/iotcontroller/api/v1"
	zclv1proto "github.com/zachfi/iotcontroller/proto/zcl/v1"
)

func sptr(s string) *string { return &s }
func bptr(b bool) *bool     { return &b }

// TestFromZ2M_Action: an action message becomes a single PropertyAction event.
func TestFromZ2M_Action(t *testing.T) {
	d := &apiv1.Device{}
	d.Name = "btn"
	got := FromZ2M(Z2MMessage{Action: sptr("single")}, d)
	require.Len(t, got, 1)
	require.Equal(t, PropertyAction, got[0].Property)
	require.Equal(t, "single", got[0].Value)
	require.Same(t, d, got[0].Device)
}

// TestFromZ2M_Multiple: a message can carry multiple asserted properties; each
// becomes its own DeviceEvent so the matcher can pair each with its own
// Binding.
func TestFromZ2M_Multiple(t *testing.T) {
	d := &apiv1.Device{}
	d.Name = "multi"
	got := FromZ2M(Z2MMessage{
		Action:    sptr("single"),
		Occupancy: bptr(true),
		WaterLeak: bptr(false),
		State:     sptr("ON"),
	}, d)
	require.Len(t, got, 4)

	props := make(map[string]string, len(got))
	for _, ev := range got {
		props[ev.Property] = ev.Value
	}
	require.Equal(t, "single", props[PropertyAction])
	require.Equal(t, "true", props[PropertyOccupancy])
	require.Equal(t, "false", props[PropertyWaterLeak])
	require.Equal(t, "on", props[PropertyState], "state should be lowercased")
}

// TestFromZ2M_None: a message with no asserted property emits no events.
func TestFromZ2M_None(t *testing.T) {
	require.Empty(t, FromZ2M(Z2MMessage{}, &apiv1.Device{}))
}

// TestFromZclMessage_OnOff: ZCL On/Off commands become PropertyAction events
// with the same vocabulary z2m uses.
func TestFromZclMessage_OnOff(t *testing.T) {
	d := &apiv1.Device{}
	d.Name = "z"

	msg := &zclv1proto.ZclMessage{
		ClusterId: 0x0006,
		Frame: &zclv1proto.ZclFrame{
			Command: &zclv1proto.ZclCommand{
				Command: &zclv1proto.ZclCommand_GenOnoffOn{},
			},
		},
	}
	got := FromZclMessage(msg, d)
	require.Len(t, got, 1)
	require.Equal(t, PropertyAction, got[0].Property)
	require.Equal(t, "on", got[0].Value)
}

// TestFromZclMessage_NilFrame: a malformed message (no Frame) emits no events.
func TestFromZclMessage_NilFrame(t *testing.T) {
	require.Empty(t, FromZclMessage(&zclv1proto.ZclMessage{}, &apiv1.Device{}))
}

// TestFromZclMessage_Nil: nil input is safe.
func TestFromZclMessage_Nil(t *testing.T) {
	require.Empty(t, FromZclMessage(nil, &apiv1.Device{}))
}

// TestFromZclMessage_LevelControlStep_Direction: the direction-aware mapping
// (move/step up vs down) is preserved through the events layer.
func TestFromZclMessage_LevelControlStep_Direction(t *testing.T) {
	d := &apiv1.Device{}
	cases := []struct {
		dir      zclv1proto.ZclStepMode
		expected string
	}{
		{zclv1proto.ZclStepMode_ZCL_STEP_MODE_UP, "brightness_step_up"},
		{zclv1proto.ZclStepMode_ZCL_STEP_MODE_DOWN, "brightness_step_down"},
	}
	for _, tc := range cases {
		msg := &zclv1proto.ZclMessage{
			ClusterId: 0x0008,
			Frame: &zclv1proto.ZclFrame{
				Command: &zclv1proto.ZclCommand{
					Command: &zclv1proto.ZclCommand_GenLevelcontrolStep{
						GenLevelcontrolStep: &zclv1proto.GenLevelControlStep{StepMode: tc.dir},
					},
				},
			},
		}
		got := FromZclMessage(msg, d)
		require.Len(t, got, 1)
		require.Equal(t, tc.expected, got[0].Value)
	}
}
