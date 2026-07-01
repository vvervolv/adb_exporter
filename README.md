# adb_exporter

A production-ready [Prometheus](https://prometheus.io/) exporter for monitoring
farms of Android devices over [`adb`](https://developer.android.com/tools/adb).

- **Agentless** — nothing is installed on the devices; all communication goes
  through the external `adb` executable.
- **Cross-platform** — single static binary for Windows, Linux and macOS. No CGO.
- **Scales to many devices** — a bounded worker pool limits concurrent `adb`
  processes; each device is scraped with a single `adb shell` per cycle.
- **Cache-and-serve** — a background poller refreshes an in-memory snapshot every
  few seconds; HTTP requests never touch `adb`.

Works with anything visible in `adb devices`: USB, `adb connect` (TCP), the
Android Emulator, and Genymotion.

---

## Build

Requires Go 1.24+ and `adb` on the machine that runs the exporter.

```bash
go build -o adb_exporter ./cmd/exporter
# or, with version metadata embedded:
make build
```

The result is a single executable with no runtime dependencies other than `adb`.

## Configuration

Configuration is a small YAML file. Every key is optional and falls back to the
default shown below (see [`config.example.yaml`](config.example.yaml)):

```yaml
listen: ":9105"       # HTTP listen address
poll_interval: 5s      # minimum spacing between poll cycles
adb_path: adb          # adb executable (name on PATH, or absolute path)
adb_timeout: 3s        # timeout per adb invocation (must be < poll_interval)
max_parallel_adb: 8    # worker-pool size: max concurrent adb processes
```

## Running

```bash
# Console mode with defaults:
adb_exporter

# With a config file:
adb_exporter --config /etc/adb_exporter/config.yaml

# Version info:
adb_exporter --version
```

On startup the exporter validates the config, checks `adb version`, runs
`adb start-server` (idempotent), then starts the poller and HTTP server.

### Endpoints

| Path       | Description                                                        |
|------------|--------------------------------------------------------------------|
| `/metrics` | Prometheus metrics (served from the in-memory snapshot).           |
| `/health`  | `200` if the last successful poll was recent (< 3× poll interval), else `500`. |
| `/ready`   | `200` once the first successful poll has completed, else `503`.    |

## Service mode

The exporter can run as a managed OS service (Windows Service, systemd, launchd)
via [`kardianos/service`](https://github.com/kardianos/service):

```bash
adb_exporter install --config /etc/adb_exporter/config.yaml
adb_exporter start
adb_exporter status
adb_exporter stop
adb_exporter restart
adb_exporter uninstall
```

`install` remembers the `--config` path and re-passes it when the service
manager starts the binary.

## Prometheus example

Scrape config (`prometheus.yml`):

```yaml
scrape_configs:
  - job_name: adb_exporter
    scrape_interval: 15s
    static_configs:
      - targets: ["localhost:9105"]
```

Example queries:

```promql
# Devices currently online
sum(android_farm_adb_online)

# Devices whose last scrape failed
android_farm_adb_scrape_success == 0

# Hot devices (> 45 °C on any thermal zone)
android_farm_thermal_max_temperature_celsius > 45

# Low battery
android_farm_battery_level < 20
```

## Grafana dashboard

A ready-made dashboard lives at
[`grafana/adb_exporter-dashboard.json`](grafana/adb_exporter-dashboard.json).

Import it in Grafana via **Dashboards → New → Import → Upload JSON file** (or paste
the contents), then pick your Prometheus data source when prompted. It exposes a
`Device` multi-select variable driven by the `device` label, plus panels for:

- online / discovered devices, failing scrapes and poll overruns (stats)
- CPU usage and memory used % (time series)
- battery level and temperatures — battery + thermal max (time series)
- uptime and free `/data` storage (time series / bar gauge)
- screen on/off (state timeline)
- a per-device overview table (CPU, memory, battery, temperature, free storage, uptime)

The single `device` label makes per-device breakdowns and table columns trivial,
so the dashboard scales from one device to a whole farm without changes.

## Exported metrics

All metrics are prefixed `android_farm_` and carry a single `device` label (the
raw `adb` serial), except the exporter self-metrics which have no labels.

### Per-device

| Metric | Type | Unit / values |
|---|---|---|
| `android_farm_adb_online` | Gauge | 1 if state=`device`, else 0 |
| `android_farm_adb_scrape_success` | Gauge | 1 if the device scrape succeeded |
| `android_farm_adb_scrape_duration_seconds` | Gauge | scrape duration |
| `android_farm_cpu_usage_percent` | Gauge | 0–100 (from `/proc/stat` delta) |
| `android_farm_memory_total_bytes` | Gauge | bytes |
| `android_farm_memory_available_bytes` | Gauge | bytes |
| `android_farm_memory_used_bytes` | Gauge | bytes |
| `android_farm_memory_used_percent` | Gauge | 0–100 |
| `android_farm_storage_total_bytes` | Gauge | bytes (`/`) |
| `android_farm_storage_free_bytes` | Gauge | bytes |
| `android_farm_storage_used_bytes` | Gauge | bytes |
| `android_farm_battery_level` | Gauge | 0–100 |
| `android_farm_battery_status` | Gauge | 1=unknown,2=charging,3=discharging,4=not charging,5=full |
| `android_farm_battery_temperature_celsius` | Gauge | °C |
| `android_farm_battery_voltage_volts` | Gauge | volts |
| `android_farm_thermal_max_temperature_celsius` | Gauge | °C (max over all zones) |
| `android_farm_screen_on` | Gauge | 1 if screen on, else 0 |
| `android_farm_uptime_seconds` | Gauge | seconds |

Note: metrics that could not be parsed in a given cycle are omitted (their series
is deleted) rather than exposed with a stale value. When a device disappears from
`adb devices`, all of its series are removed.

### Exporter self-metrics

| Metric | Type | Meaning |
|---|---|---|
| `android_farm_poll_cycle_duration_seconds` | Gauge | duration of the last poll cycle |
| `android_farm_poll_last_success_timestamp_seconds` | Gauge | unix time of the last successful cycle |
| `android_farm_poll_overruns_total` | Counter | cycles skipped because the previous one was still running |
| `android_farm_devices_total` | Gauge | number of discovered devices |

## Development

```bash
make fmt      # format
make vet      # go vet
make test     # go test -race ./...
make build    # build the binary
make release  # cross-compile all platforms into dist/
```

See [SPEC.md](SPEC.md) for the full specification.

## License

[MIT](LICENSE)
