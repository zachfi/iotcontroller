package util

import (
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
	Delete(prometheus.Labels) bool
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
func (d *DeviceTracker) Run(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			d.PurgeAll()
		}
	}
}

func (d *DeviceTracker) PurgeAll() {
	d.mtx.Lock()
	defer d.mtx.Unlock()

	for device, t := range d.Devices {
		if time.Since(t) >= d.purgeAfter {
			metricPurges.Inc()
			delete(d.Devices, device)
			for _, metric := range d.Metrics {
				metric.Delete(prometheus.Labels{"device": device})
			}
		}
	}
}

func (d *DeviceTracker) Track(device string) {
	d.mtx.Lock()
	defer d.mtx.Unlock()

	d.Devices[device] = time.Now()
}
