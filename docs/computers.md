# Computers

A Computer is a Go-implemented function that takes the current eval
tick context and returns an `ApplyValues` tuple describing zone state.
The periodic evaluator invokes registered Computers each tick for any
Remediation that names them, then ships the result through the new
`ApplyValues` RPC on the ZoneKeeper. This is how the Conditioner
expresses behaviour that isn't a fixed Scene — sun-tracking color
temperature, brightness ramps, color cycling, threshold-driven state
from PromQL queries.

See `modules/conditioner/computer/` for the implementations.

## Interface

```go
type Computer interface {
    Compute(ctx context.Context, now time.Time, loc Location, args map[string]string) (ApplyValues, error)
}

type Location struct {
    Lat float64
    Lon float64
}

type ApplyValues struct {
    State            iotv1proto.ZoneState
    Brightness       iotv1proto.Brightness
    ColorTemperature iotv1proto.ColorTemperature
    Color            string
}
```

### Contract

- **Pure** of `(now, loc, args)` whenever possible. Re-invoking with
  identical inputs must produce identical outputs. `query` is the
  one exception — it consults Prometheus, and falls back to a per-
  args-hash cache on transient failure to keep the contract usable
  under load.
- **Partial output**. Each Computer typically sets one or two fields
  of `ApplyValues` and leaves the rest zero-valued. The ZoneKeeper's
  `ApplyValues` server applies only non-zero fields, so a Computer
  that produces only `ColorTemperature` doesn't disturb the zone's
  brightness, state, or color.
- **Fast**. The eval loop walks every enabled Condition each tick.
  Compute should return in milliseconds; offload anything slower to
  the Computer's own cache.

## Registry

Computers register themselves at package `init()` time:

```go
func init() {
    computer.Register(MyComputerName, &myComputer{})
}
```

The eval loop resolves names via `computer.Get(name)`. Unknown names
increment `iotcontroller_conditioner_evaluation_compute_unknown_total
{compute}` and skip the Remediation — the operator's signal that they
authored a Condition before the Computer was built.

The `query` Computer is the one exception: it's registered from
`conditioner.New(...)` only when `cfg.Query.Endpoint` is non-empty.
Without an endpoint, every reference to `active_compute: query`
shows up in the unknown counter, which is the desired signal.

## Built-in Computers

### `sun_color_temperature`

Pure function of `(now, lat, lon)` → `ColorTemperature`. Maps the
current solar position to one of the five named enum steps:

| Window                          | CT |
|---------------------------------|----|
| `[rise - 30m, rise)`            | `FIRSTLIGHT` |
| `[rise, rise + 2h)`             | `MORNING` |
| `[rise + 2h, set - 2h)`         | `DAY` |
| `[set - 2h, set + 30m)`         | `LATEAFTERNOON` |
| outside all of above            | `EVENING` (warm) |

Solar events come from `github.com/nathan-osman/go-sunrise`. The
classifier checks both today's and yesterday's windows so a tick
that lands in calendar-UTC tomorrow (because local sunset crossed
midnight) still buckets correctly as today's solar day. The EVENING
fallthrough covers the long night between today's `set + 30m` and
tomorrow's `rise - 30m`.

Use:

```yaml
remediations:
  - zone: office
    active_compute: sun_color_temperature
    time_intervals:
      - weekdays: ["monday:friday"]
        location: America/Denver
```

Computer returns only `ColorTemperature` — zones that are off stay
off; the computed CT applies on the next state change. Operator-set
brightness, state, color all stay intact.

### `ramp`

Pure function of `(now, args)` interpolating `Brightness` or
`ColorTemperature` linearly over a daily window.

Args:

```yaml
active_compute: ramp
active_compute_args:
  field: brightness           # brightness | color_temperature
  from: BRIGHTNESS_LOW
  to: BRIGHTNESS_FULL
  start_at: "07:00"           # HH:MM 24-hour
  duration: "30m"             # Go duration
  timezone: America/Denver    # IANA, default UTC
```

Behaviour:

| Time                                | Output |
|-------------------------------------|--------|
| `now < today's start_at`            | `ApplyValues{}` (no apply) |
| `now in [start_at, start_at+duration]` | interpolated step toward `to` |
| `now > today's end`                 | `ApplyValues{}` (no apply) |

The `ApplyValues{}` returns let other Remediations (Scenes,
button-driven Conditions) own the zone outside the ramp window.

Interpolation uses *physical* order, not the proto enum integers.
For Brightness the proto order (`FULL=1, DIM=2, …, VERYLOW=6`) is
roughly the reverse of perceptual order; a custom table
(`brightnessPhysical`) maps to perceptual sequence. ColorTemperature
already matches enum order but uses the same table-driven approach
for consistency.

### `rotate_colors`

Pure function of `(now, args)` returning a hex color from a pool.
Subsumes the legacy `ZONE_STATE_RANDOMCOLOR` semantic.

Args:

```yaml
active_compute: rotate_colors
active_compute_args:
  pool: "#FF0000,#00FF00,#0000FF"
  mode: sequential              # sequential | random; default sequential
  interval: 5s                  # how long each color stays active; default 5s
```

Index derivation: `bucket = floor(now.UnixNano() / interval.Nanoseconds())`.

- `sequential`: `idx = bucket % len(pool)`
- `random`: seed a PCG PRNG with the bucket index, draw one
  `IntN(len(pool))`. Same `now` → same color (deterministic), but
  the sequence appears scrambled.

Returns only `Color`. Pair with a Scene that sets `state: COLOR`
to drive the actual color output rather than just record an unused
field.

### `query`

PromQL-driven Computer. Thresholds the query result against zero and
returns one of two operator-supplied `ApplyValues` tuples.

Configured at the controller level:

```
-conditioner.query.endpoint=<URL>
-conditioner.query.tenant=ops
-conditioner.query.timeout=5s
-conditioner.query.auth-token-env-var=PROM_BEARER_TOKEN
```

Used in a Remediation:

```yaml
active_compute: query
active_compute_args:
  query: avg_over_time(iot_zigbee2mqtt_temperature{zone="office"}[5m]) > 22.5
  on_true.state: ZONE_STATE_ON
  on_true.brightness: BRIGHTNESS_DIM
  on_false.state: ZONE_STATE_OFF
```

Behaviour:

| Result                | Output |
|-----------------------|--------|
| `result > 0`          | `on_true.*` values |
| `result == 0 or empty vector` | `on_false.*` values |
| HTTP / parse failure, cache hit | cached `ApplyValues` (last successful) |
| HTTP / parse failure, no cache | error → eval loop counts `compute_error`, no apply |

The cache is keyed by `sha256(canonicalize(args))` — two Conditions
with identical args share one entry, intentionally lossless. Pod
restart wipes the cache; first post-restart failure surfaces as a
real error until one success populates it.

This is the right tool for slow-rolling threshold conditions
(temperature, humidity, power consumption). The 60s eval-loop
cadence dominates the response latency, which is fine for these
use cases. For instant events (button press, leak detection,
occupancy), use a Binding instead — those activate in ~1 ms.

## Adding a new Computer

1. Create `modules/conditioner/computer/<name>.go` with a Compute
   implementation and an `init()` that calls
   `computer.Register(Name, instance)`.
2. Document the args contract in the Computer's doc comment.
3. Write tests with a controllable clock (`time.Time` injection)
   so the Computer's behaviour is verifiable without sleeping.
4. Add metrics if the Computer has unique failure modes (the
   eval-loop's `compute_error` counter is generic; per-Computer
   metrics live in `modules/conditioner/metrics.go` or local to
   the Computer package).

The Computer interface itself is intentionally small. State that
needs to live across ticks goes in `args` (e.g. ramp's `start_at` +
`duration`) or is derived from `now` directly (rotate_colors's
bucket index). Anything that genuinely can't be pure — like
`query`'s HTTP request — owns its own in-process cache and is
registered via a constructor rather than init().
