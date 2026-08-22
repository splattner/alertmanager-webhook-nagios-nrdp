package alertmanager

import (
	"strings"
	"testing"
)

func TestOutputPrefersSummary(t *testing.T) {
	a := Alert{
		Labels:      map[string]string{"alertname": "X"},
		Annotations: map[string]string{"summary": "s", "description": "d"},
	}
	if got := a.Output(); got != "s" {
		t.Errorf("Output() = %q, want %q", got, "s")
	}
}

func TestOutputFallsBackToDescription(t *testing.T) {
	a := Alert{
		Labels:      map[string]string{"alertname": "X"},
		Annotations: map[string]string{"description": "d"},
	}
	if got := a.Output(); got != "d" {
		t.Errorf("Output() = %q, want %q", got, "d")
	}
}

func TestOutputFallsBackToAlertname(t *testing.T) {
	a := Alert{Labels: map[string]string{"alertname": "X"}}
	if got := a.Output(); got != "X" {
		t.Errorf("Output() = %q, want %q", got, "X")
	}
}

func TestDecodeIgnoresUnknownFields(t *testing.T) {
	raw := `{"version":"4","status":"firing","alerts":[{"status":"firing","labels":{"alertname":"X"}}],"somethingNewAlertmanagerAdded":true}`
	p, err := Decode(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(p.Alerts) != 1 || p.Alerts[0].Labels["alertname"] != "X" {
		t.Errorf("Decode() = %+v", p)
	}
}

func TestDecodeRejectsInvalidJSON(t *testing.T) {
	if _, err := Decode(strings.NewReader("not json")); err == nil {
		t.Fatal("Decode: want error for invalid JSON")
	}
}
