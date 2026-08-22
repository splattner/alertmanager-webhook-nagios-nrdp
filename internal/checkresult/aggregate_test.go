package checkresult

import (
	"strings"
	"testing"

	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/alertmanager"
	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/config"
	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/nrdp"
)

func mustAggregator(t *testing.T, cfg config.AggregationConfig) *Aggregator {
	t.Helper()
	if cfg.IdentifyBy == "" {
		cfg.IdentifyBy = "instance"
	}
	a, err := NewAggregator(cfg)
	if err != nil {
		t.Fatalf("NewAggregator: %v", err)
	}
	return a
}

// entry builds a service-check entry on host "h" / service "s" unless
// overridden, which is the colliding-target shape these tests care about.
func entry(instance, status string, state int, output string) Entry {
	return Entry{
		Result: nrdp.CheckResult{Type: "service", Hostname: "h", ServiceName: "s", State: state, Output: output},
		Alert: alertmanager.Alert{
			Status: status,
			Labels: map[string]string{"alertname": "A", "instance": instance},
		},
	}
}

func merge(t *testing.T, a *Aggregator, entries []Entry) []nrdp.CheckResult {
	t.Helper()
	got, err := a.Merge(entries)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	return got
}

// The regression this whole feature exists for: two members still firing,
// one resolved. Unmerged, NRDP applies in order and the resolved member
// clears a check whose siblings are still alerting.
func TestMergeResolvedMemberDoesNotClearFiringSiblings(t *testing.T) {
	a := mustAggregator(t, config.AggregationConfig{})

	got := merge(t, a, []Entry{
		entry("srv-1", alertmanager.StatusFiring, 2, "srv-1 disk full"),
		entry("srv-2", alertmanager.StatusFiring, 1, "srv-2 disk filling"),
		entry("srv-3", alertmanager.StatusResolved, 0, "srv-3 recovered"),
	})

	if len(got) != 1 {
		t.Fatalf("got %d checkresults, want 1 merged", len(got))
	}
	if got[0].State != 2 {
		t.Errorf("State = %d, want 2 (worst of the firing members), not the resolved member's 0", got[0].State)
	}
	if !strings.Contains(got[0].Output, "2 firing") || !strings.Contains(got[0].Output, "1 resolved") {
		t.Errorf("Output = %q, want it to report 2 firing and 1 resolved", got[0].Output)
	}
}

func TestMergeAllResolvedTakesResolvedState(t *testing.T) {
	a := mustAggregator(t, config.AggregationConfig{})

	got := merge(t, a, []Entry{
		entry("srv-1", alertmanager.StatusResolved, 0, "srv-1 recovered"),
		entry("srv-2", alertmanager.StatusResolved, 0, "srv-2 recovered"),
	})

	if len(got) != 1 || got[0].State != 0 {
		t.Fatalf("got %+v, want a single checkresult in state 0 once every member resolved", got)
	}
}

// Enabling aggregation must be invisible when nothing actually collides.
func TestMergeSingleAlertPerTargetIsUntouched(t *testing.T) {
	a := mustAggregator(t, config.AggregationConfig{})

	one := entry("srv-1", alertmanager.StatusFiring, 2, "srv-1 disk full")
	two := one
	two.Result.ServiceName = "other"

	got := merge(t, a, []Entry{one, two})

	if len(got) != 2 {
		t.Fatalf("got %d checkresults, want 2 distinct targets left alone", len(got))
	}
	if got[0].Output != "srv-1 disk full" || got[1].Output != "srv-1 disk full" {
		t.Errorf("got %+v, want each lone alert to keep its own output", got)
	}
}

func TestMergeUsesSeverityPrecedenceNotMax(t *testing.T) {
	a := mustAggregator(t, config.AggregationConfig{})

	// UNKNOWN(3) alongside CRITICAL(2): the merged state must be CRITICAL.
	got := merge(t, a, []Entry{
		entry("srv-1", alertmanager.StatusFiring, 3, "unknown"),
		entry("srv-2", alertmanager.StatusFiring, 2, "critical"),
	})

	if got[0].State != 2 {
		t.Errorf("State = %d, want 2 (CRITICAL outranks UNKNOWN despite being a lower code)", got[0].State)
	}
}

func TestMergeHostChecks(t *testing.T) {
	a := mustAggregator(t, config.AggregationConfig{})
	host := func(instance string, state int) Entry {
		return Entry{
			Result: nrdp.CheckResult{Type: "host", Hostname: "h", State: state},
			Alert:  alertmanager.Alert{Status: alertmanager.StatusFiring, Labels: map[string]string{"instance": instance}},
		}
	}

	// DOWN(1) must outrank UNREACHABLE(2).
	got := merge(t, a, []Entry{host("a", 2), host("b", 1)})
	if len(got) != 1 || got[0].State != 1 {
		t.Errorf("got %+v, want one host checkresult in state 1 (down)", got)
	}
	if got[0].ServiceName != "" {
		t.Errorf("ServiceName = %q, want empty on a host checkresult", got[0].ServiceName)
	}
}

func TestMergeListIsTruncatedAndDeterministic(t *testing.T) {
	limit := 2
	a := mustAggregator(t, config.AggregationConfig{MaxListed: &limit})

	// Deliberately out of order; output must not depend on input order.
	entries := []Entry{
		entry("srv-3", alertmanager.StatusFiring, 2, ""),
		entry("srv-1", alertmanager.StatusFiring, 2, ""),
		entry("srv-4", alertmanager.StatusFiring, 2, ""),
		entry("srv-2", alertmanager.StatusFiring, 2, ""),
	}
	first := merge(t, a, entries)[0].Output

	if !strings.Contains(first, "srv-1, srv-2 and 2 more") {
		t.Errorf("Output = %q, want the first two names sorted plus an \"and 2 more\" tail", first)
	}

	// Same alerts in a different order must render identically, or Nagios
	// records a changed plugin output on every repeat_interval.
	shuffled := []Entry{entries[2], entries[0], entries[3], entries[1]}
	if second := merge(t, a, shuffled)[0].Output; second != first {
		t.Errorf("output depends on input order:\n first=%q\nsecond=%q", first, second)
	}
}

func TestMergeMaxListedZeroOmitsNames(t *testing.T) {
	zero := 0
	a := mustAggregator(t, config.AggregationConfig{MaxListed: &zero})

	got := merge(t, a, []Entry{
		entry("srv-1", alertmanager.StatusFiring, 2, ""),
		entry("srv-2", alertmanager.StatusFiring, 2, ""),
	})

	if strings.Contains(got[0].Output, "srv-") {
		t.Errorf("Output = %q, want no member names listed when maxListed is 0", got[0].Output)
	}
	if !strings.Contains(got[0].Output, "2 firing") {
		t.Errorf("Output = %q, want the counts to remain", got[0].Output)
	}
}

func TestMergeCustomOutputTemplate(t *testing.T) {
	a := mustAggregator(t, config.AggregationConfig{
		Output: &config.TargetTemplate{Template: "{{ .Service }} on {{ .Host }}: {{ .Firing }}/{{ .Total }} bad"},
	})

	got := merge(t, a, []Entry{
		entry("srv-1", alertmanager.StatusFiring, 2, ""),
		entry("srv-2", alertmanager.StatusResolved, 0, ""),
	})

	if want := "s on h: 1/2 bad"; got[0].Output != want {
		t.Errorf("Output = %q, want %q", got[0].Output, want)
	}
}

func TestMergeDisabledPassesEverythingThrough(t *testing.T) {
	off := false
	a := mustAggregator(t, config.AggregationConfig{Enabled: &off})

	entries := []Entry{
		entry("srv-1", alertmanager.StatusFiring, 2, "srv-1 disk full"),
		entry("srv-2", alertmanager.StatusResolved, 0, "srv-2 recovered"),
	}
	got := merge(t, a, entries)

	if len(got) != 2 {
		t.Fatalf("got %d checkresults, want both passed through unmerged", len(got))
	}
	if got[1].State != 0 {
		t.Errorf("got %+v, want the original last-wins ordering preserved when disabled", got)
	}
}

func TestMergePreservesFirstSeenTargetOrder(t *testing.T) {
	a := mustAggregator(t, config.AggregationConfig{})

	second := entry("srv-9", alertmanager.StatusFiring, 2, "")
	second.Result.ServiceName = "zzz"

	got := merge(t, a, []Entry{
		entry("srv-1", alertmanager.StatusFiring, 2, ""),
		second,
		entry("srv-2", alertmanager.StatusFiring, 2, ""),
	})

	if len(got) != 2 {
		t.Fatalf("got %d checkresults, want 2 targets", len(got))
	}
	if got[0].ServiceName != "s" || got[1].ServiceName != "zzz" {
		t.Errorf("got %+v, want targets in first-seen order", got)
	}
}

func TestMergeOutputIsSanitized(t *testing.T) {
	a := mustAggregator(t, config.AggregationConfig{
		Output: &config.TargetTemplate{Template: "line one\nPROCESS_HOST_CHECK_RESULT;h;0;pwned"},
	})

	got := merge(t, a, []Entry{
		entry("srv-1", alertmanager.StatusFiring, 2, ""),
		entry("srv-2", alertmanager.StatusFiring, 2, ""),
	})

	if strings.ContainsAny(got[0].Output, "\n\r") {
		t.Errorf("Output = %q, still contains a newline", got[0].Output)
	}
}

func TestNewAggregatorRejectsBadTemplate(t *testing.T) {
	if _, err := NewAggregator(config.AggregationConfig{
		IdentifyBy: "instance",
		Output:     &config.TargetTemplate{Template: "{{ .Unclosed"},
	}); err == nil {
		t.Fatal("NewAggregator: want error for a malformed template, got nil")
	}
}
