// Package metrics defines and registers the webhook's Prometheus metrics.
package metrics

import "github.com/prometheus/client_golang/prometheus"

var registry = prometheus.NewRegistry()

// Registry is the Prometheus registry served at /metrics.
func Registry() *prometheus.Registry {
	return registry
}

// AlertsReceivedTotal counts every alert seen in an inbound webhook
// payload, before mapping is attempted.
var AlertsReceivedTotal = prometheus.NewCounter(prometheus.CounterOpts{
	Name: "nrdp_webhook_alerts_received_total",
	Help: "Total number of alerts received across all webhook payloads.",
})

// MappingUnmatchedTotal counts alerts that matched no mapping rule and
// were therefore skipped.
var MappingUnmatchedTotal = prometheus.NewCounter(prometheus.CounterOpts{
	Name: "nrdp_webhook_mapping_unmatched_total",
	Help: "Total number of alerts that matched no mapping rule and were skipped.",
})

// SubmissionsTotal counts NRDP submissions (one per webhook call that
// produced at least one checkresult), labeled by result.
var SubmissionsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "nrdp_webhook_nrdp_submissions_total",
	Help: "Total number of NRDP submissions, labeled by result (ok|error).",
}, []string{"result"})

// CheckResultsForwardedTotal counts individual checkresults included in
// (successful or failed) NRDP submissions.
var CheckResultsForwardedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "nrdp_webhook_checkresults_forwarded_total",
	Help: "Total number of checkresults included in NRDP submissions, labeled by result (ok|error).",
}, []string{"result"})

// DuplicateCheckResultsTotal counts checkresults that target the same
// host (and, for a service check, service) as another checkresult already
// produced within the same webhook payload - e.g. two alerts in one
// Alertmanager group that render to the same Nagios host/service. NRDP
// applies a batch's checkresults in order, so only the last one actually
// determines the resulting Nagios state; this metric makes that silent
// overwrite visible instead.
var DuplicateCheckResultsTotal = prometheus.NewCounter(prometheus.CounterOpts{
	Name: "nrdp_webhook_duplicate_checkresults_total",
	Help: "Total number of checkresults that targeted the same host/service as another checkresult already produced within the same webhook payload.",
})

// ConfigReloadsTotal counts config (re)load attempts, labeled by result.
var ConfigReloadsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "nrdp_webhook_config_reloads_total",
	Help: "Total number of config load/reload attempts, labeled by result (ok|error).",
}, []string{"result"})

func init() {
	registry.MustRegister(
		AlertsReceivedTotal,
		MappingUnmatchedTotal,
		SubmissionsTotal,
		CheckResultsForwardedTotal,
		DuplicateCheckResultsTotal,
		ConfigReloadsTotal,
	)
}
