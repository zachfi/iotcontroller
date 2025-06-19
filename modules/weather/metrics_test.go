package weather

import (
	"context"
	"log/slog"
	"testing"

	"go.opentelemetry.io/otel"
)

func TestCollectNoaa(t *testing.T) {
	// Mock the context and tracer
	ctx := context.Background()
	tracer := otel.Tracer("test")

	// Create a Weather instance with mocked configuration
	w := &Weather{
		cfg: &Config{
			NOAA: NOAAConfig{
				Enabled: true,
			},
			Locations: []Location{
				{Latitude: 40.7128, Longitude: -74.0060}, // Example coordinates for New York City
			},
		},
		logger: slog.Default(),
		tracer: tracer,
	}

	// Call the collectNoaa method
	for _, location := range w.cfg.Locations {
		w.collectNoaa(ctx, location)
	}

	// Add assertions to verify the expected behavior
}
