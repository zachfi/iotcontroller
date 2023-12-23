package hookreceiver

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/grafana/dskit/services"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"

	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

const (
	module         = "hookreceiver"
	zoneLabel      = "zone"
	alertnameLabel = "alertname"
)

type HookReceiver struct {
	services.Service
	cfg *Config

	logger *slog.Logger
	tracer trace.Tracer

	alertReceiverClient iotv1proto.AlertReceiverServiceClient
}

func New(cfg Config, logger *slog.Logger, conn *grpc.ClientConn) (*HookReceiver, error) {
	h := &HookReceiver{
		cfg:                 &cfg,
		logger:              logger.With("module", module),
		tracer:              otel.Tracer(module),
		alertReceiverClient: iotv1proto.NewAlertReceiverServiceClient(conn),
	}

	h.Service = services.NewIdleService(h.starting, h.stopping)
	return h, nil
}

func (h *HookReceiver) Handler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	spanCtx, span := h.tracer.Start(
		ctx,
		"HookReceiver.Handler",
		trace.WithSpanKind(trace.SpanKindServer),
	)
	defer span.End()

	dec := json.NewDecoder(r.Body)
	defer r.Body.Close()

	var m HookMessage
	if err := dec.Decode(&m); err != nil {
		http.Error(w, "invalid request body", 400)
		h.logger.Error("invalid request body", "err", err)
		return
	}

	// {Version:4 GroupKey:{}/{}:{zone="tent"} Status:firing Receiver:iotcontroller GroupLabels:map[zone:tent] CommonLabels:map[cluster:k severity:critical zone:tent] CommonAnnotations:map[] ExternalURL:http://alertmanager.metric.svc.cluster.znet/alertmanager Alerts:[{Labels:map[alertname:highTemp cluster:k severity:critical zone:tent] Annotations:map[description:Temperature outside nominal range at tent (current value: 29.6) summary:High Tent Temperature] StartsAt:2023-09-14T15:10:16.419Z EndsAt:0001-01-01T00:00:00Z} {Labels:map[alertname:lowHumidity cluster:k severity:critical zone:tent] Annotations:map[description:Humidity outside nominal range at  (current value: 39.2) summary:Low Tent Humidity] StartsAt:2023-09-14T21:32:16.419Z EndsAt:0001-01-01T00:00:00Z}]}

	h.logger.Debug("hook", "msg", fmt.Sprintf("%+v", m))

	labels := make(map[string]string)
	for k, v := range m.GroupLabels {
		labels[k] = v
	}

	for k, v := range m.CommonLabels {
		labels[k] = v
	}

	for k, v := range labels {
		span.SetAttributes(
			attribute.String(k, v),
		)
	}

	span.SetAttributes(
		attribute.Int("alerts", len(m.Alerts)),
	)

	for _, alert := range m.Alerts {
		var name string
		var zone string

		for k, v := range alert.Labels {
			switch k {
			case zoneLabel:
				zone = v
			case alertnameLabel:
				name = v
			}
		}

		if name == "" || zone == "" {
			span.SetAttributes(attribute.Bool("skipped", true))

			continue
		}

		in := &iotv1proto.AlertRequest{
			Group:  m.GroupKey,
			Labels: labels,
			Name:   name,
			Status: m.Status,
			Zone:   zone,
		}

		hookreceiverReceivedTotal.WithLabelValues(name, zone).Inc()

		for k, v := range alert.Labels {
			if k == alertnameLabel {
				in.Labels[k] = v
			}
		}

		_, err := h.alertReceiverClient.Alert(spanCtx, in)
		if err != nil {
			h.logger.Error("failed to send alert", "err", err)
		}
	}
}

func (h *HookReceiver) starting(ctx context.Context) error {
	return nil
}

func (h *HookReceiver) stopping(_ error) error {
	return nil
}

type HookMessage struct {
	Version           string            `json:"version"`
	GroupKey          string            `json:"groupKey"`
	Status            string            `json:"status"`
	Receiver          string            `json:"receiver"`
	GroupLabels       map[string]string `json:"groupLabels"`
	CommonLabels      map[string]string `json:"commonLabels"`
	CommonAnnotations map[string]string `json:"commonAnnotations"`
	ExternalURL       string            `json:"externalURL"`
	Alerts            []Alert           `json:"alerts"`
}

type Label struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Alert is a single alert.
type Alert struct {
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	StartsAt    string            `json:"startsAt,omitempty"`
	EndsAt      string            `json:"EndsAt,omitempty"`
}
