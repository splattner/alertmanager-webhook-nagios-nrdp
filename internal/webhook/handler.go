// Package webhook implements the HTTP handler Alertmanager's
// webhook_configs POSTs alerts to, turning each webhook call into a single
// NRDP submission.
package webhook

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/alertmanager"
	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/checkresult"
	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/mapping"
	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/metrics"
	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/nrdp"
	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/nrdpstate"
)

// Submitter is the subset of *nrdp.Client the handler depends on, so tests
// can substitute a fake.
type Submitter interface {
	Submit(ctx context.Context, results []nrdp.CheckResult) error
}

// Handler receives an Alertmanager webhook payload, maps and scores each
// alert, and submits the batch to NRDP as one request.
type Handler struct {
	Engine       *mapping.Engine
	State        *nrdpstate.Resolver
	Client       Submitter
	MaxBodyBytes int64
	// SubmitTimeout caps one whole NRDP submission including retries, so a
	// slow Nagios cannot pin a request handler open indefinitely.
	SubmitTimeout time.Duration
	Log           *slog.Logger
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, h.MaxBodyBytes)

	payload, err := alertmanager.Decode(r.Body)
	if err != nil {
		h.Log.Warn("rejecting webhook payload", "error", err.Error())
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	metrics.AlertsReceivedTotal.Add(float64(len(payload.Alerts)))
	if payload.TruncatedAlerts > 0 {
		h.Log.Warn("alertmanager truncated alerts from this payload before sending it",
			"truncated", payload.TruncatedAlerts, "received", len(payload.Alerts))
		metrics.TruncatedAlertsTotal.Add(float64(payload.TruncatedAlerts))
	}

	results := h.mapAlerts(payload.Alerts)
	if len(results) == 0 {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Deliberately not r.Context(): the alerts are already in hand, and
	// abandoning a half-finished submission because Alertmanager hung up
	// would lose them for the whole repeat_interval. The deadline instead
	// bounds the work on its own terms - see Submit's own per-attempt
	// timeout and retry budget.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), h.SubmitTimeout)
	defer cancel()

	start := time.Now()
	err = h.Client.Submit(ctx, results)
	metrics.SubmissionDuration.Observe(time.Since(start).Seconds())
	if err != nil {
		h.Log.Error("nrdp submission failed", "error", err.Error(), "checkresults", len(results))
		metrics.SubmissionsTotal.WithLabelValues("error").Inc()
		metrics.CheckResultsForwardedTotal.WithLabelValues("error").Add(float64(len(results)))
		http.Error(w, fmt.Sprintf("nrdp submission failed: %v", err), http.StatusBadGateway)
		return
	}

	metrics.SubmissionsTotal.WithLabelValues("ok").Inc()
	metrics.CheckResultsForwardedTotal.WithLabelValues("ok").Add(float64(len(results)))
	w.WriteHeader(http.StatusOK)
}

// mapAlerts resolves every alert to an NRDP checkresult, skipping (and
// counting) any that match no mapping rule. One alert failing to map does
// not fail the rest of the batch.
func (h *Handler) mapAlerts(alerts []alertmanager.Alert) []nrdp.CheckResult {
	results := make([]nrdp.CheckResult, 0, len(alerts))
	// seen tracks which (type, host, service) targets have already appeared
	// in this payload. Alertmanager can batch several distinct alerts into
	// one webhook call (grouping), and if two of them render to the same
	// Nagios target, NRDP applies checkresults in order - only the last one
	// in the submission actually determines the resulting state. That's
	// silent unless logged/counted here.
	seen := make(map[nrdp.CheckResult]bool, len(alerts))
	for _, alert := range alerts {
		res, ok, err := h.Engine.Resolve(alert)
		if err != nil {
			h.Log.Error("mapping rule failed", "error", err.Error(), "labels", alert.Labels)
			metrics.MappingUnmatchedTotal.Inc()
			continue
		}
		if !ok {
			h.Log.Warn("alert matched no mapping rule, skipping", "labels", alert.Labels)
			metrics.MappingUnmatchedTotal.Inc()
			continue
		}

		cr, err := checkresult.Build(res, h.State, alert)
		if err != nil {
			h.Log.Error("skipping alert with an unusable Nagios target",
				"error", err.Error(), "labels", alert.Labels)
			metrics.InvalidTargetTotal.Inc()
			continue
		}

		target := checkresult.Target(cr)
		if seen[target] {
			h.Log.Warn("multiple alerts in this batch map to the same Nagios target; only the last one's state will apply",
				"type", cr.Type, "hostname", cr.Hostname, "service", cr.ServiceName, "labels", alert.Labels)
			metrics.DuplicateCheckResultsTotal.Inc()
		}
		seen[target] = true

		results = append(results, cr)
	}
	return results
}
