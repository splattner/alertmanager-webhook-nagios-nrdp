// Package mapping resolves which Nagios host/service (or host-only check)
// an Alertmanager alert targets, by evaluating the configured rules in
// order and rendering the first match's templates.
package mapping

import (
	"bytes"
	"fmt"
	"regexp"
	"text/template"

	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/alertmanager"
	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/config"
)

// Result is the outcome of resolving one alert against the rule set.
type Result struct {
	Rule      string
	CheckType config.CheckType
	Host      string
	// Service is empty for a CheckTypeHost result.
	Service string
	// Output is the rendered output template's text. Only meaningful when
	// HasOutput is true - the rule may not have configured one, in which
	// case the caller falls back to its own default (see
	// alertmanager.Alert.Output).
	Output    string
	HasOutput bool
}

// Engine holds the compiled (matchers + templates) form of a Config's
// rules, so per-alert resolution does no parsing or regex compilation.
type Engine struct {
	rules []compiledRule
}

type compiledRule struct {
	name      string
	matchers  []compiledMatch
	checkType config.CheckType
	host      *template.Template
	service   *template.Template
	output    *template.Template
}

type compiledMatch struct {
	label string
	op    config.MatchOp
	value string
	re    *regexp.Regexp
}

// templateData is what a rule's host/service template is rendered against.
type templateData struct {
	Labels      map[string]string
	Annotations map[string]string
	Status      string
	Fingerprint string
}

// newTemplate returns a template.New(name) preconfigured for rendering
// host/service names from an alert's labels/annotations.
//
// missingkey=zero matters here: without it, text/template's dot-chain map
// index (`.Labels.foo`) on an absent key yields an internal "invalid"
// reflect.Value rather than an empty string - printed directly that shows
// up as the literal text "<no value>" in a host/service name, and piped
// into a function expecting a string (e.g. `| default "x"`) it hard-fails
// execution instead of passing "". Alert label sets vary per alert, so a
// rule's template routinely runs against alerts missing whatever label it
// references.
func newTemplate(name string) *template.Template {
	return template.New(name).Option("missingkey=zero").Funcs(funcMap)
}

var funcMap = template.FuncMap{
	// default returns val unless it is empty, in which case it returns def -
	// e.g. {{ .Labels.instance | default "unknown" }}.
	"default": func(def, val string) string {
		if val == "" {
			return def
		}
		return val
	},
}

// New compiles cfg.Rules. cfg is assumed to already be defaulted and
// validated (see config.Load).
func New(cfg *config.Config) (*Engine, error) {
	rules := make([]compiledRule, 0, len(cfg.Rules))
	for _, r := range cfg.Rules {
		cr, err := compileRule(r)
		if err != nil {
			return nil, fmt.Errorf("rule %q: %w", r.Name, err)
		}
		rules = append(rules, cr)
	}
	return &Engine{rules: rules}, nil
}

func compileRule(r config.RuleConfig) (compiledRule, error) {
	cr := compiledRule{name: r.Name, checkType: r.CheckType}

	for _, m := range r.Match {
		cm := compiledMatch{label: m.Label, op: m.Op, value: m.Value}
		if m.Op == config.OpRegex || m.Op == config.OpNotRegex {
			re, err := regexp.Compile(m.Value)
			if err != nil {
				return compiledRule{}, fmt.Errorf("match on %q: %w", m.Label, err)
			}
			cm.re = re
		}
		cr.matchers = append(cr.matchers, cm)
	}

	hostTmpl, err := newTemplate(r.Name + "-host").Parse(r.Host.Template)
	if err != nil {
		return compiledRule{}, fmt.Errorf("host template: %w", err)
	}
	cr.host = hostTmpl

	if r.Service != nil && r.Service.Template != "" {
		svcTmpl, err := newTemplate(r.Name + "-service").Parse(r.Service.Template)
		if err != nil {
			return compiledRule{}, fmt.Errorf("service template: %w", err)
		}
		cr.service = svcTmpl
	}

	if r.Output != nil && r.Output.Template != "" {
		outTmpl, err := newTemplate(r.Name + "-output").Parse(r.Output.Template)
		if err != nil {
			return compiledRule{}, fmt.Errorf("output template: %w", err)
		}
		cr.output = outTmpl
	}

	return cr, nil
}

// Resolve evaluates rules in order against alert and returns the first
// match's rendered host/service names. ok is false if no rule matched.
func (e *Engine) Resolve(alert alertmanager.Alert) (result Result, ok bool, err error) {
	for _, r := range e.rules {
		if !matchesAll(r.matchers, alert.Labels) {
			continue
		}

		data := templateData{
			Labels:      alert.Labels,
			Annotations: alert.Annotations,
			Status:      alert.Status,
			Fingerprint: alert.Fingerprint,
		}

		host, err := render(r.host, data)
		if err != nil {
			return Result{}, false, fmt.Errorf("rule %q: render host: %w", r.name, err)
		}

		res := Result{Rule: r.name, CheckType: r.checkType, Host: host}
		if r.service != nil {
			svc, err := render(r.service, data)
			if err != nil {
				return Result{}, false, fmt.Errorf("rule %q: render service: %w", r.name, err)
			}
			res.Service = svc
		}
		if r.output != nil {
			out, err := render(r.output, data)
			if err != nil {
				return Result{}, false, fmt.Errorf("rule %q: render output: %w", r.name, err)
			}
			res.Output = out
			res.HasOutput = true
		}
		return res, true, nil
	}
	return Result{}, false, nil
}

func render(tmpl *template.Template, data templateData) (string, error) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func matchesAll(matchers []compiledMatch, labels map[string]string) bool {
	for _, m := range matchers {
		if !matchOne(m, labels) {
			return false
		}
	}
	return true
}

func matchOne(m compiledMatch, labels map[string]string) bool {
	v, present := labels[m.label]
	switch m.op {
	case config.OpExists:
		return present
	case config.OpAbsent:
		return !present
	case config.OpEq:
		return present && v == m.value
	case config.OpNe:
		return !present || v != m.value
	case config.OpRegex:
		return present && m.re.MatchString(v)
	case config.OpNotRegex:
		return !present || !m.re.MatchString(v)
	default:
		return false
	}
}
