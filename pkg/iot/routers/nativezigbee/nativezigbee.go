package nativezigbee

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
	kubeclient "sigs.k8s.io/controller-runtime/pkg/client"

	apiv1 "github.com/zachfi/iotcontroller/api/v1"
	"github.com/zachfi/iotcontroller/pkg/iot"
	iotutil "github.com/zachfi/iotcontroller/pkg/iot/util"
	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
	zclv1proto "github.com/zachfi/iotcontroller/proto/zcl/v1"
	zigbeev1proto "github.com/zachfi/iotcontroller/proto/zigbee/v1"
	"github.com/zachfi/zkit/pkg/tracing"
)

const routeName = "nativezigbee"

// NativeZigbee handles messages from the native Zigbee coordinator (not zigbee2mqtt).
// It receives ZclMessage and DeviceInterviewResult proto messages from the router.
type NativeZigbee struct {
	logger *slog.Logger
	tracer trace.Tracer

	kubeclient          kubeclient.Client
	zonekeeperClient    iotv1proto.ZoneKeeperServiceClient
	eventReceiverClient iotv1proto.EventReceiverServiceClient

	// ieeeToNwk caches the mapping from ieee→device name to avoid repeated kube lookups
	cacheMux  sync.RWMutex
	ieeeCache map[string]string // ieee address → device name
}

func New(logger *slog.Logger, tracer trace.Tracer, kubeclient kubeclient.Client, zonekeeperClient iotv1proto.ZoneKeeperServiceClient, eventReceiverClient iotv1proto.EventReceiverServiceClient) (*NativeZigbee, error) {
	return &NativeZigbee{
		logger:              logger.With("router", routeName),
		tracer:              tracer,
		kubeclient:          kubeclient,
		zonekeeperClient:    zonekeeperClient,
		eventReceiverClient: eventReceiverClient,
		ieeeCache:           make(map[string]string),
	}, nil
}

// DeviceRoute handles a ZclMessage proto payload for path "zigbee/{deviceID}".
func (n *NativeZigbee) DeviceRoute(ctx context.Context, b []byte, deviceID string) error {
	var err error
	ctx, span := n.tracer.Start(ctx, "NativeZigbee.DeviceRoute")
	defer tracing.ErrHandler(span, err, "device route failed", n.logger)

	zclMsg := &zclv1proto.ZclMessage{}
	if err = proto.Unmarshal(b, zclMsg); err != nil {
		return fmt.Errorf("unmarshaling ZclMessage: %w", err)
	}

	span.SetAttributes(
		attribute.String("device_id", deviceID),
		attribute.String("cluster", zclMsg.GetClusterName()),
	)

	// Resolve IEEE address from path or ZclMessage.
	// Path deviceID is IEEE hex (no "0x") for known devices, or "nwk_XXXX" for
	// devices not yet in the coordinator's NWK→IEEE map (e.g. after a restart).
	ieeeAddr := ""
	if !strings.HasPrefix(deviceID, "nwk_") {
		ieeeAddr = fmt.Sprintf("0x%s", deviceID)
	} else if zclMsg.GetSourceIeee() != "" {
		ieeeAddr = zclMsg.GetSourceIeee()
	}

	// Look up the Device CRD by IEEE address (or fall back to NWK address lookup).
	var device *apiv1.Device
	if ieeeAddr != "" {
		device, err = n.getDeviceByIEEE(ctx, ieeeAddr)
	} else if strings.HasPrefix(deviceID, "nwk_") {
		// Coordinator restarted — NWK→IEEE map lost. Try to find device by network address.
		nwkHex := fmt.Sprintf("0x%s", strings.TrimPrefix(deviceID, "nwk_"))
		device, err = n.getDeviceByNWK(ctx, nwkHex)
	}
	if err != nil || device == nil {
		n.logger.Debug("device not found, skipping action",
			slog.String("device_id", deviceID),
			slog.String("ieee", ieeeAddr),
		)
		return nil
	}

	// Update last seen
	if updateErr := iotutil.UpdateLastSeen(ctx, n.kubeclient, device); updateErr != nil {
		n.logger.Debug("failed to update last seen", slog.String("error", updateErr.Error()))
	}

	// Try binding lookup first: find a Binding for (ieee, cluster, command) and
	// activate the named Condition. If no binding matches, fall back to the
	// legacy action string path so existing configurations keep working.
	clusterID, commandID, hasIDs := zclCommandIDs(zclMsg)
	if hasIDs {
		if conditionName := n.findZCLBinding(ctx, ieeeAddr, clusterID, commandID); conditionName != "" {
			span.SetAttributes(
				attribute.String("device", device.Name),
				attribute.String("condition", conditionName),
			)
			_, err = n.eventReceiverClient.ActivateCondition(ctx, &iotv1proto.ActivateConditionRequest{
				Condition: conditionName,
			})
			return err
		}
	}

	// Map ZCL command to action string (legacy path for unbound devices)
	action := zclCommandToAction(zclMsg)
	if action == "" {
		n.logger.Debug("no action for ZCL command",
			slog.String("cluster", zclMsg.GetClusterName()),
			slog.String("device", device.Name),
		)
		return nil
	}

	zone, ok := device.Labels[iot.DeviceZoneLabel]
	if !ok {
		n.logger.Debug("device has no zone label, skipping action",
			slog.String("device", device.Name),
			slog.String("action", action),
		)
		return nil
	}

	span.SetAttributes(
		attribute.String("device", device.Name),
		attribute.String("zone", zone),
		attribute.String("action", action),
	)

	_, err = n.zonekeeperClient.ActionHandler(ctx, &iotv1proto.ActionHandlerRequest{
		Event:  action,
		Device: device.Name,
		Zone:   zone,
	})
	return err
}

// zclCommandIDs extracts the cluster ID and command ID from a ZclMessage.
// Returns (clusterID, commandID, true) when a known command is present.
func zclCommandIDs(msg *zclv1proto.ZclMessage) (clusterID uint16, commandID uint8, ok bool) {
	if msg.GetFrame() == nil || msg.GetFrame().GetCommand() == nil {
		return 0, 0, false
	}
	clusterID = uint16(msg.GetClusterId())
	switch msg.GetFrame().GetCommand().GetCommand().(type) {
	case *zclv1proto.ZclCommand_GenOnoffOff:
		return clusterID, 0x00, true
	case *zclv1proto.ZclCommand_GenOnoffOn:
		return clusterID, 0x01, true
	case *zclv1proto.ZclCommand_GenOnoffToggle:
		return clusterID, 0x02, true
	case *zclv1proto.ZclCommand_GenLevelcontrolMoveToLevel:
		return clusterID, 0x00, true
	case *zclv1proto.ZclCommand_GenLevelcontrolMove:
		return clusterID, 0x01, true
	case *zclv1proto.ZclCommand_GenLevelcontrolStep:
		return clusterID, 0x02, true
	case *zclv1proto.ZclCommand_GenLevelcontrolStop:
		return clusterID, 0x03, true
	case *zclv1proto.ZclCommand_GenColorcontrolMoveToColorTemp:
		return clusterID, 0x0A, true
	case *zclv1proto.ZclCommand_GenColorcontrolMoveColorTemp:
		return clusterID, 0x4B, true
	case *zclv1proto.ZclCommand_GenColorcontrolStepColorTemp:
		return clusterID, 0x4C, true
	case *zclv1proto.ZclCommand_GenColorcontrolMoveToHue:
		return clusterID, 0x00, true
	case *zclv1proto.ZclCommand_GenColorcontrolMoveToSaturation:
		return clusterID, 0x03, true
	case *zclv1proto.ZclCommand_GenColorcontrolMoveToHueAndSaturation:
		return clusterID, 0x06, true
	}
	return 0, 0, false
}

// findZCLBinding lists Binding CRDs and returns the condition name for the
// first ZCL binding matching (ieee, clusterID, commandID), or "" if none match.
func (n *NativeZigbee) findZCLBinding(ctx context.Context, ieee string, clusterID uint16, commandID uint8) string {
	if n.eventReceiverClient == nil {
		return ""
	}

	list := &apiv1.BindingList{}
	if err := n.kubeclient.List(ctx, list, kubeclient.InNamespace("iot")); err != nil {
		n.logger.Debug("failed to list bindings", slog.String("error", err.Error()))
		return ""
	}

	for _, b := range list.Items {
		if b.Spec.ZCL == nil {
			continue
		}
		if b.Spec.ZCL.IEEE == ieee &&
			b.Spec.ZCL.ClusterID == clusterID &&
			b.Spec.ZCL.CommandID == commandID {
			return b.Spec.Condition
		}
	}
	return ""
}

// InterviewRoute handles a DeviceInterviewResult proto payload for path "zigbee/{ieee}/interview".
func (n *NativeZigbee) InterviewRoute(ctx context.Context, b []byte, ieeeAddr string) error {
	var err error
	ctx, span := n.tracer.Start(ctx, "NativeZigbee.InterviewRoute")
	defer tracing.ErrHandler(span, err, "interview route failed", n.logger)

	result := &zigbeev1proto.DeviceInterviewResult{}
	if err = proto.Unmarshal(b, result); err != nil {
		return fmt.Errorf("unmarshaling DeviceInterviewResult: %w", err)
	}

	span.SetAttributes(
		attribute.String("ieee_address", result.GetIeeeAddress()),
		attribute.String("interview_state", result.GetInterviewState().String()),
	)

	if result.GetInterviewState() != zigbeev1proto.InterviewState_INTERVIEW_STATE_SUCCESSFUL {
		n.logger.Info("interview not successful, skipping device update",
			slog.String("ieee", result.GetIeeeAddress()),
			slog.String("state", result.GetInterviewState().String()),
		)
		return nil
	}

	// Use IEEE address as device name (normalized: strip "0x" prefix, lowercase)
	ieee := result.GetIeeeAddress()
	deviceName := strings.ToLower(strings.TrimPrefix(ieee, "0x"))

	device, err := iotutil.GetOrCreateAPIDevice(ctx, n.kubeclient, deviceName)
	if err != nil {
		return fmt.Errorf("getting/creating device %q: %w", deviceName, err)
	}

	// Populate spec from interview
	device.Spec.IEEEAddress = ieee
	device.Spec.NetworkAddress = fmt.Sprintf("0x%04x", result.GetNetworkAddress())

	// Only set Type from ZDO for coordinator/router roles using the proto enum name.
	// End-device functional type (light, button, sensor) is not known from the interview
	// alone — it must be set separately (manually or via model mapping).
	// We preserve any existing Type so manual assignments are not overwritten.
	if device.Spec.Type == "" {
		switch result.GetDeviceType() {
		case zigbeev1proto.DeviceType_DEVICE_TYPE_COORDINATOR:
			device.Spec.Type = "DEVICE_TYPE_COORDINATOR"
		case zigbeev1proto.DeviceType_DEVICE_TYPE_ROUTER:
			device.Spec.Type = "DEVICE_TYPE_ROUTER"
			// End-device: leave Type empty — set by operator or future model-based mapping.
		}
	}

	if result.GetManufacturerName() != "" {
		device.Spec.Vendor = result.GetManufacturerName()
		device.Spec.ManufactureName = result.GetManufacturerName()
	}
	if result.GetModelId() != "" {
		device.Spec.Model = result.GetModelId()
		device.Spec.ModelID = result.GetModelId()
	}
	if result.GetDateCode() != "" {
		device.Spec.DateCode = result.GetDateCode()
	}
	if result.GetPowerSource() != zigbeev1proto.PowerSource_POWER_SOURCE_UNSPECIFIED {
		device.Spec.PowerSource = result.GetPowerSource().String()
	}

	if err = n.kubeclient.Update(ctx, device); err != nil {
		return fmt.Errorf("updating device spec: %w", err)
	}

	// Update status
	device, err = iotutil.GetOrCreateAPIDevice(ctx, n.kubeclient, deviceName)
	if err != nil {
		return fmt.Errorf("refreshing device: %w", err)
	}
	device.Status.SoftwareBuildID = result.GetSoftwareBuildId()
	if err = n.kubeclient.Status().Update(ctx, device); err != nil {
		return fmt.Errorf("updating device status: %w", err)
	}

	// Invalidate IEEE cache entry
	n.cacheMux.Lock()
	delete(n.ieeeCache, ieee)
	n.cacheMux.Unlock()

	n.logger.Info("device updated from interview",
		slog.String("device", deviceName),
		slog.String("ieee", ieee),
		slog.String("model", device.Spec.Model),
		slog.String("vendor", device.Spec.Vendor),
	)

	return nil
}

// getDeviceByIEEE finds a Device CRD by its IEEEAddress spec field.
// Checks the in-memory cache first, then lists all devices.
func (n *NativeZigbee) getDeviceByIEEE(ctx context.Context, ieeeAddr string) (*apiv1.Device, error) {
	if ieeeAddr == "" {
		return nil, fmt.Errorf("empty IEEE address")
	}

	// Check cache
	n.cacheMux.RLock()
	if name, ok := n.ieeeCache[ieeeAddr]; ok {
		n.cacheMux.RUnlock()
		return iotutil.GetOrCreateAPIDevice(ctx, n.kubeclient, name)
	}
	n.cacheMux.RUnlock()

	// List all devices to find the one with matching IEEE address
	deviceList := &apiv1.DeviceList{}
	if err := n.kubeclient.List(ctx, deviceList, kubeclient.InNamespace("iot")); err != nil {
		return nil, fmt.Errorf("listing devices: %w", err)
	}

	for i := range deviceList.Items {
		d := &deviceList.Items[i]
		if d.Spec.IEEEAddress == ieeeAddr {
			// Cache hit
			n.cacheMux.Lock()
			n.ieeeCache[ieeeAddr] = d.Name
			n.cacheMux.Unlock()
			return d, nil
		}
	}

	// Not found — try using the stripped IEEE address as device name (set during interview)
	deviceName := strings.ToLower(strings.TrimPrefix(ieeeAddr, "0x"))
	device, err := iotutil.GetOrCreateAPIDevice(ctx, n.kubeclient, deviceName)
	if err != nil {
		return nil, fmt.Errorf("device %q not found and could not be created: %w", ieeeAddr, err)
	}
	return device, nil
}

// getDeviceByNWK finds a Device CRD by its NetworkAddress spec field (e.g. "0x1a2b").
// Used as a fallback when the coordinator's NWK→IEEE map is empty after restart.
func (n *NativeZigbee) getDeviceByNWK(ctx context.Context, nwkHex string) (*apiv1.Device, error) {
	deviceList := &apiv1.DeviceList{}
	if err := n.kubeclient.List(ctx, deviceList, kubeclient.InNamespace("iot")); err != nil {
		return nil, fmt.Errorf("listing devices: %w", err)
	}

	for i := range deviceList.Items {
		d := &deviceList.Items[i]
		if strings.EqualFold(d.Spec.NetworkAddress, nwkHex) {
			// Warm the IEEE cache while we have it
			if d.Spec.IEEEAddress != "" {
				n.cacheMux.Lock()
				n.ieeeCache[d.Spec.IEEEAddress] = d.Name
				n.cacheMux.Unlock()
			}
			return d, nil
		}
	}
	return nil, nil
}

// zclCommandToAction maps a ZclMessage command to a named action string.
// Returns "" for non-action messages (sensor reports, attribute reads, etc.).
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
