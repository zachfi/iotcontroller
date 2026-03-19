package zonekeeper

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	metricZonekeeperFlushTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "iotcontroller",
		Name:      "zonekeeper_flush_total",
		Help:      "Total number of zone flush operations",
	}, []string{"zone", "result"})

	metricZonekeeperStateChangesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "iotcontroller",
		Name:      "zonekeeper_state_changes_total",
		Help:      "Total number of explicit zone state changes",
	}, []string{"zone", "state"})
)
