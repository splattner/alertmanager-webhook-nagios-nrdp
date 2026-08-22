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
	return r.state(alert, r.cfg.Service, r.cfg.ResolvedService)
}

// HostState returns the Nagios host state (0=UP, 1=DOWN, 2=UNREACHABLE)
// for alert.
func (r *Resolver) HostState(alert alertmanager.Alert) int {
	return r.state(alert, r.cfg.Host, r.cfg.ResolvedHost)
}

// state looks up the severity map for a firing alert, or unconditionally
// returns resolvedKey's value for a resolved one - resolution always wins
// over whatever the severity label happens to say, so Nagios actually
// clears the check.
func (r *Resolver) state(alert alertmanager.Alert, m config.StateMapConfig, resolvedKey string) int {
	if alert.Status == alertmanager.StatusResolved {
		return m.Values[resolvedKey]
	}
	severity := alert.Labels[r.cfg.SeverityLabel]
	if v, ok := m.Values[severity]; ok {
		return v
	}
	return m.Values[m.Unmatched]
}
