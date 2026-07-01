
# Android Farm Exporter Specification

> Version: 1.1

## 1. Goal

Android Farm Exporter is a production-ready Prometheus exporter written in Go for monitoring multiple Android devices connected through ADB.

The exporter **does not install anything on Android devices**. All communication happens through the external `adb` executable.

Primary target:
- Windows
- Also support Linux and macOS

---

# Functional requirements

## Supported platforms

- Windows
- Linux
- macOS

No platform-specific APIs.

The only dependency for Android communication is `adb`.

---

## Supported devices

Any device visible in:

```bash
adb devices
```

Including:

- USB
- adb connect
- Android Emulator
- Genymotion

---

## Polling

Every 5 seconds:

1. Execute:

```bash
adb devices
```

2. Discover devices.

3. Detect newly connected devices.

4. Detect removed devices.

5. Collect metrics from every connected device.

6. Atomically replace the in-memory cache.

HTTP endpoints must NEVER execute adb.

### Non-overlapping cycles (resolves cycle-overrun)

Poll cycles MUST NOT overlap. If a cycle is still running when the next tick
fires (e.g. many devices hitting the 3s adb timeout), the new tick is **skipped**,
`android_farm_poll_overruns_total` is incremented, and a warning is logged. This
guarantees a bounded number of concurrent adb processes and prevents pile-up. The
poll interval is therefore a *minimum* spacing, not a hard guarantee.

### Device state handling (`adb devices` states)

`adb devices` lists a state per serial. The exporter maps them as follows:

| State | online | Scrape shell? | Series kept? |
|---|---|---|---|
| `device` | 1 | yes | yes |
| `offline`, `unauthorized`, `no permissions`, `recovery`, `bootloader`, `sideload`, `host`, `connecting`, other | 0 | no | yes (present, not scraped) |
| absent from list | — | no | no — series deleted |

Only `state=device` serials are scraped. Non-`device` states are considered
present-but-not-online: their `online`/`scrape_success` are 0 and no adb shell is
spawned for them (avoids guaranteed timeouts).

### Per-device state across cycles (resolves CPU delta)

CPU usage cannot be derived from a single `/proc/stat` read — it is a delta of
counters between two samples. The poller therefore keeps a small per-device store
of the **previous** `/proc/stat` totals (busy + idle jiffies), separate from the
published metrics snapshot. Each cycle:

```
usage_percent = 100 * (Δbusy) / (Δbusy + Δidle)
```

On the first successful sample for a device (no previous data), CPU usage is not
published (or published as 0 and `scrape_success` still 1 for other sections);
the first delta appears on the second cycle. This previous-sample store is pruned
when a device is removed.

---

## Discovery

Do NOT use:

```
adb track-devices
```

Discovery is based only on:

```
adb devices
```

---

## Worker pool

ADB process count must be limited.

Configurable:

```yaml
max_parallel_adb: 8
```

Example:

40 devices

↓

8 concurrent workers

↓

adb server

---

## Single adb shell

Each polling cycle for one device must execute only ONE adb shell.

### Portability rule (Windows-safe)

The command MUST be passed to `os/exec` as a **single string argument**, not as a
multi-line script. No local shell (`sh -c`) is invoked on the host — the string is
handed directly to `adb ... shell "<one-line command>"`, and the on-device shell
executes it. Commands are separated by `;` (never by real newlines) so the exact
same invocation works identically on Windows, Linux and macOS.

Canonical invocation:

```
adb -s <serial> shell "cat /proc/stat; echo @@@MEM@@@; cat /proc/meminfo; echo @@@BATTERY@@@; dumpsys battery; echo @@@UPTIME@@@; cat /proc/uptime; echo @@@DF@@@; df -k /; echo @@@POWER@@@; dumpsys power; echo @@@THERMAL@@@; cat /sys/class/thermal/thermal_zone*/temp 2>/dev/null; echo @@@END@@@"
```

Notes:

- Separator markers are bare tokens (`@@@MEM@@@`, `@@@BATTERY@@@`, …) printed on
  their own line. A trailing `@@@END@@@` marks the end of output.
- **Markers must NOT contain `#`.** In the on-device POSIX shell (mksh/toybox) a
  word beginning with `#` starts a comment, so `echo ###MEM###` prints nothing and
  — because the whole command is a single line — comments out *everything after
  it*, collapsing all sections into one. `@` has no special shell meaning and
  needs no quoting, so `@@@…@@@` markers are safe and portable. This is verified
  by a regression test that runs the command through a real shell.
- `df -k /` is used (POSIX 1K-blocks) instead of `df /`, because the default
  `df` output format is not stable across toybox/busybox versions. See §Storage parsing.
- The `thermal_zone*` glob is expanded by the **on-device** shell, so it must stay
  inside the quoted command string.

Parse output by splitting on the separator markers. A missing or empty section
sets `scrape_success` for that section's metrics to 0 but MUST NOT fail the whole
device scrape (best-effort per section).

---

# Metrics

## Namespace

All metrics are prefixed with `android_farm_`. Every device metric carries exactly
one label: `device="<adb serial>"` (see §Labels).

Full metric names, types and units:

| Metric | Type | Unit / values |
|---|---|---|
| `android_farm_adb_online` | Gauge | 1 if device is in `adb devices` with state `device`, else 0 |
| `android_farm_adb_scrape_success` | Gauge | 1 if the per-device shell scrape parsed successfully, else 0 |
| `android_farm_adb_scrape_duration_seconds` | Gauge | wall-clock duration of the device's adb shell call |
| `android_farm_cpu_usage_percent` | Gauge | 0–100, computed from `/proc/stat` delta (see §CPU) |
| `android_farm_memory_total_bytes` | Gauge | bytes |
| `android_farm_memory_available_bytes` | Gauge | bytes |
| `android_farm_memory_used_bytes` | Gauge | bytes (`total - available`) |
| `android_farm_memory_used_percent` | Gauge | 0–100 |
| `android_farm_storage_total_bytes` | Gauge | bytes (from `df -k /`, ×1024) |
| `android_farm_storage_free_bytes` | Gauge | bytes |
| `android_farm_storage_used_bytes` | Gauge | bytes |
| `android_farm_battery_level` | Gauge | 0–100 |
| `android_farm_battery_status` | Gauge | numeric `dumpsys battery` status code (see below) |
| `android_farm_battery_temperature_celsius` | Gauge | °C (raw dumpsys tenths-°C ÷ 10) |
| `android_farm_battery_voltage_volts` | Gauge | volts (raw dumpsys mV ÷ 1000) |
| `android_farm_thermal_max_temperature_celsius` | Gauge | °C, max over all thermal zones (see §Thermal) |
| `android_farm_screen_on` | Gauge | 1 if screen on, 0 if off |
| `android_farm_uptime_seconds` | Gauge | seconds (from `/proc/uptime`) |

### Exporter self-metrics (no `device` label)

| Metric | Type | Meaning |
|---|---|---|
| `android_farm_poll_cycle_duration_seconds` | Gauge | duration of the last full poll cycle |
| `android_farm_poll_last_success_timestamp_seconds` | Gauge | unix time of last successful cycle |
| `android_farm_poll_overruns_total` | Counter | cycles skipped because the previous one was still running |
| `android_farm_devices_total` | Gauge | number of devices currently discovered |

### Units & conversions (resolves unit ambiguity)

- **Battery temperature:** `dumpsys battery` reports tenths of a degree Celsius
  (e.g. `350` → `35.0 °C`). Divide by 10.
- **Battery voltage:** reported in millivolts (e.g. `4200` → `4.2 V`). Divide by 1000.
- **Battery status:** exported as the raw Android `BatteryManager` status code:
  `1=UNKNOWN, 2=CHARGING, 3=DISCHARGING, 4=NOT_CHARGING, 5=FULL`.
- **Thermal zones:** `/sys/class/thermal/thermal_zone*/temp` is conventionally
  millidegrees Celsius (e.g. `48000` → `48.0 °C`). Divide by 1000. As a sanity
  guard, if a raw value is `< 1000` treat it as already-degrees and use as-is.

### Storage parsing (`df -k /`)

Output is in 1K-blocks. Multiply the `1K-blocks`, `Used` and `Available` columns
by 1024 to get bytes. The parser MUST tolerate the case where a long filesystem
name wraps the row onto two physical lines (locate the numeric columns from the
end of the record, not by fixed field index).

---

# Metrics NOT collected

Do not collect:

- top
- ps
- process list
- RSS
- application metrics
- IMEI
- Android version
- ABI
- manufacturer
- model
- CPU cores
- other static device information

---

# Labels

Use only:

```
device="<adb serial>"
```

No additional labels.

The label value is the raw serial exactly as printed by `adb devices`. For TCP
devices this is `<ip>:<port>` (e.g. `192.168.1.5:5555`). Note that reconnecting a
TCP device on a different port produces a *different* serial and therefore a new
series (the old one is deleted once it disappears from `adb devices`). This is
accepted behaviour.

---

# Section parsing rules

Guidance for the ambiguous `dumpsys`/sysfs sections. Parsers must be
version-tolerant and fall back gracefully; an unparseable section only zeroes its
own metrics.

## screen_on (from `dumpsys power`)

Field naming varies across Android versions. Check in priority order and use the
first match:

1. `Display Power: state=ON|OFF`  → ON ⇒ 1
2. `mWakefulness=Awake|Asleep|Dozing`  → Awake ⇒ 1
3. `mScreenOn=true|false`  → true ⇒ 1

If none match, `screen_on` is unset and that metric's parse counts as a failure.

## thermal_max_temperature_celsius

`max_temperature` is the maximum across **all** thermal zones reported by
`/sys/class/thermal/thermal_zone*/temp`, not a specific CPU zone (zone identity is
not exposed as a label — that would be static device info, which is out of scope).
Apply the millidegree conversion from §Units before taking the max. Empty/absent
output (some devices restrict sysfs access) leaves the metric unset.

---

# Prometheus

Use GaugeVec metrics.

Do not dynamically register/unregister **Collectors** — the set of GaugeVec
objects is fixed at startup and never changes at runtime.

Update values in-place.

### Removed-device series (resolves stale-metric contradiction)

"Do not unregister" applies to Collectors, NOT to individual label series.
When a device disappears from `adb devices`, the exporter MUST call
`DeleteLabelValues(serial)` on every GaugeVec for that serial, so its time series
stop being exposed on `/metrics`. Otherwise a removed device would linger forever
with its last-seen values, which is misleading in Prometheus.

Rule of thumb:
- device present but not `state=device` (offline/unauthorized/…): keep series,
  set `android_farm_adb_online=0` and `android_farm_adb_scrape_success=0`.
- device absent from `adb devices` entirely: delete all its series.

---

# Cache

Metrics are stored in memory.

After every polling cycle:

- build a new snapshot
- atomically swap it

No HTTP request should touch ADB.

---

# HTTP

Endpoints:

```
/metrics
```

```
/health
```

Healthy when:

- HTTP server running
- poller alive
- last successful poll <15 seconds ago

Otherwise HTTP 500.

```
/ready
```

Returns HTTP 200 only after first successful poll.

### Definition of "successful poll" (resolves ambiguity)

A poll cycle is **successful** when `adb devices` executed and was parsed
successfully — regardless of how many individual devices failed to scrape. This
updates `android_farm_poll_last_success_timestamp_seconds` and gates `/ready`.

Rationale: a farm with zero online devices, or with every device timing out, is
still a *healthy exporter* — the failure is on the device side and is already
visible via per-device `android_farm_adb_scrape_success`. `/health` only turns
500 when the poller itself is stuck (no successful `adb devices` for >15s) or the
HTTP server is down.

---

# Config

Example:

```yaml
listen: ":9105"

poll_interval: 5s

adb_path: adb

adb_timeout: 3s

max_parallel_adb: 8
```

---

# Service mode

Support:

Console mode

```
android-farm-exporter
```

Service mode using:

github.com/kardianos/service

Commands:

```
install
uninstall
start
stop
restart
status
```

Supported:

- Windows Service
- systemd
- launchd

---

# Startup

On startup:

1. Validate config.

2. Check:

```
adb version
```

3. Execute:

```
adb start-server
```

`adb start-server` is idempotent — always call it on startup (do not try to detect
whether it is "necessary"). If a server is already running it is a no-op.

4. Start poller.

5. Start HTTP server.

---

# Logging

Log:

- startup
- shutdown
- discovered devices
- removed devices
- adb failures
- parser failures
- poll duration

Use structured logging.

---

# Error handling

ADB timeout:

Default:

```
3s
```

One broken device must never block the exporter.

Device is removed only if absent from:

```
adb devices
```

---

# Graceful shutdown

On stop:

- stop poller
- wait workers
- stop HTTP
- exit cleanly

---

# Project structure

```
cmd/
    exporter/

internal/
    adb/
    collectors/
    config/
    http/
    metrics/
    poller/
    service/

pkg/
```

---

# Build

```
go build
```

No CGO.

Single binary.

---

# CI/CD

Repository must include GitHub Actions.

## CI

Run on:

- push
- pull_request

Execute:

- go fmt
- go vet
- go test
- go build

Use Go module cache.

## Release

On GitHub Release:

Build:

- windows-amd64
- linux-amd64
- linux-arm64
- darwin-amd64
- darwin-arm64

Package binaries and upload release assets.

Embed version via ldflags.

`--version` prints:

- version
- commit
- build date
- Go version
- platform

---

# Makefile

Include:

- make fmt
- make vet
- make test
- make build
- make release
- make clean

CI should use Makefile.

---

# Testing

Add parser unit tests for:

- CPU
- Memory
- Battery
- Thermal
- Uptime
- Storage

Run cleanly under:

```
go test -race ./...
```

---

# Documentation

README must include:

- build
- configuration
- running
- service mode
- Prometheus example
- Grafana example
- exported metrics

---

# License

MIT

---

# Changelog

## v1.1 — resolved open design questions

1. **Single-line shell command** — the batch scrape is passed to `adb shell` as one
   `;`-separated string (no host `sh -c`, no newlines), so it behaves identically on
   Windows/Linux/macOS. Added `###END###` marker. See §Single adb shell.
2. **Storage** — use `df -k /` (stable POSIX 1K-blocks ×1024) instead of `df /`;
   tolerate wrapped rows. See §Storage parsing.
3. **CPU usage** — computed from a `/proc/stat` delta; poller keeps a per-device
   previous-sample store separate from the published snapshot; first value appears on
   the second cycle. See §Per-device state across cycles.
4. **Units fixed** — battery temperature (÷10 → °C), voltage (÷1000 → V), thermal
   zones (÷1000 → °C), battery status as numeric code. Metric names given units
   suffixes. See §Units & conversions.
5. **Cycle overrun** — non-overlapping cycles; a tick that arrives while a cycle runs
   is skipped and counted in `android_farm_poll_overruns_total`. See §Non-overlapping
   cycles.
6. **screen_on** — priority-ordered field fallback from `dumpsys power`. See §Section
   parsing rules.
7. **Stale metrics** — "do not unregister" applies to Collectors only; individual
   series for removed devices are dropped via `DeleteLabelValues`. See §Removed-device
   series.
8. **"Successful poll" defined** — a cycle is successful when `adb devices` itself
   succeeds, independent of per-device scrape failures; gates `/ready` and `/health`.
   See §Definition of "successful poll".
9. **adb device states** — only `state=device` is scraped; other states are
   present-but-offline (online=0, not scraped); absent ⇒ series deleted. See §Device
   state handling.
10. **Metric namespace** — all metrics prefixed `android_farm_`; full names/types/units
    tabulated. Added exporter self-metrics (poll duration, last-success timestamp,
    overruns, devices total). See §Namespace.
11. **Thermal** — `max_temperature` documented as max over all zones (no per-zone label,
    since zone identity is static info). See §Section parsing rules.
12. **adb start-server** — always called on startup (idempotent), no necessity check.
    See §Startup.
13. **TCP serials** — label is the raw `ip:port` serial; reconnect on a new port is a
    new series. See §Labels.

---

# Final result

After:

```
go build
```

User gets a single executable.

The exporter:

- discovers devices automatically
- polls every 5 seconds
- limits concurrent adb processes
- exports Prometheus metrics
- supports Windows service
- supports Linux/macOS
- is suitable for continuous production use.
