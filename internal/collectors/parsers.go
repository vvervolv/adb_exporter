package collectors

import (
	"strconv"
	"strings"
)

// CPUUsagePercent computes CPU utilisation from two /proc/stat samples.
// Returns ok=false if the delta is non-positive (e.g. counters reset or the
// samples are identical), in which case no value should be published.
func CPUUsagePercent(prev, cur CPUStat) (float64, bool) {
	if cur.Total <= prev.Total {
		return 0, false
	}
	dTotal := cur.Total - prev.Total
	dIdle := cur.Idle - prev.Idle
	if cur.Idle < prev.Idle {
		dIdle = 0
	}
	busy := float64(dTotal-dIdle) / float64(dTotal) * 100
	if busy < 0 {
		busy = 0
	}
	if busy > 100 {
		busy = 100
	}
	return busy, true
}

// parseProcStat reads the aggregate "cpu" line of /proc/stat.
//
//	cpu  user nice system idle iowait irq softirq steal guest guest_nice
//
// idle = idle + iowait; total = sum of all fields.
func parseProcStat(section string) (CPUStat, bool) {
	for _, line := range strings.Split(section, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || fields[0] != "cpu" {
			continue
		}
		var total, idle uint64
		for i, f := range fields[1:] {
			v, err := strconv.ParseUint(f, 10, 64)
			if err != nil {
				return CPUStat{}, false
			}
			total += v
			// index 3 = idle, index 4 = iowait (0-based within fields[1:])
			if i == 3 || i == 4 {
				idle += v
			}
		}
		return CPUStat{Total: total, Idle: idle}, true
	}
	return CPUStat{}, false
}

// parseMemInfo reads /proc/meminfo (values are in kB).
func parseMemInfo(section string, m *DeviceMetrics) {
	var total, avail float64
	var haveTotal, haveAvail bool
	for _, line := range strings.Split(section, "\n") {
		key, valKB, ok := memLine(line)
		if !ok {
			continue
		}
		switch key {
		case "MemTotal":
			total, haveTotal = valKB*1024, true
		case "MemAvailable":
			avail, haveAvail = valKB*1024, true
		}
	}
	if haveTotal {
		m.MemTotalBytes = opt(total)
	}
	if haveAvail {
		m.MemAvailableBytes = opt(avail)
	}
	if haveTotal && haveAvail {
		used := total - avail
		m.MemUsedBytes = opt(used)
		if total > 0 {
			m.MemUsedPercent = opt(used / total * 100)
		}
	}
}

// memLine parses "Key:   12345 kB" into ("Key", 12345, true).
func memLine(line string) (string, float64, bool) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return "", 0, false
	}
	key := strings.TrimSuffix(fields[0], ":")
	v, err := strconv.ParseFloat(fields[1], 64)
	if err != nil {
		return "", 0, false
	}
	return key, v, true
}

// parseBattery reads `dumpsys battery` output. Units are normalised: temperature
// tenths-of-°C ÷ 10, voltage mV ÷ 1000. status is the raw numeric code.
func parseBattery(section string, m *DeviceMetrics) {
	for _, line := range strings.Split(section, "\n") {
		key, val, ok := colonNumber(line)
		if !ok {
			continue
		}
		switch key {
		case "level":
			m.BatteryLevel = opt(val)
		case "status":
			m.BatteryStatus = opt(val)
		case "temperature":
			m.BatteryTemperatureCelsius = opt(val / 10)
		case "voltage":
			m.BatteryVoltageVolts = opt(val / 1000)
		}
	}
}

// colonNumber parses a "  key: 123" style line into ("key", 123, true).
func colonNumber(line string) (string, float64, bool) {
	line = strings.TrimSpace(line)
	idx := strings.Index(line, ":")
	if idx < 0 {
		return "", 0, false
	}
	key := strings.TrimSpace(line[:idx])
	valStr := strings.TrimSpace(line[idx+1:])
	v, err := strconv.ParseFloat(valStr, 64)
	if err != nil {
		return "", 0, false
	}
	return key, v, true
}

// parseUptime reads /proc/uptime: "<uptime_seconds> <idle_seconds>".
func parseUptime(section string, m *DeviceMetrics) {
	fields := strings.Fields(section)
	if len(fields) == 0 {
		return
	}
	if v, err := strconv.ParseFloat(fields[0], 64); err == nil {
		m.UptimeSeconds = opt(v)
	}
}

// parseDF reads `df -k /data` (1K-blocks). It joins all non-header data fields so
// a filesystem name that wraps onto a second line is handled, then reads the
// numeric columns from the end: ... <1K-blocks> <Used> <Available> <Use%> <Mount>.
//
// /data (userdata) is used rather than / because on Android / is a read-only
// system root that is always ~100% full and carries no useful capacity signal.
func parseDF(section string, m *DeviceMetrics) {
	var fields []string
	for _, line := range strings.Split(section, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.Contains(line, "Filesystem") || strings.Contains(line, "1K-blocks") {
			continue // header
		}
		fields = append(fields, strings.Fields(line)...)
	}
	// Need at least: blocks, used, avail, use%, mount → 5 trailing tokens.
	if len(fields) < 5 {
		return
	}
	n := len(fields)
	blocks, err1 := strconv.ParseFloat(fields[n-5], 64)
	used, err2 := strconv.ParseFloat(fields[n-4], 64)
	avail, err3 := strconv.ParseFloat(fields[n-3], 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return
	}
	m.StorageTotalBytes = opt(blocks * 1024)
	m.StorageUsedBytes = opt(used * 1024)
	m.StorageFreeBytes = opt(avail * 1024)
}

// parsePower determines screen_on from `dumpsys power`, checking fields in
// priority order (SPEC §Section parsing rules).
func parsePower(section string, m *DeviceMetrics) {
	// 1) Display Power: state=ON|OFF
	if idx := strings.Index(section, "Display Power: state="); idx >= 0 {
		rest := section[idx+len("Display Power: state="):]
		token := firstToken(rest)
		switch token {
		case "ON":
			m.ScreenOn = opt(1)
			return
		case "OFF":
			m.ScreenOn = opt(0)
			return
		}
	}
	// 2) mWakefulness=Awake|Asleep|Dozing
	if idx := strings.Index(section, "mWakefulness="); idx >= 0 {
		token := firstToken(section[idx+len("mWakefulness="):])
		if token == "Awake" {
			m.ScreenOn = opt(1)
		} else {
			m.ScreenOn = opt(0)
		}
		return
	}
	// 3) mScreenOn=true|false
	if idx := strings.Index(section, "mScreenOn="); idx >= 0 {
		token := firstToken(section[idx+len("mScreenOn="):])
		if token == "true" {
			m.ScreenOn = opt(1)
		} else {
			m.ScreenOn = opt(0)
		}
		return
	}
	// none matched → leave unset
}

// firstToken returns the leading run of non-space, non-newline characters.
func firstToken(s string) string {
	s = strings.TrimLeft(s, " \t")
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case ' ', '\t', '\n', '\r':
			return s[:i]
		}
	}
	return s
}

// parseThermal reads thermal_zone*/temp values (conventionally millidegrees C)
// and publishes the maximum. Values < 1000 are treated as already-degrees.
func parseThermal(section string, m *DeviceMetrics) {
	var max float64
	found := false
	for _, line := range strings.Split(section, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		raw, err := strconv.ParseFloat(line, 64)
		if err != nil {
			continue
		}
		c := raw
		if raw >= 1000 {
			c = raw / 1000
		}
		if !found || c > max {
			max = c
			found = true
		}
	}
	if found {
		m.ThermalMaxTemperatureCelsius = opt(max)
	}
}
