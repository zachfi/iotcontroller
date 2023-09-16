package hookreceiver

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/grafana/dskit/services"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"

	iotv1 "github.com/zachfi/iotcontroller/proto/iot/v1"
)

type HookReceiver struct {
	services.Service
	cfg *Config

	logger *slog.Logger
	tracer trace.Tracer

	alertReceiverClient iotv1.AlertReceiverServiceClient
}

func New(cfg Config, logger *slog.Logger, conn *grpc.ClientConn) (*HookReceiver, error) {
	h := &HookReceiver{
		cfg:                 &cfg,
		logger:              logger.With("module", "hookreceiver"),
		tracer:              otel.Tracer("hookreceiver"),
		alertReceiverClient: iotv1.NewAlertReceiverServiceClient(conn),
	}

	h.Service = services.NewIdleService(h.starting, h.stopping)
	return h, nil
}

func (h *HookReceiver) Handler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	_, span := h.tracer.Start(
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
		return
	}

	// {Version:4 GroupKey:{}/{}:{zone="tent"} Status:firing Receiver:iotcontroller GroupLabels:map[zone:tent] CommonLabels:map[cluster:k severity:critical zone:tent] CommonAnnotations:map[] ExternalURL:http://alertmanager.metric.svc.cluster.znet/alertmanager Alerts:[{Labels:map[alertname:highTemp cluster:k severity:critical zone:tent] Annotations:map[description:Temperature outside nominal range at tent (current value: 29.6) summary:High Tent Temperature] StartsAt:2023-09-14T15:10:16.419Z EndsAt:0001-01-01T00:00:00Z} {Labels:map[alertname:lowHumidity cluster:k severity:critical zone:tent] Annotations:map[description:Humidity outside nominal range at  (current value: 39.2) summary:Low Tent Humidity] StartsAt:2023-09-14T21:32:16.419Z EndsAt:0001-01-01T00:00:00Z}]}

	labels := make(map[string]string)
	for k, v := range m.GroupLabels {
		labels[k] = v
	}

	for k, v := range m.CommonLabels {
		labels[k] = v
	}

	for _, l := range m.Alerts {
		var name string
		if v, ok := l.Labels["alertname"]; ok {
			name = v
		} else {
			continue
		}

		in := &iotv1.AlertRequest{
			Name:   name,
			Group:  m.GroupKey,
			Status: m.Status,
			Labels: labels,
		}

		for k, v := range l.Labels {
			if _, ok := in.Labels[k]; !ok {
				in.Labels[k] = v
			}
		}

		_, err := h.alertReceiverClient.Alert(ctx, in)
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

// Alert is a single alert.
type Alert struct {
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	StartsAt    string            `json:"startsAt,omitempty"`
	EndsAt      string            `json:"EndsAt,omitempty"`
}
