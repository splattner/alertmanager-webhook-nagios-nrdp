// Command nrdp-webhook receives Prometheus Alertmanager webhook
// notifications and forwards them to Nagios as passive check results via
// the NRDP protocol.
package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "nrdp-webhook",
		Short:         "Forward Alertmanager alerts to Nagios via NRDP",
		SilenceUsage:  true,
		SilenceErrors: false,
	}
	root.AddCommand(newServeCmd())
	root.AddCommand(newCheckCmd())
	root.AddCommand(newTestCmd())
	root.AddCommand(newVersionCmd())
	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the build version",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), version)
			return nil
		},
	}
}

func newLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stderr, nil))
}
