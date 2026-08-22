package checkresult

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"text/template"

	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/alertmanager"
	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/config"
	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/nrdp"
	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/nrdpstate"
)

// Entry pairs a built checkresult with the alert it came from, which is
// what merging needs in order to tell firing members from resolved ones
// and to describe them in the summary.
type Entry struct {
	Result nrdp.CheckResult
	Alert  alertmanager.Alert
}

// defaultSummary describes a merged target: counts first, then as many
// members as MaxListed allows.
const defaultSummary = `{{ .Firing }} firing{{ if .Resolved }}, {{ .Resolved }} resolved{{ end }}` +
	`{{ if .List }}: {{ .List }}{{ end }}`

// SummaryData is what an aggregation output template renders against.
type SummaryData struct {
	// Host and Service name the merged Nagios target.
	Host    string
	Service string
	// Total, Firing and Resolved count the alerts merged into it.
	Total    int
	Firing   int
	Resolved int
	// List is the identifiers of up to MaxListed members, comma-joined,
	// with a trailing "and N more" when truncated.
	List string
	// Alerts holds every merged alert, for templates that want to do their
	// own formatting; FiringAlerts and ResolvedAlerts are its two halves.
	Alerts         []alertmanager.Alert
	FiringAlerts   []alertmanager.Alert
	ResolvedAlerts []alertmanager.Alert
}

// Aggregator merges checkresults that address the same Nagios target.
type Aggregator struct {
	enabled    bool
	identifyBy string
	maxListed  int
	summary    *template.Template
}

// NewAggregator compiles cfg. cfg is assumed to be defaulted and validated
// (see config.Load).
func NewAggregator(cfg config.AggregationConfig) (*Aggregator, error) {
	text := defaultSummary
	if cfg.Output != nil && cfg.Output.Template != "" {
		text = cfg.Output.Template
	}
	tmpl, err := template.New("aggregation-output").Option("missingkey=zero").Parse(text)
	if err != nil {
		return nil, fmt.Errorf("aggregation output template: %w", err)
	}

	maxListed := 5
	if cfg.MaxListed != nil {
		maxListed = *cfg.MaxListed
	}

	return &Aggregator{
		enabled:    cfg.AggregationEnabled(),
		identifyBy: cfg.IdentifyBy,
		maxListed:  maxListed,
		summary:    tmpl,
	}, nil
}

// Merge combines entries that share a Nagios target, preserving the order
// in which targets were first seen.
//
// A target with a single alert is passed through untouched, so enabling
// aggregation changes nothing for the common case where every alert maps
// somewhere distinct. Where several do collide, the merged state is the
// most severe among the *firing* members - it only reaches the resolved
// state once every member has resolved, which is what stops one recovered
// instance from clearing a check its siblings are still alerting on.
//
// With aggregation disabled, entries are returned as-is; NRDP then applies
// them in order and the last one wins.
func (a *Aggregator) Merge(entries []Entry) ([]nrdp.CheckResult, error) {
	if !a.enabled {
		out := make([]nrdp.CheckResult, 0, len(entries))
		for _, e := range entries {
			out = append(out, e.Result)
		}
		return out, nil
	}

	order := make([]nrdp.CheckResult, 0, len(entries)) // targets, first-seen order
	groups := make(map[nrdp.CheckResult][]Entry, len(entries))
	for _, e := range entries {
		t := Target(e.Result)
		if _, seen := groups[t]; !seen {
			order = append(order, t)
		}
		groups[t] = append(groups[t], e)
	}

	out := make([]nrdp.CheckResult, 0, len(order))
	for _, t := range order {
		members := groups[t]
		if len(members) == 1 {
			out = append(out, members[0].Result)
			continue
		}
		merged, err := a.mergeGroup(t, members)
		if err != nil {
			return nil, err
		}
		out = append(out, merged)
	}
	return out, nil
}

func (a *Aggregator) mergeGroup(target nrdp.CheckResult, members []Entry) (nrdp.CheckResult, error) {
	checkType := config.CheckType(target.Type)

	var firing, resolved []alertmanager.Alert
	all := make([]alertmanager.Alert, 0, len(members))
	firingStates := make([]int, 0, len(members))
	resolvedStates := make([]int, 0, len(members))

	for _, m := range members {
		all = append(all, m.Alert)
		if m.Alert.Status == alertmanager.StatusResolved {
			resolved = append(resolved, m.Alert)
			resolvedStates = append(resolvedStates, m.Result.State)
			continue
		}
		firing = append(firing, m.Alert)
		firingStates = append(firingStates, m.Result.State)
	}

	// Firing members decide the state; the resolved ones only get a say
	// when there is nothing left firing.
	states := firingStates
	if len(states) == 0 {
		states = resolvedStates
	}

	merged := target
	merged.State = nrdpstate.Worst(checkType, states)

	output, err := a.renderSummary(target, all, firing, resolved)
	if err != nil {
		return nrdp.CheckResult{}, err
	}
	merged.Output = nrdp.SanitizeOutput(output)
	return merged, nil
}

func (a *Aggregator) renderSummary(target nrdp.CheckResult, all, firing, resolved []alertmanager.Alert) (string, error) {
	data := SummaryData{
		Host:           target.Hostname,
		Service:        target.ServiceName,
		Total:          len(all),
		Firing:         len(firing),
		Resolved:       len(resolved),
		List:           a.list(firing, resolved),
		Alerts:         all,
		FiringAlerts:   firing,
		ResolvedAlerts: resolved,
	}

	var buf bytes.Buffer
	if err := a.summary.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render aggregation output for %s/%s: %w", target.Hostname, target.ServiceName, err)
	}
	return buf.String(), nil
}

// list names the merged members, firing ones first since they are what the
// check is actually reporting on. Identifiers are deduplicated and sorted
// so the same set of alerts always produces the same output - otherwise
// Nagios would record a "changed" plugin output on every repeat_interval
// purely from map iteration order.
func (a *Aggregator) list(firing, resolved []alertmanager.Alert) string {
	names := a.identifiers(firing)
	if len(names) == 0 {
		names = a.identifiers(resolved)
	}
	if len(names) == 0 || a.maxListed == 0 {
		return ""
	}

	if len(names) <= a.maxListed {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(names[:a.maxListed], ", "), len(names)-a.maxListed)
}

func (a *Aggregator) identifiers(alerts []alertmanager.Alert) []string {
	seen := make(map[string]bool, len(alerts))
	names := make([]string, 0, len(alerts))
	for _, alert := range alerts {
		name := alert.Labels[a.identifyBy]
		if name == "" {
			name = alert.Labels["alertname"]
		}
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
