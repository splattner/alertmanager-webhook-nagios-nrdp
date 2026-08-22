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

// InvalidTargetTotal counts alerts dropped because their rule rendered an
// empty Nagios host or service name - typically a template referencing a
// label the alert does not carry.
var InvalidTargetTotal = prometheus.NewCounter(prometheus.CounterOpts{
	Name: "nrdp_webhook_invalid_target_total",
	Help: "Total number of alerts skipped because the matched rule rendered an empty host or service name.",
})

// TruncatedAlertsTotal counts alerts Alertmanager itself dropped from a
// payload before sending it (its truncatedAlerts field). These never reach
// this service at all, so they are invisible except through this counter.
var TruncatedAlertsTotal = prometheus.NewCounter(prometheus.CounterOpts{
	Name: "nrdp_webhook_truncated_alerts_total",
	Help: "Total number of alerts Alertmanager truncated from incoming payloads before sending them.",
})

// SubmissionDuration observes how long a full NRDP submission takes,
// including any retries.
var SubmissionDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
	Name:    "nrdp_webhook_nrdp_submission_duration_seconds",
	Help:    "Duration of NRDP submissions, including retries.",
	Buckets: prometheus.DefBuckets,
})

// DuplicateCheckResultsTotal counts alerts that landed on a Nagios target
// already claimed by an earlier alert in the same payload - e.g. several
// alerts from one Alertmanager group rendering to the same host/service.
// With aggregation enabled (the default) these are merged rather than left
// to overwrite each other, so this measures how much merging is doing.
var DuplicateCheckResultsTotal = prometheus.NewCounter(prometheus.CounterOpts{
	Name: "nrdp_webhook_duplicate_checkresults_total",
	Help: "Total number of alerts that targeted the same host/service as an earlier alert within the same webhook payload, and were therefore merged (see the aggregation config).",
})

// HeartbeatsTotal counts heartbeat submissions, labeled by result.
var HeartbeatsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "nrdp_webhook_heartbeats_total",
	Help: "Total number of heartbeat submissions, labeled by result (ok|error).",
}, []string{"result"})

// HeartbeatLastSuccessTimestamp is when the last heartbeat was accepted by
// NRDP, as a Unix timestamp. Useful for alerting on the alerting pipeline
// from Prometheus's side, as a complement to the Nagios-side freshness
// check the heartbeat exists to feed.
var HeartbeatLastSuccessTimestamp = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "nrdp_webhook_heartbeat_last_success_timestamp_seconds",
	Help: "Unix timestamp of the last heartbeat successfully submitted to NRDP.",
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
		InvalidTargetTotal,
		TruncatedAlertsTotal,
		SubmissionDuration,
		DuplicateCheckResultsTotal,
		HeartbeatsTotal,
		HeartbeatLastSuccessTimestamp,
		ConfigReloadsTotal,
	)
}
