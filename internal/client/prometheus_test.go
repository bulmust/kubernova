package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewPrometheusClientRejectsInvalidURL(t *testing.T) {
	if _, err := NewPrometheusClient(PrometheusOptions{Endpoint: "localhost:9090"}); err == nil {
		t.Fatal("NewPrometheusClient() error = nil, want invalid scheme error")
	}
}

func testPrometheusClient(t *testing.T, endpoint string, verify bool) *PrometheusClient {
	t.Helper()
	client, err := NewPrometheusClient(PrometheusOptions{
		Endpoint:          endpoint,
		SSLVerify:         verify,
		ConnectionTimeout: time.Second,
		RequestTimeout:    time.Second,
	})
	if err != nil {
		t.Fatalf("NewPrometheusClient() error = %v", err)
	}
	client.retryDelay = time.Millisecond
	return client
}

func TestPrometheusQueryDecodesVectorAndHeaders(t *testing.T) {
	t.Setenv("PROM_TOKEN", "secret-token")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query" {
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("query"); got != "up" {
			t.Errorf("query = %q, want up", got)
		}
		if got := r.Header.Get("X-Scope-OrgID"); got != "tenant-a" {
			t.Errorf("tenant header = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret-token" {
			t.Errorf("authorization header = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"pod":"api","container":"api"},"value":[1,"1.25"]}]}}`)
	}))
	defer server.Close()

	client, err := NewPrometheusClient(PrometheusOptions{
		Endpoint:          server.URL,
		ConnectionTimeout: time.Second,
		RequestTimeout:    time.Second,
		TenantHeader:      "tenant-a",
		BearerTokenEnv:    "PROM_TOKEN",
	})
	if err != nil {
		t.Fatalf("NewPrometheusClient() error = %v", err)
	}
	result, err := client.Query(context.Background(), "up")
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if len(result.Result) != 1 || result.Result[0].Value != 1.25 || result.Result[0].Metric["pod"] != "api" {
		t.Errorf("Query() = %#v", result)
	}
}

func TestPrometheusQueryRetries429And503Once(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1,"2"]}]}}`)
	}))
	defer server.Close()
	client := testPrometheusClient(t, server.URL, false)
	if _, err := client.Query(context.Background(), "up"); err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if attempts != 2 {
		t.Errorf("attempts = %d, want 2", attempts)
	}
}

func TestPrometheusQueryClassification(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		kind   PrometheusErrorKind
	}{
		{"empty", http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[]}}`, PrometheusEmptyResult},
		{"auth", http.StatusUnauthorized, ``, PrometheusAuth},
		{"bad", http.StatusBadRequest, `{"status":"error","error":"bad query"}`, PrometheusBadResponse},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.status)
				_, _ = fmt.Fprint(w, test.body)
			}))
			defer server.Close()
			client := testPrometheusClient(t, server.URL, false)
			_, err := client.Query(context.Background(), "up")
			var promErr *PrometheusError
			if !errors.As(err, &promErr) || promErr.Kind != test.kind {
				t.Fatalf("Query() error = %v, want kind %s", err, test.kind)
			}
		})
	}
}

func TestPrometheusTLSVerification(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1,"1"]}]}}`)
	}))
	defer server.Close()

	if _, err := testPrometheusClient(t, server.URL, false).Query(context.Background(), "up"); err != nil {
		t.Fatalf("Query() with ssl_verify=false error = %v", err)
	}
	_, err := testPrometheusClient(t, server.URL, true).Query(context.Background(), "up")
	var promErr *PrometheusError
	if !errors.As(err, &promErr) || promErr.Kind != PrometheusTLS {
		t.Fatalf("Query() with ssl_verify=true error = %v, want TLS", err)
	}
}

func TestPrometheusProbeFallsBackToBuildInfo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/-/ready" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/api/v1/status/buildinfo" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	if err := testPrometheusClient(t, server.URL, false).Probe(context.Background()); err != nil {
		t.Errorf("Probe() error = %v", err)
	}
}

func TestPrometheusCancellation(t *testing.T) {
	block := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
	}))
	defer func() {
		close(block)
		server.Close()
	}()
	client := testPrometheusClient(t, server.URL, false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client.Query(ctx, "up")
	var promErr *PrometheusError
	if !errors.As(err, &promErr) || promErr.Kind != PrometheusTimeout {
		t.Fatalf("Query() error = %v, want timeout/cancelled", err)
	}
}
