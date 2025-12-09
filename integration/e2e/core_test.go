package e2e

import (
	"fmt"
	"testing"
	"time"

	"github.com/grafana/e2e"
	"github.com/stretchr/testify/require"
	"github.com/zachfi/iotcontroller/integration/e2e/util"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	configFile         = "config.yaml"
	kubeConfigFile     = "kubeconfig.yaml"
	configFileTemplate = "config.yaml.tmpl"
)

func TestCore(t *testing.T) {
	s, err := e2e.NewScenario("iot_e2e")
	require.NoError(t, err)
	defer s.Close()

	const configMosquitto = "util/mosquitto.conf"
	_, err = util.CopyTemplateToSharedDir(s, configMosquitto, "mosquitto.conf", nil)
	require.NoError(t, err)

	mqtt := util.NewMQTTServer("mqtt")
	require.NotNil(t, mqtt)

	err = s.StartAndWaitReady(mqtt)
	require.NoError(t, err)

	k3d := util.NewK3dCluster("k3d")
	require.NotNil(t, k3d)

	apiCfg, _, _ := util.K3dStartAndWaitReady(t, s, k3d, kubeConfigFile)

	content, err := clientcmd.Write(*apiCfg)
	t.Logf("content: %+v", string(content))
	require.NoError(t, err)
	_, err = util.WriteFileToSharedDir(s, kubeConfigFile, content)
	require.NoError(t, err)

	core := util.NewTargetServer("core", configFile, kubeConfigFile)
	require.NotNil(t, core)

	tmplConfig := map[string]any{
		"MQTTUrl":       fmt.Sprintf("mqtt://%s", mqtt.NetworkEndpoint(1883)),
		"RouterAddress": core.NetworkEndpoint(9090),
	}

	_, err = util.CopyTemplateToSharedDir(s, configFileTemplate, configFile, tmplConfig)
	require.NoError(t, err)

	receiver := util.NewTargetServer("receiver", configFile, kubeConfigFile)
	require.NotNil(t, receiver)

	require.NoError(t, s.StartAndWaitReady(core, receiver))
}

func TestZoneLifecycle(t *testing.T) {
	s, err := e2e.NewScenario("iot_e2e")
	require.NoError(t, err)
	defer s.Close()

	// … start mqtt, k3d, core, receiver …

	k3d := util.NewK3dCluster("k3d")
	require.NotNil(t, k3d)

	_, _, k8sClient := util.K3dStartAndWaitReady(t, s, k3d, kubeConfigFile)

	// Create a zone
	zone := util.NewZone("sample-zone", []string{"dev-1", "dev-2"}, []string{"red", "blue"})
	util.ApplyZone(t, k8sClient, zone)

	// Wait for the controller to mark it ready
	util.WaitForZoneState(t, k8sClient, "sample-zone", "default", "", 10*time.Second)

	// Inspect a Device or status as desired …
}
