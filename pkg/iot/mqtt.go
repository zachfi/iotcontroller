package iot

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"math/rand"
	"net/url"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

const clientPrefix = "iotcontroller"

func NewMQTTClient(cfg MQTTConfig, logger *slog.Logger) (mqtt.Client, error) {
	var (
		mqttClient mqtt.Client
		src        = rand.New(rand.NewSource(time.Now().UnixNano()))
		rnd        = rand.New(src)
	)

	onConnected := func(_ mqtt.Client) {
		logger.Info("mqtt connected")
	}

	onLost := func(_ mqtt.Client, err error) {
		logger.Error("mqtt connection lost", "err", err)
	}

	onReconnect := func(_ mqtt.Client, _ *mqtt.ClientOptions) {
		logger.Info("mqtt reconnecting")
	}

	onConnect := func(broker *url.URL, tlsCfg *tls.Config) *tls.Config {
		logger.Info("mqtt connecting", "broker", broker)
		return tlsCfg
	}

	mqttOpts := mqtt.NewClientOptions()

	mqttOpts.SetOnConnectHandler(onConnected)
	mqttOpts.SetConnectionLostHandler(onLost)
	mqttOpts.SetReconnectingHandler(onReconnect)
	mqttOpts.SetConnectionAttemptHandler(onConnect)

	mqttOpts.AddBroker(cfg.URL)
	// mqttOpts.SetAutoReconnect(true) // default true
	mqttOpts.SetCleanSession(true)
	mqttOpts.SetClientID(fmt.Sprintf("%s-%x", clientPrefix, rnd.Uint64()))
	// mqttOpts.SetConnectRetryInterval(3 * time.Second)
	// mqttOpts.SetConnectRetry(true) // default is false, unsure how this plays with the autoreconnect true above
	mqttOpts.SetConnectTimeout(10 * time.Second)
	mqttOpts.SetKeepAlive(10 * time.Second)
	mqttOpts.SetMaxReconnectInterval(time.Minute)
	mqttOpts.SetOrderMatters(false)

	mqttOpts.SetWriteTimeout(5 * time.Second)

	if cfg.Username != "" && cfg.Password != "" {
		mqttOpts.SetUsername(cfg.Username)
		mqttOpts.SetPassword(cfg.Password)
	}

	mqttClient = mqtt.NewClient(mqttOpts)

	token := mqttClient.Connect()
	token.Wait()

	if err := token.Error(); err != nil {
		return nil, err
	}

	logger.Debug("mqtt connected", "url", cfg.URL)

	return mqttClient, nil
}
