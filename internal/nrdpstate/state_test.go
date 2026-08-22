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

func TestWorstServiceUsesSeverityNotNumericOrder(t *testing.T) {
	tests := []struct {
		name   string
		states []int
		want   int
	}{
		{"empty is ok", nil, 0},
		{"single", []int{1}, 1},
		{"critical beats warning", []int{1, 2}, 2},
		// The trap: UNKNOWN is 3 and CRITICAL is 2, so a plain max() would
		// pick UNKNOWN here and under-report a real outage.
		{"critical beats unknown", []int{3, 2}, 2},
		{"critical beats unknown, order reversed", []int{2, 3}, 2},
		{"unknown beats warning", []int{1, 3}, 3},
		{"unknown beats ok", []int{0, 3}, 3},
		{"all ok stays ok", []int{0, 0}, 0},
		{"everything at once", []int{0, 1, 3, 2}, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Worst(config.CheckTypeService, tt.states); got != tt.want {
				t.Errorf("Worst(service, %v) = %d, want %d", tt.states, got, tt.want)
			}
		})
	}
}

func TestWorstHostUsesSeverityNotNumericOrder(t *testing.T) {
	tests := []struct {
		name   string
		states []int
		want   int
	}{
		{"empty is up", nil, 0},
		// DOWN is 1 and UNREACHABLE is 2, so again max() would be wrong.
		{"down beats unreachable", []int{2, 1}, 1},
		{"down beats unreachable, order reversed", []int{1, 2}, 1},
		{"unreachable beats up", []int{0, 2}, 2},
		{"all up stays up", []int{0, 0}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Worst(config.CheckTypeHost, tt.states); got != tt.want {
				t.Errorf("Worst(host, %v) = %d, want %d", tt.states, got, tt.want)
			}
		})
	}
}
