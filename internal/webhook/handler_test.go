package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/alertmanager"
	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/checkresult"
	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/config"
	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/mapping"
	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/nrdp"
	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/nrdpstate"
)

type fakeSubmitter struct {
	submitted [][]nrdp.CheckResult
	err       error
}

func (f *fakeSubmitter) Submit(_ context.Context, results []nrdp.CheckResult) error {
	f.submitted = append(f.submitted, results)
	return f.err
}

func testHandler(t *testing.T, submitter Submitter) *Handler {
	t.Helper()
	cfg := &config.Config{
		State: config.StateConfig{
			SeverityLabel: "severity",
			Service:       config.StateMapConfig{Values: map[string]string{"ok": "ok", "critical": "critical"}, Unmatched: "unknown", Resolved: "ok"},
			Host:          config.StateMapConfig{Values: map[string]string{"ok": "up", "critical": "down"}, Unmatched: "down", Resolved: "up"},
		},
		Rules: []config.RuleConfig{{
			Name:      "default",
			CheckType: config.CheckTypeService,
			Host:      &config.TargetTemplate{Template: "{{ .Labels.instance }}"},
			Service:   &config.TargetTemplate{Template: "{{ .Labels.alertname }}"},
		}},
	}
	engine, err := mapping.New(cfg)
	if err != nil {
		t.Fatalf("mapping.New: %v", err)
	}
	aggregator, err := checkresult.NewAggregator(config.AggregationConfig{IdentifyBy: "instance"})
	if err != nil {
		t.Fatalf("checkresult.NewAggregator: %v", err)
	}
	return &Handler{
		Engine:        engine,
		Aggregator:    aggregator,
		State:         nrdpstate.New(cfg.State),
		Client:        submitter,
		MaxBodyBytes:  1 << 20,
		SubmitTimeout: 5 * time.Second,
		Log:           slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func postPayload(t *testing.T, h *Handler, payload alertmanager.WebhookPayload) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHandlerSubmitsMappedAlerts(t *testing.T) {
	sub := &fakeSubmitter{}
	h := testHandler(t, sub)

	rec := postPayload(t, h, alertmanager.WebhookPayload{
		Status: "firing",
		Alerts: []alertmanager.Alert{
			{Status: "firing", Labels: map[string]string{"instance": "db1", "alertname": "DiskFull", "severity": "critical"}},
		},
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if len(sub.submitted) != 1 || len(sub.submitted[0]) != 1 {
		t.Fatalf("submitted = %+v, want one submission of one checkresult", sub.submitted)
	}
	cr := sub.submitted[0][0]
	if cr.Hostname != "db1" || cr.ServiceName != "DiskFull" || cr.State != 2 {
		t.Errorf("checkresult = %+v, want host=db1 service=DiskFull state=2", cr)
	}
}

func TestHandlerSkipsUnmatchedAlerts(t *testing.T) {
	sub := &fakeSubmitter{}
	h := testHandler(t, sub)

	// No rule requires "instance"/"alertname" to be set, so this alert still
	// maps (templates just render empty) - use a handler with a match
	// condition instead to exercise the actual skip path.
	h.Engine, _ = mapping.New(&config.Config{Rules: []config.RuleConfig{{
		Name:      "only-prod",
		CheckType: config.CheckTypeService,
		Match:     []config.MatchConfig{{Label: "env", Op: config.OpEq, Value: "prod"}},
		Host:      &config.TargetTemplate{Template: "{{ .Labels.instance }}"},
		Service:   &config.TargetTemplate{Template: "{{ .Labels.alertname }}"},
	}}})

	rec := postPayload(t, h, alertmanager.WebhookPayload{
		Alerts: []alertmanager.Alert{{Status: "firing", Labels: map[string]string{"env": "staging"}}},
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(sub.submitted) != 0 {
		t.Errorf("submitted = %+v, want no submission for an all-unmatched batch", sub.submitted)
	}
}

func TestHandlerNRDPFailureReturns502(t *testing.T) {
	sub := &fakeSubmitter{err: errNRDPDown}
	h := testHandler(t, sub)

	rec := postPayload(t, h, alertmanager.WebhookPayload{
		Alerts: []alertmanager.Alert{{Status: "firing", Labels: map[string]string{"instance": "db1", "alertname": "DiskFull"}}},
	})

	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", rec.Code)
	}
}

func TestHandlerRejectsMalformedJSON(t *testing.T) {
	h := testHandler(t, &fakeSubmitter{})
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader([]byte("not json")))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHandlerUsesRuleOutputTemplate(t *testing.T) {
	sub := &fakeSubmitter{}
	h := testHandler(t, sub)
	var err error
	h.Engine, err = mapping.New(&config.Config{Rules: []config.RuleConfig{{
		Name:      "default",
		CheckType: config.CheckTypeService,
		Host:      &config.TargetTemplate{Template: "{{ .Labels.instance }}"},
		Service:   &config.TargetTemplate{Template: "{{ .Labels.alertname }}"},
		Output:    &config.TargetTemplate{Template: "custom output for {{ .Labels.alertname }}"},
	}}})
	if err != nil {
		t.Fatalf("mapping.New: %v", err)
	}

	postPayload(t, h, alertmanager.WebhookPayload{
		Alerts: []alertmanager.Alert{
			{Status: "firing", Labels: map[string]string{"instance": "db1", "alertname": "DiskFull"}, Annotations: map[string]string{"summary": "ignored"}},
		},
	})

	if len(sub.submitted) != 1 || sub.submitted[0][0].Output != "custom output for DiskFull" {
		t.Errorf("submitted = %+v, want the rule's output template to override the summary annotation", sub.submitted)
	}
}

func TestHandlerResolvedAlertForcesOK(t *testing.T) {
	sub := &fakeSubmitter{}
	h := testHandler(t, sub)

	postPayload(t, h, alertmanager.WebhookPayload{
		Alerts: []alertmanager.Alert{
			{Status: "resolved", Labels: map[string]string{"instance": "db1", "alertname": "DiskFull", "severity": "critical"}},
		},
	})

	if len(sub.submitted) != 1 || sub.submitted[0][0].State != 0 {
		t.Errorf("submitted = %+v, want state=0 (ok) for a resolved alert regardless of severity", sub.submitted)
	}
}

var errNRDPDown = &submitError{"nrdp unreachable"}

type submitError struct{ msg string }

func (e *submitError) Error() string { return e.msg }

func TestHandlerSkipsEmptyRenderedTarget(t *testing.T) {
	sub := &fakeSubmitter{}
	h := testHandler(t, sub)

	// The alert carries no "instance" label, so the host template renders
	// empty. Submitting that would give Nagios a nameless host, which it
	// discards without telling anyone.
	rec := postPayload(t, h, alertmanager.WebhookPayload{
		Alerts: []alertmanager.Alert{
			{Status: "firing", Labels: map[string]string{"alertname": "NoInstance", "severity": "critical"}},
		},
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(sub.submitted) != 0 {
		t.Errorf("submitted = %+v, want nothing submitted for an empty hostname", sub.submitted)
	}
}

func TestHandlerSanitizesNamesAndOutput(t *testing.T) {
	sub := &fakeSubmitter{}
	h := testHandler(t, sub)

	postPayload(t, h, alertmanager.WebhookPayload{
		Alerts: []alertmanager.Alert{{
			Status: "firing",
			Labels: map[string]string{
				"instance":  "db1;evil",
				"alertname": "Disk\nFull;INJECTED",
				"severity":  "critical",
			},
			Annotations: map[string]string{
				"summary": "disk full\nPROCESS_HOST_CHECK_RESULT;db1;0;pwned",
			},
		}},
	})

	if len(sub.submitted) != 1 {
		t.Fatalf("submitted = %+v, want one submission", sub.submitted)
	}
	cr := sub.submitted[0][0]
	if cr.Hostname != "db1evil" {
		t.Errorf("Hostname = %q, want semicolon removed", cr.Hostname)
	}
	if strings.ContainsAny(cr.ServiceName, ";\n\r") {
		t.Errorf("ServiceName = %q, still contains a delimiter", cr.ServiceName)
	}
	// Semicolons survive in output (it is the trailing field and they are
	// legitimate in prose), but the newline must not.
	if strings.ContainsAny(cr.Output, "\n\r") {
		t.Errorf("Output = %q, still contains a newline", cr.Output)
	}
	if !strings.Contains(cr.Output, "disk full") {
		t.Errorf("Output = %q, want the original text preserved", cr.Output)
	}
}

func TestHandlerCountsTruncatedAlerts(t *testing.T) {
	sub := &fakeSubmitter{}
	h := testHandler(t, sub)
	var buf bytes.Buffer
	h.Log = slog.New(slog.NewTextHandler(&buf, nil))

	postPayload(t, h, alertmanager.WebhookPayload{
		TruncatedAlerts: 7,
		Alerts: []alertmanager.Alert{
			{Status: "firing", Labels: map[string]string{"instance": "db1", "alertname": "A"}},
		},
	})

	if !strings.Contains(buf.String(), "truncated") {
		t.Errorf("expected a warning about truncated alerts, got:\n%s", buf.String())
	}
}

func TestHandlerSubmitOutlivesClientDisconnect(t *testing.T) {
	// Alertmanager hanging up must not abandon a submission whose alerts we
	// already hold: they would be lost until the next repeat_interval.
	ctx, cancel := context.WithCancel(context.Background())
	var gotErr error
	sub := &submitterFunc{fn: func(c context.Context, _ []nrdp.CheckResult) error {
		gotErr = c.Err()
		return nil
	}}
	h := testHandler(t, sub)

	body, err := json.Marshal(alertmanager.WebhookPayload{
		Alerts: []alertmanager.Alert{{Status: "firing", Labels: map[string]string{"instance": "db1", "alertname": "A"}}},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body)).WithContext(ctx)
	cancel() // client is gone before the handler reaches the submission

	h.ServeHTTP(httptest.NewRecorder(), req)

	if gotErr != nil {
		t.Errorf("Submit saw a cancelled context (%v); it should be detached from the request", gotErr)
	}
}

type submitterFunc struct {
	fn func(context.Context, []nrdp.CheckResult) error
}

func (s *submitterFunc) Submit(ctx context.Context, r []nrdp.CheckResult) error { return s.fn(ctx, r) }

func TestHandlerMergesGroupedAlertsOnSameTarget(t *testing.T) {
	sub := &fakeSubmitter{}
	h := testHandler(t, sub)

	// A coarse rule: every alert in the group lands on one static target,
	// which is exactly how you get a single Nagios check per group.
	var err error
	h.Engine, err = mapping.New(&config.Config{Rules: []config.RuleConfig{{
		Name:      "per-alertname",
		CheckType: config.CheckTypeService,
		Host:      &config.TargetTemplate{Template: "nagios-am"},
		Service:   &config.TargetTemplate{Template: "{{ .Labels.alertname }}"},
	}}})
	if err != nil {
		t.Fatalf("mapping.New: %v", err)
	}

	rec := postPayload(t, h, alertmanager.WebhookPayload{
		Alerts: []alertmanager.Alert{
			{Status: "firing", Labels: map[string]string{"alertname": "DiskFull", "instance": "srv-1", "severity": "critical"}},
			{Status: "firing", Labels: map[string]string{"alertname": "DiskFull", "instance": "srv-2", "severity": "ok"}},
			{Status: "resolved", Labels: map[string]string{"alertname": "DiskFull", "instance": "srv-3", "severity": "critical"}},
		},
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(sub.submitted) != 1 || len(sub.submitted[0]) != 1 {
		t.Fatalf("submitted = %+v, want one submission of one merged checkresult", sub.submitted)
	}
	cr := sub.submitted[0][0]
	if cr.State != 2 {
		t.Errorf("State = %d, want 2: srv-3 resolving must not clear a check srv-1 is still critical on", cr.State)
	}
	if !strings.Contains(cr.Output, "srv-1") {
		t.Errorf("Output = %q, want the firing members named", cr.Output)
	}
}
