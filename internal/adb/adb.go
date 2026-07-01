// Package adb wraps the external adb executable. It never installs anything on
// devices; all communication is through adb subprocesses.
package adb

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Client runs adb commands with a bounded number of concurrent processes.
//
// The worker-pool semaphore only gates per-device Shell calls (the many,
// potentially-slow invocations). One-off control commands (Version,
// StartServer, Devices) run outside the pool.
type Client struct {
	path    string
	timeout time.Duration
	sem     chan struct{}
}

// New returns a Client. maxParallel must be >= 1 (validated by config).
func New(path string, timeout time.Duration, maxParallel int) *Client {
	if maxParallel < 1 {
		maxParallel = 1
	}
	return &Client{
		path:    path,
		timeout: timeout,
		sem:     make(chan struct{}, maxParallel),
	}
}

// Device is one entry from `adb devices`.
type Device struct {
	Serial string
	State  string // raw state token(s): "device", "offline", "unauthorized", ...
}

// Online reports whether the device is fully available for scraping.
func (d Device) Online() bool { return d.State == "device" }

// run executes adb with the given args under a fresh timeout context.
func (c *Client) run(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, c.path, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("adb %s: timed out after %s", strings.Join(args, " "), c.timeout)
	}
	if err != nil {
		return stdout.String(), fmt.Errorf("adb %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// Version runs `adb version` to confirm adb is present and executable.
func (c *Client) Version(ctx context.Context) (string, error) {
	out, err := c.run(ctx, "version")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// StartServer runs `adb start-server`. It is idempotent (SPEC §Startup).
func (c *Client) StartServer(ctx context.Context) error {
	_, err := c.run(ctx, "start-server")
	return err
}

// Devices runs `adb devices` and parses the result. Discovery is based only on
// this command (never adb track-devices).
func (c *Client) Devices(ctx context.Context) ([]Device, error) {
	out, err := c.run(ctx, "devices")
	if err != nil {
		return nil, err
	}
	return ParseDevices(out), nil
}

// Shell runs a single `adb -s <serial> shell "<command>"` bounded by the worker
// pool and per-call timeout. command is passed as ONE argument — no host shell
// is involved, so it behaves identically across platforms (SPEC §Single adb shell).
func (c *Client) Shell(ctx context.Context, serial, command string) (string, error) {
	select {
	case c.sem <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-c.sem }()

	return c.run(ctx, "-s", serial, "shell", command)
}

// ParseDevices parses `adb devices` output into a slice of Devices.
//
// It skips the header line and any daemon status lines ("* daemon ..."). The
// serial is the first field; the remaining fields are joined as the state so
// multi-word states like "no permissions" are preserved.
func ParseDevices(out string) []Device {
	var devices []Device
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "*") {
			continue // daemon messages
		}
		if strings.HasPrefix(line, "List of devices") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue // malformed / incomplete line
		}
		devices = append(devices, Device{
			Serial: fields[0],
			State:  strings.Join(fields[1:], " "),
		})
	}
	return devices
}
