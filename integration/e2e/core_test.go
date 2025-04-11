package e2e

import (
	"fmt"
	"testing"

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

	k3d := util.NewK3dCluster("k3d")
	require.NotNil(t, mqtt)

	kfg := util.K3dStartAndWaitReady(t, s, k3d)
	t.Logf("K3d config: %v", kfg.Clusters["k3d-k3d"].Server)

	content, err := clientcmd.Write(*kfg)
	require.NoError(t, err)
	_, err = util.WriteFileToSharedDir(s, kubeConfigFile, content)
	require.NoError(t, err)

	err = s.StartAndWaitReady(mqtt)
	require.NoError(t, err)

	core := util.NewTargetServer("core", configFile, kubeConfigFile)
	require.NotNil(t, core)

	tmplConfig := map[string]any{
		"MQTTUrl":       fmt.Sprintf("mqtt://%s", mqtt.NetworkEndpoint(1883)),
		"RouterAddress": core.NetworkEndpoint(9090),
	}

	t.Logf("Config: %v", tmplConfig)
	_, err = util.CopyTemplateToSharedDir(s, configFileTemplate, configFile, tmplConfig)
	require.NoError(t, err)

	receiver := util.NewTargetServer("receiver", configFile, kubeConfigFile)
	require.NotNil(t, receiver)

	require.NoError(t, s.StartAndWaitReady(core, receiver))
}
