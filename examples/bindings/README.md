# Example Bindings + the default button convention

A `Binding` maps a normalized device event (property + value, optionally
scoped via Selector) to a named `Condition`. When the event fires, the
Condition's remediations are applied. The matcher returns the
highest-specificity Binding for a given event; ties break by sorted
Binding name. See `api/v1/binding_types.go` for the full schema.

## What goes where

| File | Demonstrates |
|---|---|
| `zigbee2mqtt-button.yaml` | A z2m-discovered button, IEEE-scoped event Bindings. |
| `third-reality-button.yaml` | Single button with three actions (`single` / `double` / `hold`) covering the full default convention below, including `active_state: toggle`. |

## The default convention for single-button remotes

Most cheap one-button remotes (Third Reality 3RSB22BZ, Aqara WXKG11LM,
the Sonoff SNZB-01, etc.) expose three actions: `single`, `double`,
`hold`. Multi-button scene controllers (Moes XH-SY-04Z and friends)
expose `N_single` / `N_double` per button. With only one button, the
expressive choice is:

```
single → toggle      (predictable approach to lights — "make it
                      whatever the opposite of now is")
double → on / full   (decisive "lights on, full brightness")
hold   → on / dim    (decisive "lights on, soft")
```

The matching Conditions look like:

```yaml
# Three Conditions per zone, reusable across every button in that zone.
apiVersion: iot.iot/v1
kind: Condition
metadata:
  name: <zone>-toggle
  namespace: iot
spec:
  enabled: true
  remediations:
    - zone: <zone>
      active_state: toggle      # ← resolved to on/off based on current state
---
apiVersion: iot.iot/v1
kind: Condition
metadata:
  name: <zone>-full
spec:
  enabled: true
  remediations:
    - zone: <zone>
      active_state: on
      active_scene: full-bright # ← Scene CR with brightness=FULL, colorTemp=DAY
---
apiVersion: iot.iot/v1
kind: Condition
metadata:
  name: <zone>-dim
spec:
  enabled: true
  remediations:
    - zone: <zone>
      active_state: on
      active_scene: dim          # ← Scene CR with brightness=DIM
```

Then the Bindings:

```yaml
spec: { event: { property: action, value: single, selector: { ieee: 0x… } }, condition: <zone>-toggle }
spec: { event: { property: action, value: double, selector: { ieee: 0x… } }, condition: <zone>-full   }
spec: { event: { property: action, value: hold,   selector: { ieee: 0x… } }, condition: <zone>-dim    }
```

For multi-button remotes (Moes 4-button), apply the same pattern per
button:

```
1_single → <zone>-toggle      1_double → <zone>-full      1_hold → <zone>-dim
2_single → <zone-other>-toggle
3_single → <zone>-randomcolor      # bonus pattern: a button for "party mode"
```

## Why `toggle` is a first-class state

Without it, the most natural use of a single button — "make the room
the opposite of how it is now" — isn't expressible from a Binding,
because every other `active_state` is a fixed enum. The legacy
`ActionHandler.ActionSingle` had toggle baked in as a code path, but
that was only reachable when the Binding didn't match. As the
ActionHandler retires, toggle has to live in the Condition layer.

`active_state: toggle` is resolved by the Conditioner at apply time:
it reads the zone's CRD `status.state`, picks the opposite, and sends
a concrete `on`/`off` SetState to the ZoneKeeper. Everything downstream
(applyDesired idempotency cache, zone flush dedup, Zigbee transport)
only ever sees `on` or `off`.

A side-effect of going through the cache: two rapid presses while the
zone's status hasn't propagated yet both resolve to the same direction
and the second is absorbed by the cache — physical-button-bounce
debounce, not a missed toggle. If you intentionally want N toggles
faster than the status-update round-trip, that's a config concern
(`-conditioner.apply-desired-refresh-age`) not an API one.

## Cross-device vocabulary: `event.values`

Different button families emit different action strings for the same
physical intent: Third Reality says `single`, Moes says `1_single`,
Philips/Hue says `on_press`. To avoid writing N near-identical Bindings
per zone, an `EventTrigger` can list multiple accepted values:

```yaml
spec:
  event:
    property: action
    values: [single, 1_single, button_1_press]
    selector:
      zone: office
  condition: office-toggle
```

This one Binding handles "primary press" on every device in `office`,
whichever vocabulary they emit. Rules:

- `value` (singular) and `values` (list) coexist on the schema.
- When `values` is non-empty it wins; `value` is ignored.
- Both empty → wildcard match on the property.

**Reserve `values` for aliases of the same intent.** Bindings stay 1:1
with Conditions; if `single` should fire one Condition and `double`
another, write two Bindings. Don't try to express different intents
through a single multi-value list.

## Specificity recap

The matcher's selector precedence (highest specificity wins; ties
broken by sorted Binding name):

```
IEEE             = 16
Device           =  8
LabelSelector    =  4 per key
DeviceType       =  2
Zone             =  1
(no selector)    =  0
```

So a "fleet default" Binding (`selector.device_type=DEVICE_TYPE_BUTTON`,
condition=any-button-toggle) loses cleanly to a per-IEEE override.
