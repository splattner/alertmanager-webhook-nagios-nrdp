package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/cobra"

	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/checkresult"
	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/config"
	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/heartbeat"
	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/mapping"
	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/metrics"
	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/nrdp"
	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/nrdpstate"
	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/tlsutil"
	"github.com/splattner/alertmanager-webhook-nagios-nrdp/internal/webhook"
)

func newServeCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the webhook server",
		RunE: func(_ *cobra.Command, _ []string) error {
			return runServe(configPath)
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "/etc/nrdp-webhook/config.yaml", "path to the config file")
	return cmd
}

// appState is everything a config (re)load builds. server swaps it in
// atomically so a request in flight always sees a consistent set of
// engine/resolver/client, never a mix of an old engine with a new client.
type appState struct {
	cfg        *config.Config
	engine     *mapping.Engine
	resolver   *nrdpstate.Resolver
	aggregator *checkresult.Aggregator
	client     *nrdp.Client
	serverTLS  *tls.Config
}

// server holds the mutable state a running `serve` invocation swaps as
// config reloads happen.
type server struct {
	configPath string
	log        *slog.Logger
	current    atomic.Pointer[appState]
}

func newServer(configPath string, log *slog.Logger) *server {
	return &server{configPath: configPath, log: log}
}

// buildState loads and compiles configPath into a fresh appState. It has
// no side effects on the running server - a caller decides whether/when to
// publish the result.
func buildState(configPath string) (*appState, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}

	engine, err := mapping.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("build mapping engine: %w", err)
	}

	aggregator, err := checkresult.NewAggregator(cfg.Aggregation)
	if err != nil {
		return nil, err
	}

	var nrdpTLS *tls.Config
	if cfg.NRDP.TLS != nil {
		t := cfg.NRDP.TLS
		nrdpTLS, err = tlsutil.ClientConfig(t.CAFile, t.CertFile, t.KeyFile, t.InsecureSkipVerify)
		if err != nil {
			return nil, fmt.Errorf("build nrdp tls config: %w", err)
		}
	}

	var serverTLS *tls.Config
	if cfg.Server.TLS != nil {
		t := cfg.Server.TLS
		serverTLS, err = tlsutil.ServerConfig(t.CertFile, t.KeyFile, t.ClientCAFile)
		if err != nil {
			return nil, fmt.Errorf("build server tls config: %w", err)
		}
	}

	return &appState{
		cfg:        cfg,
		engine:     engine,
		resolver:   nrdpstate.New(cfg.State),
		aggregator: aggregator,
		client:     nrdp.New(cfg.NRDP, nrdpTLS),
		serverTLS:  serverTLS,
	}, nil
}

func (s *server) applyConfig() error {
	st, err := buildState(s.configPath)
	if err != nil {
		return err
	}
	s.current.Store(st)
	return nil
}

// ServeHTTP dispatches to the webhook handler built from whatever appState
// is current at request time, wrapped in that state's auth config - so an
// auth or mapping change from a reload takes effect on the next request
// without a restart.
func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	st := s.current.Load()
	if st == nil {
		http.Error(w, "config not loaded", http.StatusServiceUnavailable)
		return
	}
	h := &webhook.Handler{
		Engine:        st.engine,
		State:         st.resolver,
		Aggregator:    st.aggregator,
		Client:        st.client,
		MaxBodyBytes:  st.cfg.Server.MaxBodyBytes,
		SubmitTimeout: submitBudget(st.cfg.NRDP),
		Log:           s.log,
	}
	webhook.WithAuth(st.cfg.Webhook.Auth, h).ServeHTTP(w, r)
}

// submitBudget is the ceiling for one whole NRDP submission: every attempt
// at its full timeout, plus the fixed backoff between them, plus a small
// margin. Derived rather than separately configurable so that raising
// nrdp.timeout or nrdp.retries cannot silently exceed it.
func submitBudget(cfg config.NRDPConfig) time.Duration {
	attempts := time.Duration(cfg.Retries + 1)
	return attempts*time.Duration(cfg.Timeout) + time.Duration(cfg.Retries)*nrdp.RetryBackoff + time.Second
}

func runServe(configPath string) error {
	log := newLogger()
	s := newServer(configPath, log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	reload := recordReload(s.applyConfig)
	if err := reload(); err != nil {
		return fmt.Errorf("initial config load: %w", err)
	}
	log.Info("config loaded", "path", configPath)

	go watchReload(ctx, configPath, log, reload)

	// Reads the live state on every beat, so enabling, disabling or
	// retargeting the heartbeat takes effect on reload without a restart.
	go heartbeat.Run(ctx, func() (config.HeartbeatConfig, heartbeat.Submitter) {
		st := s.current.Load()
		if st == nil {
			return config.HeartbeatConfig{}, nil
		}
		return st.cfg.Heartbeat, st.client
	}, log)

	mux := http.NewServeMux()
	mux.Handle("POST /webhook", s)
	mux.Handle("/metrics", promhttp.HandlerFor(metrics.Registry(), promhttp.HandlerOpts{}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if s.current.Load() == nil {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /-/reload", func(w http.ResponseWriter, _ *http.Request) {
		if err := reload(); err != nil {
			log.Error("reload failed", "error", err.Error())
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		log.Info("config reloaded via /-/reload")
		w.WriteHeader(http.StatusOK)
	})

	initialCfg := s.current.Load().cfg
	httpSrv := &http.Server{
		Addr:    initialCfg.Server.Listen,
		Handler: mux,
		// Bounded so a slow or stalled peer cannot hold a connection (and
		// its goroutine) open indefinitely. WriteTimeout has to clear the
		// worst-case handler, which is dominated by the NRDP submission
		// budget, or a slow Nagios would cut the response short.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      submitBudget(initialCfg.NRDP) + 30*time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Whether the listener serves TLS is fixed at startup from the initial
	// config; a reload can rotate the certificate/key/client CA (read fresh
	// on every handshake below) but cannot turn TLS on or off for an
	// already-running listener.
	useTLS := initialCfg.Server.TLS != nil
	if useTLS {
		httpSrv.TLSConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
			GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
				st := s.current.Load()
				if st == nil || st.serverTLS == nil {
					return nil, fmt.Errorf("server tls not configured")
				}
				return st.serverTLS, nil
			},
		}
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	}()

	log.Info("listening", "addr", initialCfg.Server.Listen, "tls", useTLS)
	var err error
	if useTLS {
		err = httpSrv.ListenAndServeTLS("", "")
	} else {
		err = httpSrv.ListenAndServe()
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// recordReload wraps fn (a config-apply attempt) so every reload trigger -
// initial load, SIGHUP/file watch, POST /-/reload - reports through the
// same nrdp_webhook_config_reloads_total metric regardless of what
// triggered it.
func recordReload(fn func() error) func() error {
	return func() error {
		if err := fn(); err != nil {
			metrics.ConfigReloadsTotal.WithLabelValues("error").Inc()
			return err
		}
		metrics.ConfigReloadsTotal.WithLabelValues("ok").Inc()
		return nil
	}
}

// watchReload triggers reload on SIGHUP and on changes to the config
// file's parent directory (a ConfigMap volume update replaces the file via
// a symlink swap, which a watch on the file's own inode would miss).
func watchReload(ctx context.Context, configPath string, log *slog.Logger, reload func() error) {
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Warn("config file watch disabled", "error", err.Error())
		watcher = nil
	} else {
		defer func() { _ = watcher.Close() }()
		if err := watcher.Add(filepath.Dir(configPath)); err != nil {
			log.Warn("config file watch disabled", "error", err.Error())
		}
	}

	base := filepath.Base(configPath)
	trigger := func(source string) {
		if err := reload(); err != nil {
			log.Error("reload failed", "source", source, "error", err.Error())
			return
		}
		log.Info("config reloaded", "source", source)
	}

	var events <-chan fsnotify.Event
	if watcher != nil {
		events = watcher.Events
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-hup:
			trigger("SIGHUP")
		case ev, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			if filepath.Base(ev.Name) == base {
				trigger("file watch")
			}
		}
	}
}
