package poller

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vvervolv/adb_exporter/internal/adb"
	"github.com/vvervolv/adb_exporter/internal/metrics"
)

// --- fakes ---

type fakeADB struct {
	devices []adb.Device
	devErr  error

	mu      sync.Mutex
	calls   map[string]int
	outputs func(serial string, call int) (string, error)
}

func (f *fakeADB) Devices(context.Context) ([]adb.Device, error) {
	return f.devices, f.devErr
}

func (f *fakeADB) Shell(_ context.Context, serial, _ string) (string, error) {
	f.mu.Lock()
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	call := f.calls[serial]
	f.calls[serial]++
	f.mu.Unlock()
	return f.outputs(serial, call)
}

type fakeSink struct {
	mu        sync.Mutex
	lastApply []metrics.DeviceSnapshot
	applies   int
	overruns  atomic.Int64
	successes int
}

func (s *fakeSink) Apply(d []metrics.DeviceSnapshot) {
	s.mu.Lock()
	s.lastApply = d
	s.applies++
	s.mu.Unlock()
}
func (s *fakeSink) SetPollCycle(_ float64, success bool, _ float64) {
	if success {
		s.mu.Lock()
		s.successes++
		s.mu.Unlock()
	}
}
func (s *fakeSink) IncOverruns() { s.overruns.Add(1) }

func (s *fakeSink) snapshot() []metrics.DeviceSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastApply
}

func statOutput(total, idle uint64) string {
	// cpu user nice system idle iowait ... → total = (total-idle)+idle
	return fmt.Sprintf("cpu %d 0 0 %d 0 0 0 0\n###END###\n", total-idle, idle)
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// --- tests ---

func TestCycleOfflineDeviceNotScraped(t *testing.T) {
	fa := &fakeADB{
		devices: []adb.Device{
			{Serial: "on", State: "device"},
			{Serial: "off", State: "unauthorized"},
		},
		outputs: func(string, int) (string, error) { return statOutput(1000, 800), nil },
	}
	sink := &fakeSink{}
	p := New(fa, sink, discardLogger(), time.Second)

	p.cycle(context.Background())

	if fa.calls["off"] != 0 {
		t.Errorf("offline device must not be scraped, got %d calls", fa.calls["off"])
	}
	if fa.calls["on"] != 1 {
		t.Errorf("online device should be scraped once, got %d", fa.calls["on"])
	}

	snaps := sink.snapshot()
	byid := map[string]metrics.DeviceSnapshot{}
	for _, s := range snaps {
		byid[s.Serial] = s
	}
	if byid["off"].Online || byid["off"].ScrapeSuccess {
		t.Errorf("offline snapshot wrong: %+v", byid["off"])
	}
	if !byid["on"].Online || !byid["on"].ScrapeSuccess {
		t.Errorf("online snapshot wrong: %+v", byid["on"])
	}
}

func TestCPUDeltaAcrossCycles(t *testing.T) {
	fa := &fakeADB{
		devices: []adb.Device{{Serial: "d", State: "device"}},
		outputs: func(_ string, call int) (string, error) {
			// cycle 0: total 1000/idle 800; cycle 1: total 2000/idle 1600.
			if call == 0 {
				return statOutput(1000, 800), nil
			}
			return statOutput(2000, 1600), nil
		},
	}
	sink := &fakeSink{}
	p := New(fa, sink, discardLogger(), time.Second)

	// First cycle: no previous sample → CPU usage unset.
	p.cycle(context.Background())
	if sink.snapshot()[0].Metrics.CPUUsagePercent.Set {
		t.Error("first cycle should not publish CPU usage")
	}

	// Second cycle: delta 200 busy / 1000 total = 20%.
	p.cycle(context.Background())
	cpu := sink.snapshot()[0].Metrics.CPUUsagePercent
	if !cpu.Set || cpu.Value < 19.9 || cpu.Value > 20.1 {
		t.Errorf("second cycle CPU = %+v, want ~20", cpu)
	}
}

func TestScrapeFailureMarksUnsuccessful(t *testing.T) {
	fa := &fakeADB{
		devices: []adb.Device{{Serial: "d", State: "device"}},
		outputs: func(string, int) (string, error) { return "", fmt.Errorf("boom") },
	}
	sink := &fakeSink{}
	p := New(fa, sink, discardLogger(), time.Second)
	p.cycle(context.Background())

	s := sink.snapshot()[0]
	if s.ScrapeSuccess {
		t.Error("failed scrape must have ScrapeSuccess=false")
	}
	if !s.Online {
		t.Error("device is still online even if scrape failed")
	}
}

func TestDevicesErrorIsNotSuccessful(t *testing.T) {
	fa := &fakeADB{devErr: fmt.Errorf("adb down")}
	sink := &fakeSink{}
	p := New(fa, sink, discardLogger(), time.Second)
	p.cycle(context.Background())

	if sink.successes != 0 {
		t.Error("adb devices failure must not count as a successful cycle")
	}
	if p.Ready() {
		t.Error("poller should not be ready after only a failed cycle")
	}
}

func TestReadyAndHealthyWithClock(t *testing.T) {
	fa := &fakeADB{
		devices: []adb.Device{{Serial: "d", State: "device"}},
		outputs: func(string, int) (string, error) { return statOutput(1000, 800), nil },
	}
	sink := &fakeSink{}
	p := New(fa, sink, discardLogger(), 5*time.Second) // healthTimeout = 15s

	base := time.Unix(1_000_000, 0)
	current := base
	p.now = func() time.Time { return current }

	if p.Ready() || p.Healthy() {
		t.Fatal("should not be ready/healthy before first cycle")
	}

	p.cycle(context.Background())
	if !p.Ready() || !p.Healthy() {
		t.Fatal("should be ready and healthy right after a successful cycle")
	}

	// 10s later: still healthy (<15s).
	current = base.Add(10 * time.Second)
	if !p.Healthy() {
		t.Error("should still be healthy at 10s")
	}
	// 20s later: stale → unhealthy, but still ready.
	current = base.Add(20 * time.Second)
	if p.Healthy() {
		t.Error("should be unhealthy at 20s")
	}
	if !p.Ready() {
		t.Error("ready must stay true once set")
	}
}

func TestPruneCPUOnDeviceRemoval(t *testing.T) {
	fa := &fakeADB{
		devices: []adb.Device{{Serial: "d", State: "device"}},
		outputs: func(string, int) (string, error) { return statOutput(1000, 800), nil },
	}
	sink := &fakeSink{}
	p := New(fa, sink, discardLogger(), time.Second)

	p.cycle(context.Background())
	p.mu.Lock()
	_, had := p.prevCPU["d"]
	p.mu.Unlock()
	if !had {
		t.Fatal("prevCPU should hold d after first cycle")
	}

	// Device disappears.
	fa.devices = nil
	p.cycle(context.Background())
	p.mu.Lock()
	_, still := p.prevCPU["d"]
	p.mu.Unlock()
	if still {
		t.Error("prevCPU for removed device should be pruned")
	}
}

func TestRunNonOverlappingCountsOverruns(t *testing.T) {
	release := make(chan struct{})
	var started atomic.Int64
	fa := &fakeADB{
		devices: []adb.Device{{Serial: "d", State: "device"}},
		outputs: func(string, int) (string, error) {
			started.Add(1)
			<-release // block the cycle so ticks pile up
			return statOutput(1000, 800), nil
		},
	}
	sink := &fakeSink{}
	p := New(fa, sink, discardLogger(), 10*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()

	// Let several ticks fire while the first cycle is blocked.
	time.Sleep(80 * time.Millisecond)
	if sink.overruns.Load() == 0 {
		t.Error("expected overruns while the first cycle is blocked")
	}
	if started.Load() != 1 {
		t.Errorf("only one cycle should have started, got %d", started.Load())
	}

	close(release)
	cancel()
	<-done
}
