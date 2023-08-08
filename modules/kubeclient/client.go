package kubeclient

import (
	"context"

	"github.com/go-kit/log"
	"github.com/grafana/dskit/services"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	iotv1 "github.com/zachfi/iotcontroller/api/v1"
)

type KubeClient struct {
	services.Service
	cfg *Config

	// clientset *kubernetes.Clientset
	client client.Client

	logger log.Logger
}

func New(cfg Config, logger log.Logger) (*KubeClient, error) {
	logger = log.With(logger, "module", "timer")

	k := &KubeClient{
		cfg:    &cfg,
		logger: logger,
	}

	k.Service = services.NewBasicService(k.starting, k.running, k.stopping)

	config, err := clientcmd.BuildConfigFromFlags("", k.cfg.KubeConfig)
	if err != nil {
		return nil, err
	}

	iotConfig := *config
	iotConfig.GroupVersion = &schema.GroupVersion{
		Group:   iotv1.GroupVersion.Group,
		Version: iotv1.GroupVersion.Version,
	}
	iotConfig.APIPath = "/apis"
	iotConfig.NegotiatedSerializer = serializer.NewCodecFactory(scheme.Scheme)
	iotConfig.UserAgent = rest.DefaultKubernetesUserAgent()

	// TODO: Using global scheme.  runtime.New() would also provide a specific scheme.
	err = iotv1.AddToScheme(scheme.Scheme)
	if err != nil {
		return nil, err
	}

	// iotClient, err := rest.UnversionedRESTClientFor(&iotConfig)
	// if err != nil {
	// 	return nil, err
	// }

	// Example
	// result := v1alpha1.ProjectList{}
	// err := exampleRestClient.
	// 		Get().
	// 		Resource("projects").
	// 		Do().
	// 		Into(&result)
	//

	client, err := client.New(config, client.Options{Scheme: scheme.Scheme})
	if err != nil {
		return nil, err
	}

	// create the clientset, based on the iotconfig.  Using config here would
	// enable the default client without the CRD.
	// clientset, err := kubernetes.NewForConfig(&iotConfig)
	// if err != nil {
	// 	return nil, err
	// }

	// k.clientset = clientset
	k.client = client

	// k.store = WatchResource(clientSet)

	return k, nil
}

func (k *KubeClient) Client() client.Client {
	return k.client
}

func (k *KubeClient) starting(ctx context.Context) error {
	return nil
}

func (k *KubeClient) running(ctx context.Context) error {
	err := k.run(ctx)
	if err != nil {
		return err
	}
	<-ctx.Done()
	return nil
}

func (k *KubeClient) stopping(_ error) error {
	return nil
}

func (k *KubeClient) run(ctx context.Context) error {
	return nil
}

// func WatchResources(clientSet client_v1alpha1.ExampleV1Alpha1Interface) cache.Store {
// 	projectStore, projectController := cache.NewInformer(
// 		&cache.ListWatch{
// 			ListFunc: func(lo metav1.ListOptions) (result runtime.Object, err error) {
// 				return clientSet.Projects("some-namespace").List(lo)
// 			},
// 			WatchFunc: func(lo metav1.ListOptions) (watch.Interface, error) {
// 				return clientSet.Projects("some-namespace").Watch(lo)
// 			},
// 		},
// 		&v1alpha1.Project{},
// 		1*time.Minute,
// 		cache.ResourceEventHandlerFuncs{},
// 	)
//
// 	go projectController.Run(wait.NeverStop)
// 	return projectStore
// }
