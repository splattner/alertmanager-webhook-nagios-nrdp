package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/alertmanager"
	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/config"
	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/mapping"
	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/nrdp"
	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/nrdpstate"
)

func newTestCmd() *cobra.Command {
	var configPath, alertPath string

	cmd := &cobra.Command{
		Use:   "test",
		Short: "Dry-run a sample Alertmanager payload through the mapping/state rules and print the resulting NRDP checkresults",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runTest(cmd, configPath, alertPath)
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "/etc/nrdp-webhook/config.yaml", "path to the config file")
	cmd.Flags().StringVar(&alertPath, "alert", "", "path to a JSON file: an Alertmanager webhook payload, an array of alerts, or a single alert object")
	_ = cmd.MarkFlagRequired("alert")
	return cmd
}

func runTest(cmd *cobra.Command, configPath, alertPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	engine, err := mapping.New(cfg)
	if err != nil {
		return fmt.Errorf("compile rules: %w", err)
	}
	resolver := nrdpstate.New(cfg.State)

	alerts, err := readAlerts(alertPath)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	results := make([]nrdp.CheckResult, 0, len(alerts))
	for i, a := range alerts {
		_, _ = fmt.Fprintf(out, "=== alert %d: status=%s labels=%v ===\n", i, a.Status, a.Labels)

		res, ok, err := engine.Resolve(a)
		if err != nil {
			_, _ = fmt.Fprintf(out, "  ERROR: %v\n", err)
			continue
		}
		if !ok {
			_, _ = fmt.Fprintln(out, "  no rule matched, skipped")
			continue
		}

		output := a.Output()
		if res.HasOutput {
			output = res.Output
		}
		cr := nrdp.CheckResult{Type: string(res.CheckType), Hostname: res.Host, ServiceName: res.Service, Output: output}
		if res.CheckType == config.CheckTypeHost {
			cr.State = resolver.HostState(a)
			_, _ = fmt.Fprintf(out, "  rule %s: host=%s state=%d output=%q\n", res.Rule, res.Host, cr.State, output)
		} else {
			cr.State = resolver.ServiceState(a)
			_, _ = fmt.Fprintf(out, "  rule %s: host=%s service=%s state=%d output=%q\n", res.Rule, res.Host, res.Service, cr.State, output)
		}
		results = append(results, cr)
	}

	if len(results) == 0 {
		_, _ = fmt.Fprintln(out, "\nno checkresults would be submitted")
		return nil
	}

	xmlData, err := nrdp.BuildXML(results)
	if err != nil {
		return fmt.Errorf("build nrdp xml: %w", err)
	}
	_, _ = fmt.Fprintln(out, "\n--- NRDP XMLDATA that would be submitted ---")
	_, _ = out.Write(xmlData)
	_, _ = fmt.Fprintln(out)
	return nil
}

// readAlerts accepts a file containing an Alertmanager webhook payload, a
// JSON array of alerts, or a single alert object, so a sample can be
// grabbed straight from Alertmanager's own webhook log or hand-written
// as a one-off.
func readAlerts(path string) ([]alertmanager.Alert, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var payload alertmanager.WebhookPayload
	if err := json.Unmarshal(raw, &payload); err == nil && len(payload.Alerts) > 0 {
		return payload.Alerts, nil
	}

	var many []alertmanager.Alert
	if err := json.Unmarshal(raw, &many); err == nil {
		return many, nil
	}

	var single alertmanager.Alert
	if err := json.Unmarshal(raw, &single); err != nil {
		return nil, fmt.Errorf("parse %s as a webhook payload, an alert array, or a single alert: %w", path, err)
	}
	return []alertmanager.Alert{single}, nil
}
