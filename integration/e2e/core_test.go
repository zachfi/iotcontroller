package e2e

import (
	"testing"

	"github.com/grafana/e2e"
	"github.com/stretchr/testify/require"
	"github.com/zachfi/iotcontroller/integration/e2e/util"
)

func TestCore(t *testing.T) {
	s, err := e2e.NewScenario("iot_e2e")
	require.NoError(t, err)
	defer s.Close()

	mqtt := util.NewMQTTServer("mqtt")
	require.NotNil(t, mqtt)
	require.NoError(t, s.StartAndWaitReady(mqtt))
}
