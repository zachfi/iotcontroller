package router

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"regexp"
	"strconv"
	"sync"

	"github.com/grafana/dskit/services"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	kubeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/zachfi/iotcontroller/pkg/iot/messages/zigbee2mqtt"
	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
	"github.com/zachfi/zkit/pkg/boundedwaitgroup"
	"github.com/zachfi/zkit/pkg/tracing"
)

var module = "router"

type RouteTypes int

const (
	Zigbee2MQTT RouteTypes = iota
)

type Router struct {
	services.Service
	mtx sync.Mutex

	cfg    *Config
	logger *slog.Logger
	tracer trace.Tracer

	kubeclient kubeclient.Client

	// TODO: think about if the router is the first and only stop, then do we
	// metric in the Telemetry and then will need to call.
	/* telemetryClient telemetryv1proto.TelemetryServiceClient */

	zonekeeperClient iotv1proto.ZoneKeeperServiceClient
	queue            chan *iotv1proto.RouteRequest

	regexps map[string]*regexp.Regexp
	routers map[RouteTypes]interface{}
}

/* type RouteFunc func([]byte, ...interface{}) error */

func New(cfg Config, logger *slog.Logger, kubeclient kubeclient.Client, conn *grpc.ClientConn) (*Router, error) {
	c := &Router{
		cfg:              &cfg,
		logger:           logger.With("module", module),
		tracer:           otel.Tracer(module),
		kubeclient:       kubeclient,
		zonekeeperClient: iotv1proto.NewZoneKeeperServiceClient(conn),
		queue:            make(chan *iotv1proto.RouteRequest, 10000),
		regexps:          make(map[string]*regexp.Regexp, 10),
		routers:          make(map[RouteTypes]interface{}),
	}

	z2m, err := zigbee2mqtt.New(logger, c.tracer, kubeclient, conn)
	if err != nil {
		return nil, err
	}

	c.routers[Zigbee2MQTT] = z2m

	c.Service = services.NewIdleService(c.starting, c.stopping)
	return c, nil
}

func (r *Router) Send(ctx context.Context, path string, payload []byte) error {
	if errors.Is(ctx.Err(), context.Canceled) {
		return nil
	}

	var (
		err      error
		deviceID string
	)

	switch {
	case r.match(path, "zigbee2mqtt/([^/]+)", &deviceID):
		if err = r.zigbee2Mqtt().DeviceRoute(ctx, payload, deviceID); err != nil {
			return err
		}
	case r.match(path, "zigbee2mqtt/([^/]+)/set", &deviceID):
		// TODO:
	case r.match(path, "zigbee2mqtt/bridge/devices"):
		if err = r.zigbee2Mqtt().DevicesRoute(ctx, payload); err != nil {
			return err
		}
	case r.match(path, "zigbee2mqtt/bridge/state"):
		// TODO:
	case r.match(path, "zigbee2mqtt/bridge/info"):
	case r.match(path, "zigbee2mqtt/bridge/logging"):
	case r.match(path, "zigbee2mqtt/bridge/extensions"):
	default:
		metricUnhandledRoute.WithLabelValues(path).Inc()
	}

	return nil
}

func (r *Router) Route(stream iotv1proto.RouteService_RouteServer) error {
	for {
		req, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			// Close the connection and return the response to the client
			return stream.SendAndClose(&iotv1proto.RouteResponse{})
		}

		if errors.Is(err, context.Canceled) {
			return nil
		}

		if err != nil {
			r.logger.Error("stream error", "err", err)
			return err
		}

		r.queue <- req
		metricQueueLength.With(prometheus.Labels{}).Set(float64(len(r.queue)))
	}
}

func (r *Router) routeReceiver(ctx context.Context) {
	var (
		req *iotv1proto.RouteRequest
		bg  = boundedwaitgroup.New(r.cfg.ReportConcurrency)
	)

	for {
		select {
		case <-ctx.Done():
			return
		case req = <-r.queue:

			bg.Add(1)
			go func() {
				bg.Done()

				ctx, span := r.tracer.Start(context.Background(), "Telemetry.ReportIOTDevice", trace.WithSpanKind(trace.SpanKindServer))
				err := r.Send(ctx, req.Path, req.Message)
				tracing.ErrHandler(span, err, "route failed", r.logger)
			}()
		}
	}
}

func (r *Router) starting(ctx context.Context) error {
	go r.routeReceiver(ctx)
	return nil
}

func (r *Router) stopping(_ error) error {
	return nil
}

// match reports whether path matches regex ^pattern$, and if it matches,
// assigns any capture groups to the *string or *int vars.
func (r *Router) match(path, pattern string, vars ...interface{}) bool {
	regex := r.mustCompileCached(pattern)
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
			panic("vars must be *string or *int")
		}
	}
	return true
}

func (r *Router) mustCompileCached(pattern string) *regexp.Regexp {
	r.mtx.Lock()
	defer r.mtx.Unlock()

	regex := r.regexps[pattern]
	if regex == nil {
		regex = regexp.MustCompile("^" + pattern + "$")
		r.regexps[pattern] = regex
	}
	return regex
}

func (r *Router) zigbee2Mqtt() *zigbee2mqtt.Zigbee2Mqtt {
	return r.routers[Zigbee2MQTT].(*zigbee2mqtt.Zigbee2Mqtt)
}
