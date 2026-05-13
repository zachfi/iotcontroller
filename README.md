# iotcontroller

A Kubernetes operator for IoT devices. Manages `Device`, `Zone`, `Scene`,
`Condition`, and `Binding` CRDs, normalizes events from one or more
device transports, and enforces desired zone state by driving the
underlying hardware. Primary use case: Zigbee-based home automation
where lighting, climate, leak sensors, and relays are managed as
declarative Kubernetes resources.

## What it does

- Receives device events from MQTT (zigbee2mqtt), a direct Zigbee dongle,
  or any future transport that normalizes into the shared `DeviceEvent`
  shape.
- Resolves each event against `Binding` resources to decide which
  `Condition` to activate.
- Activates Conditions, which apply `Remediation` entries to one or more
  zones — setting absolute values via Scenes, relative brightness
  adjustments, or computed values from a registry of `Computer`
  implementations (sun-tracking, ramps, color rotation, PromQL-driven).
- Enforces zone state by emitting Zigbee commands through the
  appropriate transport.

The operator is intentionally transport-agnostic at the matcher and
Conditioner layers; adding a new transport (Matter, Home Assistant,
ESPHome) means writing one event-normalization adapter, not rewiring
the control plane.

## Architecture

```
device → transport router → matcher → ActivateCondition → Conditioner
                ↓                                              ↓
            metrics + tracing                          ZoneKeeper → device
```

### Modules

All modules implement `grafana/dskit/services.Service` (BasicService
lifecycle: `starting` → `running` → `stopping`).

| Module               | Role |
|----------------------|------|
| `harvester`          | Reads MQTT, forwards messages to the router. Fan-out worker pool keeps the consumer responsive when downstream calls slow. |
| `router`             | Routes inbound messages by topic. Dispatches device events through the binding matcher; falls back to an observability counter when no binding matches. |
| `zigbeecoordinator`  | Optional native Zigbee dongle stack (ZNP / Ember). Replaces zigbee2mqtt for direct serial control of the network. |
| `mqttclient`         | Shared MQTT connection with auto-reconnect. |
| `conditioner`        | Owns Conditions, applies Remediations, runs the periodic evaluator. Idempotency cache prevents amplification. |
| `zonekeeper`         | Per-zone state machine. Translates `SetState` / `SetScene` / `ApplyValues` / `AdjustBrightness` RPCs into Zigbee commands. |
| `hookreceiver`       | Alertmanager-webhook endpoint. Routes alerts to `Conditioner.Alert`. |
| `weather`            | OpenWeatherMap polling, exports metrics. |
| `controller`         | Kubernetes controller-runtime reconciler for the iot CRDs. |

### Resources

| Kind        | Purpose |
|-------------|---------|
| `Device`    | A physical thing (light, sensor, relay, button). Carries IEEE address, type, model, vendor. |
| `Zone`      | A grouping of Devices that share a control intent (`office`, `bedside-zach`, `pond-pump`). State is enforced at the zone level. |
| `Scene`     | Named bundle of absolute values: brightness, color temperature, color, state. Reusable across zones. |
| `Condition` | An activation intent with one or more `Remediations`. Triggered by alerts, bindings, or the periodic evaluator. |
| `Binding`   | Maps `(property, value, selector)` → `Condition`. Optional `MinDuration` debounces flapping sensors. |

See [docs/](docs/) for a detailed walkthrough of each piece.

## Event flow

A button press, leak detection, or motion event flows the same way
regardless of transport:

```
1. device publishes event (MQTT or Zigbee)
2. transport router normalizes to DeviceEvent{Property, Value, Device}
3. matcher selects the most-specific Binding for the event
   (specificity: IEEE > Device > Label > DeviceType > Zone)
4. if MinDuration is set: wait for the dwell threshold
5. matcher dispatches ActivateCondition(name)
6. Conditioner walks the Condition's Remediations
7. each Remediation routes through applyDesired (idempotency cache)
8. ZoneKeeper translates to Zigbee commands and emits
```

End-to-end latency for a button press: ~1–2 ms inside the controller.
Window-driven Remediations (`time_intervals`) fire from the periodic
evaluator at `cfg.EvaluationInterval` (default 60s). See
[docs/conditioner.md](docs/conditioner.md) for the evaluator design.

## Conventions

- Logging: `log/slog` with `module=<name>` attribute on every logger.
- Tracing: OpenTelemetry via `go.opentelemetry.io/otel`; export configured
  by `-tracing.otel.endpoint`.
- Protobuf: defined in `proto/`, generated via `buf`. Run `make generate`
  after changing `.proto` files.
- CRD types: defined in `api/v1`. Run `make generate manifests` after
  schema changes.
- Vendor: dependencies are vendored under `vendor/`. Run `go mod vendor`
  after adding deps.

## Building

```sh
# Build the binary
make build

# Build a Docker image
make docker-build IMG=<registry>/iotcontroller:tag

# Regenerate CRDs + deepcopy after API changes
make generate manifests

# Regenerate proto after .proto changes
buf generate

# Run unit tests
go test ./...
```

## Deploying

The intended deployment path is via [Tanka](https://tanka.dev/) using the
operator's deployment_tools jsonnet wrapper. The CRDs live as a separate
[jsonnet-libs](https://github.com/zachfi/jsonnet-libs) module, regenerated
when the API schema changes. The controller runs as three pods:

- `controller-core` — Conditioner + Router + ZoneKeeper. The decision/apply tier.
- `controller-receiver` — Controller (reconciler) + Harvester + HookReceiver + Weather. MQTT ingress and alert webhook.
- `controller-zigbee-coordinator` — Optional: ZigbeeCoordinator module bound to a USB Zigbee dongle.

The standalone `make deploy` path is intentionally minimal — most of the
day-to-day deployment surface (Conditions, Scenes, Bindings) is declared
in the operator's jsonnet, not in `config/`.

## Status

The Binding system, periodic Conditioner evaluator, and the four
built-in computers (`sun_color_temperature`, `ramp`, `rotate_colors`,
`query`) are shipped and in production. The native Zigbee coordinator
(ZNP working; Ember partially) is being trialled alongside zigbee2mqtt
on the `zigbeeCoordinator` branch. The legacy `Spec.Schedule` cron field
and `Epoch` RPC are deprecated and scheduled for deletion in an upcoming
release.

## License

Apache 2.0. See [LICENSE](LICENSE).
