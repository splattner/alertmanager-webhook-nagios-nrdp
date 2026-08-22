// Package heartbeat periodically submits a passive OK to a dedicated
// Nagios check, so that this service being down is detectable.
//
// Alert forwarding is a silent-failure pipeline: if the webhook, or
// Alertmanager, or the network in between stops working, Nagios sees no
// checkresults - which is indistinguishable from nothing being wrong. A
// heartbeat converts that into a check going stale, which Nagios can raise
// on its own via check_freshness.
package heartbeat

import (
	"context"
	"log/slog"
	"time"

	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/config"
	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/metrics"
	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/nrdp"
)

// Submitter is the subset of *nrdp.Client this package needs, so tests can
// substitute a fake.
type Submitter interface {
	Submit(ctx context.Context, results []nrdp.CheckResult) error
}

// Source supplies the current heartbeat configuration and the client to
// submit through. It is consulted on every beat rather than captured once,
// so a config reload takes effect without restarting the loop - including
// enabling or disabling the heartbeat outright.
type Source func() (config.HeartbeatConfig, Submitter)

// idleInterval is how often the loop re-checks a config that currently has
// the heartbeat disabled. It only bounds how quickly a reload that *enables*
// the heartbeat takes effect, so it does not need to be short.
const idleInterval = 30 * time.Second

// Run beats until ctx is cancelled. It returns only on cancellation.
func Run(ctx context.Context, src Source, log *slog.Logger) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	// Drain the initial fire so the first wait below is a real interval:
	// beating immediately at startup would submit before the process is
	// known good, and readiness has not been established yet.
	<-timer.C
	timer.Reset(idleInterval)

	for {
		cfg, client := src()
		wait := idleInterval
		if cfg.Enabled {
			wait = time.Duration(cfg.Interval)
		}
		timer.Reset(wait)

		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		// Re-read after waiting: a reload may have changed things while the
		// timer was running.
		cfg, client = src()
		if !cfg.Enabled || client == nil {
			continue
		}
		beat(ctx, cfg, client, log)
	}
}

func beat(ctx context.Context, cfg config.HeartbeatConfig, client Submitter, log *slog.Logger) {
	cr := nrdp.CheckResult{
		Type:     string(cfg.CheckType),
		Hostname: nrdp.SanitizeName(cfg.Host),
		State:    config.States(cfg.CheckType)[cfg.State],
		Output:   nrdp.SanitizeOutput(cfg.Output),
	}
	if cfg.CheckType == config.CheckTypeService {
		cr.ServiceName = nrdp.SanitizeName(cfg.Service)
	}

	if err := client.Submit(ctx, []nrdp.CheckResult{cr}); err != nil {
		// Not fatal: the next beat retries, and a heartbeat that killed the
		// process on a transient NRDP blip would take alert forwarding down
		// with it - the opposite of the point.
		if ctx.Err() != nil {
			return // shutting down, not a real failure
		}
		log.Error("heartbeat submission failed", "error", err.Error(),
			"hostname", cr.Hostname, "service", cr.ServiceName)
		metrics.HeartbeatsTotal.WithLabelValues("error").Inc()
		return
	}

	metrics.HeartbeatsTotal.WithLabelValues("ok").Inc()
	metrics.HeartbeatLastSuccessTimestamp.Set(float64(time.Now().Unix()))
	log.Debug("heartbeat submitted", "hostname", cr.Hostname, "service", cr.ServiceName)
}
