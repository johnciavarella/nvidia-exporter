# nvidia-exporter

A Prometheus exporter for GPU metrics. By default it collects NVIDIA metrics via `nvidia-smi`; with `--backend=intel` it collects Intel GPU metrics via `intel_gpu_top` (see [Intel GPUs](#intel-gpus)). Metrics are exposed on an HTTP endpoint for Prometheus scraping.

## Requirements

- Go 1.22+
- NVIDIA GPU with drivers installed
- `nvidia-smi` available in PATH (or specify path with `--nvidia-smi-path`)

## Build

```bash
# Build
make build

# Run tests
make test

# Clean build artifacts
make clean
```

Or directly with Go:

```bash
go build -o bin/nvidia-exporter ./cmd/nvidia-exporter
```

## Usage

```bash
./bin/nvidia-exporter [flags]
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--listen-address` | `:9835` | Address to listen on for HTTP requests |
| `--metrics-path` | `/metrics` | Path under which to expose metrics |
| `--backend` | `nvidia` | GPU backend: `nvidia` (nvidia-smi) or `intel` (intel_gpu_top) |
| `--nvidia-smi-path` | `nvidia-smi` | Path to nvidia-smi binary |
| `--intel-gpu-top-path` | `intel_gpu_top` | Path to intel_gpu_top binary (`--backend=intel`) |
| `--intel-device` | _(first GPU)_ | intel_gpu_top `-d` device filter, e.g. `drm:/dev/dri/renderD128` (`--backend=intel`) |
| `--intel-hwmon` | `auto` | Intel GPU hwmon directory for power/energy/temperature/fan (`--backend=intel`): `auto` (find the Intel GPU under `/sys/class/drm`), `off`, or a path such as `/sys/class/drm/card1/device/hwmon/hwmon5` |
| `--collect-interval` | `10s` | Interval between metric collections (sampling period for `--backend=intel`) |
| `--log-level` | `info` | Log level (debug, info, warn, error) |

### Endpoints

| Endpoint | Description |
|----------|-------------|
| `/` | Landing page with link to metrics |
| `/metrics` | Prometheus metrics endpoint |
| `/health` | Health check (returns 200 OK) |

## Metrics

### GPU Metrics

All GPU metrics are labeled with `gpu` (index), `name`, and `uuid`.

| Metric | Type | Description |
|--------|------|-------------|
| `nvidia_gpu_utilization_ratio` | gauge | GPU utilization (0-1) |
| `nvidia_memory_utilization_ratio` | gauge | Memory utilization (0-1) |
| `nvidia_memory_used_bytes` | gauge | Memory used in bytes |
| `nvidia_memory_total_bytes` | gauge | Total memory in bytes |
| `nvidia_gpu_temperature_celsius` | gauge | GPU temperature in Celsius |
| `nvidia_gpu_power_draw_watts` | gauge | Power draw in Watts |
| `nvidia_gpu_power_limit_watts` | gauge | Power limit in Watts |
| `nvidia_gpu_fan_speed_ratio` | gauge | Fan speed (0-1) |
| `nvidia_gpu_clock_graphics_hz` | gauge | Graphics clock in Hz |
| `nvidia_gpu_clock_memory_hz` | gauge | Memory clock in Hz |
| `nvidia_gpu_performance_state` | gauge | Performance state (0=P0 max, 15=P15 min) |

### Exporter Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `nvidia_exporter_last_collection_timestamp_seconds` | gauge | Unix timestamp of last successful collection |
| `nvidia_exporter_collection_error` | gauge | 1 if last collection failed, 0 otherwise |

## Intel GPUs

`--backend=intel` reads Intel GPU data from `intel_gpu_top -J` (package `intel-gpu-tools` on Debian/Ubuntu). Unlike `nvidia-smi`, `intel_gpu_top` measures over a period, so the exporter keeps one `intel_gpu_top` process running with `-s <collect-interval>` and serves its newest sample on each scrape. It restarts the process if it exits.

```bash
./bin/nvidia-exporter --backend=intel --listen-address=127.0.0.1:9836
```

Requirements:

- `intel_gpu_top` needs permission to read GPU performance counters: run as root, or grant `CAP_PERFMON` (systemd: `AmbientCapabilities=CAP_PERFMON`), or set `kernel.perf_event_paranoid=0`. It also needs access to `/dev/dri`.
- `intel_gpu_top` does not report power on discrete Arc cards (and never reports VRAM). Power, temperature and fan data come from the i915/xe **hwmon** interface instead (`--intel-hwmon`, on by default when an Intel GPU hwmon directory is found). Which sensors exist depends on the card and kernel: the `hwmon_*` metrics other than energy are only emitted if the driver provides them. hwmon is read at scrape time and works even if `intel_gpu_top` is failing.
- Power is derived from the `energy1_input` counter between two scrapes, so it appears from the second scrape on. `rate(intel_gpu_hwmon_energy_joules_total[5m])` gives the same number in Prometheus.
- With `--intel-device pci:slot=0000:00:10.0` (or a `sys:` path) the hwmon of that PCI device is used; otherwise the first Intel GPU.

All Intel metrics carry a `device` label (the `--intel-device` value, or `default`). Metrics are exposed under the `intel_` prefix, not `nvidia_`, so existing NVIDIA dashboards and alerts are unaffected.

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `intel_gpu_engine_busy_ratio` | gauge | `engine` | Engine busy time (0-1), e.g. `Render/3D/0`, `Video/0`, `VideoEnhance/0`, `Blitter/0` |
| `intel_gpu_engine_semaphore_wait_ratio` | gauge | `engine` | Time waiting on a semaphore (0-1) |
| `intel_gpu_engine_wait_ratio` | gauge | `engine` | Time waiting on an event (0-1) |
| `intel_gpu_frequency_actual_hz` | gauge | | Actual GPU frequency in Hz |
| `intel_gpu_frequency_requested_hz` | gauge | | Requested GPU frequency in Hz |
| `intel_gpu_rc6_ratio` | gauge | | Fraction of time in the RC6 power-saving state (0-1) |
| `intel_gpu_interrupts_per_second` | gauge | | GPU interrupts per second |
| `intel_gpu_power_gpu_watts` | gauge | | GPU power draw in Watts (if reported) |
| `intel_gpu_power_package_watts` | gauge | | Package power draw in Watts (if reported) |
| `intel_gpu_imc_read_bytes_per_second` | gauge | | Memory controller read bandwidth (if reported) |
| `intel_gpu_imc_write_bytes_per_second` | gauge | | Memory controller write bandwidth (if reported) |
| `intel_gpu_hwmon_energy_joules_total` | counter | | Cumulative energy from hwmon `energy1_input` in Joules |
| `intel_gpu_hwmon_power_watts` | gauge | | Power draw derived from the energy counter between scrapes |
| `intel_gpu_hwmon_power_limit_watts` | gauge | | Power limit (`power1_max`/`power1_cap`), if reported |
| `intel_gpu_hwmon_voltage_volts` | gauge | | Voltage (`in0_input`), if reported |
| `intel_gpu_hwmon_temperature_celsius` | gauge | `sensor` | Temperatures (`temp*_input`, named by `temp*_label`), if reported |
| `intel_gpu_hwmon_fan_rpm` | gauge | | Fan speed (`fan1_input`), if reported |
| `intel_exporter_hwmon_error` | gauge | | 1 if hwmon is enabled but unreadable (only present when hwmon is enabled) |
| `intel_exporter_last_collection_timestamp_seconds` | gauge | | Unix time of the last `intel_gpu_top` sample |
| `intel_exporter_collection_error` | gauge | | 1 if there is no fresh sample (process failing, no data yet, or sample older than 3 sampling periods + 5s), 0 otherwise. GPU metrics are omitted while it is 1. |

## systemd Unit Example

```ini
[Unit]
Description=NVIDIA GPU Prometheus Exporter
After=network.target

[Service]
Type=simple
User=nobody
ExecStart=/usr/local/bin/nvidia-exporter --listen-address=:9835
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

## Prometheus Configuration

```yaml
scrape_configs:
  - job_name: 'nvidia'
    static_configs:
      - targets: ['localhost:9835']
```

## License

MIT
