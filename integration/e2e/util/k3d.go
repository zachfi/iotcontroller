package util

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/grafana/e2e"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/client"

	iotv1 "github.com/zachfi/iotcontroller/api/v1"
)

var k3dImage = "ghcr.io/k3d-io/k3d:5.8.3-dind"

func NewK3dCluster(name string) *e2e.ConcreteService {
	port := 6443

	// The K3d image is a Docker-in-Docker image.  The default entrypoint
	// launches docker, but the resulting HTTP service will require
	s := e2e.NewConcreteService(
		name,
		k3dImage,
		nil,
		// Skip the TLS negotiation.
		e2e.NewTCPReadinessProbe(port),
		port,
	)

	s.SetPrivileged(true)

	return s
}

func K3dStartAndWaitReady(t *testing.T, s *e2e.Scenario, k3d *e2e.ConcreteService, kubeConfigFile string) (internal *api.Config, external *api.Config, k8sClient client.Client) {
	err := s.Start(k3d)
	require.NoError(t, err)

	time.Sleep(3 * time.Second)

	clusterCreate := e2e.NewCommand(
		"k3d",
		"cluster",
		"create",
		k3d.Name(),
		"--api-port",
		"0.0.0.0:6443",
		"--network",
		s.NetworkName(),
		// "--name",
		// "iot_e2e-k3d",
		// "--server-arg",
		// "--tls-san=\"iot_e2e-k3d\"",
		// "--help",
	)

	_, errOut, err := k3d.Exec(clusterCreate)
	require.NoError(t, err, errOut)

	time.Sleep(3 * time.Second)

	return K3dConfigure(t, s, k3d, kubeConfigFile)
}

func K3dConfigure(t *testing.T, s *e2e.Scenario, k3d *e2e.ConcreteService, kubeConfigFile string) (internal, external *api.Config, k8sClient client.Client) {
	// kubectl config view --raw
	kubectlConfig := e2e.NewCommand("kubectl", "config", "view", "--raw")
	rawConfig, _, err := k3d.Exec(kubectlConfig)
	require.NoError(t, err)

	var (
		internalServer = fmt.Sprintf("https://%s", k3d.NetworkEndpoint(6443))
		hostPort       = strings.Split(k3d.Endpoint(6443), ":")[1]
		externalServer = fmt.Sprintf("https://0.0.0.0:%s", hostPort)
	)

	internal, err = clientcmd.Load([]byte(rawConfig))
	require.NoError(t, err)
	require.NotNil(t, internal)

	internal.Clusters["k3d-k3d"].Server = internalServer
	internal.Clusters["k3d-k3d"].TLSServerName = "kubernetes"

	require.Len(t, internal.Clusters, 1)
	require.Equal(t, internal.Clusters["k3d-k3d"].Server, internalServer)
	require.Len(t, internal.Contexts, 1)
	require.Len(t, internal.AuthInfos, 1)

	err = s.WaitReady(k3d)
	require.NoError(t, err)

	external = internal.DeepCopy()
	external.Clusters["k3d-k3d"].Server = externalServer
	external.Clusters["k3d-k3d"].TLSServerName = "localhost"

	// Return the client configurations

	content, err := clientcmd.Write(*internal)
	require.NoError(t, err)
	_, err = WriteFileToSharedDir(s, kubeConfigFile, content)
	require.NoError(t, err)

	// Get a client for the external server endpoint to use from the test suite.
	clientConfig := clientcmd.NewDefaultClientConfig(*external, nil)

	apiCfg, err := clientConfig.ClientConfig()
	require.NoError(t, err) // Now 'cfg' is your *rest.Config

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(iotv1.AddToScheme(scheme))

	k8sClient, err = client.New(apiCfg, client.Options{Scheme: scheme})
	require.NoError(t, err)

	loadCRDs(t, k8sClient)

	time.Sleep(3 * time.Second)

	return internal, external, k8sClient
}

func loadCRDs(t *testing.T, k8sClient client.Client) {
	files, err := os.ReadDir("../../config/crd/bases")
	require.NoError(t, err)

	ctx := context.Background()

	decode := scheme.Codecs.UniversalDeserializer().Decode
	for _, file := range files {
		t.Logf("Loading CRD %s", file.Name())

		b, err := os.ReadFile(filepath.Join("../../config/crd/bases", file.Name()))
		require.NoError(t, err)
		require.NotNil(t, b)

		o := &unstructured.Unstructured{}
		_, _, err = decode(b, nil, o)
		require.NoError(t, err)

		// Load the CRD using the client
		err = k8sClient.Create(ctx, o)
		require.NoError(t, err)
	}
}
