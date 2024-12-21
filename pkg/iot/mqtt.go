package iot

import (
	"fmt"
	"log/slog"
	"math/rand"
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

	mqttOpts := mqtt.NewClientOptions()
	mqttOpts.AddBroker(cfg.URL)
	mqttOpts.SetCleanSession(true)
	mqttOpts.SetAutoReconnect(true)
	mqttOpts.SetConnectRetry(true)
	mqttOpts.SetConnectRetryInterval(3 * time.Second)
	mqttOpts.SetOrderMatters(false)
	mqttOpts.SetKeepAlive(10 * time.Second)
	mqttOpts.SetClientID(fmt.Sprintf("%s-%x", clientPrefix, rnd.Uint64()))

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
