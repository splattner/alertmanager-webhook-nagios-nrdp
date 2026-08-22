package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/config"
	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/mapping"
	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/tlsutil"
)

func newCheckCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "check",
		Short: "Validate a config file and exit",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCheck(cmd, configPath)
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "/etc/nrdp-webhook/config.yaml", "path to the config file")
	return cmd
}

func runCheck(cmd *cobra.Command, configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	// Compiling the rule engine here catches a bad template/regex at check
	// time rather than at first webhook call.
	if _, err := mapping.New(cfg); err != nil {
		return fmt.Errorf("compile rules: %w", err)
	}

	if err := checkTLS(cfg); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "ok: %d rule(s), nrdp target %s\n", len(cfg.Rules), cfg.NRDP.URL)
	return nil
}

// checkTLS builds the same tls.Config values serve would, so a bad or
// missing cert/key/CA file is caught here rather than surfacing later at
// startup or, worse, at first connection.
func checkTLS(cfg *config.Config) error {
	if cfg.Server.TLS != nil {
		t := cfg.Server.TLS
		if _, err := tlsutil.ServerConfig(t.CertFile, t.KeyFile, t.ClientCAFile); err != nil {
			return fmt.Errorf("server.tls: %w", err)
		}
	}
	if cfg.NRDP.TLS != nil {
		t := cfg.NRDP.TLS
		if _, err := tlsutil.ClientConfig(t.CAFile, t.CertFile, t.KeyFile, t.InsecureSkipVerify); err != nil {
			return fmt.Errorf("nrdp.tls: %w", err)
		}
	}
	return nil
}
