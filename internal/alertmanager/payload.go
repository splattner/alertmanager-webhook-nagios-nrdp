// Package alertmanager defines the shape of the JSON payload Alertmanager
// POSTs to a webhook receiver.
//
// See https://prometheus.io/docs/alerting/latest/configuration/#webhook_config
// and https://pkg.go.dev/github.com/prometheus/alertmanager/template#Data.
// These types are hand-rolled rather than imported from Alertmanager itself
// so this project does not pull in the whole alertmanager module tree for
// what is, on the wire, a small and stable JSON shape.
package alertmanager

import (
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// Status values an Alert or a WebhookPayload can carry.
const (
	StatusFiring   = "firing"
	StatusResolved = "resolved"
)

// Alert is one alert within a webhook payload.
type Alert struct {
	Status       string            `json:"status"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     time.Time         `json:"startsAt"`
	EndsAt       time.Time         `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL"`
	Fingerprint  string            `json:"fingerprint"`
}

// Output returns human-readable plugin-output text for the alert: the
// "summary" annotation (a common Alertmanager convention), falling back to
// "description", falling back to the alertname label so every alert still
// produces readable output even without either annotation.
func (a Alert) Output() string {
	if s := a.Annotations["summary"]; s != "" {
		return s
	}
	if s := a.Annotations["description"]; s != "" {
		return s
	}
	return a.Labels["alertname"]
}

// WebhookPayload is the top-level object Alertmanager POSTs to a webhook
// receiver, containing one or more Alerts sharing a group key.
type WebhookPayload struct {
	Version           string            `json:"version"`
	GroupKey          string            `json:"groupKey"`
	TruncatedAlerts   int               `json:"truncatedAlerts,omitempty"`
	Status            string            `json:"status"`
	Receiver          string            `json:"receiver"`
	GroupLabels       map[string]string `json:"groupLabels"`
	CommonLabels      map[string]string `json:"commonLabels"`
	CommonAnnotations map[string]string `json:"commonAnnotations"`
	ExternalURL       string            `json:"externalURL"`
	Alerts            []Alert           `json:"alerts"`
}

// Decode parses a webhook payload from r. Unknown fields are ignored
// (unlike this project's own config parsing): Alertmanager's payload has
// grown fields across versions, and a forward-compatible receiver should
// not fail on an addition it doesn't yet know about.
func Decode(r io.Reader) (*WebhookPayload, error) {
	var p WebhookPayload
	if err := json.NewDecoder(r).Decode(&p); err != nil {
		return nil, fmt.Errorf("decode alertmanager webhook payload: %w", err)
	}
	return &p, nil
}
