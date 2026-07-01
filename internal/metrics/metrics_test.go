package metrics

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/vvervolv/adb_exporter/internal/collectors"
)

func TestApplySetsAndDeletesRemovedDevice(t *testing.T) {
	r := New()

	// Cycle 1: two devices online with a few metrics.
	r.Apply([]DeviceSnapshot{
		{
			Serial: "dev-a", Online: true, ScrapeSuccess: true, ScrapeDuration: 0.2,
			Metrics: collectors.DeviceMetrics{
				CPUUsagePercent: collectors.OptFloat{Value: 42, Set: true},
				BatteryLevel:    collectors.OptFloat{Value: 90, Set: true},
			},
		},
		{
			Serial: "dev-b", Online: false, ScrapeSuccess: false, ScrapeDuration: 0,
		},
	})

	if got := testutil.ToFloat64(r.cpuUsage.WithLabelValues("dev-a")); got != 42 {
		t.Errorf("dev-a cpu = %v, want 42", got)
	}
	if got := testutil.ToFloat64(r.online.WithLabelValues("dev-b")); got != 0 {
		t.Errorf("dev-b online = %v, want 0", got)
	}
	if got := testutil.ToFloat64(r.devicesTotal); got != 2 {
		t.Errorf("devices_total = %v, want 2", got)
	}

	// Cycle 2: dev-b disappears entirely → its series must be gone.
	r.Apply([]DeviceSnapshot{
		{
			Serial: "dev-a", Online: true, ScrapeSuccess: true, ScrapeDuration: 0.1,
			Metrics: collectors.DeviceMetrics{
				CPUUsagePercent: collectors.OptFloat{Value: 10, Set: true},
			},
		},
	})

	out := gather(t, r)
	if strings.Contains(out, `device="dev-b"`) {
		t.Errorf("removed device dev-b should have no series, got:\n%s", out)
	}
	if !strings.Contains(out, `android_farm_cpu_usage_percent{device="dev-a"} 10`) {
		t.Errorf("dev-a cpu should be updated to 10, got:\n%s", out)
	}
	if got := testutil.ToFloat64(r.devicesTotal); got != 1 {
		t.Errorf("devices_total = %v, want 1", got)
	}
}

func TestApplyDeletesUnsetOptionalMetric(t *testing.T) {
	r := New()

	r.Apply([]DeviceSnapshot{{
		Serial: "dev-a", Online: true, ScrapeSuccess: true,
		Metrics: collectors.DeviceMetrics{
			ScreenOn: collectors.OptFloat{Value: 1, Set: true},
		},
	}})
	if !strings.Contains(gather(t, r), `android_farm_screen_on{device="dev-a"} 1`) {
		t.Fatal("screen_on should be present in cycle 1")
	}

	// Cycle 2: device still online but screen_on unparseable this time → unset.
	r.Apply([]DeviceSnapshot{{
		Serial: "dev-a", Online: true, ScrapeSuccess: true,
		Metrics: collectors.DeviceMetrics{},
	}})
	if strings.Contains(gather(t, r), "android_farm_screen_on") {
		t.Error("unset screen_on should be deleted, not kept stale")
	}
}

func TestPollCycleMetrics(t *testing.T) {
	r := New()
	r.SetPollCycle(1.5, true, 1000)
	r.IncOverruns()
	r.IncOverruns()

	if got := testutil.ToFloat64(r.pollDuration); got != 1.5 {
		t.Errorf("poll duration = %v, want 1.5", got)
	}
	if got := testutil.ToFloat64(r.pollLastSuccess); got != 1000 {
		t.Errorf("last success = %v, want 1000", got)
	}
	if got := testutil.ToFloat64(r.pollOverruns); got != 2 {
		t.Errorf("overruns = %v, want 2", got)
	}

	// A failed cycle must NOT advance the last-success timestamp.
	r.SetPollCycle(0.5, false, 2000)
	if got := testutil.ToFloat64(r.pollLastSuccess); got != 1000 {
		t.Errorf("last success after failed cycle = %v, want 1000", got)
	}
}

// gather renders the registry in a minimal text format sufficient for these
// assertions: "name{label="v",...} value" per line.
func gather(t *testing.T, r *Registry) string {
	t.Helper()
	mfs, err := r.Gatherer().Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	var sb strings.Builder
	for _, mf := range mfs {
		for _, m := range mf.GetMetric() {
			var labels []string
			for _, l := range m.GetLabel() {
				labels = append(labels, fmt.Sprintf("%s=%q", l.GetName(), l.GetValue()))
			}
			sort.Strings(labels)
			var val float64
			switch {
			case m.GetGauge() != nil:
				val = m.GetGauge().GetValue()
			case m.GetCounter() != nil:
				val = m.GetCounter().GetValue()
			}
			labelStr := ""
			if len(labels) > 0 {
				labelStr = "{" + strings.Join(labels, ",") + "}"
			}
			fmt.Fprintf(&sb, "%s%s %s\n", mf.GetName(), labelStr, strconv.FormatFloat(val, 'g', -1, 64))
		}
	}
	return sb.String()
}
