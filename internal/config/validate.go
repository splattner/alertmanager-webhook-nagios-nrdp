package config

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// Validate checks a defaulted Config for internal consistency. It does not
// touch the filesystem or network - see the `check` subcommand for that
// (TLS file existence, etc.).
func Validate(cfg *Config) error {
	if cfg.Server.Listen == "" {
		return fmt.Errorf("server.listen must not be empty")
	}
	if cfg.Server.MaxBodyBytes <= 0 {
		return fmt.Errorf("server.maxBodyBytes must be positive")
	}

	if err := validateWebhookAuth(cfg.Webhook.Auth); err != nil {
		return fmt.Errorf("webhook.auth: %w", err)
	}

	if cfg.NRDP.URL == "" {
		return fmt.Errorf("nrdp.url must not be empty")
	}
	if err := validateURL(cfg.NRDP.URL); err != nil {
		return fmt.Errorf("nrdp.url: %w", err)
	}
	if cfg.NRDP.Retries < 0 {
		return fmt.Errorf("nrdp.retries must not be negative")
	}
	if cfg.NRDP.Timeout <= 0 {
		return fmt.Errorf("nrdp.timeout must be positive")
	}

	if err := validateStateMap("state.service", cfg.State.Service, ServiceStates); err != nil {
		return err
	}
	if err := validateStateMap("state.host", cfg.State.Host, HostStates); err != nil {
		return err
	}

	if err := validateHeartbeat(cfg.Heartbeat); err != nil {
		return fmt.Errorf("heartbeat: %w", err)
	}

	if len(cfg.Rules) == 0 {
		return fmt.Errorf("rules must contain at least one rule")
	}
	seen := make(map[string]bool, len(cfg.Rules))
	for i, r := range cfg.Rules {
		if err := validateRule(r); err != nil {
			return fmt.Errorf("rules[%d] (%s): %w", i, r.Name, err)
		}
		if seen[r.Name] {
			return fmt.Errorf("rules[%d]: duplicate rule name %q", i, r.Name)
		}
		seen[r.Name] = true
	}

	return nil
}

func validateWebhookAuth(auth *WebhookAuthConfig) error {
	if auth == nil {
		return nil
	}
	switch auth.Type {
	case "", AuthNone:
	case AuthBasic:
		if auth.Username == "" || auth.Password == "" {
			return fmt.Errorf("username and password are required when type is %q", AuthBasic)
		}
	case AuthBearer:
		if auth.Token == "" {
			return fmt.Errorf("token is required when type is %q", AuthBearer)
		}
	default:
		return fmt.Errorf("unknown type %q (want %q, %q, or %q)", auth.Type, AuthNone, AuthBasic, AuthBearer)
	}
	return nil
}

// validateStateMap checks that every state name the map can produce is in
// the check type's vocabulary. Because the config names states rather than
// numbering them, this rules out an out-of-range Nagios state code by
// construction - there is no way to spell one.
func validateStateMap(field string, m StateMapConfig, vocab map[string]int) error {
	for severity, state := range m.Values {
		if _, ok := vocab[state]; !ok {
			return fmt.Errorf("%s.values[%q]: %w", field, severity, unknownState(state, vocab))
		}
	}
	if m.Unmatched == "" {
		return fmt.Errorf("%s.unmatched must not be empty", field)
	}
	if _, ok := vocab[m.Unmatched]; !ok {
		return fmt.Errorf("%s.unmatched: %w", field, unknownState(m.Unmatched, vocab))
	}
	if m.Resolved == "" {
		return fmt.Errorf("%s.resolved must not be empty", field)
	}
	if _, ok := vocab[m.Resolved]; !ok {
		return fmt.Errorf("%s.resolved: %w", field, unknownState(m.Resolved, vocab))
	}
	return nil
}

func unknownState(state string, vocab map[string]int) error {
	valid := make([]string, 0, len(vocab))
	for name := range vocab {
		valid = append(valid, name)
	}
	sort.Strings(valid)
	return fmt.Errorf("unknown state %q (want one of: %s)", state, strings.Join(valid, ", "))
}

func validateHeartbeat(h HeartbeatConfig) error {
	if !h.Enabled {
		return nil
	}
	if h.Interval <= 0 {
		return fmt.Errorf("interval must be positive")
	}
	if h.Host == "" {
		return fmt.Errorf("host is required")
	}

	switch h.CheckType {
	case CheckTypeService:
		if h.Service == "" {
			return fmt.Errorf("service is required when checkType is %q", CheckTypeService)
		}
	case CheckTypeHost:
		if h.Service != "" {
			return fmt.Errorf("service must not be set when checkType is %q", CheckTypeHost)
		}
	default:
		return fmt.Errorf("unknown checkType %q (want %q or %q)", h.CheckType, CheckTypeService, CheckTypeHost)
	}

	vocab := States(h.CheckType)
	if _, ok := vocab[h.State]; !ok {
		return fmt.Errorf("state: %w", unknownState(h.State, vocab))
	}
	return nil
}

// validateURL rejects anything that is not an absolute http(s) URL. A bare
// host or a typo'd scheme would otherwise only surface as a confusing
// transport error on the first alert.
func validateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("not a valid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("must be an http:// or https:// URL, got scheme %q", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("must include a host")
	}
	return nil
}

func validateRule(r RuleConfig) error {
	if r.Name == "" {
		return fmt.Errorf("name must not be empty")
	}
	for i, m := range r.Match {
		if err := validateMatch(m); err != nil {
			return fmt.Errorf("match[%d]: %w", i, err)
		}
	}

	switch r.CheckType {
	case CheckTypeService:
		if r.Host == nil || r.Host.Template == "" {
			return fmt.Errorf("host.template is required")
		}
		if r.Service == nil || r.Service.Template == "" {
			return fmt.Errorf("service.template is required when checkType is %q", CheckTypeService)
		}
	case CheckTypeHost:
		if r.Host == nil || r.Host.Template == "" {
			return fmt.Errorf("host.template is required")
		}
		if r.Service != nil && r.Service.Template != "" {
			return fmt.Errorf("service must not be set when checkType is %q", CheckTypeHost)
		}
	default:
		return fmt.Errorf("unknown checkType %q (want %q or %q)", r.CheckType, CheckTypeService, CheckTypeHost)
	}
	return nil
}

func validateMatch(m MatchConfig) error {
	if m.Label == "" {
		return fmt.Errorf("label must not be empty")
	}
	switch m.Op {
	case OpExists, OpAbsent:
		if m.Value != "" {
			return fmt.Errorf("value must not be set for op %q", m.Op)
		}
	case OpEq, OpNe:
		// Value may legitimately be empty (matching an empty label value).
	case OpRegex, OpNotRegex:
		if _, err := regexp.Compile(m.Value); err != nil {
			return fmt.Errorf("invalid regex %q: %w", m.Value, err)
		}
	default:
		return fmt.Errorf("unknown op %q", m.Op)
	}
	return nil
}
