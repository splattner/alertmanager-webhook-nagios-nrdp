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
			Values:    map[string]int{"ok": 0, "warning": 1, "critical": 2, "unknown": 3},
			Unmatched: "unknown",
		},
		Host: config.StateMapConfig{
			Values:    map[string]int{"up": 0, "down": 1, "unreachable": 2},
			Unmatched: "down",
		},
		ResolvedService: "ok",
		ResolvedHost:    "up",
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
