package nrdpstate

import (
	"testing"

	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/alertmanager"
	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/config"
)

func testStateConfig() config.StateConfig {
	return config.StateConfig{
		SeverityLabel: "severity",
		Service: config.StateMapConfig{
			Values:    map[string]string{"ok": "ok", "warning": "warning", "critical": "critical", "unknown": "unknown"},
			Unmatched: "unknown",
			Resolved:  "ok",
		},
		Host: config.StateMapConfig{
			Values:    map[string]string{"ok": "up", "warning": "down", "critical": "down"},
			Unmatched: "down",
			Resolved:  "up",
		},
	}
}

func TestServiceStateFromSeverity(t *testing.T) {
	r := New(testStateConfig())
	alert := alertmanager.Alert{Status: alertmanager.StatusFiring, Labels: map[string]string{"severity": "critical"}}
	if got := r.ServiceState(alert); got != 2 {
		t.Errorf("ServiceState = %d, want 2", got)
	}
}

func TestServiceStateUnmatchedSeverity(t *testing.T) {
	r := New(testStateConfig())
	alert := alertmanager.Alert{Status: alertmanager.StatusFiring, Labels: map[string]string{"severity": "nonsense"}}
	if got := r.ServiceState(alert); got != 3 {
		t.Errorf("ServiceState = %d, want 3 (unknown)", got)
	}
}

func TestServiceStateMissingSeverityLabel(t *testing.T) {
	r := New(testStateConfig())
	alert := alertmanager.Alert{Status: alertmanager.StatusFiring, Labels: map[string]string{}}
	if got := r.ServiceState(alert); got != 3 {
		t.Errorf("ServiceState = %d, want 3 (unknown)", got)
	}
}

func TestServiceStateResolvedIgnoresSeverity(t *testing.T) {
	r := New(testStateConfig())
	alert := alertmanager.Alert{Status: alertmanager.StatusResolved, Labels: map[string]string{"severity": "critical"}}
	if got := r.ServiceState(alert); got != 0 {
		t.Errorf("ServiceState = %d, want 0 (ok, forced by resolve)", got)
	}
}

func TestHostState(t *testing.T) {
	r := New(testStateConfig())

	firing := alertmanager.Alert{Status: alertmanager.StatusFiring, Labels: map[string]string{"severity": "critical"}}
	if got := r.HostState(firing); got != 1 {
		t.Errorf("HostState(firing) = %d, want 1 (down, unmatched fallback)", got)
	}

	resolved := alertmanager.Alert{Status: alertmanager.StatusResolved, Labels: map[string]string{"severity": "critical"}}
	if got := r.HostState(resolved); got != 0 {
		t.Errorf("HostState(resolved) = %d, want 0 (up)", got)
	}
}
