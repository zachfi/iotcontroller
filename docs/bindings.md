# Bindings

A `Binding` maps a normalized device event to a `Condition`. When a
matching event fires (a button press, a leak detection, a state
change, etc.), the named Condition activates and its Remediations
apply. Bindings are the transport-agnostic event-to-policy layer:
the same Binding shape works regardless of whether the underlying
event arrived from MQTT/zigbee2mqtt or from the native Zigbee
coordinator.

## Schema

```yaml
apiVersion: iot.iot/v1
kind: Binding
metadata:
  name: bedside-zach-single
spec:
  condition: bedside-zach-toggle      # name of the Condition to activate
  event:
    property: action                  # the normalized expose name
    value: single                     # exact match
    # values:                         # alternative: multi-value match
    #   - single
    #   - 1_single
    #   - button_1_press
    selector:                         # optional; empty = match every device
      ieee: "0xffffb40e06036411"
      # device:        "<CR name>"
      # zone:          "<zone name>"
      # device_type:   "DEVICE_TYPE_BUTTON"
      # labels:        { "any": "label", "key": "value" }
    min_duration: 30s                 # optional debounce; defaults to 0
```

### Property and value

`event.property` is the normalized expose name — `action`,
`occupancy`, `water_leak`, `state`, `contact`, `tamper`, etc. (see
`pkg/iot/events` for the canonical list).

`event.value` matches exactly. For booleans use `"true"` / `"false"`;
for action enums use the action name (`"single"`, `"double"`, `"on"`).

`event.values` (list) takes precedence over `event.value` when
non-empty. Use it to alias multiple device-specific vocabularies for
the same intent — e.g. `["single", "1_single", "button_1_press"]`
when different vendors emit different strings for "primary press."

### Selector specificity

When multiple Bindings match the same event, the most specific one
wins:

| Field              | Weight |
|--------------------|--------|
| `selector.ieee`    | 16     |
| `selector.device`  | 8      |
| `selector.labels` (per key) | 4 |
| `selector.device_type` | 2  |
| `selector.zone`    | 1      |

Ties on specificity are broken by Binding name ascending. A tie is
logged at debug with the chosen winner and the tie count, so an
operator can audit "did the right binding fire?"

Use the highest specificity that captures the intent. A button-
specific override (`selector.ieee=...`) should beat a zone-default
(`selector.zone=...`), which should beat a fleet-default
(`selector.device_type=DEVICE_TYPE_BUTTON`).

### MinDuration

```yaml
spec:
  event:
    property: water_leak
    value: "true"
    min_duration: 30s
```

When set, the matcher only dispatches the ActivateCondition once the
matching event has been **continuously observed** for at least
`MinDuration` on the `(property, device)` pair. A value transition
(e.g. `true → false → true`) resets the debounce timer, even if the
intermediate value doesn't match the Binding's target.

Use for sensors that flap or for any case where a sustained signal
matters more than a single sample:

- **Leak sensors** — Sonoff/Aqara leak sensors can pulse false
  positives. A 30s `MinDuration` filters those before activating
  a pump relay.
- **Motion sensors** — a 5s `MinDuration` filters insect flyovers
  while still feeling responsive to actual presence.
- **State changes** — a 60s `MinDuration` on `state=OFF` could power
  down auxiliary equipment only after the primary has been off long
  enough to count.

The debounce is per-(property, device), shared across all Bindings
watching that pair. Two Bindings on the same `(water_leak, sensor-1)`
with different `MinDuration` values share the same observed-value
entry — a 30s Binding fires at t+30, a 60s Binding fires at t+60
from the same observation.

State is in-process; a pod restart resets every debounce timer.
First post-restart event for a value starts a fresh dwell.

#### Backward compatibility

`MinDuration: 0` (the default) bypasses the debounce path entirely.
Every existing Binding without `min_duration` keeps the historical
"fire on every match" behaviour.

#### Observability

`iotcontroller_bindings_debounce_events_total{binding, outcome}` —
counter labeled by `(start, pending, fired, suppressed)`. A flapping
sensor shows high `start` + high `pending` without `fired`. A
sustained value shows one `start` followed by one `fired` and then a
long tail of `suppressed`.

## Authoring patterns

### Single-button override

Bind a specific device's action to a specific Condition:

```yaml
spec:
  condition: bedside-zach-toggle
  event:
    property: action
    value: single
    selector:
      ieee: "0xffffb40e06036411"
```

### Zone-wide vocabulary

Bind every button in a zone to the same Condition for any of several
action vocabularies:

```yaml
spec:
  condition: office-toggle
  event:
    property: action
    values: [single, "1_single", button_1_press, tap]
    selector:
      zone: office
```

Per-IEEE Bindings (specificity 16) override the zone-wide one
(specificity 1) on devices where both apply.

### Sensor-driven actuator with debounce

```yaml
spec:
  condition: pond-pump-on
  event:
    property: water_leak
    value: "true"
    selector:
      zone: pond
    min_duration: 30s
```

The Binding fires `ActivateCondition(pond-pump-on)` after 30s of
continuous `water_leak=true`. Pair with a symmetric Binding for
`value: "false"` if you want the pump to turn off when the leak
clears.

### Bindings can't deactivate directly

A Binding only dispatches `ActivateCondition` — there is no
`DeactivateCondition` RPC. To express "fire pump off when leak
clears," use two separate Conditions (`pond-pump-on` and
`pond-pump-off`, each with `active_state: on` / `active_state: off`
respectively) and two Bindings (one for `value: "true"`, one for
`value: "false"`).

Alternative for symmetric on/off: use the Alert pipeline (which
supports both fire and resolve via Alertmanager), or write a
`Condition.matches` with `active_state` + `inactive_state` and drive
it via the legacy alert path.

## Matcher implementation

See `pkg/iot/bindings/match.go`. The `FindCondition` flow:

1. `observeValue(ev)` — unconditionally records the current value
   for `(property, device)`. A value transition resets the
   per-Binding `fired` map for that key.
2. List all Bindings in the namespace (served from the informer cache).
3. For each Binding, check `propertyMatches` + `selectorMatches`.
4. Sort matching candidates by specificity score, break ties by name.
5. If the winning candidate has `MinDuration > 0`, call
   `debounceDispatch` — fires only when dwell satisfied and not
   already fired for this stable-value window.
6. Otherwise return `winner.condition` immediately.

The matcher is called per event. There is no background goroutine,
no time-based wake-up — every fire decision happens synchronously
inside `FindCondition`. This keeps the implementation simple and
makes the behaviour fully traceable via per-event spans.
