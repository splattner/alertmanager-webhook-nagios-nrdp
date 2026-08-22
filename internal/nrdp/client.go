// Package nrdp implements a client for Nagios's NRDP (Nagios Remote Data
// Processor) protocol, used here to submit passive host/service check
// results. See https://docs.nagios.com/nrdp/current/using.html.
package nrdp

import (
	"context"
	"crypto/tls"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/config"
)

// RetryBackoff is the fixed pause between submission attempts. A fixed
// short backoff is enough here: NRDP submissions are small and infrequent
// (one per Alertmanager webhook call), so this is not a high-QPS client
// that needs jitter to avoid thundering-herd effects.
const RetryBackoff = time.Second

// Client submits checkresults to a single NRDP endpoint.
type Client struct {
	url        string
	token      string
	httpClient *http.Client
	timeout    time.Duration
	retries    int
}

// New builds a Client from cfg. tlsConfig is nil unless cfg.TLS is set.
func New(cfg config.NRDPConfig, tlsConfig *tls.Config) *Client {
	transport := http.DefaultTransport
	if tlsConfig != nil {
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.TLSClientConfig = tlsConfig
		transport = t
	}
	return &Client{
		url:        cfg.URL,
		token:      cfg.Token,
		httpClient: &http.Client{Transport: transport},
		timeout:    time.Duration(cfg.Timeout),
		retries:    cfg.Retries,
	}
}

// Submit builds an NRDP submitcheck request for results and POSTs it,
// retrying the whole submission up to Client.retries additional times on
// failure. Submitting zero results is a no-op.
func (c *Client) Submit(ctx context.Context, results []CheckResult) error {
	if len(results) == 0 {
		return nil
	}

	xmlData, err := buildXML(results)
	if err != nil {
		return fmt.Errorf("build nrdp xml: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt <= c.retries; attempt++ {
		if lastErr != nil {
			select {
			case <-ctx.Done():
				return fmt.Errorf("submit to nrdp (gave up after %d attempt(s)): %w", attempt, lastErr)
			case <-time.After(RetryBackoff):
			}
		}

		if err := c.submitOnce(ctx, xmlData); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return fmt.Errorf("submit to nrdp after %d attempt(s): %w", c.retries+1, lastErr)
}

func (c *Client) submitOnce(ctx context.Context, xmlData []byte) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	form := url.Values{
		"token":   {c.token},
		"cmd":     {"submitcheck"},
		"XMLDATA": {string(xmlData)},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("nrdp returned http %d", resp.StatusCode)
	}

	var parsed responseXML
	if err := xml.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return fmt.Errorf("decode nrdp response: %w", err)
	}
	if parsed.Status != 0 {
		return fmt.Errorf("nrdp rejected submission (status=%d): %s", parsed.Status, parsed.Message)
	}
	return nil
}
