package computer

import (
	"context"
	"time"

	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

// static.go — the "static" Computer is the bridge's vehicle for
// pushing pre-resolved Scene + State values onto the Active Computer
// Stack. Unlike circadian / fade / query etc. which derive their
// output from time/PromQL/etc., static just unpacks its args into the
// matching ApplyValues fields.
//
// Used by the eval-loop → reconciler bridge for TimeInterval-driven
// Remediations: the bridge pre-resolves the Scene CR to its component
// values, packs them into the per-axis Activation args, and pushes
// one Activation per claimed axis. The reconciler invokes
// staticComputer.Compute each reconcile tick, and the args are the
// single source of truth for the resolved values.
//
// Recognized args (all optional; each axis recognizes its own field):
//
//	state              — ZoneState enum string (e.g. "ZONE_STATE_ON")
//	brightness         — Brightness enum string (e.g. "BRIGHTNESS_FULL")
//	color_temperature  — ColorTemperature enum string
//	color              — color name (passed through as-is)
//
// Unknown / unparseable enum strings produce the corresponding zero
// value (UNSPECIFIED) — the reconciler's per-axis switch then treats
// that axis as "no contribution."

const StaticName = "static"

type staticComputer struct{}

func (staticComputer) Compute(_ context.Context, _ time.Time, _ Location, args map[string]string) (ApplyValues, error) {
	v := ApplyValues{}
	if s := args["state"]; s != "" {
		if val, ok := iotv1proto.ZoneState_value[s]; ok {
			v.State = iotv1proto.ZoneState(val)
		}
	}
	if s := args["brightness"]; s != "" {
		if val, ok := iotv1proto.Brightness_value[s]; ok {
			v.Brightness = iotv1proto.Brightness(val)
		}
	}
	if s := args["color_temperature"]; s != "" {
		if val, ok := iotv1proto.ColorTemperature_value[s]; ok {
			v.ColorTemperature = iotv1proto.ColorTemperature(val)
		}
	}
	if s := args["color"]; s != "" {
		v.Color = s
	}
	return v, nil
}

func init() {
	Register(StaticName, staticComputer{})
}
