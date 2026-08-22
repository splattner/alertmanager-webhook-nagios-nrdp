// Package nrdpstate translates an Alertmanager alert's status and severity
// label into the Nagios state code an NRDP checkresult carries.
package nrdpstate

import (
	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/alertmanager"
	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/config"
)

// Resolver computes Nagios state codes from a Config's StateConfig. cfg is
// assumed to already be defaulted and validated (see config.Load).
type Resolver struct {
	cfg config.StateConfig
}

// New returns a Resolver for cfg.
func New(cfg config.StateConfig) *Resolver {
	return &Resolver{cfg: cfg}
}

// ServiceState returns the Nagios service state (0=OK, 1=WARNING,
// 2=CRITICAL, 3=UNKNOWN) for alert.
func (r *Resolver) ServiceState(alert alertmanager.Alert) int {
	return r.state(alert, r.cfg.Service, config.ServiceStates)
}

// HostState returns the Nagios host state (0=UP, 1=DOWN, 2=UNREACHABLE)
// for alert.
func (r *Resolver) HostState(alert alertmanager.Alert) int {
	return r.state(alert, r.cfg.Host, config.HostStates)
}

// state resolves the alert to a state name and then to that name's Nagios
// code. A resolved alert unconditionally takes m.Resolved - resolution
// always wins over whatever the severity label happens to say, so Nagios
// actually clears the check.
//
// Every name reachable here is checked against vocab at config-load time,
// so the lookups cannot miss; a zero from an absent key would be OK/UP,
// which is exactly the wrong direction to fail in, hence the validation.
func (r *Resolver) state(alert alertmanager.Alert, m config.StateMapConfig, vocab map[string]int) int {
	if alert.Status == alertmanager.StatusResolved {
		return vocab[m.Resolved]
	}
	severity := alert.Labels[r.cfg.SeverityLabel]
	if name, ok := m.Values[severity]; ok {
		return vocab[name]
	}
	return vocab[m.Unmatched]
}
