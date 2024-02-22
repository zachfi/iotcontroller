# iotcontroller

IOTController is a Kubernetes controller for IOT devices.

## Description

### Harvester

The harvester reads messages from an MQTT topic and forwards them to the router over gRPC.

### Router

The router reads the topic to determine which route is capable of parsing the
message and calls the route with the corresponding payload.

Two message routes are supported currently, though new routes may be added
quite easily to extend support additional platforms and messages in the future.

A couple future ideas that come to mind are perhaps a Home Assistant route to
allow compatibility with anything in that ecosystem. Similarly, an ESPHome
format could be included so that devices flashed with their firmware could be
read into this project.

As new devices appear on the bus, they are written as `Device` resources in
Kubernetes. These `Device` resources are grouped using `Zone` which inform the
Conditioner and other components how to operate these devices.

#### Zigbee2Mqtt

[Zigbee2Mqtt](https://www.zigbee2mqtt.io/) is a great open source source
project for bridging a Zigbee network with MQTT. This means that instead of
interfacing directly with a Zigbee network, all interactions can be done
through an MQTT topic, which this project leverages. Currently, this is the
primary use of this tool to controll zigbee lights, switches and metric sensor
data.

#### iSpindel

These messages are sent over WiFi directly to an MQTT topic. The router reads
these messages and exports metrics based on their data. The data format here
is pretty simple and is used to track the progress of home brew cider
fermentations.

### Conditioner

The Conditioner's responsibility is to allow users to express `Condition` type
resources to express the desired handling when certain events are received.
These events come from both the HookReceiver and Weather modules. The events
have a name and a set of labels. Thees labels are used to match against the
`Condition` resources to determine what the appropriate action for the zone is.
The Conditioner then calls the ZoneKeeper to apply the change.

### Controller

This project was built using `kubebuilder`. The Controller here is the main
Kubernetes controller, but moved into the module structure this project uses.
Primarily it is used to create the caching client used to interface with
Kubernets for the various components. This Client is a dependency of several
other modules. Bother Zone and Device reconcilers exist for syncing labels and
linking of resource types for zone ownership over devices.

### Hook Receiver

The HookReceiver exposes an HTTP endpoint compatible with the Alertmanager web hook payload. This allows the configuration of alerts in alert manager and forward to the controller to determine if action on a zone is required. For example, if a low temperature alert is fired, a `Condition` resource can match the associated labels and turn on a Zigbee switch to turn on a heater. The event labels are read from the alert. This allows for some programming; using events to apply states to zones for remediation. Additionally, an inactive state cane be added to these `Condition` resources so that when the alert sends a "resolved" status, the condition can be set back to its original state.

### Weather

This module reads data from Open Weather Map based on coordinates. Metrics for the various weather events are exported on a Prometheus endpoint, for alerting and graphing purposes. Additionally "epoch" events are sent to the conditioner directly for "sunset" and "sunrise" events so that conditions can express a desire to handle for these events.

### Zone Keeper

ZoneKeeper is the actual enforcement of state on a zone. It keeps state of all
desires on all zones, and which devices of which type and how to handle those
actions. This module has no opinions of its own, and only enforces what the
Conditioner tells it to enforce.

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
