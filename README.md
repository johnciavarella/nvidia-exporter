# nvidia-exporter

A Prometheus exporter for GPU metrics from NVIDIA, AMD and Intel GPUs. Each instance runs one backend:

| `--backend` | Source | See |
|---|---|---|
| `nvidia` (default) | `nvidia-smi` | |
| `amd` | the `amdgpu` kernel driver through sysfs (no ROCm needed) | [AMD GPUs](#amd-gpus) |
| `intel` | `intel_gpu_top`, plus hwmon and optionally `xpu-smi` | [Intel GPUs](#intel-gpus) |

All backends export the same [common `gpu_*` metrics](#common-metrics), so one dashboard or alert works for every vendor. Metrics that only one vendor has keep a vendor prefix (`nvidia_`, `amd_`, `intel_`). Metrics are exposed on an HTTP endpoint for Prometheus scraping.

## Requirements

- Go 1.23+ to build
- NVIDIA: drivers installed and `nvidia-smi` in PATH (or `--nvidia-smi-path`)
- AMD: the `amdgpu` kernel driver; read access to `/sys` (no root)
- Intel: see [Intel GPUs](#intel-gpus)

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
| `--backend` | `nvidia` | GPU backend: `nvidia` (nvidia-smi), `amd` (amdgpu sysfs) or `intel` (intel_gpu_top) |
| `--legacy-metrics` | `true` | Also export the `nvidia_*`/`intel_*` metrics that duplicate the common `gpu_*` metrics. Set `--legacy-metrics=false` once dashboards use `gpu_*` |
| `--amd-sysfs-root` | `/sys` | sysfs mount point to find amdgpu devices under (`--backend=amd`), e.g. `/host/sys` in a container |
| `--nvidia-smi-path` | `nvidia-smi` | Path to nvidia-smi binary |
| `--intel-gpu-top-path` | `intel_gpu_top` | Path to intel_gpu_top binary (`--backend=intel`) |
| `--intel-device` | _(first GPU)_ | intel_gpu_top `-d` device filter, e.g. `drm:/dev/dri/renderD128` (`--backend=intel`) |
| `--intel-hwmon` | `auto` | Intel GPU hwmon directory for power/energy/temperature/fan (`--backend=intel`): `auto` (find the Intel GPU under `/sys/class/drm`), `off`, or a path such as `/sys/class/drm/card1/device/hwmon/hwmon5` |
| `--intel-xpu-smi-path` | _(off)_ | Path to Intel `xpu-smi`; when set, GPU memory used/total is exported (`--backend=intel`; needs root and a working `xpu-smi`) |
| `--intel-xpu-device` | `0` | `xpu-smi` device id used with `--intel-xpu-smi-path` |
| `--collect-interval` | `10s` | Interval between metric collections (sampling period for `--backend=intel`) |
| `--log-level` | `info` | Log level (debug, info, warn, error) |

### Endpoints

| Endpoint | Description |
|----------|-------------|
| `/` | Landing page with link to metrics |
| `/metrics` | Prometheus metrics endpoint |
| `/health` | Health check (returns 200 OK) |

## Metrics

### Common metrics

Every backend exports these under the same names, units and labels. A metric is left out when the GPU or driver does not report it (for example `[N/A]` from `nvidia-smi`, or a sysfs file the card does not have).

Per-GPU labels: `gpu` (index on the host: nvidia-smi index, DRM card number for AMD, `0` for Intel), `vendor` (`nvidia`, `amd`, `intel`), `name` (product name, or PCI `vendor:device` ID when the driver gives none) and `id` (NVIDIA UUID, otherwise the PCI address).

| Metric | Type | NVIDIA | AMD | Intel |
|--------|------|--------|-----|-------|
| `gpu_utilization_ratio` | gauge | `utilization.gpu` | `gpu_busy_percent` | estimated from RC6 and engine busy times, see [Intel utilization](#intel-utilization) |
| `gpu_memory_utilization_ratio` | gauge | `utilization.memory` | `mem_busy_percent` | |
| `gpu_memory_used_bytes` | gauge | `memory.used` | `mem_info_vram_used` | `xpu-smi` |
| `gpu_memory_total_bytes` | gauge | `memory.total` | `mem_info_vram_total` | `xpu-smi` |
| `gpu_temperature_celsius` | gauge, label `sensor` | `temperature.gpu` (`sensor="gpu"`) | hwmon (`edge`, `junction`, `mem`) | hwmon (e.g. `pkg`, `vram`) |
| `gpu_power_draw_watts` | gauge | `power.draw` | hwmon `power1_input`/`power1_average` | hwmon energy delta, else `intel_gpu_top` |
| `gpu_power_limit_watts` | gauge | `power.limit` | hwmon `power1_cap` | hwmon `power1_max`/`power1_cap` |
| `gpu_energy_joules_total` | counter | | | hwmon `energy1_input` |
| `gpu_fan_speed_ratio` | gauge | `fan.speed` | hwmon `pwm1`/`pwm1_max` | |
| `gpu_fan_speed_rpm` | gauge | | hwmon `fan1_input` | hwmon `fan1_input` |
| `gpu_clock_graphics_hz` | gauge | `clocks.current.graphics` | hwmon `sclk` | `intel_gpu_top` actual frequency |
| `gpu_clock_memory_hz` | gauge | `clocks.current.memory` | hwmon `mclk` | |
| `gpu_voltage_volts` | gauge | | hwmon `in0_input` (vddgfx) | hwmon `in0_input` |
| `gpu_exporter_collection_error` | gauge, label `vendor` | `nvidia-smi` failed | no amdgpu device found | no fresh `intel_gpu_top` sample |
| `gpu_exporter_last_collection_timestamp_seconds` | gauge, label `vendor` | | | |

Example: GPU utilization across a fleet of mixed hosts is `gpu_utilization_ratio`, and `max by (instance) (gpu_temperature_celsius)` is the hottest sensor per host.

### NVIDIA metrics

`nvidia_gpu_performance_state` (gauge, labels `gpu`, `name`, `uuid`) is always exported: the P-state (0=P0 max performance, 15=P15 min).

The metrics below are **legacy**: they duplicate the common metrics and are only exported with `--legacy-metrics=true` (the default). They are labeled with `gpu`, `name` and `uuid`. `[N/A]` values are reported as 0.

| Metric | Common equivalent |
|--------|-------------------|
| `nvidia_gpu_utilization_ratio` | `gpu_utilization_ratio` |
| `nvidia_memory_utilization_ratio` | `gpu_memory_utilization_ratio` |
| `nvidia_memory_used_bytes` | `gpu_memory_used_bytes` |
| `nvidia_memory_total_bytes` | `gpu_memory_total_bytes` |
| `nvidia_gpu_temperature_celsius` | `gpu_temperature_celsius{sensor="gpu"}` |
| `nvidia_gpu_power_draw_watts` | `gpu_power_draw_watts` |
| `nvidia_gpu_power_limit_watts` | `gpu_power_limit_watts` |
| `nvidia_gpu_fan_speed_ratio` | `gpu_fan_speed_ratio` |
| `nvidia_gpu_clock_graphics_hz` | `gpu_clock_graphics_hz` |
| `nvidia_gpu_clock_memory_hz` | `gpu_clock_memory_hz` |
| `nvidia_exporter_last_collection_timestamp_seconds` | `gpu_exporter_last_collection_timestamp_seconds` |
| `nvidia_exporter_collection_error` | `gpu_exporter_collection_error` |

## AMD GPUs

`--backend=amd` reads the `amdgpu` kernel driver's sysfs files directly at scrape time. It needs no ROCm, `amd-smi` or root, only read access to `/sys`. Every amdgpu card (PCI vendor `0x1002`) is exported. Cards are discovered on each scrape, so a GPU that is rebound or hot-plugged is picked up without a restart.

```bash
./bin/nvidia-exporter --backend=amd --listen-address=:9837
```

- Which sensors exist depends on the GPU generation and kernel. RDNA3 and newer report `power1_input`, older cards `power1_average`; consumer cards usually have no `product_name`, so `name` is the PCI ID (e.g. `1002:744c`).
- A dGPU in runtime power management sleep (`power/runtime_status` = `suspended`) is not woken up: only `amd_gpu_runtime_suspended 1` is exported for it until something else wakes it.
- In a container, mount the host's `/sys` read-only and point `--amd-sysfs-root` at it if it is not at `/sys`.

Besides the common metrics (labels `gpu`, `name`, `id`):

| Metric | Type | Description |
|--------|------|-------------|
| `amd_gpu_gtt_used_bytes` | gauge | System memory mapped for the GPU (GTT) in use (`mem_info_gtt_used`) |
| `amd_gpu_gtt_total_bytes` | gauge | System memory the GPU can map (`mem_info_gtt_total`) |
| `amd_gpu_runtime_suspended` | gauge | 1 if the GPU is runtime-suspended and its sensors were not read |

## Intel GPUs

`--backend=intel` reads Intel GPU data from `intel_gpu_top -J` (package `intel-gpu-tools` on Debian/Ubuntu). Unlike `nvidia-smi`, `intel_gpu_top` measures over a period, so the exporter keeps one `intel_gpu_top` process running with `-s <collect-interval>` and serves its newest sample on each scrape. It restarts the process if it exits.

```bash
./bin/nvidia-exporter --backend=intel --listen-address=127.0.0.1:9836
```

Requirements:

- `intel_gpu_top` needs permission to read GPU performance counters: run as root, or grant `CAP_PERFMON` (systemd: `AmbientCapabilities=CAP_PERFMON`), or set `kernel.perf_event_paranoid=0`. It also needs access to `/dev/dri`.
- `intel_gpu_top` does not report power on discrete Arc cards (and never reports VRAM). Power, temperature and fan data come from the i915/xe **hwmon** interface instead (`--intel-hwmon`, on by default when an Intel GPU hwmon directory is found). Which sensors exist depends on the card and kernel: the `hwmon_*` metrics other than energy are only emitted if the driver provides them. hwmon is read at scrape time and works even if `intel_gpu_top` is failing.
- Neither `intel_gpu_top` nor hwmon reports memory use on Arc. With `--intel-xpu-smi-path=xpu-smi` the exporter polls `xpu-smi stats -d <id> -j` every collection interval for used memory, and reads the physical size once from `xpu-smi discovery -d <id> -j`. Like hwmon, it is independent of `intel_gpu_top`. It is off by default because it needs Intel's `xpu-smi` installed.
- Power is derived from the `energy1_input` counter between two scrapes, so it appears from the second scrape on. `rate(intel_gpu_hwmon_energy_joules_total[5m])` gives the same number in Prometheus.
- With `--intel-device pci:slot=0000:00:10.0` (or a `sys:` path) the hwmon of that PCI device is used; otherwise the first Intel GPU.

### Intel utilization

`nvidia-smi`'s utilization is the share of time *any* engine was busy. `intel_gpu_top` only reports busy time per engine (render, video, video enhance, blitter, compute), and engines run at the same time, so the overall value is not reported directly. `gpu_utilization_ratio` for Intel is estimated as:

- **1 − RC6**: the share of the sampling period the GPU was awake, out of its RC6 sleep state. This is the closest match to "the GPU was doing something".
- **clamped** to at least the busiest engine and at most the sum of all engines (capped at 1), the bounds the true value must lie in. The clamp corrects 1 − RC6 reading high when the GPU stays awake briefly after work, or reads 100% because RC6 is disabled.
- If `intel_gpu_top` does not report RC6, the busiest engine is used.

For example, Video at 80% and Render at 37.5% give a value between 0.8 and 1, depending on how much RC6 time there was. Per-engine detail stays in `intel_gpu_engine_busy_ratio`; `max(intel_gpu_engine_busy_ratio)` gives the busiest engine.

Intel GPUs also export the [common metrics](#common-metrics), combined from all three sources: hwmon and `xpu-smi` values are exported even while `intel_gpu_top` is failing. In those, `id` is the PCI address of the hwmon device (else the `--intel-device` value, or `default`) and `name` its PCI ID.

The `intel_*` metrics below carry a `device` label (the `--intel-device` value, or `default`). Those marked **legacy** duplicate a common metric and are only exported with `--legacy-metrics=true` (the default).

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `intel_gpu_engine_busy_ratio` | gauge | `engine` | Engine busy time (0-1), e.g. `Render/3D/0`, `Video/0`, `VideoEnhance/0`, `Blitter/0` |
| `intel_gpu_engine_semaphore_wait_ratio` | gauge | `engine` | Time waiting on a semaphore (0-1) |
| `intel_gpu_engine_wait_ratio` | gauge | `engine` | Time waiting on an event (0-1) |
| `intel_gpu_frequency_actual_hz` | gauge | | **Legacy** (`gpu_clock_graphics_hz`). Actual GPU frequency in Hz |
| `intel_gpu_frequency_requested_hz` | gauge | | Requested GPU frequency in Hz |
| `intel_gpu_rc6_ratio` | gauge | | Fraction of time in the RC6 power-saving state (0-1), if reported |
| `intel_gpu_interrupts_per_second` | gauge | | GPU interrupts per second |
| `intel_gpu_power_gpu_watts` | gauge | | GPU power draw in Watts (if reported) |
| `intel_gpu_power_package_watts` | gauge | | Package power draw in Watts (if reported) |
| `intel_gpu_imc_read_bytes_per_second` | gauge | | Memory controller read bandwidth (if reported) |
| `intel_gpu_imc_write_bytes_per_second` | gauge | | Memory controller write bandwidth (if reported) |
| `intel_gpu_hwmon_energy_joules_total` | counter | | **Legacy** (`gpu_energy_joules_total`). Cumulative energy from hwmon `energy1_input` in Joules |
| `intel_gpu_hwmon_power_watts` | gauge | | **Legacy** (`gpu_power_draw_watts`). Power draw derived from the energy counter between scrapes |
| `intel_gpu_hwmon_power_limit_watts` | gauge | | **Legacy** (`gpu_power_limit_watts`). Power limit (`power1_max`/`power1_cap`), if reported |
| `intel_gpu_hwmon_voltage_volts` | gauge | | **Legacy** (`gpu_voltage_volts`). Voltage (`in0_input`), if reported |
| `intel_gpu_hwmon_temperature_celsius` | gauge | `sensor` | **Legacy** (`gpu_temperature_celsius`). Temperatures (`temp*_input`, named by `temp*_label`), if reported |
| `intel_gpu_hwmon_fan_rpm` | gauge | | **Legacy** (`gpu_fan_speed_rpm`). Fan speed (`fan1_input`), if reported |
| `intel_gpu_memory_used_bytes` | gauge | | **Legacy** (`gpu_memory_used_bytes`). GPU memory in use, from `xpu-smi` (only with `--intel-xpu-smi-path`) |
| `intel_gpu_memory_total_bytes` | gauge | | **Legacy** (`gpu_memory_total_bytes`). Physical GPU memory, from `xpu-smi discovery` (only once discovery has succeeded) |
| `intel_exporter_xpu_error` | gauge | | 1 if there is no fresh `xpu-smi` reading, 0 otherwise (only present with `--intel-xpu-smi-path`) |
| `intel_exporter_hwmon_error` | gauge | | 1 if hwmon is enabled but unreadable (only present when hwmon is enabled) |
| `intel_exporter_last_collection_timestamp_seconds` | gauge | | **Legacy**. Unix time of the last `intel_gpu_top` sample |
| `intel_exporter_collection_error` | gauge | | **Legacy** (`gpu_exporter_collection_error`). 1 if there is no fresh sample (process failing, no data yet, or sample older than 3 sampling periods + 5s), 0 otherwise. GPU metrics are omitted while it is 1. |

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
