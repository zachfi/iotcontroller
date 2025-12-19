package iot

import (
	// "bufio"

	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace/noop"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/grafana/dskit/flagext"
	"github.com/stretchr/testify/require"
)

// This test only records data for fixtures.
func _TestUpdateMessageFixtures(t *testing.T) {
	cfg := MQTTConfig{
		URL:      "tcp://localhost:1883",
		Topic:    "#",
		Username: flagext.SecretWithValue("iot"),
		Password: flagext.SecretWithValue("xxx"),
	}

	ctx := context.Background()
	tracer := noop.NewTracerProvider().Tracer("test")

	var onMessageReceived mqtt.MessageHandler = func(_ mqtt.Client, msg mqtt.Message) {
		topicPath, err := ParseTopicPath(msg.Topic())
		require.NoError(t, err)
		discovery := ParseDiscoveryMessage(topicPath, msg)
		_, err = ReadZigbeeMessage(ctx, tracer, discovery)
		require.NoError(t, err)
		e := strings.Join(discovery.Endpoints, "/")

		switch e {
		case "devices":
			err := os.WriteFile("../../testdata/devices.json", msg.Payload(), 0o644)
			require.NoError(t, err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	mqttClient, clientCtx, err := NewMQTTClient(cfg, slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{})))
	require.NoError(t, err)

	token := mqttClient.Subscribe(cfg.Topic, 0, onMessageReceived)
	token.Wait()
	require.NoError(t, token.Error())

	topic := "zigbee2mqtt/bridge/config/devices"
	token = mqttClient.Publish(topic, byte(0), false, "")
	token.Wait()
	require.NoError(t, token.Error())

	<-ctx.Done()

	require.ErrorAs(t, clientCtx.Err(), context.Canceled)
}
