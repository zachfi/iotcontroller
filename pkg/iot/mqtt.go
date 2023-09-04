package iot

import (
	"log/slog"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

func NewMQTTClient(cfg MQTTConfig, logger *slog.Logger) (mqtt.Client, error) {
	var mqttClient mqtt.Client

	mqttOpts := mqtt.NewClientOptions()
	mqttOpts.AddBroker(cfg.URL)
	mqttOpts.SetCleanSession(true)
	mqttOpts.SetAutoReconnect(true)
	mqttOpts.SetConnectRetry(true)
	mqttOpts.SetConnectRetryInterval(10 * time.Second)

	if cfg.Username != "" && cfg.Password != "" {
		mqttOpts.SetUsername(cfg.Username)
		mqttOpts.SetPassword(cfg.Password)
	}

	mqttClient = mqtt.NewClient(mqttOpts)

	if token := mqttClient.Connect(); token.Wait() && token.Error() != nil {
		logger.Error("mqtt connection failed", "err", token.Error())
	} else {
		logger.Debug("mqtt connected", "url", cfg.URL)
	}

	return mqttClient, nil
}
