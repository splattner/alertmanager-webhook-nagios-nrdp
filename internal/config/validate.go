package config

import (
	"fmt"
	"regexp"
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
	if cfg.NRDP.Retries < 0 {
		return fmt.Errorf("nrdp.retries must not be negative")
	}

	if err := validateStateMap("state.service", cfg.State.Service, cfg.State.ResolvedService); err != nil {
		return err
	}
	if err := validateStateMap("state.host", cfg.State.Host, cfg.State.ResolvedHost); err != nil {
		return err
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

func validateStateMap(field string, m StateMapConfig, resolvedKey string) error {
	if len(m.Values) == 0 {
		return fmt.Errorf("%s.values must not be empty", field)
	}
	if m.Unmatched == "" {
		return fmt.Errorf("%s.unmatched must not be empty", field)
	}
	if _, ok := m.Values[m.Unmatched]; !ok {
		return fmt.Errorf("%s.unmatched %q is not a key in %s.values", field, m.Unmatched, field)
	}
	if resolvedKey == "" {
		return fmt.Errorf("resolved state key must not be empty")
	}
	if _, ok := m.Values[resolvedKey]; !ok {
		return fmt.Errorf("resolved state key %q is not a key in %s.values", resolvedKey, field)
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
