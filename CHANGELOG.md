# Changelog

Each release's binaries are built by pushing its tag (`git tag vX.Y.Z && git push origin vX.Y.Z`). The release notes are taken from that version's section below.

## [v0.5.0]

AMD GPU support, and one set of metrics for every GPU vendor.

### Added

- **AMD backend** (`--backend=amd`). It reads the `amdgpu` kernel driver through sysfs when Prometheus scrapes, and needs no ROCm, `amd-smi` or root.
  - Exports every amdgpu card on the host. Cards are found on each scrape, so a hot-plugged or rebound GPU is picked up without a restart.
  - Reports utilization, VRAM, temperatures (edge, junction, mem), power and power cap, fan, graphics and memory clocks, and voltage, when the card provides them.
  - A GPU in runtime power-management sleep is not woken up: `amd_gpu_runtime_suspended` is 1 and its sensors are not read.
  - Also exports `amd_gpu_gtt_used_bytes` and `amd_gpu_gtt_total_bytes`.
  - `--amd-sysfs-root` (default `/sys`) is for containers that mount the host's `/sys` elsewhere.
- **Common `gpu_*` metrics** from every backend, with the same names, units and labels (`gpu`, `vendor`, `name`, `id`), so one dashboard or alert works for NVIDIA, AMD and Intel:
  `gpu_utilization_ratio`, `gpu_memory_utilization_ratio`, `gpu_memory_used_bytes`, `gpu_memory_total_bytes`, `gpu_temperature_celsius{sensor}`, `gpu_power_draw_watts`, `gpu_power_limit_watts`, `gpu_energy_joules_total`, `gpu_fan_speed_ratio`, `gpu_fan_speed_rpm`, `gpu_clock_graphics_hz`, `gpu_clock_memory_hz`, `gpu_voltage_volts`, `gpu_exporter_collection_error{vendor}`, `gpu_exporter_last_collection_timestamp_seconds{vendor}`.
  A metric is left out when the GPU does not report it.
- **Overall utilization on Intel.** `gpu_utilization_ratio` is estimated as the time the GPU was out of RC6 sleep, kept between the busiest engine and the sum of all engines. Without RC6 it is the busiest engine. See "Intel utilization" in the README.
- `--legacy-metrics` (default `true`) controls the `nvidia_*`/`intel_*` metrics that duplicate a `gpu_*` metric.
- `--version` flag, the version in the startup log, and a `gpu_exporter_build_info{version}` metric.

### Changed

- nvidia-smi `[N/A]` / `[Not Supported]` values are left out of the `gpu_*` metrics. The legacy `nvidia_*` metrics still report them as 0.
- `intel_gpu_rc6_ratio` is left out when `intel_gpu_top` does not report RC6, instead of being 0.
- Release notes now come from this changelog.

### Upgrading

Nothing breaks: every existing metric is still exported by default. To move to the common metrics:

1. Point dashboards and alerts at `gpu_*`. The `nvidia_*` → `gpu_*` mapping is in the README. NVIDIA temperature is `gpu_temperature_celsius{sensor="gpu"}`.
2. Run with `--legacy-metrics=false` to stop exporting the duplicates.

The legacy metrics may be turned off by default in a later release.

## [v0.4.0]

### Added

- Intel: GPU memory used and total from Intel's `xpu-smi` (opt-in with `--intel-xpu-smi-path`; needs root).

## [v0.3.0]

### Added

- Intel: power, energy, power limit, voltage, temperature and fan from the i915/xe hwmon interface (`--intel-hwmon`, on by default when found).

## [v0.2.0]

### Added

- Intel GPU backend (`--backend=intel`) using `intel_gpu_top`.

## [v0.1.0]

- First release: NVIDIA metrics via `nvidia-smi`, with CI and release workflows.

[v0.5.0]: https://github.com/johnciavarella/nvidia-exporter/compare/v0.4.0...v0.5.0
[v0.4.0]: https://github.com/johnciavarella/nvidia-exporter/compare/v0.3.0...v0.4.0
[v0.3.0]: https://github.com/johnciavarella/nvidia-exporter/compare/v0.2.0...v0.3.0
[v0.2.0]: https://github.com/johnciavarella/nvidia-exporter/compare/v0.1.0...v0.2.0
[v0.1.0]: https://github.com/johnciavarella/nvidia-exporter/releases/tag/v0.1.0
