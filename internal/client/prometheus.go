package client

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// PrometheusErrorKind classifies Prometheus client failures.
type PrometheusErrorKind string

const (
	PrometheusUnreachable PrometheusErrorKind = "unreachable"
	PrometheusTLS         PrometheusErrorKind = "tls"
	PrometheusAuth        PrometheusErrorKind = "authentication"
	PrometheusTimeout     PrometheusErrorKind = "timeout"
	PrometheusBadResponse PrometheusErrorKind = "bad_response"
	PrometheusEmptyResult PrometheusErrorKind = "empty_result"
)

// PrometheusError exposes only a sanitized host and status; it never includes
// query text, credentials, or response bodies.
type PrometheusError struct {
	Kind       PrometheusErrorKind
	Host       string
	StatusCode int
	Err        error
}

func (e *PrometheusError) Error() string {
	message := string(e.Kind)
	if e.Host != "" {
		message += " for " + e.Host
	}
	if e.StatusCode != 0 {
		message += fmt.Sprintf(" (status %d)", e.StatusCode)
	}
	if e.Err != nil {
		message += ": " + e.Err.Error()
	}
	return message
}

func (e *PrometheusError) Unwrap() error { return e.Err }

// PrometheusOptions configures a Prometheus client.
type PrometheusOptions struct {
	Endpoint          string
	SSLVerify         bool
	ConnectionTimeout time.Duration
	RequestTimeout    time.Duration
	TenantHeader      string
	BearerTokenEnv    string
	BasicUsernameEnv  string
	BasicPasswordEnv  string
}

// PrometheusClient talks to the Prometheus HTTP API.
type PrometheusClient struct {
	endpoint          *url.URL
	httpClient        *http.Client
	connectionTimeout time.Duration
	requestTimeout    time.Duration
	tenantHeader      string
	bearerTokenEnv    string
	basicUsernameEnv  string
	basicPasswordEnv  string
	retryDelay        time.Duration
}

// PrometheusSample is one vector sample.
type PrometheusSample struct {
	Metric map[string]string
	Value  float64
}

// PrometheusResult is a decoded instant-query result.
type PrometheusResult struct {
	ResultType string
	Result     []PrometheusSample
}

// NewPrometheusClient validates the endpoint and builds a bounded HTTP client.
func NewPrometheusClient(options PrometheusOptions) (*PrometheusClient, error) {
	endpoint := strings.TrimSpace(options.Endpoint)
	if endpoint == "" {
		return nil, &PrometheusError{Kind: PrometheusBadResponse, Err: errors.New("prometheus URL is empty")}
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, &PrometheusError{Kind: PrometheusBadResponse, Err: errors.New("parse prometheus URL: invalid URL")}
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, &PrometheusError{Kind: PrometheusBadResponse, Err: errors.New("prometheus URL must start with http:// or https://")}
	}
	if parsed.Host == "" {
		return nil, &PrometheusError{Kind: PrometheusBadResponse, Err: errors.New("prometheus URL has no host")}
	}
	if parsed.User != nil {
		return nil, &PrometheusError{Kind: PrometheusBadResponse, Err: errors.New("prometheus URL must not contain credentials")}
	}
	if options.ConnectionTimeout <= 0 {
		options.ConnectionTimeout = 10 * time.Second
	}
	if options.RequestTimeout <= 0 {
		options.RequestTimeout = 30 * time.Second
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: !options.SSLVerify} // #nosec G402 - explicit user setting.
	return &PrometheusClient{
		endpoint: parsed,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   options.RequestTimeout,
		},
		connectionTimeout: options.ConnectionTimeout,
		requestTimeout:    options.RequestTimeout,
		tenantHeader:      strings.TrimSpace(options.TenantHeader),
		bearerTokenEnv:    strings.TrimSpace(options.BearerTokenEnv),
		basicUsernameEnv:  strings.TrimSpace(options.BasicUsernameEnv),
		basicPasswordEnv:  strings.TrimSpace(options.BasicPasswordEnv),
		retryDelay:        100 * time.Millisecond,
	}, nil
}

// Probe checks readiness, falling back to the build-info endpoint when the
// readiness endpoint is unavailable.
func (c *PrometheusClient) Probe(ctx context.Context) error {
	if c.connectionTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.connectionTimeout)
		defer cancel()
	}
	err := c.probePath(ctx, "/-/ready")
	if err == nil {
		return nil
	}
	var promErr *PrometheusError
	if errors.As(err, &promErr) && (promErr.StatusCode == http.StatusNotFound || promErr.StatusCode == http.StatusMethodNotAllowed) {
		return c.probePath(ctx, "/api/v1/status/buildinfo")
	}
	return err
}

func (c *PrometheusClient) probePath(ctx context.Context, path string) error {
	response, err := c.request(ctx, path, nil)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return c.statusError(response.StatusCode)
	}
	return nil
}

// Query executes one instant query.
func (c *PrometheusClient) Query(ctx context.Context, query string) (PrometheusResult, error) {
	if strings.TrimSpace(query) == "" {
		return PrometheusResult{}, &PrometheusError{Kind: PrometheusBadResponse, Err: errors.New("prometheus query is empty")}
	}
	if c.requestTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.requestTimeout)
		defer cancel()
	}
	values := url.Values{}
	values.Set("query", query)
	response, err := c.request(ctx, "/api/v1/query", values)
	if err != nil {
		return PrometheusResult{}, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, response.Body)
		return PrometheusResult{}, c.statusError(response.StatusCode)
	}

	var payload struct {
		Status string `json:"status"`
		Error  string `json:"error"`
		Data   struct {
			ResultType string          `json:"resultType"`
			Result     json.RawMessage `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&payload); err != nil {
		return PrometheusResult{}, &PrometheusError{Kind: PrometheusBadResponse, Host: c.endpoint.Host, Err: errors.New("decode prometheus response")}
	}
	if payload.Status != "success" {
		return PrometheusResult{}, &PrometheusError{Kind: PrometheusBadResponse, Host: c.endpoint.Host, Err: errors.New("prometheus returned an unsuccessful response")}
	}
	result := PrometheusResult{ResultType: payload.Data.ResultType}
	switch payload.Data.ResultType {
	case "vector":
		var items []struct {
			Metric map[string]string `json:"metric"`
			Value  []any             `json:"value"`
		}
		if err := json.Unmarshal(payload.Data.Result, &items); err != nil {
			return PrometheusResult{}, &PrometheusError{Kind: PrometheusBadResponse, Host: c.endpoint.Host, Err: errors.New("decode prometheus vector")}
		}
		for _, item := range items {
			sample, err := parseSample(item.Metric, item.Value)
			if err != nil {
				return PrometheusResult{}, &PrometheusError{Kind: PrometheusBadResponse, Host: c.endpoint.Host, Err: err}
			}
			result.Result = append(result.Result, sample)
		}
	case "scalar", "string":
		var value []any
		if err := json.Unmarshal(payload.Data.Result, &value); err != nil {
			return PrometheusResult{}, &PrometheusError{Kind: PrometheusBadResponse, Host: c.endpoint.Host, Err: errors.New("decode prometheus scalar")}
		}
		sample, err := parseSample(nil, value)
		if err != nil {
			return PrometheusResult{}, &PrometheusError{Kind: PrometheusBadResponse, Host: c.endpoint.Host, Err: err}
		}
		result.Result = append(result.Result, sample)
	default:
		return PrometheusResult{}, &PrometheusError{Kind: PrometheusBadResponse, Host: c.endpoint.Host, Err: errors.New("unsupported prometheus result type")}
	}
	if len(result.Result) == 0 {
		return PrometheusResult{}, &PrometheusError{Kind: PrometheusEmptyResult, Host: c.endpoint.Host, Err: errors.New("prometheus returned no series")}
	}
	return result, nil
}

func parseSample(metric map[string]string, value []any) (PrometheusSample, error) {
	sample := PrometheusSample{Metric: metric}
	if len(value) < 2 {
		return sample, errors.New("invalid prometheus sample")
	}
	text, ok := value[1].(string)
	if !ok {
		return sample, errors.New("invalid prometheus sample value")
	}
	if _, err := fmt.Sscanf(text, "%f", &sample.Value); err != nil {
		return sample, errors.New("invalid prometheus sample value")
	}
	return sample, nil
}

func (c *PrometheusClient) request(ctx context.Context, path string, query url.Values) (*http.Response, error) {
	if query != nil {
		values := cloneValues(c.endpoint.Query())
		for key, list := range query {
			for _, value := range list {
				values.Add(key, value)
			}
		}
		query = values
	}
	requestURL := *c.endpoint
	requestURL.Path = strings.TrimRight(c.endpoint.Path, "/") + path
	requestURL.RawQuery = query.Encode()

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
		if err != nil {
			return nil, &PrometheusError{Kind: PrometheusBadResponse, Host: c.endpoint.Host, Err: errors.New("create prometheus request")}
		}
		c.applyHeaders(request)
		response, err := c.httpClient.Do(request)
		if err != nil {
			lastErr = classifyRequestError(c.endpoint.Host, err)
			return nil, lastErr
		}
		if response.StatusCode == http.StatusTooManyRequests || response.StatusCode == http.StatusServiceUnavailable {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			lastErr = c.statusError(response.StatusCode)
			if attempt == 0 {
				if err := sleepContext(ctx, c.retryDelay); err != nil {
					return nil, classifyRequestError(c.endpoint.Host, err)
				}
				continue
			}
			return nil, lastErr
		}
		return response, nil
	}
	return nil, lastErr
}

func (c *PrometheusClient) applyHeaders(request *http.Request) {
	if c.tenantHeader != "" {
		request.Header.Set("X-Scope-OrgID", c.tenantHeader)
	}
	if c.bearerTokenEnv != "" {
		if token := strings.TrimSpace(os.Getenv(c.bearerTokenEnv)); token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
			return
		}
	}
	if c.basicUsernameEnv != "" && c.basicPasswordEnv != "" {
		username := os.Getenv(c.basicUsernameEnv)
		password := os.Getenv(c.basicPasswordEnv)
		if username != "" || password != "" {
			request.SetBasicAuth(username, password)
		}
	}
}

func (c *PrometheusClient) statusError(status int) error {
	kind := PrometheusBadResponse
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		kind = PrometheusAuth
	case http.StatusGatewayTimeout:
		kind = PrometheusTimeout
	}
	return &PrometheusError{Kind: kind, Host: c.endpoint.Host, StatusCode: status}
}

func classifyRequestError(host string, err error) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return &PrometheusError{Kind: PrometheusTimeout, Host: host, Err: errors.New("request timed out or was cancelled")}
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return &PrometheusError{Kind: PrometheusTimeout, Host: host, Err: errors.New("request timed out")}
	}
	var certErr *tls.CertificateVerificationError
	if errors.As(err, &certErr) {
		return &PrometheusError{Kind: PrometheusTLS, Host: host, Err: errors.New("TLS certificate verification failed")}
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "x509") || strings.Contains(message, "tls:") || strings.Contains(message, "certificate") {
		return &PrometheusError{Kind: PrometheusTLS, Host: host, Err: errors.New("TLS handshake failed")}
	}
	return &PrometheusError{Kind: PrometheusUnreachable, Host: host, Err: errors.New("request failed")}
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func cloneValues(values url.Values) url.Values {
	cloned := make(url.Values, len(values))
	for key, list := range values {
		cloned[key] = append([]string(nil), list...)
	}
	return cloned
}
