// Package poller runs the periodic adb polling loop. Cycles never overlap and
// the results are published to the metrics registry via an atomic Apply. HTTP
// endpoints only read the derived health state here — they never poll adb.
package poller

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vvervolv/adb_exporter/internal/adb"
	"github.com/vvervolv/adb_exporter/internal/collectors"
	"github.com/vvervolv/adb_exporter/internal/metrics"
)

// ADB is the subset of the adb client the poller needs (interface for testing).
type ADB interface {
	Devices(ctx context.Context) ([]adb.Device, error)
	Shell(ctx context.Context, serial, command string) (string, error)
}

// Sink receives published snapshots and cycle bookkeeping.
type Sink interface {
	Apply(devices []metrics.DeviceSnapshot)
	SetPollCycle(durationSeconds float64, success bool, unixTimestamp float64)
	IncOverruns()
}

// Poller polls devices on an interval and publishes metrics.
type Poller struct {
	adb      ADB
	sink     Sink
	log      *slog.Logger
	interval time.Duration

	// healthTimeout is how stale the last success may be before /health fails.
	healthTimeout time.Duration

	// now is injectable for tests; defaults to time.Now.
	now func() time.Time

	mu      sync.Mutex
	prevCPU map[string]collectors.CPUStat

	lastSuccessUnixNano atomic.Int64
	ready               atomic.Bool
}

// New creates a Poller. interval is the minimum spacing between cycles.
func New(client ADB, sink Sink, log *slog.Logger, interval time.Duration) *Poller {
	if log == nil {
		log = slog.Default()
	}
	return &Poller{
		adb:           client,
		sink:          sink,
		log:           log,
		interval:      interval,
		healthTimeout: 3 * interval, // SPEC: 15s at the default 5s interval
		now:           time.Now,
		prevCPU:       make(map[string]collectors.CPUStat),
	}
}

// Run executes the polling loop until ctx is cancelled. It runs an immediate
// first cycle, then one per tick, skipping ticks that arrive while a cycle is
// still running (counted as overruns). It returns after any in-flight cycle
// completes (graceful shutdown: wait workers).
func (p *Poller) Run(ctx context.Context) {
	var wg sync.WaitGroup
	var busy atomic.Bool

	runCycle := func() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer busy.Store(false)
			p.cycle(ctx)
		}()
	}

	// Immediate first cycle so /ready flips as soon as possible.
	busy.Store(true)
	runCycle()

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			p.log.Info("poller stopped")
			return
		case <-ticker.C:
			if !busy.CompareAndSwap(false, true) {
				p.sink.IncOverruns()
				p.log.Warn("poll cycle overrun: previous cycle still running, skipping tick")
				continue
			}
			runCycle()
		}
	}
}

// cycle performs one full poll: discover devices, scrape online ones in
// parallel (bounded by the adb worker pool), and publish an atomic snapshot.
func (p *Poller) cycle(ctx context.Context) {
	start := p.now()

	devices, err := p.adb.Devices(ctx)
	if err != nil {
		p.sink.SetPollCycle(p.now().Sub(start).Seconds(), false, 0)
		p.log.Error("adb devices failed", "err", err)
		return
	}

	snaps := make([]metrics.DeviceSnapshot, len(devices))
	var wg sync.WaitGroup
	for i := range devices {
		d := devices[i]
		if !d.Online() {
			snaps[i] = metrics.DeviceSnapshot{Serial: d.Serial, Online: false, ScrapeSuccess: false}
			continue
		}
		wg.Add(1)
		go func(idx int, serial string) {
			defer wg.Done()
			snaps[idx] = p.scrapeDevice(ctx, serial)
		}(i, d.Serial)
	}
	wg.Wait()

	p.pruneCPU(devices)
	p.sink.Apply(snaps)

	now := p.now()
	p.lastSuccessUnixNano.Store(now.UnixNano())
	p.ready.Store(true)
	p.sink.SetPollCycle(now.Sub(start).Seconds(), true, float64(now.Unix()))

	online := 0
	for _, d := range devices {
		if d.Online() {
			online++
		}
	}
	p.log.Info("poll cycle complete",
		"devices", len(devices), "online", online,
		"duration_ms", now.Sub(start).Milliseconds())
}

// scrapeDevice runs the single batch shell for one online device and parses it.
func (p *Poller) scrapeDevice(ctx context.Context, serial string) metrics.DeviceSnapshot {
	start := p.now()
	out, err := p.adb.Shell(ctx, serial, collectors.ShellCommand)
	dur := p.now().Sub(start).Seconds()

	snap := metrics.DeviceSnapshot{Serial: serial, Online: true, ScrapeDuration: dur}
	if err != nil {
		snap.ScrapeSuccess = false
		p.log.Warn("device scrape failed", "device", serial, "err", err)
		return snap
	}

	sample := collectors.Parse(out)
	snap.ScrapeSuccess = true
	snap.Metrics = sample.Metrics

	if sample.CPUOK {
		p.mu.Lock()
		if prev, ok := p.prevCPU[serial]; ok {
			if pct, ok := collectors.CPUUsagePercent(prev, sample.CPU); ok {
				snap.Metrics.CPUUsagePercent = collectors.OptFloat{Value: pct, Set: true}
			}
		}
		p.prevCPU[serial] = sample.CPU
		p.mu.Unlock()
	}
	return snap
}

// pruneCPU drops previous-sample state for devices no longer present.
func (p *Poller) pruneCPU(devices []adb.Device) {
	present := make(map[string]struct{}, len(devices))
	for _, d := range devices {
		present[d.Serial] = struct{}{}
	}
	p.mu.Lock()
	for serial := range p.prevCPU {
		if _, ok := present[serial]; !ok {
			delete(p.prevCPU, serial)
		}
	}
	p.mu.Unlock()
}

// Ready reports whether at least one poll cycle has completed successfully.
func (p *Poller) Ready() bool { return p.ready.Load() }

// Healthy reports whether the last successful poll is recent enough.
func (p *Poller) Healthy() bool {
	if !p.ready.Load() {
		return false
	}
	last := time.Unix(0, p.lastSuccessUnixNano.Load())
	return p.now().Sub(last) < p.healthTimeout
}
