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
	if cfg.State.Service.Values["critical"] != 2 {
		t.Errorf("State.Service.Values[critical] = %d, want 2", cfg.State.Service.Values["critical"])
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

func TestValidateResolvedKeyMustExistInValues(t *testing.T) {
	raw := `
nrdp:
  url: "https://nagios.example.com/nrdp/"
  token: "secret"
state:
  resolvedService: "nonexistent"
rules:
  - name: default
    host: {template: "h"}
    service: {template: "s"}
`
	if _, err := Parse([]byte(raw)); err == nil {
		t.Fatal("Parse: want error for resolvedService not in state.service.values, got nil")
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
