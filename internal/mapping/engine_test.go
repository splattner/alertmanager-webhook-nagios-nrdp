package mapping

import (
	"testing"

	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/alertmanager"
	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/config"
)

func mustEngine(t *testing.T, cfg *config.Config) *Engine {
	t.Helper()
	e, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return e
}

func TestResolveFirstMatchWins(t *testing.T) {
	cfg := &config.Config{Rules: []config.RuleConfig{
		{
			Name:      "host-checks",
			CheckType: config.CheckTypeHost,
			Match:     []config.MatchConfig{{Label: "nagios_type", Op: config.OpEq, Value: "host"}},
			Host:      &config.TargetTemplate{Template: "{{ .Labels.instance }}"},
		},
		{
			Name:      "default",
			CheckType: config.CheckTypeService,
			Host:      &config.TargetTemplate{Template: "{{ .Labels.instance }}"},
			Service:   &config.TargetTemplate{Template: "{{ .Labels.alertname }}"},
		},
	}}
	e := mustEngine(t, cfg)

	hostAlert := alertmanager.Alert{Labels: map[string]string{"nagios_type": "host", "instance": "db1"}}
	res, ok, err := e.Resolve(hostAlert)
	if err != nil || !ok {
		t.Fatalf("Resolve(hostAlert) = %v, %v, %v", res, ok, err)
	}
	if res.Rule != "host-checks" || res.CheckType != config.CheckTypeHost || res.Host != "db1" || res.Service != "" {
		t.Errorf("Resolve(hostAlert) = %+v, want rule=host-checks type=host host=db1 service=\"\"", res)
	}

	serviceAlert := alertmanager.Alert{Labels: map[string]string{"instance": "db1", "alertname": "DiskFull"}}
	res, ok, err = e.Resolve(serviceAlert)
	if err != nil || !ok {
		t.Fatalf("Resolve(serviceAlert) = %v, %v, %v", res, ok, err)
	}
	if res.Rule != "default" || res.Host != "db1" || res.Service != "DiskFull" {
		t.Errorf("Resolve(serviceAlert) = %+v, want rule=default host=db1 service=DiskFull", res)
	}
}

func TestResolveNoMatch(t *testing.T) {
	cfg := &config.Config{Rules: []config.RuleConfig{
		{
			Name:      "only-prod",
			Match:     []config.MatchConfig{{Label: "env", Op: config.OpEq, Value: "prod"}},
			Host:      &config.TargetTemplate{Template: "{{ .Labels.instance }}"},
			Service:   &config.TargetTemplate{Template: "{{ .Labels.alertname }}"},
			CheckType: config.CheckTypeService,
		},
	}}
	e := mustEngine(t, cfg)

	_, ok, err := e.Resolve(alertmanager.Alert{Labels: map[string]string{"env": "staging"}})
	if err != nil {
		t.Fatalf("Resolve: unexpected error %v", err)
	}
	if ok {
		t.Fatal("Resolve: want ok=false for non-matching alert")
	}
}

func TestMatchOperators(t *testing.T) {
	tests := []struct {
		name   string
		op     config.MatchOp
		value  string
		labels map[string]string
		want   bool
	}{
		{"exists true", config.OpExists, "", map[string]string{"x": "1"}, true},
		{"exists false", config.OpExists, "", map[string]string{}, false},
		{"absent true", config.OpAbsent, "", map[string]string{}, true},
		{"absent false", config.OpAbsent, "", map[string]string{"x": "1"}, false},
		{"eq match", config.OpEq, "1", map[string]string{"x": "1"}, true},
		{"eq mismatch", config.OpEq, "1", map[string]string{"x": "2"}, false},
		{"eq missing", config.OpEq, "1", map[string]string{}, false},
		{"ne mismatch", config.OpNe, "1", map[string]string{"x": "2"}, true},
		{"ne missing", config.OpNe, "1", map[string]string{}, true},
		{"ne match", config.OpNe, "1", map[string]string{"x": "1"}, false},
		{"regex match", config.OpRegex, "^db.*", map[string]string{"x": "db1"}, true},
		{"regex mismatch", config.OpRegex, "^db.*", map[string]string{"x": "web1"}, false},
		{"notregex match", config.OpNotRegex, "^db.*", map[string]string{"x": "web1"}, true},
		{"notregex missing", config.OpNotRegex, "^db.*", map[string]string{}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{Rules: []config.RuleConfig{{
				Name:      "r",
				CheckType: config.CheckTypeHost,
				Match:     []config.MatchConfig{{Label: "x", Op: tt.op, Value: tt.value}},
				Host:      &config.TargetTemplate{Template: "h"},
			}}}
			e := mustEngine(t, cfg)
			_, ok, err := e.Resolve(alertmanager.Alert{Labels: tt.labels})
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if ok != tt.want {
				t.Errorf("Resolve() ok = %v, want %v", ok, tt.want)
			}
		})
	}
}

func TestResolveOutputTemplate(t *testing.T) {
	cfg := &config.Config{Rules: []config.RuleConfig{{
		Name:      "r",
		CheckType: config.CheckTypeHost,
		Host:      &config.TargetTemplate{Template: "h"},
		Output:    &config.TargetTemplate{Template: "custom: {{ .Labels.reason }}"},
	}}}
	e := mustEngine(t, cfg)

	res, ok, err := e.Resolve(alertmanager.Alert{Labels: map[string]string{"reason": "disk full"}})
	if err != nil || !ok {
		t.Fatalf("Resolve = %v, %v, %v", res, ok, err)
	}
	if !res.HasOutput || res.Output != "custom: disk full" {
		t.Errorf("Output = %+v, want HasOutput=true Output=%q", res, "custom: disk full")
	}
}

func TestResolveNoOutputTemplateLeavesHasOutputFalse(t *testing.T) {
	cfg := &config.Config{Rules: []config.RuleConfig{{
		Name:      "r",
		CheckType: config.CheckTypeHost,
		Host:      &config.TargetTemplate{Template: "h"},
	}}}
	e := mustEngine(t, cfg)

	res, ok, err := e.Resolve(alertmanager.Alert{Labels: map[string]string{}})
	if err != nil || !ok {
		t.Fatalf("Resolve = %v, %v, %v", res, ok, err)
	}
	if res.HasOutput {
		t.Errorf("HasOutput = true, want false when the rule sets no output template")
	}
}

func TestTemplateDefaultFunc(t *testing.T) {
	cfg := &config.Config{Rules: []config.RuleConfig{{
		Name:      "r",
		CheckType: config.CheckTypeHost,
		Host:      &config.TargetTemplate{Template: `{{ .Labels.instance | default "unknown" }}`},
	}}}
	e := mustEngine(t, cfg)

	res, ok, err := e.Resolve(alertmanager.Alert{Labels: map[string]string{}})
	if err != nil || !ok {
		t.Fatalf("Resolve = %v, %v, %v", res, ok, err)
	}
	if res.Host != "unknown" {
		t.Errorf("Host = %q, want unknown", res.Host)
	}
}
