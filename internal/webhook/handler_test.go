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

	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/alertmanager"
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
			SeverityLabel:   "severity",
			Service:         config.StateMapConfig{Values: map[string]int{"ok": 0, "critical": 2, "unknown": 3}, Unmatched: "unknown"},
			Host:            config.StateMapConfig{Values: map[string]int{"up": 0, "down": 1}, Unmatched: "down"},
			ResolvedService: "ok",
			ResolvedHost:    "up",
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
	return &Handler{
		Engine:       engine,
		State:        nrdpstate.New(cfg.State),
		Client:       submitter,
		MaxBodyBytes: 1 << 20,
		Log:          slog.New(slog.NewTextHandler(io.Discard, nil)),
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

func TestHandlerGroupedAlertsCollidingOnSameTarget(t *testing.T) {
	sub := &fakeSubmitter{}
	h := testHandler(t, sub)
	var buf bytes.Buffer
	h.Log = slog.New(slog.NewTextHandler(&buf, nil))

	// Two distinct alerts (different alertname) that a coarse rule maps to
	// the same host+service - simulates an Alertmanager group where the
	// mapping rule doesn't key on whatever varies between them.
	var err error
	h.Engine, err = mapping.New(&config.Config{Rules: []config.RuleConfig{{
		Name:      "default",
		CheckType: config.CheckTypeService,
		Host:      &config.TargetTemplate{Template: "{{ .Labels.instance }}"},
		Service:   &config.TargetTemplate{Template: "static-service"},
	}}})
	if err != nil {
		t.Fatalf("mapping.New: %v", err)
	}

	rec := postPayload(t, h, alertmanager.WebhookPayload{
		Alerts: []alertmanager.Alert{
			{Status: "firing", Labels: map[string]string{"instance": "db1", "alertname": "AlertA", "severity": "critical"}},
			{Status: "firing", Labels: map[string]string{"instance": "db1", "alertname": "AlertB", "severity": "critical"}},
		},
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	// Both checkresults are still submitted - nrdp-webhook does not silently
	// drop either one, it only surfaces that they collide.
	if len(sub.submitted) != 1 || len(sub.submitted[0]) != 2 {
		t.Fatalf("submitted = %+v, want one submission of two checkresults", sub.submitted)
	}
	if !strings.Contains(buf.String(), "same Nagios target") {
		t.Errorf("expected a warning log about the colliding target, got:\n%s", buf.String())
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
