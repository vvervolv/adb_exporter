// Package metrics owns the Prometheus GaugeVecs. The set of collectors is fixed
// at construction; runtime updates only Set values or Delete label series
// (SPEC §Prometheus). No HTTP request ever touches this beyond gathering.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/vvervolv/adb_exporter/internal/collectors"
)

const namespace = "android_farm"

// DeviceSnapshot is the per-device result the poller hands to Apply.
type DeviceSnapshot struct {
	Serial         string
	Online         bool
	ScrapeSuccess  bool
	ScrapeDuration float64 // seconds
	Metrics        collectors.DeviceMetrics
}

// Registry holds every metric and the underlying Prometheus registry.
type Registry struct {
	reg *prometheus.Registry

	// Per-device metrics (label: device).
	online         *prometheus.GaugeVec
	scrapeSuccess  *prometheus.GaugeVec
	scrapeDuration *prometheus.GaugeVec

	cpuUsage *prometheus.GaugeVec

	memTotal     *prometheus.GaugeVec
	memAvailable *prometheus.GaugeVec
	memUsed      *prometheus.GaugeVec
	memPercent   *prometheus.GaugeVec

	storageTotal *prometheus.GaugeVec
	storageFree  *prometheus.GaugeVec
	storageUsed  *prometheus.GaugeVec

	batteryLevel   *prometheus.GaugeVec
	batteryStatus  *prometheus.GaugeVec
	batteryTemp    *prometheus.GaugeVec
	batteryVoltage *prometheus.GaugeVec

	thermalMax *prometheus.GaugeVec

	screenOn *prometheus.GaugeVec

	uptime *prometheus.GaugeVec

	// Exporter self-metrics (no label).
	pollDuration    prometheus.Gauge
	pollLastSuccess prometheus.Gauge
	pollOverruns    prometheus.Counter
	devicesTotal    prometheus.Gauge

	// Serials published in the previous Apply, to detect removals.
	lastSerials map[string]struct{}

	// deviceVecs is the fixed list of per-device GaugeVecs, used for full
	// deletion when a device disappears.
	deviceVecs []*prometheus.GaugeVec
}

func deviceGauge(name, help string) *prometheus.GaugeVec {
	return prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      name,
		Help:      help,
	}, []string{"device"})
}

// New builds and registers all metrics.
func New() *Registry {
	r := &Registry{
		reg:         prometheus.NewRegistry(),
		lastSerials: make(map[string]struct{}),

		online:         deviceGauge("adb_online", "1 if device is present with state=device, else 0"),
		scrapeSuccess:  deviceGauge("adb_scrape_success", "1 if the per-device shell scrape succeeded, else 0"),
		scrapeDuration: deviceGauge("adb_scrape_duration_seconds", "Duration of the device's adb shell scrape in seconds"),
		cpuUsage:       deviceGauge("cpu_usage_percent", "CPU utilisation percent (0-100) from /proc/stat delta"),
		memTotal:       deviceGauge("memory_total_bytes", "Total RAM in bytes"),
		memAvailable:   deviceGauge("memory_available_bytes", "Available RAM in bytes"),
		memUsed:        deviceGauge("memory_used_bytes", "Used RAM in bytes (total - available)"),
		memPercent:     deviceGauge("memory_used_percent", "Used RAM percent (0-100)"),
		storageTotal:   deviceGauge("storage_total_bytes", "Total storage of /data in bytes"),
		storageFree:    deviceGauge("storage_free_bytes", "Free storage of /data in bytes"),
		storageUsed:    deviceGauge("storage_used_bytes", "Used storage of /data in bytes"),
		batteryLevel:   deviceGauge("battery_level", "Battery charge level (0-100)"),
		batteryStatus:  deviceGauge("battery_status", "Battery status code (1=unknown,2=charging,3=discharging,4=not charging,5=full)"),
		batteryTemp:    deviceGauge("battery_temperature_celsius", "Battery temperature in Celsius"),
		batteryVoltage: deviceGauge("battery_voltage_volts", "Battery voltage in volts"),
		thermalMax:     deviceGauge("thermal_max_temperature_celsius", "Maximum temperature across all thermal zones in Celsius"),
		screenOn:       deviceGauge("screen_on", "1 if the screen is on, else 0"),
		uptime:         deviceGauge("uptime_seconds", "Device uptime in seconds"),

		pollDuration: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace, Name: "poll_cycle_duration_seconds",
			Help: "Duration of the last poll cycle in seconds",
		}),
		pollLastSuccess: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace, Name: "poll_last_success_timestamp_seconds",
			Help: "Unix timestamp of the last successful poll cycle",
		}),
		pollOverruns: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace, Name: "poll_overruns_total",
			Help: "Number of poll cycles skipped because the previous cycle was still running",
		}),
		devicesTotal: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace, Name: "devices_total",
			Help: "Number of devices currently discovered",
		}),
	}

	r.deviceVecs = []*prometheus.GaugeVec{
		r.online, r.scrapeSuccess, r.scrapeDuration, r.cpuUsage,
		r.memTotal, r.memAvailable, r.memUsed, r.memPercent,
		r.storageTotal, r.storageFree, r.storageUsed,
		r.batteryLevel, r.batteryStatus, r.batteryTemp, r.batteryVoltage,
		r.thermalMax, r.screenOn, r.uptime,
	}

	for _, v := range r.deviceVecs {
		r.reg.MustRegister(v)
	}
	r.reg.MustRegister(r.pollDuration, r.pollLastSuccess, r.pollOverruns, r.devicesTotal)
	return r
}

// Gatherer exposes the registry for the HTTP /metrics handler.
func (r *Registry) Gatherer() prometheus.Gatherer { return r.reg }

// Apply publishes a full snapshot: it sets values for present devices, deletes
// series for removed devices, and deletes individual unset optional metrics.
func (r *Registry) Apply(devices []DeviceSnapshot) {
	current := make(map[string]struct{}, len(devices))
	for i := range devices {
		d := &devices[i]
		current[d.Serial] = struct{}{}
		r.applyDevice(d)
	}

	// Delete series for devices that vanished from adb devices.
	for serial := range r.lastSerials {
		if _, ok := current[serial]; !ok {
			for _, v := range r.deviceVecs {
				v.DeleteLabelValues(serial)
			}
		}
	}
	r.lastSerials = current
	r.devicesTotal.Set(float64(len(devices)))
}

func (r *Registry) applyDevice(d *DeviceSnapshot) {
	s := d.Serial
	setBool(r.online, s, d.Online)
	setBool(r.scrapeSuccess, s, d.ScrapeSuccess)
	r.scrapeDuration.WithLabelValues(s).Set(d.ScrapeDuration)

	m := d.Metrics
	setOpt(r.cpuUsage, s, m.CPUUsagePercent)
	setOpt(r.memTotal, s, m.MemTotalBytes)
	setOpt(r.memAvailable, s, m.MemAvailableBytes)
	setOpt(r.memUsed, s, m.MemUsedBytes)
	setOpt(r.memPercent, s, m.MemUsedPercent)
	setOpt(r.storageTotal, s, m.StorageTotalBytes)
	setOpt(r.storageFree, s, m.StorageFreeBytes)
	setOpt(r.storageUsed, s, m.StorageUsedBytes)
	setOpt(r.batteryLevel, s, m.BatteryLevel)
	setOpt(r.batteryStatus, s, m.BatteryStatus)
	setOpt(r.batteryTemp, s, m.BatteryTemperatureCelsius)
	setOpt(r.batteryVoltage, s, m.BatteryVoltageVolts)
	setOpt(r.thermalMax, s, m.ThermalMaxTemperatureCelsius)
	setOpt(r.screenOn, s, m.ScreenOn)
	setOpt(r.uptime, s, m.UptimeSeconds)
}

// SetPollCycle records timing for a completed cycle. success updates the
// last-success timestamp used by /health and /ready.
func (r *Registry) SetPollCycle(durationSeconds float64, success bool, unixTimestamp float64) {
	r.pollDuration.Set(durationSeconds)
	if success {
		r.pollLastSuccess.Set(unixTimestamp)
	}
}

// IncOverruns bumps the skipped-cycle counter.
func (r *Registry) IncOverruns() { r.pollOverruns.Inc() }

func setBool(v *prometheus.GaugeVec, serial string, b bool) {
	if b {
		v.WithLabelValues(serial).Set(1)
	} else {
		v.WithLabelValues(serial).Set(0)
	}
}

// setOpt sets the value when present, otherwise deletes the series so no stale
// number is exposed.
func setOpt(v *prometheus.GaugeVec, serial string, o collectors.OptFloat) {
	if o.Set {
		v.WithLabelValues(serial).Set(o.Value)
	} else {
		v.DeleteLabelValues(serial)
	}
}
