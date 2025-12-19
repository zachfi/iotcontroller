package hookreceiver

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/grafana/dskit/services"

	"github.com/prometheus/alertmanager/notify/webhook"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/zachfi/zkit/pkg/tracing"

	"github.com/zachfi/iotcontroller/pkg/iot"
	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

const (
	module = "hookreceiver"
)

type HookReceiver struct {
	services.Service
	cfg *Config

	logger *slog.Logger
	tracer trace.Tracer

	eventReceiverClient iotv1proto.EventReceiverServiceClient
}

func New(cfg Config, logger *slog.Logger, eventReceiverClient iotv1proto.EventReceiverServiceClient) (*HookReceiver, error) {
	h := &HookReceiver{
		cfg:                 &cfg,
		logger:              logger.With("module", module),
		tracer:              otel.Tracer(module),
		eventReceiverClient: eventReceiverClient,
	}

	h.Service = services.NewIdleService(h.starting, h.stopping)
	return h, nil
}

func (h *HookReceiver) Handler(w http.ResponseWriter, r *http.Request) {
	var err error

	ctx, span := h.tracer.Start(
		r.Context(),
		"HookReceiver.Handler",
		trace.WithSpanKind(trace.SpanKindServer),
	)
	defer tracing.ErrHandler(span, err, "HookReceiver.Handler", h.logger)

	dec := json.NewDecoder(r.Body)
	defer r.Body.Close()

	var m webhook.Message
	if err = dec.Decode(&m); err != nil {
		http.Error(w, "invalid request body", 400)
		h.logger.Error("invalid request body", "err", err)
		return
	}

	// {Version:4 GroupKey:{}/{}:{zone="tent"} Status:firing Receiver:iotcontroller GroupLabels:map[zone:tent] CommonLabels:map[cluster:k severity:critical zone:tent] CommonAnnotations:map[] ExternalURL:http://alertmanager.metric.svc.cluster.znet/alertmanager Alerts:[{Labels:map[alertname:highTemp cluster:k severity:critical zone:tent] Annotations:map[description:Temperature outside nominal range at tent (current value: 29.6) summary:High Tent Temperature] StartsAt:2023-09-14T15:10:16.419Z EndsAt:0001-01-01T00:00:00Z} {Labels:map[alertname:lowHumidity cluster:k severity:critical zone:tent] Annotations:map[description:Humidity outside nominal range at  (current value: 39.2) summary:Low Tent Humidity] StartsAt:2023-09-14T21:32:16.419Z EndsAt:0001-01-01T00:00:00Z}]}

	/*
		{Version:4 GroupKey:{}/{}:{} Status:firing Receiver:iotcontroller GroupLabels:map[] CommonLabels:map[cluster:k] CommonAnnotations:map[] ExternalURL:https://admin.ops.svc.cluster.znet/alertmanager Alerts:[{Labels:map[alertname:CoreDNSForwardHealthcheckFailureCount cluster:k container:coredns job:kube-system/kube-dns namespace:kube-system severity:warning to:10.16.15.10:53] Annotations:map[description:CoreDNS health checks have failed to upstream server 10.16.15.10:53. runbook_url:https://github.com/povilasv/coredns-mixin/tree/master/runbook.md#alert-name-corednsforwardhealthcheckfailurecount summary:CoreDNS health checks have failed to upstream server.] StartsAt:2023-12-26T16:28:14.427Z EndsAt:0001-01-01T00:00:00Z} {Labels:map[alertname:NodeSystemSaturation cluster:k instance:k4 job:metric/node-exporter namespace:metric severity:warning] Annotations:map[description:System load per core at k4 has been above 2 for the last 15 minutes, is currently at 3.26.
		This might indicate this instance resources saturation and can cause it becoming unresponsive.
		 summary:System saturated, load per core is very high.] StartsAt:2023-12-26T19:47:09.164Z EndsAt:0001-01-01T00:00:00Z} {Labels:map[alertname:CPUThrottlingHigh cluster:k container:query-frontend namespace:trace pod:query-frontend-6456dd6bcd-kd8fh severity:info] Annotations:map[description:30.65% throttling of CPU in namespace trace for container query-frontend in pod query-frontend-6456dd6bcd-kd8fh. runbook_url:https://github.com/kubernetes-monitoring/kubernetes-mixin/tree/master/runbook.md#alert-name-cputhrottlinghigh summary:Processes experience elevated CPU throttling.] StartsAt:2023-12-27T22:04:22.139Z EndsAt:0001-01-01T00:00:00Z}]}
	*/

	/* h.logger.Debug("hook", "msg", fmt.Sprintf("%+v", m)) */

	commonLabels := make(map[string]string, 7)
	for k, v := range m.GroupLabels {
		commonLabels[k] = v
	}

	for k, v := range m.CommonLabels {
		commonLabels[k] = v
	}

	for k, v := range commonLabels {
		span.SetAttributes(
			attribute.String(k, v),
		)
	}

	span.SetAttributes(attribute.Int("alerts", len(m.Alerts)))

	for i, alert := range m.Alerts {
		var (
			name string
			zone string
		)

		span.SetAttributes(attribute.String(iot.StatusLabel, m.Status))

		for k, v := range alert.Labels {
			switch k {
			case iot.ZoneLabel:
				zone = v
			case iot.AlertNameLabel:
				name = v
			}
		}

		if zone == "" && name == "" {
			span.SetAttributes(attribute.String("skipped", "name or zone not set"))
			continue
		}

		span.SetAttributes(attribute.String(fmt.Sprintf("%s_%d", iot.ZoneLabel, i), zone))
		span.SetAttributes(attribute.String(fmt.Sprintf("%s_%d", iot.AlertNameLabel, i), name))

		hookreceiverReceivedTotal.WithLabelValues(name, zone).Inc()

		a := &iotv1proto.AlertRequest{
			Name:   name,
			Status: m.Status,
			Zone:   zone,
		}

		_, err := h.eventReceiverClient.Alert(ctx, a)
		if err != nil {
			hookreceiverReceiverErrorsTotal.WithLabelValues(name, zone).Inc()
			h.logger.Error("failed to send event", "err", err)
		}
	}
}

func (h *HookReceiver) starting(_ context.Context) error {
	return nil
}

func (h *HookReceiver) stopping(_ error) error {
	return nil
}
