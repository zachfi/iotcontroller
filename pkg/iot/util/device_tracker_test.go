package util

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type MockMetric struct{}

func (m *MockMetric) DeletePartialMatch(labels prometheus.Labels) int {
	return 0
}

func TestDeviceTracker(t *testing.T) {
	// Create a mock DeviceTracker
	metrics := []Metric{
		&MockMetric{},
	}
	purgeAfter := time.Second
	tracker := NewDeviceTracker(metrics, purgeAfter)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go tracker.Run(ctx, time.Second)

	// Add a device to the tracker
	deviceID := "test-device"
	tracker.Track(deviceID)

	// Check that the device is present
	if _, ok := tracker.Devices[deviceID]; !ok {
		t.Errorf("Expected device %s to be present", deviceID)
	}

	// Wait for the purge to occur
	time.Sleep(purgeAfter + time.Second)

	// Check that the device has been purged
	if _, ok := tracker.Devices[deviceID]; ok {
		t.Errorf("Expected device %s to be purged", deviceID)
	}
}
