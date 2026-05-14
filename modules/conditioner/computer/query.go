package computer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
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
// The HTTP client is process-wide and reused across every Compute
// call — connection pooling against Mimir / Prometheus stays warm,
// and a slow query consumes one in-flight goroutine, not a fresh TCP
// handshake each tick.
func NewQuery(cfg QueryConfig) Computer {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	var token string
	if cfg.AuthTokenEnvVar != "" {
		token = os.Getenv(cfg.AuthTokenEnvVar)
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &query{
		client: &http.Client{Timeout: timeout},
		cfg:    cfg,
		token:  token,
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
//
// Behaviour:
//
//	result > 0:  return on_true.*
//	result == 0 or empty:  return on_false.*
//	HTTP / parse error: return last-known-good ApplyValues for this
//	                     args set if cached; else return error (the
//	                     eval loop counts it as compute_error). On
//	                     cached fallback the failure is logged at
//	                     debug; the operator sees the failed metric
//	                     and reconciles with logs.
//
// The cache key is sha256(canonicalize(args)). Two Conditions with
// identical args share one cache entry — they'd compute the same
// ApplyValues anyway, so collision is intentional and lossless.
type query struct {
	client *http.Client
	cfg    QueryConfig
	token  string
	logger *slog.Logger

	cacheMu sync.Mutex
	cache   map[string]ApplyValues
}

// promResult is the subset of Prometheus's query-API response shape
// that we consume. We only support `resultType=scalar` and
// `resultType=vector` (taking the first sample of the vector).
type promResult struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string          `json:"resultType"`
		Result     json.RawMessage `json:"result"`
	} `json:"data"`
	ErrorType string `json:"errorType"`
	Error     string `json:"error"`
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

	value, err := q.fetch(ctx, promql, now)
	if err != nil {
		// Transient failure path: return cached last-known-good if
		// any, with err==nil so the eval loop applies it. Cached
		// fallback is silent except for a debug log so operators can
		// trace post-hoc.
		q.cacheMu.Lock()
		cached, ok := q.cache[cacheKey]
		q.cacheMu.Unlock()
		if ok {
			q.logger.Debug("query: HTTP/parse failure, returning cached",
				slog.String("promql", promql),
				slog.String("error", err.Error()),
			)
			return cached, nil
		}
		// First-time failure with no cache: surface the error so the
		// eval loop can count it.
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

// fetch issues a Prometheus /api/v1/query against q.cfg.Endpoint and
// returns the scalar value (first sample for vector results). Returns
// an error on any HTTP, parse, or response-status problem.
func (q *query) fetch(ctx context.Context, promql string, now time.Time) (float64, error) {
	endpoint := strings.TrimRight(q.cfg.Endpoint, "/")
	u, err := url.Parse(endpoint + "/api/v1/query")
	if err != nil {
		return 0, fmt.Errorf("parse endpoint %q: %w", q.cfg.Endpoint, err)
	}
	params := u.Query()
	params.Set("query", promql)
	params.Set("time", strconv.FormatInt(now.Unix(), 10))
	u.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}
	if q.cfg.Tenant != "" {
		req.Header.Set("X-Scope-OrgID", q.cfg.Tenant)
	}
	if q.token != "" {
		req.Header.Set("Authorization", "Bearer "+q.token)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := q.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("HTTP: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}

	var pr promResult
	if err := json.Unmarshal(body, &pr); err != nil {
		return 0, fmt.Errorf("parse JSON: %w", err)
	}
	if pr.Status != "success" {
		return 0, fmt.Errorf("response status %q: %s: %s", pr.Status, pr.ErrorType, pr.Error)
	}

	switch pr.Data.ResultType {
	case "scalar":
		// [<unix>, "<value>"]
		var pair [2]json.RawMessage
		if err := json.Unmarshal(pr.Data.Result, &pair); err != nil {
			return 0, fmt.Errorf("parse scalar: %w", err)
		}
		return parseFloatValue(pair[1])

	case "vector":
		// [{metric, value: [<unix>, "<value>"]}, ...]. Take first.
		var samples []struct {
			Value [2]json.RawMessage `json:"value"`
		}
		if err := json.Unmarshal(pr.Data.Result, &samples); err != nil {
			return 0, fmt.Errorf("parse vector: %w", err)
		}
		if len(samples) == 0 {
			// Empty vector: nothing matched. Treat as zero —
			// operators can use on_false to express "no instances"
			// behaviour.
			return 0, nil
		}
		return parseFloatValue(samples[0].Value[1])

	default:
		return 0, fmt.Errorf("unsupported resultType %q", pr.Data.ResultType)
	}
}

// parseFloatValue strips the quotes Prometheus wraps numeric values in
// when serializing the [time, value] pair.
func parseFloatValue(raw json.RawMessage) (float64, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		// Some servers return the value already as a number rather
		// than a quoted string. Fall back.
		var f float64
		if err2 := json.Unmarshal(raw, &f); err2 == nil {
			return f, nil
		}
		return 0, fmt.Errorf("parse value: %w", err)
	}
	return strconv.ParseFloat(s, 64)
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
