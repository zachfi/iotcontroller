package hookreceiver

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	hookreceiverAlertsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "iotcontroller_hookreceiver_alerts_total",
		Help: "Total number of alerts processed by the hookreceiver, by result",
	}, []string{"name", "zone", "result"})

	hookreceiverGRPCDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:                            "iotcontroller_hookreceiver_grpc_duration_seconds",
		Help:                            "Duration of gRPC Alert calls from the hookreceiver",
		NativeHistogramBucketFactor:     1.1,
		NativeHistogramMaxBucketNumber:  100,
		NativeHistogramMinResetDuration: 1 * time.Hour,
	})
)
