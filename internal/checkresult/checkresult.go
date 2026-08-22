// Package checkresult turns a matched mapping rule plus its alert into the
// NRDP checkresult that would be submitted for it.
//
// It exists so the serving path and the `nrdp-webhook test` dry-run cannot
// drift apart: a dry-run that showed a checkresult the server would
// actually discard - or showed it unsanitized - would be worse than no
// dry-run at all.
package checkresult

import (
	"fmt"

	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/alertmanager"
	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/config"
	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/mapping"
	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/nrdp"
	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/nrdpstate"
)

// Build converts a resolved mapping result and its alert into a
// checkresult, scoring the state and sanitizing every field.
//
// It returns an error when the rule rendered an unusable target - an empty
// host, or an empty service on a service check. Submitted, those become a
// checkresult for a nameless object that Nagios discards without comment,
// so callers are expected to drop the alert and say so.
func Build(res mapping.Result, state *nrdpstate.Resolver, alert alertmanager.Alert) (nrdp.CheckResult, error) {
	output := alert.Output()
	if res.HasOutput {
		output = res.Output
	}

	cr := nrdp.CheckResult{
		Type:     string(res.CheckType),
		Hostname: nrdp.SanitizeName(res.Host),
		Output:   nrdp.SanitizeOutput(output),
	}
	if res.CheckType == config.CheckTypeHost {
		cr.State = state.HostState(alert)
	} else {
		cr.ServiceName = nrdp.SanitizeName(res.Service)
		cr.State = state.ServiceState(alert)
	}

	if cr.Hostname == "" {
		return cr, fmt.Errorf("rule %q rendered an empty host name", res.Rule)
	}
	if cr.Type == string(config.CheckTypeService) && cr.ServiceName == "" {
		return cr, fmt.Errorf("rule %q rendered an empty service name", res.Rule)
	}
	return cr, nil
}

// Target reduces a checkresult to just the Nagios object it addresses, for
// detecting two alerts in one payload that would land on the same check.
func Target(cr nrdp.CheckResult) nrdp.CheckResult {
	return nrdp.CheckResult{Type: cr.Type, Hostname: cr.Hostname, ServiceName: cr.ServiceName}
}
