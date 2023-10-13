package zonekeeper

import (
	"context"
	"log/slog"
	"sync"

	"github.com/grafana/dskit/services"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	apiv1 "github.com/zachfi/iotcontroller/api/v1"
	"github.com/zachfi/iotcontroller/modules/mqttclient"
	"github.com/zachfi/iotcontroller/pkg/iot"
	iotv1 "github.com/zachfi/iotcontroller/proto/iot/v1"
)

const (
	module    = "zonekeeper"
	namespace = "iot"
)

func New(cfg Config, logger *slog.Logger, mqttClient *mqttclient.MQTTClient, kubeclient client.Client) (*ZoneKeeper, error) {
	z := &ZoneKeeper{
		cfg:        &cfg,
		logger:     logger.With("module", module),
		tracer:     otel.Tracer(module),
		mqttClient: mqttClient,
		kubeclient: kubeclient,
		// zoneStates: make(map[string]iotv1.ZoneState),
		zones: make(map[string]*iot.Zone),
	}

	z.Service = services.NewBasicService(z.starting, z.running, z.stopping)

	return z, nil
}

type ZoneKeeper struct {
	services.Service
	sync.Mutex

	cfg    *Config
	logger *slog.Logger
	tracer trace.Tracer

	mqttClient *mqttclient.MQTTClient
	kubeclient client.Client

	// handlers           []Handler
	// colorTempScheduler ColorTempSchedulerFunc

	// zoneStates map[string]iotv1.ZoneState
	zones map[string]*iot.Zone
}

func (z *ZoneKeeper) SetState(ctx context.Context, req *iotv1.ZoneServiceSetStateRequest) (*iotv1.ZoneServiceSetStateResponse, error) {
	_, span := z.tracer.Start(
		ctx,
		"ZoneKeeper.SetState",
		trace.WithSpanKind(trace.SpanKindServer),
	)
	defer span.End()

	span.SetAttributes(
		attribute.String("name", req.Name),
	)

	z.Lock()
	if _, ok := z.zones[req.Name]; !ok {
		z.zones[req.Name] = iot.NewZone(req.Name)
	}
	z.zones[req.Name].SetState(ctx, req.State)
	z.Unlock()

	err := z.Flush(ctx, req.Name)

	return &iotv1.ZoneServiceSetStateResponse{}, err
}

// Flush handles pushing the current state out to each of the hnadlers.
func (z *ZoneKeeper) Flush(ctx context.Context, name string) error {
	span := trace.SpanFromContext(ctx)
	// span.SetName("ZoneKeeper.Flush")
	defer span.End()

	z.Lock()
	state := z.zones[name].State()
	z.Unlock()

	// switch state {
	// case iotv1.ZoneState_ZONE_STATE_ON:
	// case iotv1.ZoneState_ZONE_STATE_OFF:
	// case iotv1.ZoneState_ZONE_STATE_OFFTIMER:
	// case iotv1.ZoneState_ZONE_STATE_COLOR:
	// case iotv1.ZoneState_ZONE_STATE_RANDOMCOLOR:
	// case iotv1.ZoneState_ZONE_STATE_NIGHTVISION:
	// case iotv1.ZoneState_ZONE_STATE_EVENINGVISION:
	// case iotv1.ZoneState_ZONE_STATE_MORNINGVISION:
	// default:
	// 	z.logger.Warn("unknown zone state", "state", state.String())
	// }

	var zone apiv1.Zone
	nsn := types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}

	_, getSpan := z.tracer.Start(ctx, "iot.Zone/Get")
	err := z.kubeclient.Get(ctx, nsn, &zone)
	if err != nil {
		if apierrors.IsNotFound(err) {
			zone.SetName(name)
			zone.SetNamespace(namespace)

			getSpan.AddEvent("zone not found")

			_, createSpan := z.tracer.Start(ctx, "iot.Zone/Create")
			err = z.kubeclient.Create(ctx, &zone)
			if err != nil {
				return errHandler(createSpan, err)
			}
			createSpan.AddEvent("created")
			createSpan.End()
		}

		return errHandler(getSpan, err)
	}
	getSpan.End()

	zone.Status.State = state.String()

	_, statusUpdateSpan := z.tracer.Start(ctx, "iot.Zone/Status/Update")
	if err = z.kubeclient.Status().Update(ctx, &zone); err != nil {
		statusUpdateSpan.SetStatus(codes.Error, err.Error())
		statusUpdateSpan.End()
		return err
	}
	statusUpdateSpan.End()

	// switch z.state {
	// case ZoneState_ON:
	// 	return z.handleOn(ctx)
	// case ZoneState_OFF:
	// 	return z.handleOff(ctx)
	// case ZoneState_COLOR:
	// 	for _, h := range z.handlers {
	// 		err := h.SetColor(ctx, z.name, z.color)
	// 		if err != nil {
	// 			return fmt.Errorf("%s color: %w", z.name, ErrHandlerFailed)
	// 		}
	// 	}
	// case ZoneState_RANDOMCOLOR:
	// 	for _, h := range z.handlers {
	// 		err := h.RandomColor(ctx, z.name, z.colorPool)
	// 		if err != nil {
	// 			return fmt.Errorf("%s random color: %w", z.name, ErrHandlerFailed)
	// 		}
	// 	}
	// case ZoneState_NIGHTVISION:
	// 	z.color = nightVisionColor
	// 	return z.handleColor(ctx)
	// case ZoneState_EVENINGVISION:
	// 	z.colorTemp = eveningTemp
	// 	return z.handleColorTemperature(ctx)
	// case ZoneState_MORNINGVISION:
	// 	z.colorTemp = morningTemp
	// 	return z.handleColorTemperature(ctx)
	// }
	//
	return nil
}

func (z *ZoneKeeper) starting(ctx context.Context) error {
	// go z.zoneUpdaterLoop(ctx)

	return nil
}

func (z *ZoneKeeper) running(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

func (z *ZoneKeeper) stopping(_ error) error {
	return nil
}

// func (z *ZoneKeeper) zoneUpdaterLoop(ctx context.Context) {
// 	ticker := time.NewTicker(10 * time.Second)
//
// 	for {
// 		select {
// 		case <-ctx.Done():
// 			return
// 		case <-ticker.C:
// 			var deviceList *apiv1.DeviceList
// 			opts := &client.ListOptions{
// 				Namespace: namespace,
// 			}
//
// 			// TODO: update/create each zone with its list of devices.
// 			err := z.kubeclient.List(ctx, deviceList, opts)
// 			if err != nil {
// 				z.logger.Error("failed to list apiv1.Devices", "err", err)
// 			}
//
// 			for _, d := range deviceList.Items {
//
// 				if _, ok := z.zones[d.Spec.Zone]; !ok {
// 					z.zones[d.Spec.Zone] = iot.NewZone(d.Spec)
// 				}
//
// 				z.zones[d.Spec.Zone].SetDevice(d)
// 			}
// 		}
// 	}
// }

func errHandler(span trace.Span, err error) error {
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
	return err
}
