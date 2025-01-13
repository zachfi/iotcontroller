package e2e

import (
	"testing"
	"time"

	"github.com/grafana/e2e"
	"github.com/stretchr/testify/require"
	"github.com/zachfi/iotcontroller/integration/e2e/util"
)

func TestCore(t *testing.T) {
	s, err := e2e.NewScenario("iot_e2e")
	require.NoError(t, err)
	defer s.Close()

	const configMosquitto = "util/mosquitto.conf"
	// tmplConfig := map[string]any{"": nil}
	_, err = util.CopyTemplateToSharedDir(s, configMosquitto, "mosquitto.conf", nil)
	require.NoError(t, err)

	mqtt := util.NewMQTTServer("mqtt")
	mqtt.SetPrivileged(true)
	require.NotNil(t, mqtt)

	// kind := util.NewKindCluster("kind")
	// kind.SetPrivileged(true)
	// require.NotNil(t, kind)

	k3d := util.NewK3dCluster("k3d")
	k3d.SetPrivileged(true)
	require.NotNil(t, k3d)

	require.NoError(t, s.StartAndWaitReady(mqtt, k3d))

	time.Sleep(1000)
}
