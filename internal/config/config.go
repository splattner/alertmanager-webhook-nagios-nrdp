// Package config defines the nrdp-webhook's YAML configuration shape and
// loads it from disk.
package config

import (
	"fmt"
	"os"
	"regexp"
	"time"

	"sigs.k8s.io/yaml"
)

// Config is the webhook's top-level configuration.
type Config struct {
	Server  ServerConfig  `json:"server"`
	Webhook WebhookConfig `json:"webhook,omitempty"`
	NRDP    NRDPConfig    `json:"nrdp"`
	State   StateConfig   `json:"state,omitempty"`
	Rules   []RuleConfig  `json:"rules"`
}

// ServerConfig configures the webhook's own HTTP listener.
type ServerConfig struct {
	Listen       string         `json:"listen"`
	MaxBodyBytes int64          `json:"maxBodyBytes"`
	TLS          *ServerTLSSpec `json:"tls,omitempty"`
}

// ServerTLSSpec configures TLS for the webhook's own listener.
type ServerTLSSpec struct {
	CertFile string `json:"certFile"`
	KeyFile  string `json:"keyFile"`
	// ClientCAFile, if set, requires and verifies a client certificate on
	// every connection (mutual TLS).
	ClientCAFile string `json:"clientCAFile,omitempty"`
}

// ClientTLSSpec configures TLS trust/identity for an outbound connection
// (currently only the NRDP target).
type ClientTLSSpec struct {
	// CAFile trusts an additional CA (e.g. self-signed or internal)
	// alongside the system pool - the common case for an in-house Nagios.
	CAFile string `json:"caFile,omitempty"`
	// CertFile/KeyFile present a client certificate for mutual TLS.
	CertFile string `json:"certFile,omitempty"`
	KeyFile  string `json:"keyFile,omitempty"`
	// InsecureSkipVerify disables certificate verification entirely. An
	// explicit, discouraged escape hatch - prefer CAFile.
	InsecureSkipVerify bool `json:"insecureSkipVerify,omitempty"`
}

// WebhookConfig controls the inbound side: how Alertmanager must
// authenticate to POST alerts.
type WebhookConfig struct {
	Auth *WebhookAuthConfig `json:"auth,omitempty"`
}

// WebhookAuthType selects how an inbound request is authenticated.
type WebhookAuthType string

// Supported WebhookAuthConfig.Type values.
const (
	AuthNone   WebhookAuthType = "none"
	AuthBasic  WebhookAuthType = "basic"
	AuthBearer WebhookAuthType = "bearer"
)

// WebhookAuthConfig requires every inbound request to present credentials
// matching Username/Password (basic) or Token (bearer) before it is
// processed. Alertmanager's webhook_configs supports both via
// http_config.basic_auth / authorization.
type WebhookAuthConfig struct {
	Type     WebhookAuthType `json:"type"`
	Username string          `json:"username,omitempty"`
	Password string          `json:"password,omitempty"`
	Token    string          `json:"token,omitempty"`
}

// NRDPConfig is the single Nagios NRDP endpoint checkresults are submitted
// to.
type NRDPConfig struct {
	URL     string   `json:"url"`
	Token   string   `json:"token"`
	Timeout Duration `json:"timeout"`
	// Retries is the number of additional attempts made after an initial
	// failed submission (0, the default, means a single attempt). NRDP has
	// no per-checkresult status in its response - a submission succeeds or
	// fails as a whole, so a retry resubmits the entire batch.
	Retries int            `json:"retries"`
	TLS     *ClientTLSSpec `json:"tls,omitempty"`
}

// StateConfig controls how an alert's Alertmanager status and severity
// label are translated into Nagios state codes.
type StateConfig struct {
	// SeverityLabel is the alert label read to determine severity for a
	// firing alert (e.g. "severity"). Ignored for a resolved alert, which
	// always maps to ResolvedService/ResolvedHost.
	SeverityLabel string `json:"severityLabel"`
	// Service maps a severity value to a Nagios service state (0=OK,
	// 1=WARNING, 2=CRITICAL, 3=UNKNOWN).
	Service StateMapConfig `json:"service"`
	// Host maps a severity value to a Nagios host state (0=UP, 1=DOWN,
	// 2=UNREACHABLE).
	Host StateMapConfig `json:"host"`
	// ResolvedService/ResolvedHost are severity-map keys (not raw state
	// numbers) applied to every resolved alert regardless of its severity
	// label, so Nagios actually clears the check on resolve.
	ResolvedService string `json:"resolvedService"`
	ResolvedHost    string `json:"resolvedHost"`
}

// StateMapConfig maps named severity values to Nagios state codes.
type StateMapConfig struct {
	Values map[string]int `json:"values"`
	// Unmatched is the map key used when an alert's severity label value
	// is not found in Values (including when the label is absent).
	Unmatched string `json:"unmatched"`
}

// CheckType selects what kind of Nagios passive check a rule submits.
type CheckType string

// Supported RuleConfig.CheckType values.
const (
	CheckTypeService CheckType = "service"
	CheckTypeHost    CheckType = "host"
)

// MatchOp is a label matcher comparison operator.
type MatchOp string

// Matcher operators usable in a RuleConfig's Match list.
const (
	OpExists   MatchOp = "exists"
	OpAbsent   MatchOp = "absent"
	OpEq       MatchOp = "eq"
	OpNe       MatchOp = "ne"
	OpRegex    MatchOp = "regex"
	OpNotRegex MatchOp = "notregex"
)

// MatchConfig is one label matcher; a rule's matchers are ANDed together.
type MatchConfig struct {
	Label string  `json:"label"`
	Op    MatchOp `json:"op"`
	Value string  `json:"value,omitempty"`
}

// TargetTemplate renders a Nagios host or service name from an alert's
// labels/annotations via Go's text/template.
type TargetTemplate struct {
	Template string `json:"template"`
}

// RuleConfig is one mapping rule: match, then render a Nagios host (and,
// for service checks, service) name. Rules are evaluated in order; the
// first whose Match conditions all hold is used, and no further rules are
// tried.
type RuleConfig struct {
	Name  string        `json:"name"`
	Match []MatchConfig `json:"match,omitempty"`
	// CheckType defaults to "service" if empty.
	CheckType CheckType       `json:"checkType,omitempty"`
	Host      *TargetTemplate `json:"host"`
	// Service is required when CheckType is "service" and must be empty
	// for "host" (an NRDP host checkresult carries no service name).
	Service *TargetTemplate `json:"service,omitempty"`
	// Output overrides the checkresult's plugin-output text. If unset, it
	// defaults to the alert's "summary" annotation, falling back to
	// "description", falling back to the alertname label.
	Output *TargetTemplate `json:"output,omitempty"`
}

// Load reads, expands ${ENV} references, and validates a config file.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	return Parse(raw)
}

// Parse expands and validates already-read config bytes.
func Parse(raw []byte) (*Config, error) {
	expanded := expandEnv(raw)

	// Strict: an unknown key is an error, not a silent no-op. A typo like
	// `sevirityLabel:` would otherwise parse cleanly, leave the default in
	// place, and give `nrdp-webhook check` no reason to complain - a config
	// change that appears to apply and doesn't is a bad failure mode for
	// the component alert delivery to Nagios runs through.
	var cfg Config
	if err := yaml.UnmarshalStrict(expanded, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	applyDefaults(&cfg)

	if err := Validate(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

var envVarPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// expandEnv replaces ${VAR} with the environment variable's value, left
// untouched if unset, so a typo'd variable name fails validation loudly
// (as an unresolved ${...}) rather than silently becoming an empty string.
func expandEnv(raw []byte) []byte {
	return envVarPattern.ReplaceAllFunc(raw, func(match []byte) []byte {
		name := envVarPattern.FindSubmatch(match)[1]
		if v, ok := os.LookupEnv(string(name)); ok {
			return []byte(v)
		}
		return match
	})
}

// applyDefaults fills in zero-valued fields with the webhook's defaults.
func applyDefaults(cfg *Config) {
	if cfg.Server.Listen == "" {
		cfg.Server.Listen = ":8080"
	}
	if cfg.Server.MaxBodyBytes == 0 {
		cfg.Server.MaxBodyBytes = 8 << 20
	}
	if cfg.NRDP.Timeout == 0 {
		cfg.NRDP.Timeout = Duration(5 * time.Second)
	}

	if cfg.State.SeverityLabel == "" {
		cfg.State.SeverityLabel = "severity"
	}
	if cfg.State.Service.Values == nil {
		cfg.State.Service.Values = map[string]int{"ok": 0, "warning": 1, "critical": 2, "unknown": 3}
	}
	if cfg.State.Service.Unmatched == "" {
		cfg.State.Service.Unmatched = "unknown"
	}
	if cfg.State.Host.Values == nil {
		cfg.State.Host.Values = map[string]int{"up": 0, "down": 1, "unreachable": 2}
	}
	if cfg.State.Host.Unmatched == "" {
		cfg.State.Host.Unmatched = "down"
	}
	if cfg.State.ResolvedService == "" {
		cfg.State.ResolvedService = "ok"
	}
	if cfg.State.ResolvedHost == "" {
		cfg.State.ResolvedHost = "up"
	}

	for i := range cfg.Rules {
		if cfg.Rules[i].CheckType == "" {
			cfg.Rules[i].CheckType = CheckTypeService
		}
	}
}
