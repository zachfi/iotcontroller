# iotcontroller

IOTController is Kubernetes controller for IOT devices.

## Description

```mermaid
C4Context
    title System diagram for IOTController
    Enterprise_Boundary(b0, "iotcontroller") {
        System(moduleClient, "grpc client", "Allows inter-module communication")
        System(moduleConditioner, "zone conditioner", "Uses rules for controlling zones based on conditions")
        System(moduleController, "kubernetes controller", "reconciles and manages custom resources")
        System(moduleHarvester, "reads messages from mqtt and reports to telemetry")
        System(moduleMqttclient, "mqtt client", "Used to read reports from devices and send commands")
        System(moduleTelemetry, "iot telemetry", "Used to create metrics and respond to device events")
        System(moduleZoneKeeper", "zone keeper", "Used to manage the zones and their state")
        System(moduleHookReceiver, "hook receiver", "Receives alertmanager webooks and updates conditioner")
    }

    System(alertmanager, "alertmanager", "Alertmanager")
    System(kubernetes, "kubernetes", "Kubernetes")
    System(mqtt, "MQTT", "MQTT")

    Rel(alertmanager, moduleHookReceiver, "Receives webhooks about alerts")
    Rel(kubernetes, moduleController, "Manages kubernetes objects")
    BiRel(mqtt, mqttClient, "Manages kubernetes objects")

    UpdateLayoutConfig($c4ShapeInRow="3", $c4BoundaryInRow="1")


```

## Getting Started

You’ll need a Kubernetes cluster to run against. You can use [KIND](https://sigs.k8s.io/kind) to get a local cluster for testing, or run against a remote cluster.
**Note:** Your controller will automatically use the current context in your kubeconfig file (i.e. whatever cluster `kubectl cluster-info` shows).

### Running on the cluster

1. Install Instances of Custom Resources:

```sh
kubectl apply -f config/samples/
```

2. Build and push your image to the location specified by `IMG`:

```sh
make docker-build docker-push IMG=<some-registry>/iotcontroller:tag
```

3. Deploy the controller to the cluster with the image specified by `IMG`:

```sh
make deploy IMG=<some-registry>/iotcontroller:tag
```

### Uninstall CRDs

To delete the CRDs from the cluster:

```sh
make uninstall
```

### Undeploy controller

UnDeploy the controller to the cluster:

```sh
make undeploy
```

## Contributing

// TODO(user): Add detailed information on how you would like others to contribute to this project

### How it works

This project aims to follow the Kubernetes [Operator pattern](https://kubernetes.io/docs/concepts/extend-kubernetes/operator/)

It uses [Controllers](https://kubernetes.io/docs/concepts/architecture/controller/)
which provides a reconcile function responsible for synchronizing resources untile the desired state is reached on the cluster

### Test It Out

1. Install the CRDs into the cluster:

```sh
make install
```

2. Run your controller (this will run in the foreground, so switch to a new terminal if you want to leave it running):

```sh
make run
```

**NOTE:** You can also run this in one step by running: `make install run`

### Modifying the API definitions

If you are editing the API definitions, generate the manifests such as CRs or CRDs using:

```sh
make manifests
```

**NOTE:** Run `make --help` for more information on all potential `make` targets

More information can be found via the [Kubebuilder Documentation](https://book.kubebuilder.io/introduction.html)

## License

Copyright 2022.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
