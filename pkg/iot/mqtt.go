package iot

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"math/rand"
	"net/url"
	sync "sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/grafana/dskit/backoff"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const clientPrefix = "iotcontroller"

var (
	metricsNamespace = "iot"
	metricsSubsystem = "mqtt"

	metricMQTTClientOnConnect = promauto.NewCounter(prometheus.CounterOpts{
		Name:      "on_connect",
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystem,
		Help:      "Occurs when the client connects to the broker",
	})
	metricMQTTClientOnReconnect = promauto.NewCounter(prometheus.CounterOpts{
		Name:      "on_reconnect",
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystem,
		Help:      "Occurs when the client reconnects to the broker",
	})
	metricMQTTClientOnLost = promauto.NewCounter(prometheus.CounterOpts{
		Name:      "on_lost",
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystem,
		Help:      "Occurs when the client loses connection to the broker",
	})

	metricMQTTClientOnConnected = promauto.NewCounter(prometheus.CounterOpts{
		Name:      "on_connected",
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystem,
		Help:      "Occurs when the client is connected to the broker",
	})
)

// NewMQTTClient creates a new MQTT client with the given configuration.  The
// returned client requires the receiver call Connect() in order to connect to
// the broker.  The returned context is used to signal the client to shutdown
// in case the retry limit is reached.
func NewMQTTClient(cfg MQTTConfig, logger *slog.Logger) (mqtt.Client, context.Context, error) {
	var (
		mqttClient  mqtt.Client
		src         = rand.New(rand.NewSource(time.Now().UnixNano()))
		rnd         = rand.New(src)
		ctx, cancel = context.WithCancel(context.Background())
		mtx         sync.Mutex
		b           = backoff.New(ctx, cfg.Backoff)
	)

	onConnected := func(_ mqtt.Client) {
		logger.Info("mqtt connected")
		metricMQTTClientOnConnected.Inc()

		mtx.Lock()
		defer mtx.Unlock()

		b.Reset()
	}

	onLost := func(c mqtt.Client, err error) {
		logger.Error("mqtt connection lost", "err", err)
		metricMQTTClientOnLost.Inc()
	}

	onReconnect := func(_ mqtt.Client, _ *mqtt.ClientOptions) {
		logger.Info("mqtt reconnecting", "delay", b.NextDelay())
		metricMQTTClientOnReconnect.Inc()

		mtx.Lock()
		defer mtx.Unlock()

		if b.Err() != nil {
			cancel()
		}

		b.Wait()
	}

	onConnect := func(broker *url.URL, tlsCfg *tls.Config) *tls.Config {
		logger.Info("mqtt connecting", "broker", broker)
		metricMQTTClientOnConnect.Inc()

		return tlsCfg
	}

	mqttOpts := mqtt.NewClientOptions()

	mqttOpts.SetOnConnectHandler(onConnected)
	mqttOpts.SetConnectionLostHandler(onLost)
	mqttOpts.SetReconnectingHandler(onReconnect)
	mqttOpts.SetConnectionAttemptHandler(onConnect)

	mqttOpts.AddBroker(cfg.URL)
	// mqttOpts.SetAutoReconnect(true) // default true
	mqttOpts.SetClientID(fmt.Sprintf("%s-%x", clientPrefix, rnd.Uint64()))
	// mqttOpts.SetConnectRetryInterval(3 * time.Second)
	// mqttOpts.SetConnectRetry(true) // default is false, unsure how this plays with the autoreconnect true above
	mqttOpts.SetConnectTimeout(10 * time.Second)
	mqttOpts.SetKeepAlive(10 * time.Second)
	mqttOpts.SetMaxReconnectInterval(time.Minute)
	mqttOpts.SetOrderMatters(false)
	mqttOpts.SetResumeSubs(true)

	mqttOpts.SetWriteTimeout(5 * time.Second)

	if cfg.Username.String() != "" && cfg.Password.String() != "" {
		mqttOpts.SetUsername(cfg.Username.String())
		mqttOpts.SetPassword(cfg.Password.String())
	}

	mqttClient = mqtt.NewClient(mqttOpts)

	return mqttClient, ctx, nil
}
