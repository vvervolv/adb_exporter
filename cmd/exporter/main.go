// Command exporter is the adb_exporter entrypoint: a Prometheus exporter that
// monitors Android devices over adb.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/vvervolv/adb_exporter/internal/adb"
	"github.com/vvervolv/adb_exporter/internal/config"
	xhttp "github.com/vvervolv/adb_exporter/internal/http"
	"github.com/vvervolv/adb_exporter/internal/metrics"
	"github.com/vvervolv/adb_exporter/internal/poller"
	"github.com/vvervolv/adb_exporter/internal/service"
)

// Version metadata, overridden at build time via -ldflags (SPEC §CI/CD).
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	var (
		configPath  string
		showVersion bool
	)
	flag.StringVar(&configPath, "config", "", "path to YAML config file (defaults are used if empty)")
	flag.BoolVar(&showVersion, "version", false, "print version information and exit")
	flag.Usage = usage
	flag.Parse()

	if showVersion {
		printVersion()
		return
	}

	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load(configPath)
	if err != nil {
		log.Error("config error", "err", err)
		os.Exit(1)
	}

	app := service.App{Run: func(ctx context.Context) error {
		return run(ctx, cfg, log)
	}}

	// Re-pass --config so a managed service uses the same file it was installed with.
	var args []string
	if configPath != "" {
		args = []string{"--config", configPath}
	}
	svc, err := service.New(app, service.Options{
		Name:        "adb_exporter",
		DisplayName: "ADB Exporter",
		Description: "Prometheus exporter for Android devices via adb",
		Arguments:   args,
	}, log)
	if err != nil {
		log.Error("service init failed", "err", err)
		os.Exit(1)
	}

	// A control subcommand (install/uninstall/start/stop/restart/status)?
	if cmd := flag.Arg(0); cmd != "" {
		if !service.IsControlCommand(cmd) {
			log.Error("unknown command", "command", cmd, "valid", service.ControlCommands)
			os.Exit(2)
		}
		if err := service.Control(svc, cmd); err != nil {
			log.Error("service control failed", "command", cmd, "err", err)
			os.Exit(1)
		}
		return
	}

	// No subcommand: run the exporter (console or under a service manager).
	if err := service.Run(svc); err != nil {
		log.Error("service run failed", "err", err)
		os.Exit(1)
	}
}

// run performs the startup sequence and blocks until ctx is cancelled, then
// shuts down gracefully: stop poller, wait workers, stop HTTP.
func run(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	log.Info("starting adb_exporter",
		"version", version, "listen", cfg.Listen,
		"poll_interval", cfg.PollInterval, "max_parallel_adb", cfg.MaxParallelADB)

	client := adb.New(cfg.ADBPath, cfg.ADBTimeout, cfg.MaxParallelADB)

	// Validate adb is usable and (idempotently) start its server.
	startupCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	ver, err := client.Version(startupCtx)
	if err != nil {
		cancel()
		return fmt.Errorf("adb not usable (check adb_path): %w", err)
	}
	log.Info("adb detected", "version", firstLine(ver))
	if err := client.StartServer(startupCtx); err != nil {
		log.Warn("adb start-server failed, continuing", "err", err)
	}
	cancel()

	reg := metrics.New()
	p := poller.New(client, reg, log, cfg.PollInterval)
	srv := xhttp.NewServer(cfg.Listen, reg.Gatherer(), p)

	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	pollerDone := make(chan struct{})
	go func() {
		defer close(pollerDone)
		p.Run(runCtx)
	}()

	httpErr := make(chan error, 1)
	go func() {
		log.Info("http server listening", "addr", cfg.Listen)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			httpErr <- err
		}
	}()

	select {
	case <-ctx.Done():
		log.Info("shutdown requested")
	case err := <-httpErr:
		log.Error("http server failed", "err", err)
	}

	// Graceful shutdown in SPEC order: stop poller, wait workers, then stop HTTP.
	cancelRun()
	<-pollerDone

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Warn("http shutdown error", "err", err)
	}
	log.Info("shutdown complete")
	return nil
}

func usage() {
	fmt.Fprintf(os.Stderr, `adb_exporter - Prometheus exporter for Android devices over adb

Usage:
  adb_exporter [flags]              run the exporter (console mode)
  adb_exporter [flags] <command>    manage the OS service

Service commands: %s

Flags:
`, strings.Join(service.ControlCommands, ", "))
	flag.PrintDefaults()
}

func printVersion() {
	fmt.Printf("adb_exporter %s\n", version)
	fmt.Printf("  commit:   %s\n", commit)
	fmt.Printf("  built:    %s\n", date)
	fmt.Printf("  go:       %s\n", runtime.Version())
	fmt.Printf("  platform: %s/%s\n", runtime.GOOS, runtime.GOARCH)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
