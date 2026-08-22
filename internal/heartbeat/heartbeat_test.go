package heartbeat

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/config"
	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/nrdp"
)

type recorder struct {
	mu   sync.Mutex
	got  [][]nrdp.CheckResult
	err  error
	beat chan struct{}
}

func newRecorder() *recorder {
	return &recorder{beat: make(chan struct{}, 16)}
}

func (r *recorder) Submit(_ context.Context, results []nrdp.CheckResult) error {
	r.mu.Lock()
	r.got = append(r.got, results)
	err := r.err
	r.mu.Unlock()
	select {
	case r.beat <- struct{}{}:
	default:
	}
	return err
}

func (r *recorder) calls() [][]nrdp.CheckResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][]nrdp.CheckResult(nil), r.got...)
}

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// awaitBeat waits for one submission, failing the test rather than hanging
// forever if the loop never beats.
func awaitBeat(t *testing.T, r *recorder) {
	t.Helper()
	select {
	case <-r.beat:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for a heartbeat submission")
	}
}

func serviceConfig() config.HeartbeatConfig {
	return config.HeartbeatConfig{
		Enabled:   true,
		Interval:  config.Duration(10 * time.Millisecond),
		CheckType: config.CheckTypeService,
		Host:      "nagios-webhook",
		Service:   "alertmanager-pipeline",
		State:     "ok",
		Output:    "alive",
	}
}

func TestRunSubmitsServiceHeartbeat(t *testing.T) {
	rec := newRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go Run(ctx, func() (config.HeartbeatConfig, Submitter) { return serviceConfig(), rec }, testLogger())
	awaitBeat(t, rec)
	cancel()

	calls := rec.calls()
	if len(calls) == 0 || len(calls[0]) != 1 {
		t.Fatalf("calls = %+v, want at least one submission of one checkresult", calls)
	}
	cr := calls[0][0]
	if cr.Type != "service" || cr.Hostname != "nagios-webhook" || cr.ServiceName != "alertmanager-pipeline" {
		t.Errorf("checkresult = %+v, want the configured service target", cr)
	}
	if cr.State != 0 {
		t.Errorf("State = %d, want 0 (ok)", cr.State)
	}
	if cr.Output != "alive" {
		t.Errorf("Output = %q, want %q", cr.Output, "alive")
	}
}

func TestRunSubmitsHostHeartbeat(t *testing.T) {
	rec := newRecorder()
	cfg := serviceConfig()
	cfg.CheckType = config.CheckTypeHost
	cfg.Service = ""
	cfg.State = "up"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Run(ctx, func() (config.HeartbeatConfig, Submitter) { return cfg, rec }, testLogger())
	awaitBeat(t, rec)
	cancel()

	cr := rec.calls()[0][0]
	if cr.Type != "host" || cr.ServiceName != "" || cr.State != 0 {
		t.Errorf("checkresult = %+v, want a host checkresult in state up with no service name", cr)
	}
}

func TestRunDoesNotBeatWhenDisabled(t *testing.T) {
	rec := newRecorder()
	cfg := serviceConfig()
	cfg.Enabled = false

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	Run(ctx, func() (config.HeartbeatConfig, Submitter) { return cfg, rec }, testLogger())

	if n := len(rec.calls()); n != 0 {
		t.Errorf("got %d submissions while disabled, want 0", n)
	}
}

func TestRunSurvivesSubmitFailure(t *testing.T) {
	rec := newRecorder()
	rec.err = errors.New("nrdp down")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Run(ctx, func() (config.HeartbeatConfig, Submitter) { return serviceConfig(), rec }, testLogger())

	// A failing beat must not stop the loop - the next one has to still fire,
	// or one NRDP blip would silently end heartbeating for good.
	awaitBeat(t, rec)
	awaitBeat(t, rec)
	cancel()
}

func TestRunPicksUpConfigChanges(t *testing.T) {
	rec := newRecorder()
	var mu sync.Mutex
	cfg := serviceConfig()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Run(ctx, func() (config.HeartbeatConfig, Submitter) {
		mu.Lock()
		defer mu.Unlock()
		return cfg, rec
	}, testLogger())

	awaitBeat(t, rec)

	// Retarget mid-flight, as a config reload would.
	mu.Lock()
	cfg.Service = "retargeted"
	mu.Unlock()

	deadline := time.After(3 * time.Second)
	for {
		select {
		case <-rec.beat:
			calls := rec.calls()
			if calls[len(calls)-1][0].ServiceName == "retargeted" {
				cancel()
				return
			}
		case <-deadline:
			t.Fatal("heartbeat never picked up the changed configuration")
		}
	}
}

func TestRunStopsOnContextCancel(t *testing.T) {
	rec := newRecorder()
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		Run(ctx, func() (config.HeartbeatConfig, Submitter) { return serviceConfig(), rec }, testLogger())
		close(done)
	}()

	awaitBeat(t, rec)
	cancel()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}
}

func TestRunSanitizesConfiguredNames(t *testing.T) {
	rec := newRecorder()
	cfg := serviceConfig()
	cfg.Host = "host;evil"
	cfg.Service = "svc\nevil"
	cfg.Output = "alive\nPROCESS_HOST_CHECK_RESULT;x;0;pwned"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Run(ctx, func() (config.HeartbeatConfig, Submitter) { return cfg, rec }, testLogger())
	awaitBeat(t, rec)
	cancel()

	cr := rec.calls()[0][0]
	if cr.Hostname != "hostevil" {
		t.Errorf("Hostname = %q, want the semicolon removed", cr.Hostname)
	}
	if cr.ServiceName != "svc evil" {
		t.Errorf("ServiceName = %q, want the newline neutralized", cr.ServiceName)
	}
	if want := "alive PROCESS_HOST_CHECK_RESULT;x;0;pwned"; cr.Output != want {
		t.Errorf("Output = %q, want %q", cr.Output, want)
	}
}
