# iotcontroller — Project Intentions

## What This Project Is

A Kubernetes operator for IoT devices. It manages `Device` and `Zone` CRDs,
routes messages from physical devices, and enforces desired state across zones.

Primary use case: Zigbee lighting and sensor control, with alerting-driven
automation (e.g. Alertmanager webhooks trigger zone state changes).

## Active Work: `zigbeeCoordinator` branch

We are building **direct Zigbee dongle support** — replacing the zigbee2mqtt
bridge with native serial communication. The goal is to own the full Zigbee
stack inside this project.

### Key packages

- `pkg/zigbee-dongle/` — stack-agnostic dongle interface (`Dongle`)
  - `znp/` — Z-Stack Network Processor (TI CC253X chips, Sonoff Dongle Plus P)
  - `ember/` — EmberZNet/EZSP over ASH (Silicon Labs, Sonoff ZBDongle-E)
- `modules/zigbeecoordinator/` — service module that wraps the dongle,
  handles network formation, permit join, device interviews, and forwards
  messages to the router over gRPC

### Current status

- **ZNP stack**: Working. Health check, network formation, permit join, device
  join events, device interview (node descriptor + simple descriptors) all
  functional.
- **Ember stack**: Partially implemented. Known issue: device returns status
  `0x58` (TRANSMIT_BLOCKED) for network-related commands. Initialization
  sequence needs to match zigbee-herdsman's `initEzsp()` → `initTrustCenter()`
  → `formNetwork()` flow. See `COMPARISON.md` for detailed notes.

### Intended architecture

```
Serial (ZNP/Ember) → pkg/zigbee-dongle → modules/zigbeecoordinator
                                                  ↓ gRPC
                                          modules/router → modules/zonekeeper
```

Messages from the dongle are forwarded to the router via `RouteServiceClient`.
Interview results follow path `zigbee/{ieee}/interview`.
Regular messages follow path `zigbee/{nwk_addr}`.

## Module structure

All modules implement `grafana/dskit/services.Service` (BasicService pattern
with `starting`, `running`, `stopping` lifecycle methods).

Modules: conditioner, controller, harvester, hookreceiver, mqttclient, router,
timer, weather, zigbeecoordinator, zonekeeper.

## Conventions

- Logging: `log/slog` with `module=` attribute on all loggers; dongle uses
  `layer=znp` or `layer=zigbee` attributes to distinguish protocol layers.
- Tracing: OpenTelemetry via `go.opentelemetry.io/otel`.
- Protobuf: defined in `proto/`, generated via `buf`. Run `make generate` after
  changing `.proto` files.
- Vendor: dependencies are vendored (`vendor/`). Run `go mod vendor` after
  adding deps.
- Config: YAML-based, loaded via `cmd/app/config.go`.

## Key decisions / constraints

- The `Dongle` interface must remain stack-agnostic: all stacks produce the
  same `IncomingMessage`/`OutgoingMessage` types.
- Network state (PAN ID, extended PAN ID, channel, network key) is persisted to
  a state file (`cfg.StateFile`) to survive dongle swaps.
- Network key is never readable from the dongle after formation; it must be
  preserved in the state file or configured explicitly.
- Device interviews are deduplicated with a mutex map (`interviewing map[uint16]bool`).
- Permit join is capped at 254 seconds (matches zigbee-herdsman).
