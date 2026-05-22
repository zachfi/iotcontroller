package computer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

// QueryName is the registered name. Conditions reference the computer
// via Remediation.ActiveCompute = QueryName. The computer is
// registered only when the conditioner is configured with a non-empty
// Query.Endpoint — operators that don't pull from Prometheus get no
// `query` Computer at all and any Condition referencing it shows up
// in iotcontroller_conditioner_evaluation_compute_unknown_total.
const QueryName = "query"

// QueryConfig is the constructor input for NewQuery. Endpoint and
// (optionally) Tenant + AuthToken come from operator-level controller
// flags; per-Condition behaviour (the PromQL string, the on_true /
// on_false ApplyValues) lives in Remediation.ActiveComputeArgs.
//
// AuthTokenEnvVar names a process env var. The token is read at
// NewQuery time (not on each Compute) so secret rotation requires a
// pod restart — same trade-off env-var secretKeyRef ships with
// elsewhere in iotcontroller.
type QueryConfig struct {
	Endpoint        string
	Tenant          string
	Timeout         time.Duration
	AuthTokenEnvVar string
	// Logger is optional. When nil, the computer emits no logs.
	Logger *slog.Logger
}

// NewQuery builds a query Computer with a configured HTTP client. The
// returned Computer is ready to register: callers typically do
// `computer.Register(QueryName, NewQuery(cfg))` from the conditioner
// module's start hook when cfg.Endpoint is non-empty.
//
// The HTTP client lives inside a shared *promClient — both `query`
// and `prom_scalar` (continuous-output sibling) construct their own
// promClient from the same QueryConfig, so each Computer gets its
// own pooled connections to Mimir but the wire-level fetch logic is
// the same.
func NewQuery(cfg QueryConfig) Computer {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &query{
		pc:     newPromClient(cfg),
		logger: logger.With("computer", QueryName),
		cache:  map[string]ApplyValues{},
	}
}

// query pulls a PromQL scalar from a configured Prometheus / Mimir
// endpoint, thresholds the result against zero, and returns one of two
// operator-supplied ApplyValues tuples (on_true / on_false).
//
// Args:
//
//	query           PromQL expression. Required.
//	on_true.state            ZoneState enum name (e.g. "ZONE_STATE_ON").
//	on_true.brightness       Brightness enum name. Defaults to UNSPECIFIED.
//	on_true.color_temperature  ColorTemperature enum name.
//	on_true.color            Hex color "#RRGGBB".
//	on_false.{state,brightness,color_temperature,color}  Same shape; defaults to UNSPECIFIED / "".
//	on_error.{state,brightness,color_temperature,color}  OPTIONAL fail-safe
//	                  applied when the PromQL fetch errors. Takes
//	                  precedence over the cache fallback so operators
//	                  can declare an explicit safe direction
//	                  (e.g. pump → OFF) instead of inheriting whatever
//	                  the last successful query returned.
//
// Behaviour:
//
//	result > 0:  return on_true.*
//	result == 0 or empty:  return on_false.*
//	HTTP / parse error: fail-safe resolution order —
//	                     1. if any on_error.* arg is set, return parsed
//	                        on_error.* (the operator's declared safe
//	                        direction);
//	                     2. else if a previous Compute for this args set
//	                        succeeded, return the cached result;
//	                     3. else surface the error so the eval loop
//	                        counts it as compute_error.
//	                     Step 1 exists for safety-critical zones (pump,
//	                     heater) where "keep whatever was last applied"
//	                     is the wrong direction during an outage. Step 2
//	                     preserves the previous lighting-zone behavior
//	                     where a Mimir blip shouldn't toggle the lamp.
//
// The cache key is sha256(canonicalize(args)). Two Conditions with
// identical args share one cache entry — they'd compute the same
// ApplyValues anyway, so collision is intentional and lossless.
type query struct {
	pc     *promClient
	logger *slog.Logger

	cacheMu sync.Mutex
	cache   map[string]ApplyValues
}

func (q *query) Compute(ctx context.Context, now time.Time, _ Location, args map[string]string) (ApplyValues, error) {
	promql := strings.TrimSpace(args["query"])
	if promql == "" {
		return ApplyValues{}, fmt.Errorf("query: args.query (PromQL) is required")
	}

	cacheKey := hashArgs(args)

	// Read the operator-visible labels the eval loop injects so the
	// metric labels read as "condition X, zone Y" rather than an
	// args-hash. Empty labels are harmless; they just produce a less
	// informative time series.
	condLabel := args["_condition"]
	zoneLabel := args["_zone"]

	value, err := q.pc.Fetch(ctx, promql, now)
	if err != nil {
		// Resolution order: declared fail-safe > cache > error.
		// Operators of safety-critical zones (pump, heater) MUST set
		// on_error.* to claim explicit direction; lighting Conditions
		// without on_error.* keep the pre-existing cache-fallback
		// behavior so a Mimir blip doesn't toggle the lamp.
		if hasOnErrorArgs(args) {
			failSafe, perr := parseApplyValues(args, "on_error")
			if perr != nil {
				return ApplyValues{}, fmt.Errorf("query: on_error parse: %w", perr)
			}
			metricQueryFailSafe.WithLabelValues(condLabel, zoneLabel).Inc()
			q.logger.Debug("query: fetch failed, returning declared on_error fail-safe",
				slog.String("promql", promql),
				slog.String("error", err.Error()),
			)
			return failSafe, nil
		}
		q.cacheMu.Lock()
		cached, ok := q.cache[cacheKey]
		q.cacheMu.Unlock()
		if ok {
			q.logger.Debug("query: HTTP/parse failure, returning cached last-known-good",
				slog.String("promql", promql),
				slog.String("error", err.Error()),
			)
			return cached, nil
		}
		// First-time failure with no cache and no fail-safe declared:
		// surface the error so the eval loop can count it.
		return ApplyValues{}, fmt.Errorf("query: %w", err)
	}

	metricQueryValue.WithLabelValues(condLabel, zoneLabel).Set(value)

	var prefix string
	var outcome float64
	if value > 0 {
		prefix = "on_true"
		outcome = 1
	} else {
		prefix = "on_false"
		outcome = 0
	}
	metricQueryOutcome.WithLabelValues(condLabel, zoneLabel).Set(outcome)

	vals, perr := parseApplyValues(args, prefix)
	if perr != nil {
		return ApplyValues{}, fmt.Errorf("query: %w", perr)
	}

	q.cacheMu.Lock()
	q.cache[cacheKey] = vals
	q.cacheMu.Unlock()

	return vals, nil
}

// hashArgs produces a stable cache key for the args map. Map iteration
// order in Go is randomized, so we sort the keys before hashing to get
// a deterministic key. Two args maps with identical content hash to
// the same key regardless of authoring order.
func hashArgs(args map[string]string) string {
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		fmt.Fprintf(h, "%s=%s\x00", k, args[k])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// onErrorKeys lists the arg names that, if any is set, indicate the
// operator has declared a fail-safe direction. Used by Compute to
// route fetch errors through parseApplyValues(args, "on_error")
// instead of cache fallback.
var onErrorKeys = []string{
	"on_error.state",
	"on_error.brightness",
	"on_error.color_temperature",
	"on_error.color",
}

// hasOnErrorArgs returns true if at least one on_error.* arg carries a
// non-empty value. The empty-string check matters because operators
// may template the key in but leave the value blank for axes they
// don't want to touch — that's not a declared fail-safe, just a
// scaffolding artifact.
func hasOnErrorArgs(args map[string]string) bool {
	for _, k := range onErrorKeys {
		if strings.TrimSpace(args[k]) != "" {
			return true
		}
	}
	return false
}

// parseApplyValues reads on_true.* / on_false.* keys from args and
// builds an ApplyValues tuple. Unset keys leave the corresponding
// field at its zero value; this is the same partial-apply pattern
// every other Computer uses.
func parseApplyValues(args map[string]string, prefix string) (ApplyValues, error) {
	var vals ApplyValues

	if s := args[prefix+".state"]; s != "" {
		if v, ok := iotv1proto.ZoneState_value[s]; ok {
			vals.State = iotv1proto.ZoneState(v)
		} else {
			return ApplyValues{}, fmt.Errorf("unknown %s.state %q", prefix, s)
		}
	}
	if s := args[prefix+".brightness"]; s != "" {
		b, ok := parseBrightness(s)
		if !ok {
			return ApplyValues{}, fmt.Errorf("unknown %s.brightness %q", prefix, s)
		}
		vals.Brightness = b
	}
	if s := args[prefix+".color_temperature"]; s != "" {
		c, ok := parseColorTemp(s)
		if !ok {
			return ApplyValues{}, fmt.Errorf("unknown %s.color_temperature %q", prefix, s)
		}
		vals.ColorTemperature = c
	}
	if s := args[prefix+".color"]; s != "" {
		// Accept either "#RRGGBB" or "RRGGBB"; normalize.
		s = strings.TrimSpace(s)
		if !hexColorRE.MatchString(s) {
			return ApplyValues{}, fmt.Errorf("invalid %s.color %q (want #RRGGBB)", prefix, s)
		}
		if !strings.HasPrefix(s, "#") {
			s = "#" + s
		}
		vals.Color = strings.ToUpper(s)
	}
	return vals, nil
}
