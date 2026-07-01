// Package service adapts the exporter to run either in the console or as a
// managed OS service (Windows Service, systemd, launchd) via kardianos/service.
package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/kardianos/service"
)

// App is the exporter's run body. Run must block until ctx is cancelled and
// then return after completing a graceful shutdown.
type App struct {
	Run func(ctx context.Context) error
}

// Options configures the OS service registration.
type Options struct {
	Name        string
	DisplayName string
	Description string
	// Arguments the service manager passes to the binary when starting it.
	Arguments []string
}

// ControlCommands are the supported service subcommands (SPEC §Service mode).
var ControlCommands = []string{"install", "uninstall", "start", "stop", "restart", "status"}

// stopTimeout bounds how long Stop waits for graceful shutdown.
const stopTimeout = 15 * time.Second

type program struct {
	app    App
	log    *slog.Logger
	cancel context.CancelFunc
	done   chan struct{}
}

func (p *program) Start(service.Service) error {
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.done = make(chan struct{})
	go func() {
		defer close(p.done)
		if err := p.app.Run(ctx); err != nil {
			p.log.Error("exporter run exited with error", "err", err)
		}
	}()
	return nil
}

func (p *program) Stop(service.Service) error {
	if p.cancel != nil {
		p.cancel()
	}
	select {
	case <-p.done:
	case <-time.After(stopTimeout):
		p.log.Warn("graceful shutdown timed out", "timeout", stopTimeout)
	}
	return nil
}

// New builds a kardianos service around the app.
func New(app App, opts Options, log *slog.Logger) (service.Service, error) {
	if log == nil {
		log = slog.Default()
	}
	cfg := &service.Config{
		Name:        opts.Name,
		DisplayName: opts.DisplayName,
		Description: opts.Description,
		Arguments:   opts.Arguments,
	}
	return service.New(&program{app: app, log: log}, cfg)
}

// Control runs a service control subcommand (install/uninstall/start/...).
func Control(svc service.Service, cmd string) error {
	if !IsControlCommand(cmd) {
		return fmt.Errorf("unknown service command %q (valid: %v)", cmd, ControlCommands)
	}
	if cmd == "status" {
		status, err := svc.Status()
		if err != nil {
			return err
		}
		fmt.Println(statusString(status))
		return nil
	}
	return service.Control(svc, cmd)
}

// IsControlCommand reports whether s is a recognised service subcommand.
func IsControlCommand(s string) bool {
	for _, c := range ControlCommands {
		if c == s {
			return true
		}
	}
	return false
}

func statusString(s service.Status) string {
	switch s {
	case service.StatusRunning:
		return "running"
	case service.StatusStopped:
		return "stopped"
	default:
		return "unknown"
	}
}

// Run starts the service (console or managed). It blocks until stopped.
func Run(svc service.Service) error { return svc.Run() }
