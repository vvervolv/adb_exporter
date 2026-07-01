// Package collectors turns the single per-device adb shell output into a set of
// parsed metric values. Parsing is best-effort: a missing or malformed section
// only leaves its own metrics unset and never fails the whole device.
package collectors

import "strings"

// Separator markers emitted between sections by ShellCommand. They are printed
// on their own line by `echo`.
const (
	markerMem     = "###MEM###"
	markerBattery = "###BATTERY###"
	markerUptime  = "###UPTIME###"
	markerDF      = "###DF###"
	markerPower   = "###POWER###"
	markerThermal = "###THERMAL###"
	markerEnd     = "###END###"
)

// ShellCommand is the single one-line command executed per device per cycle.
// It is passed to `adb -s <serial> shell "<ShellCommand>"` as one argument;
// no host shell is involved (SPEC §Single adb shell). Sections are separated by
// bare markers and terminated by ###END###.
const ShellCommand = "cat /proc/stat" +
	"; echo " + markerMem + "; cat /proc/meminfo" +
	"; echo " + markerBattery + "; dumpsys battery" +
	"; echo " + markerUptime + "; cat /proc/uptime" +
	"; echo " + markerDF + "; df -k /" +
	"; echo " + markerPower + "; dumpsys power" +
	"; echo " + markerThermal + "; cat /sys/class/thermal/thermal_zone*/temp 2>/dev/null" +
	"; echo " + markerEnd

// OptFloat is an optional metric value. Set=false means "no value" — the metric
// series should be deleted rather than published with a stale number.
type OptFloat struct {
	Value float64
	Set   bool
}

func opt(v float64) OptFloat { return OptFloat{Value: v, Set: true} }

// DeviceMetrics holds every parsed value for one device. CPUUsagePercent is
// filled by the poller from a /proc/stat delta, not by Parse.
type DeviceMetrics struct {
	CPUUsagePercent OptFloat

	MemTotalBytes     OptFloat
	MemAvailableBytes OptFloat
	MemUsedBytes      OptFloat
	MemUsedPercent    OptFloat

	StorageTotalBytes OptFloat
	StorageFreeBytes  OptFloat
	StorageUsedBytes  OptFloat

	BatteryLevel              OptFloat
	BatteryStatus             OptFloat
	BatteryTemperatureCelsius OptFloat
	BatteryVoltageVolts       OptFloat

	ThermalMaxTemperatureCelsius OptFloat

	ScreenOn OptFloat

	UptimeSeconds OptFloat
}

// CPUStat is a raw /proc/stat aggregate cpu sample. CPU usage is the delta of
// two of these (SPEC §Per-device state across cycles).
type CPUStat struct {
	Total uint64
	Idle  uint64
}

// Sample is the full result of parsing one device's shell output.
type Sample struct {
	Metrics DeviceMetrics
	CPU     CPUStat
	CPUOK   bool // whether /proc/stat parsed into a usable sample
}

// Parse splits the batch output by markers and parses each section
// independently. CPU percent is not computed here (needs the previous sample).
func Parse(output string) Sample {
	sections := splitSections(output)

	var s Sample
	if cpu, ok := parseProcStat(sections["cpu"]); ok {
		s.CPU = cpu
		s.CPUOK = true
	}

	m := &s.Metrics
	parseMemInfo(sections["mem"], m)
	parseBattery(sections["battery"], m)
	parseUptime(sections["uptime"], m)
	parseDF(sections["df"], m)
	parsePower(sections["power"], m)
	parseThermal(sections["thermal"], m)
	return s
}

// splitSections partitions the raw output into named sections using the markers.
// The text before the first marker is the "cpu" section.
func splitSections(output string) map[string]string {
	order := []struct {
		marker string
		key    string
	}{
		{markerMem, "mem"},
		{markerBattery, "battery"},
		{markerUptime, "uptime"},
		{markerDF, "df"},
		{markerPower, "power"},
		{markerThermal, "thermal"},
		{markerEnd, ""},
	}

	sections := make(map[string]string)
	current := "cpu"
	var buf strings.Builder

	flush := func(next string) {
		sections[current] = buf.String()
		buf.Reset()
		current = next
	}

	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		matched := false
		for _, o := range order {
			if trimmed == o.marker {
				flush(o.key)
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		buf.WriteString(line)
		buf.WriteByte('\n')
	}
	// Flush whatever trailing section remains (e.g. thermal before ###END###
	// was already flushed; this catches output with no ###END###).
	if current != "" {
		sections[current] = buf.String()
	}
	return sections
}
