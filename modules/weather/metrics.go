package weather

import (
	"context"
	"fmt"
	"strconv"
	"time"

	owm "github.com/briandowns/openweathermap"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/icodealot/noaa"
)

var (
	metricWeatherForecastConditions = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "weather_forecast",
		Help: "The total number of notice calls that include an unhandled object ID.",
	}, []string{"location", "condition", "future_hours"})

	metricWeatherCurrentConditions = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "weather_current",
		Help: "Weather condition current",
	}, []string{"location", "condition"},
	)

	metricPollutionCurrent = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "pollution_current_aqi",
		Help: "Current Air Pollution (AQI)",
	}, []string{"location"})

	metricWeatherEpoch = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "weather_epoch",
		Help: "Weather event: (sunrise|sunset|moonrise|moonset)",
	}, []string{"location", "event"})

	metricWeatherSummary = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "weather_summary",
		Help: "Weather description",
	}, []string{"location", "main", "description"})

	metricWeatherNOAAForecast = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "weather_noaa",
		Help: "Weather data from NOAA",
	}, []string{"location", "condition", "future_hours"})

	metricWeatherNOAAWindSpeedForecast = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Subsystem: "weather",
		Name:      "noaa_wind_speed",
		Help:      "Wind speed from NOAA",
	}, []string{"location", "direction", "future_hours"})

	metricWeatherNOAATemperatureForecast = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Subsystem: "weather",
		Name:      "noaa_temperature",
		Help:      "Temperature from NOAA",
	}, []string{"location", "future_hours"})
)

func (w *Weather) Collect(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, w.cfg.Timeout)
	defer cancel()

	ctx, span := w.tracer.Start(ctx, "Collect")
	defer span.End()

	// bg := boundedwaitgroup.New(4)

	for _, location := range w.cfg.Locations {
		// bg.Add(1)
		// go func(loc Location) {
		// 	defer bg.Done()
		// 	w.collectPollution(ctx, loc)
		// }(location)
		//
		// bg.Add(1)
		// go func(loc Location) {
		// 	defer bg.Done()
		// 	w.collectOne(ctx, loc)
		// }(location)

		// bg.Add(1)
		// go func(loc Location) {
		// defer bg.Done()
		w.collectNoaa(ctx, location)
		// }(location)
	}
	// bg.Wait()
}

// collectNoaa collects weather data from NOAA for the configured locations.
func (w *Weather) collectNoaa(ctx context.Context, location Location) {
	_, span := w.tracer.Start(ctx, "collectNoaa")
	defer span.End()

	if !w.cfg.NOAA.Enabled {
		return
	}

	w.logger.Info("collecting weather data", "location", location.Name)

	span.SetAttributes(attribute.Bool("noaa_enabled", w.cfg.NOAA.Enabled))

	var (
		lat      string
		lon      string
		err      error
		forecast *noaa.HourlyForecastResponse
	)

	lat = fmt.Sprintf("%f", location.Latitude)
	lon = fmt.Sprintf("%f", location.Longitude)

	forecast, err = noaa.HourlyForecast(lat, lon)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		w.logger.Error("failed to get forecast", "err", err, "lat", lat, "lon", lon)
		return
	}

	span.SetAttributes(attribute.Int("forecast_periods", len(forecast.Periods)))
	for _, f := range forecast.Periods {

		startTime, err := time.Parse(time.RFC3339, f.StartTime)
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
			w.logger.Error("failed to parse start time", "err", err, "start_time", f.StartTime)
			continue
		}

		futureHours := time.Until(startTime).Round(time.Hour).Hours()
		futureHoursStr := fmt.Sprintf("%dh", int(futureHours))

		metricWeatherNOAAForecast.WithLabelValues(location.Name, f.Summary, futureHoursStr).Inc()

		metricWeatherNOAAWindSpeedForecast.WithLabelValues(location.Name, f.WindDirection, futureHoursStr).Set(f.QuantitativeWindSpeed.Value)

		metricWeatherNOAATemperatureForecast.WithLabelValues(location.Name, futureHoursStr).Set(f.Temperature)
	}
}

func (w *Weather) collectPollution(ctx context.Context, location Location) {
	_, span := w.tracer.Start(ctx, "collectPollution")
	defer span.End()

	span.SetAttributes(attribute.String("location", location.Name))

	coord := &owm.Coordinates{
		Longitude: location.Longitude,
		Latitude:  location.Latitude,
	}

	pollution, err := owm.NewPollution(w.cfg.APIKey, owm.WithHttpClient(w.owmClient))
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		w.logger.Error("failed to get new pollution data", "err", err)
	}

	params := &owm.PollutionParameters{
		Location: *coord,
		Datetime: "current",
	}

	if err := pollution.PollutionByParams(params); err != nil {
		span.SetStatus(codes.Error, err.Error())
		w.logger.Error("failed to update pollution data", "err", err)
	}

	for _, p := range pollution.List {
		metricPollutionCurrent.WithLabelValues(location.Name).Set(p.Main.Aqi)
		// TODO: implement metrics for the Air components.  Currently present on p.Components.
	}
}

func (w *Weather) collectOne(ctx context.Context, location Location) {
	_, span := w.tracer.Start(ctx, "collectOne")
	defer span.End()

	span.SetAttributes(attribute.String("location", location.Name))

	coord := &owm.Coordinates{
		Longitude: location.Longitude,
		Latitude:  location.Latitude,
	}

	// Possibility to exclude information. For example exclude daily information []string{ExcludeDaily}
	o, err := owm.NewOneCall("C", "EN", w.cfg.APIKey, []string{})
	if err != nil {
		w.logger.Error("onecall failed", "err", err)
		return
	}

	err = o.OneCallByCoordinates(coord)
	if err != nil {
		w.logger.Error("onecall coordinates failed", "err", err)
	}

	// Current conditions
	currentConditions := map[string]float64{
		"clouds":      float64(o.Current.Clouds),
		"dew_point":   o.Current.DewPoint,
		"feels_like":  o.Current.FeelsLike,
		"humidity":    float64(o.Current.Humidity),
		"pressure":    float64(o.Current.Pressure),
		"rain_1h":     o.Current.Rain.OneH,
		"rain_3h":     o.Current.Rain.ThreeH,
		"snow_1h":     o.Current.Snow.OneH,
		"snow_3h":     o.Current.Snow.ThreeH,
		"temp":        o.Current.Temp,
		"uvi":         o.Current.UVI,
		"visibility":  float64(o.Current.Visibility),
		"wind_degree": o.Current.WindDeg,
		"wind_gust":   o.Current.WindGust,
		"wind_speed":  o.Current.WindSpeed,
	}

	for condition, value := range currentConditions {
		if o.Current.Dt > 0 {
			metricWeatherCurrentConditions.WithLabelValues(location.Name, condition).Set(value)
		}
	}

	for _, weather := range o.Current.Weather {
		if o.Current.Dt > 0 {
			w.weatherSummary(location, weather)
		}
	}

	for _, hour := range o.Hourly {
		hourlyConditions := map[string]float64{
			"clouds":      float64(hour.Clouds),
			"dew_point":   hour.DewPoint,
			"feels_like":  hour.FeelsLike,
			"humidity":    float64(hour.Humidity),
			"pressure":    float64(hour.Pressure),
			"rain_1h":     hour.Rain.OneH,
			"rain_3h":     hour.Rain.ThreeH,
			"snow_1h":     hour.Snow.OneH,
			"snow_3h":     hour.Snow.ThreeH,
			"temp":        hour.Temp,
			"uvi":         hour.UVI,
			"visibility":  float64(hour.Visibility),
			"wind_degree": hour.WindDeg,
			"wind_gust":   hour.WindGust,
			"wind_speed":  hour.WindSpeed,
		}

		if hour.Dt > 0 {
			i, err := strconv.ParseInt(strconv.Itoa(hour.Dt), 10, 64)
			if err != nil {

				/* _ = tracing.ErrHandler(span, err, "failed to handle zigbee report", l.logger) */

				w.logger.Error("failed to parse int", "err", err)
				continue
			}

			tm := time.Until(time.Unix(i, 0)).Round(1 * time.Hour).Hours()

			for condition, value := range hourlyConditions {
				metricWeatherForecastConditions.WithLabelValues(location.Name, condition, fmt.Sprintf("%dh", int(tm))).Set(value)
			}
		}

		// for _, weather := range hour.Weather {
		// 	if hour.Dt > 0 {
		// 		o.weatherSummary(ctx, ch, location, weather)
		// 	}
		// }

	}
}

func (w *Weather) weatherSummary(location Location, summary owm.Weather) {
	metricWeatherSummary.WithLabelValues(location.Name, summary.Main, summary.Description).Inc()
}
