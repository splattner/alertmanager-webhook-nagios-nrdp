package config

import (
	"strings"
	"testing"
	"time"
)

const minimalConfig = `
nrdp:
  url: "https://nagios.example.com/nrdp/"
  token: "secret"
rules:
  - name: default
    match:
      - {label: nagios_type, op: ne, value: host}
    host: {template: "{{ .Labels.instance }}"}
    service: {template: "{{ .Labels.alertname }}"}
`

func TestParseAppliesDefaults(t *testing.T) {
	cfg, err := Parse([]byte(minimalConfig))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Server.Listen != ":8080" {
		t.Errorf("Server.Listen = %q, want :8080", cfg.Server.Listen)
	}
	if cfg.Server.MaxBodyBytes != 8<<20 {
		t.Errorf("Server.MaxBodyBytes = %d, want %d", cfg.Server.MaxBodyBytes, 8<<20)
	}
	if cfg.State.SeverityLabel != "severity" {
		t.Errorf("State.SeverityLabel = %q, want severity", cfg.State.SeverityLabel)
	}
	if cfg.State.Service.Values["critical"] != "critical" {
		t.Errorf("State.Service.Values[critical] = %q, want %q", cfg.State.Service.Values["critical"], "critical")
	}
	if cfg.State.Service.Resolved != "ok" {
		t.Errorf("State.Service.Resolved = %q, want ok", cfg.State.Service.Resolved)
	}
	if cfg.Rules[0].CheckType != CheckTypeService {
		t.Errorf("Rules[0].CheckType = %q, want %q", cfg.Rules[0].CheckType, CheckTypeService)
	}
}

func TestParseExpandsEnv(t *testing.T) {
	t.Setenv("NRDP_TOKEN", "from-env")
	raw := strings.Replace(minimalConfig, `token: "secret"`, `token: "${NRDP_TOKEN}"`, 1)
	cfg, err := Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.NRDP.Token != "from-env" {
		t.Errorf("NRDP.Token = %q, want from-env", cfg.NRDP.Token)
	}
}

func TestParseRejectsUnknownField(t *testing.T) {
	raw := minimalConfig + "\nbogusField: true\n"
	if _, err := Parse([]byte(raw)); err == nil {
		t.Fatal("Parse: want error for unknown field, got nil")
	}
}

func TestParseRejectsInvalidYAML(t *testing.T) {
	if _, err := Parse([]byte("not: [valid")); err == nil {
		t.Fatal("Parse: want error for invalid YAML, got nil")
	}
}

func TestValidateRequiresNRDPURL(t *testing.T) {
	raw := strings.Replace(minimalConfig, `url: "https://nagios.example.com/nrdp/"`, `url: ""`, 1)
	if _, err := Parse([]byte(raw)); err == nil {
		t.Fatal("Parse: want error for empty nrdp.url, got nil")
	}
}

func TestValidateRequiresAtLeastOneRule(t *testing.T) {
	raw := `
nrdp:
  url: "https://nagios.example.com/nrdp/"
  token: "secret"
rules: []
`
	if _, err := Parse([]byte(raw)); err == nil {
		t.Fatal("Parse: want error for empty rules, got nil")
	}
}

func TestValidateRejectsDuplicateRuleNames(t *testing.T) {
	raw := `
nrdp:
  url: "https://nagios.example.com/nrdp/"
  token: "secret"
rules:
  - name: dup
    host: {template: "h"}
    service: {template: "s"}
  - name: dup
    host: {template: "h"}
    service: {template: "s"}
`
	if _, err := Parse([]byte(raw)); err == nil {
		t.Fatal("Parse: want error for duplicate rule names, got nil")
	}
}

func TestValidateHostCheckRejectsService(t *testing.T) {
	raw := `
nrdp:
  url: "https://nagios.example.com/nrdp/"
  token: "secret"
rules:
  - name: bad-host
    checkType: host
    host: {template: "h"}
    service: {template: "should not be set"}
`
	if _, err := Parse([]byte(raw)); err == nil {
		t.Fatal("Parse: want error for service set on a host-typed rule, got nil")
	}
}

func TestValidateRejectsInvalidRegex(t *testing.T) {
	raw := `
nrdp:
  url: "https://nagios.example.com/nrdp/"
  token: "secret"
rules:
  - name: bad-regex
    match:
      - {label: instance, op: regex, value: "("}
    host: {template: "h"}
    service: {template: "s"}
`
	if _, err := Parse([]byte(raw)); err == nil {
		t.Fatal("Parse: want error for invalid regex, got nil")
	}
}

func TestValidateRejectsUnknownStateName(t *testing.T) {
	for _, tc := range []struct{ name, block string }{
		{"service value", "state:\n  service:\n    values: {critical: bogus}\n"},
		{"service unmatched", "state:\n  service:\n    unmatched: bogus\n"},
		{"service resolved", "state:\n  service:\n    resolved: bogus\n"},
		// "critical" is a valid *service* state but not a host state.
		{"host value borrows service state", "state:\n  host:\n    values: {critical: critical}\n"},
		{"host unmatched", "state:\n  host:\n    unmatched: bogus\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := `
nrdp:
  url: "https://nagios.example.com/nrdp/"
  token: "secret"
rules:
  - name: default
    host: {template: "h"}
    service: {template: "s"}
` + tc.block
			if _, err := Parse([]byte(raw)); err == nil {
				t.Fatalf("Parse: want error for %s, got nil", tc.name)
			}
		})
	}
}

func TestParseRejectsUnresolvedEnvVar(t *testing.T) {
	raw := strings.Replace(minimalConfig, `token: "secret"`, `token: "${NOT_SET_ANYWHERE_XYZ}"`, 1)
	_, err := Parse([]byte(raw))
	if err == nil {
		t.Fatal("Parse: want error for an unset ${ENV} reference, got nil")
	}
	if !strings.Contains(err.Error(), "NOT_SET_ANYWHERE_XYZ") {
		t.Errorf("Parse error = %v, want it to name the unresolved variable", err)
	}
}

func TestValidateRejectsBadNRDPURL(t *testing.T) {
	for _, bad := range []string{"nagios.example.com/nrdp/", "ftp://nagios/nrdp/", "https://"} {
		raw := strings.Replace(minimalConfig, `url: "https://nagios.example.com/nrdp/"`, `url: "`+bad+`"`, 1)
		if _, err := Parse([]byte(raw)); err == nil {
			t.Errorf("Parse(%q): want error, got nil", bad)
		}
	}
}

func TestDurationParsesFromYAML(t *testing.T) {
	raw := `
nrdp:
  url: "https://nagios.example.com/nrdp/"
  token: "secret"
  timeout: 2s
rules:
  - name: default
    host: {template: "h"}
    service: {template: "s"}
`
	cfg, err := Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if time.Duration(cfg.NRDP.Timeout) != 2*time.Second {
		t.Errorf("NRDP.Timeout = %v, want 2s", time.Duration(cfg.NRDP.Timeout))
	}
}
