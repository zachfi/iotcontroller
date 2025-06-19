package router

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"sync"

	"github.com/grafana/dskit/services"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	kubeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/zachfi/zkit/pkg/boundedwaitgroup"
	"github.com/zachfi/zkit/pkg/tracing"

	"github.com/zachfi/iotcontroller/pkg/iot/routers/ispindel"
	"github.com/zachfi/iotcontroller/pkg/iot/routers/zigbee2mqtt"
	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

var module = "router"

type RouteTypes int

const (
	Zigbee2MQTT RouteTypes = iota
	Ispindel
)

type Router struct {
	services.Service
	mtx sync.Mutex

	cfg    *Config
	logger *slog.Logger
	tracer trace.Tracer

	kubeclient kubeclient.Client

	zonekeeperClient iotv1proto.ZoneKeeperServiceClient
	itemCh           chan *item

	regexps map[string]*regexp.Regexp
	routers map[RouteTypes]any
}

type item struct {
	ctx     context.Context
	Path    string
	Payload []byte
}

/* type RouteFunc func([]byte, ...interface{}) error */

func New(cfg Config, logger *slog.Logger, kubeclient kubeclient.Client, zonekeeperClient iotv1proto.ZoneKeeperServiceClient) (*Router, error) {
	c := &Router{
		cfg:              &cfg,
		logger:           logger.With("module", module),
		tracer:           otel.Tracer(module, trace.WithInstrumentationAttributes(attribute.String("module", module))),
		kubeclient:       kubeclient,
		zonekeeperClient: zonekeeperClient,
		itemCh:           make(chan *item, 10000),
		regexps:          make(map[string]*regexp.Regexp, 10),
		routers:          make(map[RouteTypes]any),
	}

	z2m, err := zigbee2mqtt.New(logger, c.tracer, kubeclient, c.zonekeeperClient)
	if err != nil {
		return nil, err
	}

	c.routers[Zigbee2MQTT] = z2m

	isp, err := ispindel.New(logger, c.tracer, kubeclient, c.zonekeeperClient)
	if err != nil {
		return nil, err
	}

	c.routers[Ispindel] = isp

	c.Service = services.NewBasicService(c.starting, c.running, c.stopping)
	return c, nil
}

func (r *Router) send(ctx context.Context, path string, payload []byte) error {
	var (
		err      error
		deviceID string
	)

	span := trace.SpanFromContext(ctx)
	span.SetAttributes(attribute.String("path", path))
	defer tracing.ErrHandler(span, err, "router send failed", r.logger)

	if errors.Is(ctx.Err(), context.Canceled) {
		span.AddEvent("context canceled")
	}

	switch {
	// Zigbee routes
	case r.match(path, "zigbee2mqtt/([^/]+)", &deviceID):
		if err = r.zigbee2Mqtt().DeviceRoute(ctx, payload, deviceID); err != nil {
			return err
		}
	case r.match(path, "zigbee2mqtt/([^/]+)/set", &deviceID):
		// ignore
	case r.match(path, "zigbee2mqtt/bridge/devices"):
		if err = r.zigbee2Mqtt().DevicesRoute(ctx, payload); err != nil {
			return err
		}
	case r.match(path, "zigbee2mqtt/bridge/state"):
		if err = r.zigbee2Mqtt().BridgeStateRoute(ctx, payload); err != nil {
			return err
		}
	case r.match(path, "zigbee2mqtt/bridge/info"):
	case r.match(path, "zigbee2mqtt/bridge/logging"):
	case r.match(path, "zigbee2mqtt/bridge/extensions"):

		// iSpindel
	case r.match(path, "ispindel/([^/]+)/tilt", &deviceID):
		r.ispindel().TiltRoute(ctx, payload, deviceID)
	case r.match(path, "ispindel/([^/]+)/temperature", &deviceID):
		r.ispindel().TemperatureRoute(ctx, payload, deviceID)
	case r.match(path, "ispindel/([^/]+)/battery", &deviceID):
		r.ispindel().BatteryRoute(ctx, payload, deviceID)
	case r.match(path, "ispindel/([^/]+)/gravity", &deviceID):
		r.ispindel().SpecificGravityRoute(ctx, payload, deviceID)
	case r.match(path, "ispindel/([^/]+)/RSSI", &deviceID):
		r.ispindel().RSSI(ctx, payload, deviceID)
		// TODO:
		/*
			ispindel/brewHydroBlack/tilt 26.08455
			ispindel/brewHydroBlack/temperature 9.6875
			ispindel/brewHydroBlack/temp_units C
			ispindel/brewHydroBlack/battery 3.534932
			ispindel/brewHydroBlack/gravity 1.002085
			ispindel/brewHydroBlack/interval 900
			ispindel/brewHydroBlack/RSSI -91
		*/
	default:
		r.logger.Debug("unhandled route", "path", path)
		span.AddEvent("unhandled route")
		metricUnhandledRoute.WithLabelValues(path).Inc()
	}

	return nil
}

func (r *Router) Send(ctx context.Context, req *iotv1proto.SendRequest) (*iotv1proto.SendResponse, error) {
	attributes := []attribute.KeyValue{
		attribute.String("path", req.Path),
	}

	ctx, span := r.tracer.Start(ctx, "Router.Send", trace.WithAttributes(attributes...), trace.WithSpanKind(trace.SpanKindServer))
	span.SetAttributes(attributes...)
	defer span.End()

	go func() {
		// Use a new span context, so that the context is not canceled when the
		// request is finished.  Link to the parent.
		spanCtx, _ := r.tracer.Start(context.Background(), "Router.Send",
			trace.WithSpanKind(trace.SpanKindInternal),
			trace.WithLinks(trace.LinkFromContext(ctx)),
		)
		// The span context is passed through, and rehydrated in the receiver goroutine.
		r.itemCh <- &item{
			ctx:     spanCtx,
			Path:    req.Path,
			Payload: req.Message,
		}
	}()

	return &iotv1proto.SendResponse{}, nil
}

func (r *Router) starting(_ context.Context) error {
	return nil
}

func (r *Router) running(ctx context.Context) error {
	bg := boundedwaitgroup.New(r.cfg.ReportConcurrency)

	for {
		select {
		case <-ctx.Done():
			bg.Wait()
			return nil
		case i := <-r.itemCh:

			bg.Add(1)
			metricActiveReceiverRoutines.Inc()
			go func() {
				defer bg.Done()
				defer metricActiveReceiverRoutines.Dec()
				var err error
				_, span := r.tracer.Start(i.ctx, "Router.receiver")
				defer tracing.ErrHandler(span, err, "send failed", r.logger)
				err = r.send(i.ctx, i.Path, i.Payload)
				if err != nil {
					r.logger.Error("failed to send item", "path", i.Path, "err", err)
				}
			}()
		}
	}
}

func (r *Router) stopping(_ error) error {
	close(r.itemCh)
	return nil
}

// match reports whether path matches regex ^pattern$, and if it matches,
// assigns any capture groups to the *string or *int vars.
func (r *Router) match(path, pattern string, vars ...any) bool {
	regex, err := r.compileCached(pattern)
	if err != nil {
		r.logger.Error(err.Error())
		return false
	}
	matches := regex.FindStringSubmatch(path)
	if len(matches) <= 0 {
		return false
	}
	for i, match := range matches[1:] {
		switch p := vars[i].(type) {
		case *string:
			*p = match
		case *int:
			n, err := strconv.Atoi(match)
			if err != nil {
				return false
			}
			*p = n
		default:
			r.logger.Error("unsupported type for regex capture group", "type", p, "pattern", pattern)
			return false
		}
	}
	return true
}

func (r *Router) compileCached(pattern string) (*regexp.Regexp, error) {
	var (
		regex = r.regexps[pattern]
		err   error
	)

	r.mtx.Lock()
	defer r.mtx.Unlock()

	if regex == nil {
		regex, err = regexp.Compile("^" + pattern + "$")
		if err != nil {
			return nil, fmt.Errorf("failed to compile regex %q: %w", pattern, err)
		}
		r.regexps[pattern] = regex
	}
	return regex, nil
}

func (r *Router) zigbee2Mqtt() *zigbee2mqtt.Zigbee2Mqtt {
	return r.routers[Zigbee2MQTT].(*zigbee2mqtt.Zigbee2Mqtt)
}

func (r *Router) ispindel() *ispindel.Ispindel {
	return r.routers[Ispindel].(*ispindel.Ispindel)
}
