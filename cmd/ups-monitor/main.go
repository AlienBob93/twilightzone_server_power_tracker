// Command ups-monitor collects UPS telemetry from a NUT server, stores it in
// SQLite and serves a dashboard and JSON API.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"twilightzone/ups-monitor/internal/api"
	"twilightzone/ups-monitor/internal/collector"
	"twilightzone/ups-monitor/internal/config"
	"twilightzone/ups-monitor/internal/nut"
	"twilightzone/ups-monitor/internal/store"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ups-monitor:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		configPath  string
		showVersion bool
	)
	flag.StringVar(&configPath, "config", "", "path to config.yaml (environment variables and built-in defaults are used when omitted)")
	flag.BoolVar(&showVersion, "version", false, "print the version and exit")
	flag.Parse()

	if showVersion {
		fmt.Println(version)
		return nil
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	logger := newLogger(cfg)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, cfg.Storage.Path)
	if err != nil {
		return err
	}
	defer st.Close()
	logger.Info("storage ready", "path", cfg.Storage.Path)

	client := nut.New(
		cfg.NUT.Host,
		cfg.NUT.Port,
		cfg.NUT.Username,
		cfg.NUT.Password,
		cfg.NUT.Timeout.Std(),
	)
	collectorInstance := collector.New(
		client,
		st,
		cfg.Collector.Interval.Std(),
		cfg.NUT.UPS,
		cfg.Storage.Retention.Std(),
		logger,
	)

	staleAfter := 3 * cfg.Collector.Interval.Std()
	if staleAfter < 30*time.Second {
		staleAfter = 30 * time.Second
	}

	apiServer, err := api.New(st, collectorInstance, client, staleAfter, cfg.Server.RefreshSeconds, logger)
	if err != nil {
		return err
	}

	httpServer := &http.Server{
		Addr:              cfg.Server.Address,
		Handler:           apiServer.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       90 * time.Second,
	}

	go collectorInstance.Run(ctx)

	serverError := make(chan error, 1)
	go func() {
		logger.Info("dashboard listening",
			"address", cfg.Server.Address,
			"nut", fmt.Sprintf("%s:%d", cfg.NUT.Host, cfg.NUT.Port),
			"ups", displayUPS(cfg.NUT.UPS),
		)
		serverError <- httpServer.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		stop()
	case err := <-serverError:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("http server: %w", err)
		}
		return nil
	}

	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}

func newLogger(cfg config.Config) *slog.Logger {
	options := &slog.HandlerOptions{Level: cfg.SlogLevel()}

	var handler slog.Handler
	if strings.EqualFold(cfg.Logging.Format, "json") {
		handler = slog.NewJSONHandler(os.Stdout, options)
	} else {
		handler = slog.NewTextHandler(os.Stdout, options)
	}
	return slog.New(handler).With("version", version)
}

func displayUPS(name string) string {
	if strings.TrimSpace(name) == "" {
		return "auto-detect"
	}
	return name
}
