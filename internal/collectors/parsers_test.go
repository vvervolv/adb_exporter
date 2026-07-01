package collectors

import (
	"math"
	"testing"
)

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestParseProcStatAndUsage(t *testing.T) {
	// cpu  user nice system idle iowait irq softirq steal ...
	prev, ok := parseProcStat("cpu  100 0 100 700 100 0 0 0\ncpu0 ...\n")
	if !ok {
		t.Fatal("prev parse failed")
	}
	// total = 100+0+100+700+100 = 1000, idle = 700+100 = 800
	if prev.Total != 1000 || prev.Idle != 800 {
		t.Fatalf("prev = %+v, want {1000 800}", prev)
	}

	cur, ok := parseProcStat("cpu  200 0 200 1400 200 0 0 0\n")
	if !ok {
		t.Fatal("cur parse failed")
	}
	// dTotal = 2000-1000 = 1000, dIdle = 1600-800 = 800 → busy = 200/1000 = 20%
	pct, ok := CPUUsagePercent(prev, cur)
	if !ok {
		t.Fatal("usage not ok")
	}
	if !approx(pct, 20) {
		t.Fatalf("cpu usage = %v, want 20", pct)
	}
}

func TestCPUUsageNoDelta(t *testing.T) {
	s := CPUStat{Total: 1000, Idle: 800}
	if _, ok := CPUUsagePercent(s, s); ok {
		t.Error("identical samples should yield ok=false")
	}
	// counter reset (cur < prev)
	if _, ok := CPUUsagePercent(CPUStat{Total: 1000}, CPUStat{Total: 10}); ok {
		t.Error("counter reset should yield ok=false")
	}
}

func TestParseMemInfo(t *testing.T) {
	section := `MemTotal:        4000000 kB
MemFree:          500000 kB
MemAvailable:    1000000 kB
Buffers:          100000 kB
`
	var m DeviceMetrics
	parseMemInfo(section, &m)
	if !m.MemTotalBytes.Set || !approx(m.MemTotalBytes.Value, 4000000*1024) {
		t.Errorf("total = %+v", m.MemTotalBytes)
	}
	if !m.MemAvailableBytes.Set || !approx(m.MemAvailableBytes.Value, 1000000*1024) {
		t.Errorf("available = %+v", m.MemAvailableBytes)
	}
	wantUsed := float64(4000000-1000000) * 1024
	if !approx(m.MemUsedBytes.Value, wantUsed) {
		t.Errorf("used = %v, want %v", m.MemUsedBytes.Value, wantUsed)
	}
	if !approx(m.MemUsedPercent.Value, 75) {
		t.Errorf("used_percent = %v, want 75", m.MemUsedPercent.Value)
	}
}

func TestParseBattery(t *testing.T) {
	section := `Current Battery Service state:
  AC powered: false
  level: 87
  status: 3
  temperature: 305
  voltage: 4123
`
	var m DeviceMetrics
	parseBattery(section, &m)
	if !approx(m.BatteryLevel.Value, 87) {
		t.Errorf("level = %v", m.BatteryLevel.Value)
	}
	if !approx(m.BatteryStatus.Value, 3) {
		t.Errorf("status = %v", m.BatteryStatus.Value)
	}
	if !approx(m.BatteryTemperatureCelsius.Value, 30.5) {
		t.Errorf("temperature = %v, want 30.5", m.BatteryTemperatureCelsius.Value)
	}
	if !approx(m.BatteryVoltageVolts.Value, 4.123) {
		t.Errorf("voltage = %v, want 4.123", m.BatteryVoltageVolts.Value)
	}
}

func TestParseUptime(t *testing.T) {
	var m DeviceMetrics
	parseUptime("12345.67 98765.43\n", &m)
	if !m.UptimeSeconds.Set || !approx(m.UptimeSeconds.Value, 12345.67) {
		t.Errorf("uptime = %+v", m.UptimeSeconds)
	}

	var empty DeviceMetrics
	parseUptime("", &empty)
	if empty.UptimeSeconds.Set {
		t.Error("empty uptime should be unset")
	}
}

func TestParseDF(t *testing.T) {
	section := `Filesystem     1K-blocks     Used Available Use% Mounted on
/dev/block/dm-5 100000000 40000000  60000000  40% /
`
	var m DeviceMetrics
	parseDF(section, &m)
	if !approx(m.StorageTotalBytes.Value, 100000000*1024) {
		t.Errorf("total = %v", m.StorageTotalBytes.Value)
	}
	if !approx(m.StorageUsedBytes.Value, 40000000*1024) {
		t.Errorf("used = %v", m.StorageUsedBytes.Value)
	}
	if !approx(m.StorageFreeBytes.Value, 60000000*1024) {
		t.Errorf("free = %v", m.StorageFreeBytes.Value)
	}
}

func TestParseDFWrappedLine(t *testing.T) {
	// A long filesystem name wraps onto its own line before the numbers.
	section := `Filesystem     1K-blocks     Used Available Use% Mounted on
/dev/block/mapper/very-long-name-that-wraps
                100000000 40000000  60000000  40% /
`
	var m DeviceMetrics
	parseDF(section, &m)
	if !approx(m.StorageTotalBytes.Value, 100000000*1024) {
		t.Errorf("wrapped total = %v, want %v", m.StorageTotalBytes.Value, 100000000.0*1024)
	}
	if !approx(m.StorageFreeBytes.Value, 60000000*1024) {
		t.Errorf("wrapped free = %v", m.StorageFreeBytes.Value)
	}
}

func TestParseThermal(t *testing.T) {
	var m DeviceMetrics
	parseThermal("48000\n52000\n41000\n", &m)
	if !approx(m.ThermalMaxTemperatureCelsius.Value, 52) {
		t.Errorf("max thermal = %v, want 52", m.ThermalMaxTemperatureCelsius.Value)
	}

	// Already-degrees values (< 1000) are used as-is.
	var m2 DeviceMetrics
	parseThermal("45\n50\n", &m2)
	if !approx(m2.ThermalMaxTemperatureCelsius.Value, 50) {
		t.Errorf("degrees thermal = %v, want 50", m2.ThermalMaxTemperatureCelsius.Value)
	}

	var empty DeviceMetrics
	parseThermal("", &empty)
	if empty.ThermalMaxTemperatureCelsius.Set {
		t.Error("empty thermal should be unset")
	}
}

func TestParsePowerScreenOn(t *testing.T) {
	tests := []struct {
		name    string
		section string
		want    float64
		set     bool
	}{
		{"display on", "Display Power: state=ON\n", 1, true},
		{"display off", "Display Power: state=OFF\n", 0, true},
		{"wakefulness awake", "mWakefulness=Awake\n", 1, true},
		{"wakefulness asleep", "mWakefulness=Asleep\n", 0, true},
		{"mScreenOn true", "mScreenOn=true\n", 1, true},
		{"mScreenOn false", "mScreenOn=false\n", 0, true},
		{"none", "some unrelated dump\n", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m DeviceMetrics
			parsePower(tt.section, &m)
			if m.ScreenOn.Set != tt.set {
				t.Fatalf("set = %v, want %v", m.ScreenOn.Set, tt.set)
			}
			if tt.set && !approx(m.ScreenOn.Value, tt.want) {
				t.Fatalf("screen_on = %v, want %v", m.ScreenOn.Value, tt.want)
			}
		})
	}
}

func TestParsePowerPriority(t *testing.T) {
	// Display Power should win even if mScreenOn disagrees.
	section := "mScreenOn=false\nDisplay Power: state=ON\n"
	var m DeviceMetrics
	parsePower(section, &m)
	if !approx(m.ScreenOn.Value, 1) {
		t.Errorf("Display Power should take priority, got %v", m.ScreenOn.Value)
	}
}

func TestParseFull(t *testing.T) {
	output := "cpu  200 0 200 1400 200 0 0 0\n" +
		"cpu0 100 0 100 700 100 0 0 0\n" +
		markerMem + "\n" +
		"MemTotal:        4000000 kB\n" +
		"MemAvailable:    1000000 kB\n" +
		markerBattery + "\n" +
		"  level: 50\n  status: 2\n  temperature: 280\n  voltage: 3900\n" +
		markerUptime + "\n" +
		"5000.25 10000.5\n" +
		markerDF + "\n" +
		"Filesystem     1K-blocks     Used Available Use% Mounted on\n" +
		"/dev/block/dm-5 100000000 40000000  60000000  40% /\n" +
		markerPower + "\n" +
		"Display Power: state=ON\n" +
		markerThermal + "\n" +
		"40000\n45000\n" +
		markerEnd + "\n"
	s := Parse(output)
	if !s.CPUOK || s.CPU.Total != 2000 {
		t.Errorf("cpu = %+v ok=%v", s.CPU, s.CPUOK)
	}
	m := s.Metrics
	if !approx(m.MemTotalBytes.Value, 4000000*1024) {
		t.Errorf("mem total = %v", m.MemTotalBytes.Value)
	}
	if !approx(m.BatteryLevel.Value, 50) || !approx(m.BatteryTemperatureCelsius.Value, 28) {
		t.Errorf("battery = %+v", m)
	}
	if !approx(m.UptimeSeconds.Value, 5000.25) {
		t.Errorf("uptime = %v", m.UptimeSeconds.Value)
	}
	if !approx(m.StorageTotalBytes.Value, 100000000*1024) {
		t.Errorf("storage total = %v", m.StorageTotalBytes.Value)
	}
	if !approx(m.ScreenOn.Value, 1) {
		t.Errorf("screen = %v", m.ScreenOn.Value)
	}
	if !approx(m.ThermalMaxTemperatureCelsius.Value, 45) {
		t.Errorf("thermal = %v", m.ThermalMaxTemperatureCelsius.Value)
	}
}

func TestParseMissingSections(t *testing.T) {
	// Only CPU + END; every other section absent → their metrics stay unset.
	output := "cpu  100 0 100 700 100 0 0 0\n" + markerEnd + "\n"
	s := Parse(output)
	if !s.CPUOK {
		t.Error("cpu should parse")
	}
	if s.Metrics.MemTotalBytes.Set || s.Metrics.BatteryLevel.Set || s.Metrics.ScreenOn.Set {
		t.Error("absent sections must leave metrics unset")
	}
}
