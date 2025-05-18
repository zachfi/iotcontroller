package util

import (
	"context"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	metricsNamespace = "iot_device_tracker"

	metricPurges = promauto.NewCounter(prometheus.CounterOpts{
		Name:      "purges_total",
		Namespace: metricsNamespace,
		Help:      "Total number of devices purged",
	})
)

type Metric interface {
	// DeletePartialMatch from prometheus.MetricVec
	DeletePartialMatch(prometheus.Labels) int
}

type DeviceTracker struct {
	mtx        sync.Mutex
	Devices    map[string]time.Time
	Metrics    []Metric
	purgeAfter time.Duration
}

func NewDeviceTracker(metrics []Metric, purgeAfter time.Duration) *DeviceTracker {
	return &DeviceTracker{
		Devices:    make(map[string]time.Time),
		Metrics:    metrics,
		purgeAfter: purgeAfter,
	}
}

// A go routine that periodically purges devices that have not been seen for a while.
func (d *DeviceTracker) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.PurgeAll()
		}
	}
}

// PurgeAll removes devices that have not been seen for a while.
func (d *DeviceTracker) PurgeAll() {
	d.mtx.Lock()
	defer d.mtx.Unlock()

	for device, t := range d.Devices {
		if time.Since(t) >= d.purgeAfter {
			metricPurges.Inc()
			delete(d.Devices, device)
			for _, metric := range d.Metrics {
				metric.DeletePartialMatch(prometheus.Labels{"device": device})
			}
		}
	}
}

// Track adds a device to the tracker.
func (d *DeviceTracker) Track(device string) {
	d.mtx.Lock()
	defer d.mtx.Unlock()

	d.Devices[device] = time.Now()
}

// Tracked returns true if the device is being tracked.
func (d *DeviceTracker) tracked(device string) bool {
	d.mtx.Lock()
	defer d.mtx.Unlock()

	_, ok := d.Devices[device]
	return ok
}
