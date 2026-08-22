// Package config defines the nrdp-webhook's YAML configuration shape and
// loads it from disk.
package config

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"sigs.k8s.io/yaml"
)

// Config is the webhook's top-level configuration.
type Config struct {
	Server    ServerConfig    `json:"server"`
	Webhook   WebhookConfig   `json:"webhook,omitempty"`
	NRDP      NRDPConfig      `json:"nrdp"`
	State     StateConfig     `json:"state,omitempty"`
	Heartbeat HeartbeatConfig `json:"heartbeat,omitempty"`
	Rules     []RuleConfig    `json:"rules"`
}

// HeartbeatConfig submits a passive OK to a dedicated Nagios check on a
// timer, independently of any alert traffic.
//
// Without it, this service failing is invisible to Nagios: alerts simply
// stop arriving, which looks exactly like nothing being wrong. A heartbeat
// turns that silence into a stale check - so point it at a Nagios object
// with check_freshness enabled and a freshness_threshold comfortably above
// Interval, and Nagios will raise the check itself when the pipeline dies.
type HeartbeatConfig struct {
	Enabled  bool     `json:"enabled,omitempty"`
	Interval Duration `json:"interval,omitempty"`
	// CheckType defaults to "service".
	CheckType CheckType `json:"checkType,omitempty"`
	Host      string    `json:"host,omitempty"`
	// Service is required for a service-type heartbeat and must be empty
	// for a host-type one.
	Service string `json:"service,omitempty"`
	// State is the state name submitted on each beat. Defaults to "ok"
	// ("up" for a host check) - the point is liveness, not severity.
	State string `json:"state,omitempty"`
	// Output is the plugin output text for the beat.
	Output string `json:"output,omitempty"`
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
	// always maps to the check type's Resolved state.
	SeverityLabel string `json:"severityLabel"`
	// Service maps severity values to Nagios service states.
	Service StateMapConfig `json:"service"`
	// Host maps severity values to Nagios host states.
	Host StateMapConfig `json:"host"`
}

// StateMapConfig maps an alert's severity label values to Nagios state
// names. Keys are severity values as they appear on the alert; the values,
// along with Unmatched and Resolved, are state *names* drawn from the
// check type's vocabulary (see ServiceStates / HostStates) rather than raw
// numbers, so a typo or an out-of-range code is a config error rather than
// a nonsense state submitted to Nagios.
type StateMapConfig struct {
	Values map[string]string `json:"values"`
	// Unmatched is the state used when the alert's severity value is not a
	// key in Values (including when the label is absent entirely).
	Unmatched string `json:"unmatched"`
	// Resolved is the state forced for every resolved alert, regardless of
	// its severity label, so Nagios actually clears the check on resolve.
	Resolved string `json:"resolved"`
}

// ServiceStates is the Nagios service-state vocabulary.
var ServiceStates = map[string]int{"ok": 0, "warning": 1, "critical": 2, "unknown": 3}

// HostStates is the Nagios host-state vocabulary.
var HostStates = map[string]int{"up": 0, "down": 1, "unreachable": 2}

// States returns the state vocabulary for a check type.
func States(t CheckType) map[string]int {
	if t == CheckTypeHost {
		return HostStates
	}
	return ServiceStates
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
	expanded, unresolved := expandEnv(raw)
	// An unset variable is a hard error rather than a silent empty string:
	// left to expand to "", a missing NRDP_TOKEN would be submitted to
	// Nagios as an empty credential and fail authentication with no hint
	// that the environment, not the token, was the problem. Left as the
	// literal "${NRDP_TOKEN}" it would fail just as confusingly.
	if len(unresolved) > 0 {
		sort.Strings(unresolved)
		return nil, fmt.Errorf("unresolved environment variable(s) in config: ${%s}", strings.Join(unresolved, "}, ${"))
	}

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

// expandEnv replaces ${VAR} with the environment variable's value and
// reports the names of any variables that were not set, which Parse turns
// into an error. Unset variables are deliberately not expanded to "" -
// see Parse.
func expandEnv(raw []byte) ([]byte, []string) {
	var unresolved []string
	seen := make(map[string]bool)
	expanded := envVarPattern.ReplaceAllFunc(raw, func(match []byte) []byte {
		name := string(envVarPattern.FindSubmatch(match)[1])
		if v, ok := os.LookupEnv(name); ok {
			return []byte(v)
		}
		if !seen[name] {
			seen[name] = true
			unresolved = append(unresolved, name)
		}
		return match
	})
	return expanded, unresolved
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
	// The defaults assume the conventional Prometheus severity vocabulary
	// on the left. For host checks anything short of "ok" means the host is
	// not answering, which is what a host-type alert normally encodes.
	if cfg.State.Service.Values == nil {
		cfg.State.Service.Values = map[string]string{
			"ok": "ok", "warning": "warning", "critical": "critical", "unknown": "unknown",
		}
	}
	if cfg.State.Service.Unmatched == "" {
		cfg.State.Service.Unmatched = "unknown"
	}
	if cfg.State.Service.Resolved == "" {
		cfg.State.Service.Resolved = "ok"
	}
	if cfg.State.Host.Values == nil {
		cfg.State.Host.Values = map[string]string{
			"ok": "up", "warning": "down", "critical": "down", "unknown": "down",
		}
	}
	if cfg.State.Host.Unmatched == "" {
		cfg.State.Host.Unmatched = "down"
	}
	if cfg.State.Host.Resolved == "" {
		cfg.State.Host.Resolved = "up"
	}

	for i := range cfg.Rules {
		if cfg.Rules[i].CheckType == "" {
			cfg.Rules[i].CheckType = CheckTypeService
		}
	}

	if cfg.Heartbeat.Enabled {
		if cfg.Heartbeat.CheckType == "" {
			cfg.Heartbeat.CheckType = CheckTypeService
		}
		if cfg.Heartbeat.Interval == 0 {
			cfg.Heartbeat.Interval = Duration(time.Minute)
		}
		if cfg.Heartbeat.State == "" {
			if cfg.Heartbeat.CheckType == CheckTypeHost {
				cfg.Heartbeat.State = "up"
			} else {
				cfg.Heartbeat.State = "ok"
			}
		}
		if cfg.Heartbeat.Output == "" {
			cfg.Heartbeat.Output = "alertmanager-webhook-nagios-nrdp is alive"
		}
	}
}
