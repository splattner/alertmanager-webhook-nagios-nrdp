package nrdp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/config"
)

func testCfg(url string) config.NRDPConfig {
	return config.NRDPConfig{URL: url, Token: "tok", Timeout: config.Duration(2 * time.Second)}
}

func TestClientSubmitSuccess(t *testing.T) {
	var gotToken, gotCmd, gotXML string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotToken = r.Form.Get("token")
		gotCmd = r.Form.Get("cmd")
		gotXML = r.Form.Get("XMLDATA")
		w.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(w, `<result><status>0</status><message>OK</message></result>`)
	}))
	defer srv.Close()

	c := New(testCfg(srv.URL), nil)
	if err := c.Submit(context.Background(), []CheckResult{{Type: "service", Hostname: "h", ServiceName: "s", State: 0, Output: "ok"}}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if gotToken != "tok" {
		t.Errorf("token = %q, want tok", gotToken)
	}
	if gotCmd != "submitcheck" {
		t.Errorf("cmd = %q, want submitcheck", gotCmd)
	}
	if !strings.Contains(gotXML, "<hostname>h</hostname>") {
		t.Errorf("XMLDATA missing hostname:\n%s", gotXML)
	}
}

func TestClientSubmitEmptyIsNoop(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer srv.Close()

	c := New(testCfg(srv.URL), nil)
	if err := c.Submit(context.Background(), nil); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if called {
		t.Error("Submit with no results should not make an HTTP request")
	}
}

func TestClientSubmitNRDPRejection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `<result><status>1</status><message>invalid token</message></result>`)
	}))
	defer srv.Close()

	c := New(testCfg(srv.URL), nil)
	err := c.Submit(context.Background(), []CheckResult{{Type: "host", Hostname: "h", State: 0}})
	if err == nil {
		t.Fatal("Submit: want error for nrdp status != 0")
	}
	if !strings.Contains(err.Error(), "invalid token") {
		t.Errorf("Submit error = %v, want it to mention the nrdp message", err)
	}
}

func TestClientSubmitRetries(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := attempts.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = io.WriteString(w, `<result><status>0</status><message>OK</message></result>`)
	}))
	defer srv.Close()

	cfg := testCfg(srv.URL)
	cfg.Retries = 2
	c := New(cfg, nil)
	// Client.Submit sleeps a fixed second between retries; override via a
	// short-timeout context is not applicable here since the sleep is
	// unconditional, so this test accepts the ~2s real-time cost of two
	// retries rather than mocking time.
	if err := c.Submit(context.Background(), []CheckResult{{Type: "host", Hostname: "h", State: 0}}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if attempts.Load() != 3 {
		t.Errorf("attempts = %d, want 3", attempts.Load())
	}
}

func TestClientSubmitExhaustsRetries(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := testCfg(srv.URL)
	cfg.Retries = 1
	c := New(cfg, nil)
	if err := c.Submit(context.Background(), []CheckResult{{Type: "host", Hostname: "h", State: 0}}); err == nil {
		t.Fatal("Submit: want error after exhausting retries")
	}
	if attempts.Load() != 2 {
		t.Errorf("attempts = %d, want 2 (1 initial + 1 retry)", attempts.Load())
	}
}
