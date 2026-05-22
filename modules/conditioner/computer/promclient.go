package computer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// promclient.go — shared Prometheus / Mimir HTTP fetch used by every
// PromQL-backed Computer in this package. Both the discrete `query`
// Computer and the continuous `prom_scalar` Computer construct a
// *promClient from the operator-configured QueryConfig and call
// Fetch to pull a scalar.
//
// Extracted from query.go so prom_scalar doesn't duplicate the
// auth / tenant / parsing logic. The wire-level behavior is identical
// to what query.go shipped previously; tests in query_test.go are
// the regression net.

// promClient wraps an *http.Client with the operator-configured
// PromQL endpoint, optional tenant header, and optional bearer
// token. The HTTP client is reused across every Fetch call —
// connection pooling stays warm across the eval loop's per-tick
// fan-out.
type promClient struct {
	endpoint string
	tenant   string
	token    string
	client   *http.Client
}

// newPromClient builds a promClient from a QueryConfig. Tenant and
// auth token are read once at construction (token from the named env
// var); rotation requires a pod restart, same trade-off the rest of
// the conditioner ships with.
func newPromClient(cfg QueryConfig) *promClient {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	var token string
	if cfg.AuthTokenEnvVar != "" {
		token = os.Getenv(cfg.AuthTokenEnvVar)
	}
	return &promClient{
		endpoint: cfg.Endpoint,
		tenant:   cfg.Tenant,
		token:    token,
		client:   &http.Client{Timeout: timeout},
	}
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

// Fetch issues a Prometheus /api/v1/query against the configured
// endpoint and returns the scalar value (first sample for vector
// results, 0 for empty vectors). Returns an error on any HTTP, parse,
// or response-status problem.
//
// `now` is used as the `time` query param so callers with injected
// clocks (tests) get reproducible queries.
func (p *promClient) Fetch(ctx context.Context, promql string, now time.Time) (float64, error) {
	endpoint := strings.TrimRight(p.endpoint, "/")
	u, err := url.Parse(endpoint + "/api/v1/query")
	if err != nil {
		return 0, fmt.Errorf("parse endpoint %q: %w", p.endpoint, err)
	}
	params := u.Query()
	params.Set("query", promql)
	params.Set("time", strconv.FormatInt(now.Unix(), 10))
	u.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}
	if p.tenant != "" {
		req.Header.Set("X-Scope-OrgID", p.tenant)
	}
	if p.token != "" {
		req.Header.Set("Authorization", "Bearer "+p.token)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
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
		return parsePromFloat(pair[1])

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
			// callers can interpret 0 as their off-side direction.
			return 0, nil
		}
		return parsePromFloat(samples[0].Value[1])

	default:
		return 0, fmt.Errorf("unsupported resultType %q", pr.Data.ResultType)
	}
}

// parsePromFloat strips the quotes Prometheus wraps numeric values in
// when serializing the [time, value] pair. Some servers return the
// number unquoted; fall back to direct float parsing in that case.
func parsePromFloat(raw json.RawMessage) (float64, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		var f float64
		if err2 := json.Unmarshal(raw, &f); err2 == nil {
			return f, nil
		}
		return 0, fmt.Errorf("parse value: %w", err)
	}
	return strconv.ParseFloat(s, 64)
}
