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
	"k8s.io/client-go/util/retry"
	kubeclient "sigs.k8s.io/controller-runtime/pkg/client"

	apiv1 "github.com/zachfi/iotcontroller/api/v1"
	"github.com/zachfi/iotcontroller/pkg/iot"
	"github.com/zachfi/iotcontroller/pkg/iot/bindings"
	"github.com/zachfi/iotcontroller/pkg/iot/events"
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
	matcher             *bindings.Matcher

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
		matcher: bindings.New(kubeclient, "iot", logger).
			WithActivateFunc(func(ctx context.Context, condition string) error {
				_, err := eventReceiverClient.ActivateCondition(ctx, &iotv1proto.ActivateConditionRequest{
					Condition: condition,
				})
				return err
			}),
		ieeeCache: make(map[string]string),
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

	// Emit sensor metrics for any attribute reports in this message
	// (temperature, humidity, battery, link quality, …). This runs
	// regardless of whether a Binding matches downstream — the metric
	// stream is independent of the action/control pipeline. A device
	// with no iot/zone label still gets a gauge written with zone="",
	// which is useful for newly-paired devices before they're zoned
	// (the closet SNZB-02 worked this way on 2026-05-15).
	zone := device.Labels[iot.DeviceZoneLabel]
	emitAttributeMetrics(n.logger, zclMsg, device.Name, zone)

	// Normalize the ZclMessage into the same DeviceEvent shape used by
	// zigbee2mqtt; one Binding spec then matches both transports.
	evs := events.FromZclMessage(zclMsg, device)
	for _, ev := range evs {
		if n.eventReceiverClient == nil {
			break
		}
		cond := n.matcher.FindCondition(ctx, ev)
		if cond == "" {
			continue
		}
		span.SetAttributes(
			attribute.String("device", device.Name),
			attribute.String("binding.property", ev.Property),
			attribute.String("binding.value", ev.Value),
			attribute.String("binding.condition", cond),
		)
		_, err = n.eventReceiverClient.ActivateCondition(ctx, &iotv1proto.ActivateConditionRequest{
			Condition: cond,
		})
		return err
	}

	// Fallback path: no Binding matched. Pre-Stage-3 we dispatched
	// through ZoneKeeper.ActionHandler, which switch-cased ~30 action
	// strings to default zone behaviour. Now the action vocabulary is
	// expressed entirely by Bindings + Conditions, and this path is
	// observability-only: record the unhandled action in the fallback
	// counter (per-device migration thermometer) and log at debug.
	action := ""
	if len(evs) > 0 && evs[0].Property == events.PropertyAction {
		action = evs[0].Value
	}
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
	span.AddEvent("unhandled action (no Binding matched)")

	metricFallbackTotal.WithLabelValues(device.Name, action, zone).Inc()

	n.logger.Debug("unhandled action (no Binding matched)",
		slog.String("device", device.Name),
		slog.String("action", action),
		slog.String("zone", zone),
	)
	return nil
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

	// Wrap the read-modify-update in RetryOnConflict so a concurrent
	// write to the same Device (e.g. the coordinator's annotation-clear
	// patch racing this update, which was observed 2026-05-15 during
	// the opportunistic re-interview path) re-fetches the fresh
	// resourceVersion and retries instead of dropping the interview
	// result. Standard k8s read-modify-update pattern; default backoff
	// retries 5 times with exponential 10ms→160ms delays.
	var device *apiv1.Device
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		d, gerr := iotutil.GetOrCreateAPIDevice(ctx, n.kubeclient, deviceName)
		if gerr != nil {
			return fmt.Errorf("getting/creating device %q: %w", deviceName, gerr)
		}

		// Populate spec from interview
		d.Spec.IEEEAddress = ieee
		d.Spec.NetworkAddress = fmt.Sprintf("0x%04x", result.GetNetworkAddress())

		// Only set Type from ZDO for coordinator/router roles using the proto enum name.
		// End-device functional type (light, button, sensor) is not known from the interview
		// alone — it must be set separately (manually or via model mapping).
		// We preserve any existing Type so manual assignments are not overwritten.
		if d.Spec.Type == "" {
			switch result.GetDeviceType() {
			case zigbeev1proto.DeviceType_DEVICE_TYPE_COORDINATOR:
				d.Spec.Type = "DEVICE_TYPE_COORDINATOR"
			case zigbeev1proto.DeviceType_DEVICE_TYPE_ROUTER:
				d.Spec.Type = "DEVICE_TYPE_ROUTER"
				// End-device: leave Type empty — set by operator or future model-based mapping.
			}
		}

		if result.GetManufacturerName() != "" {
			d.Spec.Vendor = result.GetManufacturerName()
			d.Spec.ManufactureName = result.GetManufacturerName()
		}
		if result.GetModelId() != "" {
			d.Spec.Model = result.GetModelId()
			d.Spec.ModelID = result.GetModelId()
		}
		if result.GetDateCode() != "" {
			d.Spec.DateCode = result.GetDateCode()
		}
		if result.GetPowerSource() != zigbeev1proto.PowerSource_POWER_SOURCE_UNSPECIFIED {
			d.Spec.PowerSource = result.GetPowerSource().String()
		}

		if uerr := n.kubeclient.Update(ctx, d); uerr != nil {
			return uerr // pass through unwrapped so RetryOnConflict's IsConflict check works
		}
		device = d
		return nil
	})
	if err != nil {
		return fmt.Errorf("updating device spec: %w", err)
	}

	// Status update: same retry pattern since it shares the
	// resourceVersion contention with the spec update on devices that
	// receive frequent annotations/labels from elsewhere.
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		d, gerr := iotutil.GetOrCreateAPIDevice(ctx, n.kubeclient, deviceName)
		if gerr != nil {
			return fmt.Errorf("refreshing device: %w", gerr)
		}
		d.Status.SoftwareBuildID = result.GetSoftwareBuildId()
		return n.kubeclient.Status().Update(ctx, d)
	})
	if err != nil {
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

// (zclCommandToAction lives in pkg/iot/events; the router consumes
// events.FromZclMessage so the canonical ZCL→action vocabulary stays
// in the events package.)
