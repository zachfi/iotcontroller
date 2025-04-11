package util

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/grafana/e2e"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var k3dImage = "ghcr.io/k3d-io/k3d:5.8.3-dind"

func NewK3dCluster(name string) *e2e.HTTPService {
	// The K3d image is a Docker-in-Docker image.  The default entrypoint launches docker, but the resulting HTTP service will require
	s := e2e.NewHTTPService(
		name,
		k3dImage,
		nil,
		e2e.NewTCPReadinessProbe(6443), // Skip the TLS negitiation, we will use the default k3d port
		6443,
	)

	s.SetPrivileged(true)

	return s
}

func K3dStartAndWaitReady(t *testing.T, s *e2e.Scenario, k3d *e2e.HTTPService) *api.Config {
	var err error

	err = s.Start(k3d)
	require.NoError(t, err)

	time.Sleep(3 * time.Second)

	clusterCreate := e2e.NewCommand(
		"k3d",
		"cluster",
		"create",
		k3d.Name(),
		"--api-port",
		"0.0.0.0:6443",
		// "--name",
		// "iot_e2e-k3d",
		// "--server-arg",
		// "--tls-san=\"iot_e2e-k3d\"",
		// "--help",
	)

	_, _, err = k3d.Exec(clusterCreate)
	require.NoError(t, err)

	// kubectl config view --raw
	kubectlConfig := e2e.NewCommand("kubectl", "config", "view", "--raw")
	rawConfig, _, err := k3d.Exec(kubectlConfig)
	require.NoError(t, err)

	cfg, err := clientcmd.Load([]byte(rawConfig))
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Len(t, cfg.Clusters, 1)
	require.Equal(t, cfg.Clusters["k3d-k3d"].Server, "https://0.0.0.0:6443")
	require.Len(t, cfg.Contexts, 1)
	require.Len(t, cfg.AuthInfos, 1)

	err = s.WaitReady(k3d)
	require.NoError(t, err)

	// BUG: calling here seems to modify the address, so we set it again below.
	loadCRDs(t, *cfg, k3d)

	cfg.Clusters["k3d-k3d"].Server = "https://" + k3d.NetworkEndpoint(6443)
	cfg.Clusters["k3d-k3d"].TLSServerName = "localhost"
	// cfg.Clusters["k3d-k3d"].InsecureSkipTLSVerify = true
	t.Logf("Cluster server: %s", cfg.Clusters["k3d-k3d"].Server)

	return cfg
}

func loadCRDs(t *testing.T, cfg api.Config, k3d *e2e.HTTPService) {
	files, err := os.ReadDir("../../config/crd/bases")
	require.NoError(t, err)

	cfg.Clusters["k3d-k3d"].Server = "https://" + k3d.Endpoint(6443)

	ctx := context.Background()

	clientCfg, err := clientcmd.NewDefaultClientConfig(cfg, &clientcmd.ConfigOverrides{}).ClientConfig()
	require.NoError(t, err)

	c, err := client.New(clientCfg, client.Options{})
	require.NoError(t, err)

	decode := scheme.Codecs.UniversalDeserializer().Decode
	for _, file := range files {
		t.Logf("Loading CRD %s", file.Name())

		b, err := os.ReadFile(filepath.Join("../../config/crd/bases", file.Name()))
		require.NoError(t, err)
		require.NotNil(t, b)

		o := &unstructured.Unstructured{}
		decode(b, nil, o)

		// Load the CRD using the client
		err = c.Create(ctx, o)
		require.NoError(t, err)
	}
}
