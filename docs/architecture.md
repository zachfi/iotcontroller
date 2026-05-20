# Architecture

The operator's job is to turn device events into zone-state changes,
applying operator-declared policy along the way. Every layer is split
along one of three axes:

- **Transport**: where the device speaks (MQTT via zigbee2mqtt, direct
  Zigbee via the coordinator, future Matter/Home Assistant adapters).
- **Decision**: which Condition should activate, when, and what the
  resulting `Remediation` set should produce.
- **Apply**: what Zigbee commands actually go out.

The split is deliberate. A new transport plugs in by writing one
adapter; the matcher and Conditioner don't need to know it exists. A
new decision pattern (e.g. computer-driven values) plugs in at the
Conditioner layer; the apply layer doesn't care where the values came
from.

## Event flow

```
┌────────────┐   MQTT      ┌──────────────┐ gRPC ┌─────────────┐
│ zigbee2mqtt│────────────►│  Harvester   │─────►│   Router    │
└────────────┘             └──────────────┘      │             │
                                                 │  matcher    │
┌────────────┐   serial    ┌──────────────┐ gRPC │  (Bindings) │
│  Zigbee    │────────────►│ ZigbeeCoord. │─────►│             │
│  dongle    │             └──────────────┘      └──────┬──────┘
└────────────┘                                          │
                                                        │ ActivateCondition
                                                        ▼
                                                 ┌─────────────┐
                                                 │ Conditioner │
                                                 │             │
                                                 │ eval loop   │
                                                 │ alert loop  │
                                                 │ applyDesired│
                                                 └──────┬──────┘
                                                        │ SetState/Scene/
                                                        │ ApplyValues/etc.
                                                        ▼
                                                 ┌─────────────┐  Zigbee
                                                 │ ZoneKeeper  │────────►
                                                 └─────────────┘  cmd
```

Each arrow is normalized to one of two contracts:

- **Inbound events** become a `DeviceEvent{Property, Value, Device}` at
  the matcher's entry. Every transport must produce this shape; the
  matcher sees a uniform stream.
- **Outbound commands** are gRPC calls into the `ZoneKeeperService`
  (`SetState` / `SetScene` / `AdjustBrightness` / `ApplyValues` /
  `SelfAnnounce`). The ZoneKeeper holds the per-zone state machine and
  knows how to translate to Zigbee. `OccupancyHandler` was removed in
  v0.8.0; the matcher's deferred-fire path is now the sole owner of
  occupancy event handling.

## Concrete call stack

A complete trace from a button press to a Zigbee `OnOff cluster, On
command` leaving the dongle. File:line citations are accurate as of
v0.8.6. Two entry paths converge at the ZoneKeeper.

**Path 1 — Sensor event (matcher path).** A button press, a motion
sensor going `occupancy=true`, a leak sensor flipping `water_leak=true`:

```
zigbee2mqtt (MQTT JSON)                 │ nativezigbee (ZCL proto)
  pkg/iot/routers/zigbee2mqtt/          │   pkg/iot/routers/nativezigbee/
    zigbee2mqtt.go:108  DeviceRoute     │     nativezigbee.go:64  DeviceRoute
    zigbee2mqtt.go:156  dispatchEvent   │     nativezigbee.go:128 matcher.FindCondition
                       ↓                                        ↓
                  pkg/iot/bindings/match.go:164  FindCondition
                    selects the most-specific Binding for this DeviceEvent
                    (fast path returns immediately; slow path schedules
                    deferred fire after MinDuration via match.go:312)
                       ↓
                  EventReceiverService.ActivateCondition  ←─ gRPC
                       ↓
              modules/conditioner/conditioner.go:643  ActivateCondition handler
                lists this Condition's Remediations, calls one each:
                       ↓
              modules/conditioner/conditioner.go:150  activateRemediation
                151  withinActiveWindow check (time_intervals gate)
                162  if active_brightness_delta → adjustBrightness
                168  else → applyDesired(condName, zone, request, "activate")
                       ↓
              modules/conditioner/conditioner.go:73   applyDesired
                89   condStateKey(condName, zone)
                93   cache lookup; if same (state, scene) and cache
                     fresh AND Zone Status hasn't drifted OOB → SUPPRESS
                     (metric: apply_suppressed{direction=activate})
                123  cache miss → schedule.execRequest dispatches the RPC
                       ↓
              modules/conditioner/schedule.go:208  execRequest
                fans out to the matching ZoneKeeper RPC:
                219  zonekeeperClient.SetScene(...)   if sceneReq != nil
                226  zonekeeperClient.SetState(...)   if stateReq != nil
                       ↓
              ZoneKeeperService RPC  ←─ gRPC, the apply layer entry
```

**Path 2 — Periodic evaluator (Computer path).** Every `cfg.Evaluation
Interval` (60s default), independent of any external event:

```
modules/conditioner/evaluator.go:60   evaluate(ctx)
  ticks once per interval; lists all enabled Conditions
        ↓
modules/conditioner/evaluator.go:104  evaluateCompute (per active_compute Remediation)
  168  evaluateCompute
  169  withinActiveWindow check (time_intervals gate)
  174  computer.Get(rem.ActiveCompute) — registry lookup
  227  comp.Compute(ctx, time.Now(), location, args)
  243  build ApplyValuesRequest from the Computer's partial output
  252  zonekeeperClient.ApplyValues(req)
        ↓
ZoneKeeperService.ApplyValues  ←─ gRPC, rejoins path 1's exit
```

**Both paths exit through ZoneKeeper.** The ZoneKeeper is the
enforcement boundary — gRPC calls in, per-device commands out:

```
modules/zonekeeper/zonekeeper.go
  85   SetState     → GetZone(name) → zone.SetState → zone.Flush
  259  SetScene     → GetZone(name) → setZoneScene → zone.Flush
  184  ApplyValues  → GetZone(name) → zone.Set{State,Brightness,
                                              ColorTemperature,Color}
                                    → zone.Flush
        ↓
pkg/iot/zone.go:429  Flush
  switches on z.state, calls handler-per-state:
  460  handleOn          → per-device h.On(ctx, device)
  465  handleOff         → per-device h.Off(ctx, device)
  470  handleColor       → per-device h.SetColor(ctx, device, hex)
  475  handleRandomColor → pick a hex, then handleColor
  484  handleBrightness  → after handleOn, sets brightness
  496  handleColorTemperature → after handleOn, sets CT
        ↓
pkg/iot/handlers/...  Handler interface
  nativezigbee/nativezigbee.go:35  On  → builds SendCommandRequest
                                       (ZigbeeCommandOn{}), gRPC to
                                       ZigbeeCoordinator
  zigbee/...                       MQTT publish to z2m's command topic
        ↓ (nativezigbee path)
modules/zigbeecoordinator/commands.go:20  SendCommand RPC handler
  27   lookupNWK(ieee) — resolve to network short address
  44   switch on command kind, build ZCL frame:
       46  ON  → cluster 0x0006 OnOff, frame [0x01, seq, 0x01]
       50  OFF → cluster 0x0006 OnOff, frame [0x01, seq, 0x00]
       58  SetBrightness   → cluster 0x0008 Level, level + transition
       69  SetColorTemp    → cluster 0x0300 Color, mireds + transition
  85   build OutgoingMessage(NWK, endpoint, cluster, ZCL payload)
  97   dongle.Send(ctx, msg)  ←── exits the process to the radio
```

The matcher and Conditioner never know whether the exit is z2m-via-MQTT
or native-via-ZCL. The ZoneKeeper picks the Handler per Device at
flush time based on the Device's labels.

## Why ZoneKeeper is the enforcement boundary

Everything above it deals in operator intent: "this Condition fired,
apply these values." Everything below it deals in protocol: "send
this byte sequence on this cluster to this radio address."

The boundary is the in-memory `Zone` struct in `pkg/iot/zone.go`.
Conditioner RPCs mutate the Zone's desired-state fields (atomic
under a mutex); the Zone's `Flush()` method then dispatches the
minimum set of per-device commands needed to reach that state. The
`applyDesired` cache at the Conditioner layer absorbs activate
repeats; the Zone's mode/brightness/CT/color comparison absorbs
no-op flushes.

This split is why a new transport (Matter, ESPHome, Home Assistant)
plugs in by writing a new `Handler` implementation and registering
it in the Zone's device-to-handler map. The Conditioner doesn't
change. The Binding matcher doesn't change. The Computer interface
doesn't change.

## Modules

Modules are `grafana/dskit/services.Service` instances. The Manager
starts them in dependency order; each implements `starting`, `running`,
`stopping`. See `cmd/app/modules.go` for the dependency graph.

| Module                | Pod                            | Role |
|-----------------------|--------------------------------|------|
| `harvester`           | controller-receiver            | MQTT consumer. Fan-out (default 16) into routeClient.Send to keep slow downstream calls from stalling the queue. |
| `hookreceiver`        | controller-receiver            | Alertmanager webhook endpoint. Translates HTTP POSTs into `Conditioner.Alert` RPC calls. |
| `controller`          | controller-receiver            | Reconciler for `Device` and `Zone`. Syncs `iot/zone=<name>` labels onto Devices. |
| `weather`             | controller-receiver            | OpenWeatherMap polling, metrics. Legacy epoch events deprecated. |
| `router`              | controller-core                | Topic-to-handler dispatch. Owns the binding matcher. |
| `conditioner`         | controller-core                | Owns Conditions. Applies Remediations through `applyDesired`. Runs the periodic evaluator. |
| `zonekeeper`          | controller-core                | Per-zone state. Translates RPCs to Zigbee commands via z2m or the native coordinator. |
| `mqttclient`          | controller-core, -receiver     | Shared MQTT connection. |
| `kubeclient`          | controller-core, -receiver     | Shared controller-runtime cluster.Cluster with informer cache. Per-event List/Get is in-memory. |
| `zigbeecoordinator`   | controller-zigbee-coordinator  | Optional native Zigbee dongle stack. ZNP works, Ember partial. Forwards events to Router over gRPC. |

The three-pod split keeps the MQTT ingress path (receiver) separate
from the decision/apply path (core); a stall on one side doesn't pin
the other.

## Resources (CRDs)

All under apiGroup `iot.iot/v1`.

### Device

```yaml
apiVersion: iot.iot/v1
kind: Device
metadata:
  name: "0xffffb40e06036411"
spec:
  ieee_address: "0xffffb40e06036411"
  type: DEVICE_TYPE_BUTTON
  model: 3RSB22BZ
  vendor: Third Reality
```

Stamped by the z2m router on the first message from a device. The
`type` field is inferred from exposed properties (see
`pkg/iot/routers/zigbee2mqtt/devices.go::deviceType`); the inference is
re-run on every `bridge/devices` advertise. Devices are grouped into
Zones via the `iot/zone` label, set by the Zone reconciler.

### Zone

```yaml
apiVersion: iot.iot/v1
kind: Zone
metadata:
  name: bedside-zach
spec:
  devices:
    - "0x00178801046516ef"  # bedside lamp
    - "0xffffb40e06036411"  # button
status:
  state: ZONE_STATE_ON
  brightness: BRIGHTNESS_DIM
  color_temperature: COLOR_TEMPERATURE_EVENING
```

The unit of intent. `Spec.devices` is the explicit member list; the
Zone reconciler propagates `iot/zone=bedside-zach` to those Devices.
`status` is observed (last-applied), kept up to date by the ZoneKeeper
on every apply.

### Scene

```yaml
apiVersion: iot.iot/v1
kind: Scene
metadata:
  name: dusk
spec:
  brightness: BRIGHTNESS_DIM
  color_temperature: COLOR_TEMPERATURE_EVENING
```

**Today**, a Scene is a named bundle of *static absolute values* across
the `(brightness, color_temperature, color)` axes. Zones reference a
Scene by name to get all axes set in one apply. Use for "go to dusk"
semantics. Scenes are global, not per-zone — any zone can apply any
Scene.

**Conceptually**, a Scene is one point in the operator's design space.
The other point is a *dynamic* description: "set these Computers as the
source-of-truth for these axes on this zone." Today's Scene is the
all-static degenerate case of that more general shape, where each
axis's "Computer" is a constant. The same scene-application machinery
could in principle dispatch to a Computer per axis, with the operator
declaring per-axis dependencies like:

```yaml
# Hypothetical future shape — NOT in the current CRD.
spec:
  brightness_compute: cloud_cover      # daylight-compensated brightness
  color_temperature_compute: circadian  # sun-position CT
  color: ""                             # unset; bulb stays in CT mode
```

The Computer registry (`active_compute` today) already produces
partial outputs — circadian writes only CT, leaves brightness and
state alone. Scene-as-Computer-composition would surface that
partial-output composition explicitly in the CRD instead of expressing
it as multiple Conditions on the same zone with overlapping
`time_intervals`. This is parked as a design discussion; see
`docs/scratch/` if a follow-up doc has landed there.

### Condition

```yaml
apiVersion: iot.iot/v1
kind: Condition
metadata:
  name: office-sun-cct
spec:
  enabled: true
  remediations:
    - zone: office
      active_compute: sun_color_temperature
      time_intervals:
        - weekdays: ["monday:friday"]
          location: America/Denver
```

Carries one or more `Remediations`. Each Remediation says "when this
Condition is active and the time window matches, apply this state /
scene / brightness-delta / computer-output to this zone." Triggered
by:

- **Alert RPC** (from HookReceiver) when `spec.matches` labels align.
- **ActivateCondition RPC** (from Binding match) by name.
- **Periodic evaluator** (60s ticker) for Remediations with
  `time_intervals` or `active_compute`.

See [`conditioner.md`](conditioner.md) for the full trigger / apply model.

### Binding

```yaml
apiVersion: iot.iot/v1
kind: Binding
metadata:
  name: bedside-zach-single
spec:
  condition: bedside-zach-toggle
  event:
    property: action
    value: single
    selector:
      ieee: "0xffffb40e06036411"
```

Maps a normalized device event to a Condition activation. Selector
matching uses specificity-weighted resolution
(IEEE>Device>Label>DeviceType>Zone). `MinDuration` debounces flapping
sensors. See [`bindings.md`](bindings.md) for the full schema and
matcher semantics.

## Why this shape

A few design rules that drive the layout:

**Transport-agnostic matcher.** The binding matcher consumes
`DeviceEvent{Property, Value, Device}`, not transport-specific
messages. Z2M and native Zigbee both produce this shape; a future
HomeAssistant or Matter adapter only needs to translate to it. The
matcher, Conditioner, and ZoneKeeper never grow per-transport
branches.

**Idempotent apply path.** The Conditioner's `applyDesired` cache
absorbs repeat activate calls for the same `(Condition, Zone)`
when the desired `(state, scene)` hasn't changed. This is what
keeps a 60s eval-loop tick from emitting 1 Zigbee command per
minute when the underlying value didn't actually change.

**Single source of truth.** Conditions hold operator intent.
Bindings + alerts + the eval loop are *triggers* into Conditions.
The Conditioner doesn't have parallel code paths for "Binding-fired
vs Alert-fired vs eval-tick-fired" — they all route through
`activateRemediation`, which time-gates, idempotency-checks, and
dispatches uniformly.

**Per-(property, device) debounce.** `Binding.MinDuration` tracks
the current observed value at the matcher layer, not per-Binding.
A `water_leak: true → false → true` sequence resets the "true"
Binding's fired flag via the intermediate "false," so the next
sustained "true" can fire a fresh debounce cycle. The Binding
doesn't need to see the intermediate value — the matcher's
shared observed-value entry handles it.

## Migration history

The architecture has stabilized in stages, captured in
`/home/zach/.claude/plans/conditioner-unified-evaluator.md` (private)
and the project memory under
`/home/zach/.claude/projects/-home-zach-go-src-github-com-zachfi-iotcontroller/memory/`.
At a high level:

- **Stage 1**: time-gated activation on Remediations.
- **Stage 2**: `active_brightness_delta` for relative-step bindings.
- **Stage 3**: Binding migration + ActionHandler RPC retirement.
- **Stage 4**: periodic evaluator + Computer registry +
  `sun_color_temperature`, `ramp`, `rotate_colors`, `query`.
- **Stage 5**: cron `Spec.Schedule` → `time_intervals` migration.
- **Stage 6** (in progress): delete `pkg/conditioner/schedule`,
  `Epoch` RPC, `runTimer`, `Spec.Schedule`.

Each stage shipped behaviour-preserving and was verified on the live
cluster before the next began.
