package collector

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/woo/nvidia-exporter/internal/intel"
	"github.com/woo/nvidia-exporter/internal/sysfs"
)

const intelNamespace = "intel"

// IntelCollector implements prometheus.Collector for Intel GPU metrics read
// from intel_gpu_top. It reads the provider's newest sample at scrape time and
// reports intel_exporter_collection_error=1 (with no GPU metrics) when the
// sample is missing, stale, or intel_gpu_top is failing.
type IntelCollector struct {
	provider intel.Provider
	maxAge   time.Duration
	now      func() time.Time
	legacy   bool
	common   *commonDescs
	name, id string // common "name" and "id" labels

	engineBusy    *prometheus.Desc
	engineSema    *prometheus.Desc
	engineWait    *prometheus.Desc
	freqActual    *prometheus.Desc
	freqRequested *prometheus.Desc
	rc6           *prometheus.Desc
	interrupts    *prometheus.Desc
	powerGPU      *prometheus.Desc
	powerPackage  *prometheus.Desc
	imcRead       *prometheus.Desc
	imcWrite      *prometheus.Desc
	collectTime   *prometheus.Desc
	collectError  *prometheus.Desc

	// Optional hwmon sensors (see WithHwmon).
	hwmon        *intel.Hwmon
	hwEnergy     *prometheus.Desc
	hwPower      *prometheus.Desc
	hwPowerLimit *prometheus.Desc
	hwVoltage    *prometheus.Desc
	hwTemp       *prometheus.Desc
	hwFan        *prometheus.Desc
	hwmonError   *prometheus.Desc

	// Optional xpu-smi memory metrics (see WithXPU).
	xpu      intel.XPUProvider
	memUsed  *prometheus.Desc
	memTotal *prometheus.Desc
	xpuError *prometheus.Desc
}

// NewIntel creates an IntelCollector. device is exposed as the "device" label
// (use "default" when intel_gpu_top picks the GPU itself). Samples older than
// maxAge are treated as a collection error.
func NewIntel(provider intel.Provider, device string, maxAge time.Duration) *IntelCollector {
	constLabels := prometheus.Labels{"device": device}
	engineLabels := []string{"engine"}
	desc := func(sub, name, help string, labels []string) *prometheus.Desc {
		return prometheus.NewDesc(prometheus.BuildFQName(intelNamespace, sub, name), help, labels, constLabels)
	}

	return &IntelCollector{
		provider: provider,
		maxAge:   maxAge,
		now:      time.Now,
		legacy:   true,
		common:   newCommonDescs(),
		id:       device,

		engineBusy:    desc("gpu", "engine_busy_ratio", "Fraction of the sampling period the engine was busy (0-1)", engineLabels),
		engineSema:    desc("gpu", "engine_semaphore_wait_ratio", "Fraction of the sampling period the engine waited on a semaphore (0-1)", engineLabels),
		engineWait:    desc("gpu", "engine_wait_ratio", "Fraction of the sampling period the engine waited on an event (0-1)", engineLabels),
		freqActual:    desc("gpu", "frequency_actual_hz", "Actual GPU frequency in Hz", nil),
		freqRequested: desc("gpu", "frequency_requested_hz", "Requested GPU frequency in Hz", nil),
		rc6:           desc("gpu", "rc6_ratio", "Fraction of the sampling period the GPU spent in the RC6 power-saving state (0-1)", nil),
		interrupts:    desc("gpu", "interrupts_per_second", "GPU interrupts per second", nil),
		powerGPU:      desc("gpu", "power_gpu_watts", "GPU power draw in Watts", nil),
		powerPackage:  desc("gpu", "power_package_watts", "Package power draw in Watts", nil),
		imcRead:       desc("gpu", "imc_read_bytes_per_second", "Memory controller read bandwidth in bytes per second", nil),
		imcWrite:      desc("gpu", "imc_write_bytes_per_second", "Memory controller write bandwidth in bytes per second", nil),

		collectTime: prometheus.NewDesc(
			prometheus.BuildFQName(intelNamespace, "exporter", "last_collection_timestamp_seconds"),
			"Unix timestamp of the last intel_gpu_top sample",
			nil, constLabels,
		),
		collectError: prometheus.NewDesc(
			prometheus.BuildFQName(intelNamespace, "exporter", "collection_error"),
			"1 if there is no fresh intel_gpu_top sample, 0 otherwise",
			nil, constLabels,
		),

		hwEnergy:     desc("gpu", "hwmon_energy_joules_total", "Cumulative GPU energy from the hwmon energy counter in Joules (rate() gives Watts)", nil),
		hwPower:      desc("gpu", "hwmon_power_watts", "GPU power draw in Watts, derived from the hwmon energy counter between two scrapes", nil),
		hwPowerLimit: desc("gpu", "hwmon_power_limit_watts", "GPU power limit in Watts from hwmon (if reported)", nil),
		hwVoltage:    desc("gpu", "hwmon_voltage_volts", "GPU voltage in Volts from hwmon (if reported)", nil),
		hwTemp:       desc("gpu", "hwmon_temperature_celsius", "GPU temperature in degrees Celsius from hwmon", []string{"sensor"}),
		hwFan:        desc("gpu", "hwmon_fan_rpm", "GPU fan speed in RPM from hwmon (if reported)", nil),
		memUsed:      desc("gpu", "memory_used_bytes", "GPU memory in use in bytes, from xpu-smi", nil),
		memTotal:     desc("gpu", "memory_total_bytes", "Physical GPU memory in bytes, from xpu-smi", nil),
		xpuError: prometheus.NewDesc(
			prometheus.BuildFQName(intelNamespace, "exporter", "xpu_error"),
			"1 if there is no fresh xpu-smi reading, 0 otherwise (only present when xpu-smi is enabled)",
			nil, constLabels,
		),
		hwmonError: prometheus.NewDesc(
			prometheus.BuildFQName(intelNamespace, "exporter", "hwmon_error"),
			"1 if the hwmon sensors could not be read, 0 otherwise (only present when hwmon is enabled)",
			nil, constLabels,
		),
	}
}

// WithXPU enables the xpu-smi memory metrics (used and total GPU memory). They
// do not depend on intel_gpu_top. A reading older than the collector's maxAge
// is reported as intel_exporter_xpu_error=1 with no memory metrics.
func (c *IntelCollector) WithXPU(x intel.XPUProvider) *IntelCollector {
	c.xpu = x
	return c
}

// WithHwmon enables the hwmon sensor metrics (power, energy, voltage,
// temperature, fan), which are read at scrape time and do not depend on
// intel_gpu_top. It returns the collector for chaining.
//
// The hwmon's PCI device also names the GPU in the common metrics: "id" becomes
// its PCI address and "name" its PCI ID.
func (c *IntelCollector) WithHwmon(h *intel.Hwmon) *IntelCollector {
	c.hwmon = h
	if slot := sysfs.PCISlot(h.DeviceDir()); slot != "" {
		c.id = slot
	}
	c.name = sysfs.PCIID(h.DeviceDir())
	return c
}

// SetLegacy turns the intel_* metrics that duplicate the common gpu_* metrics
// on or off (default on).
func (c *IntelCollector) SetLegacy(on bool) *IntelCollector {
	c.legacy = on
	return c
}

// Describe implements prometheus.Collector.
func (c *IntelCollector) Describe(ch chan<- *prometheus.Desc) {
	c.common.describe(ch)
	for _, d := range []*prometheus.Desc{
		c.engineBusy, c.engineSema, c.engineWait, c.freqRequested,
		c.rc6, c.interrupts, c.powerGPU, c.powerPackage, c.imcRead, c.imcWrite,
	} {
		ch <- d
	}
	if c.legacy {
		ch <- c.freqActual
		ch <- c.collectTime
		ch <- c.collectError
	}
	if c.xpu != nil {
		ch <- c.xpuError
		if c.legacy {
			ch <- c.memUsed
			ch <- c.memTotal
		}
	}
	if c.hwmon != nil {
		ch <- c.hwmonError
		if c.legacy {
			for _, d := range []*prometheus.Desc{
				c.hwEnergy, c.hwPower, c.hwPowerLimit, c.hwVoltage, c.hwTemp, c.hwFan,
			} {
				ch <- d
			}
		}
	}
}

func (c *IntelCollector) collectXPU(ch chan<- prometheus.Metric, cs *gpuStats) {
	st, at, err := c.xpu.Latest()
	if err != nil || c.now().Sub(at) > c.maxAge {
		ch <- prometheus.MustNewConstMetric(c.xpuError, prometheus.GaugeValue, 1)
		return
	}
	ch <- prometheus.MustNewConstMetric(c.xpuError, prometheus.GaugeValue, 0)
	cs.MemoryUsed = f64(st.MemoryUsedBytes)
	cs.MemoryTotal = st.MemoryTotalBytes
	if !c.legacy {
		return
	}
	ch <- prometheus.MustNewConstMetric(c.memUsed, prometheus.GaugeValue, st.MemoryUsedBytes)
	if st.MemoryTotalBytes != nil {
		ch <- prometheus.MustNewConstMetric(c.memTotal, prometheus.GaugeValue, *st.MemoryTotalBytes)
	}
}

func (c *IntelCollector) collectHwmon(ch chan<- prometheus.Metric, cs *gpuStats) {
	st, err := c.hwmon.Read()
	if err != nil {
		ch <- prometheus.MustNewConstMetric(c.hwmonError, prometheus.GaugeValue, 1)
		return
	}
	ch <- prometheus.MustNewConstMetric(c.hwmonError, prometheus.GaugeValue, 0)
	cs.EnergyJoules = f64(st.EnergyJoules)
	cs.PowerDraw = st.PowerWatts
	cs.PowerLimit = st.PowerLimit
	cs.Voltage = st.VoltageV
	cs.FanRPM = st.FanRPM
	if len(st.Temps) > 0 {
		cs.Temps = st.Temps
	}
	if !c.legacy {
		return
	}
	ch <- prometheus.MustNewConstMetric(c.hwEnergy, prometheus.CounterValue, st.EnergyJoules)
	opt := func(d *prometheus.Desc, v *float64) {
		if v != nil {
			ch <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, *v)
		}
	}
	opt(c.hwPower, st.PowerWatts)
	opt(c.hwPowerLimit, st.PowerLimit)
	opt(c.hwVoltage, st.VoltageV)
	opt(c.hwFan, st.FanRPM)
	for name, v := range st.Temps {
		ch <- prometheus.MustNewConstMetric(c.hwTemp, prometheus.GaugeValue, v, name)
	}
}

// Collect implements prometheus.Collector.
//
// The common gpu_* metrics combine all sources: hwmon and xpu-smi values are
// exported even while intel_gpu_top is failing.
func (c *IntelCollector) Collect(ch chan<- prometheus.Metric) {
	cs := gpuStats{Index: "0", Vendor: "intel", Name: c.name, ID: c.id}
	if c.hwmon != nil {
		c.collectHwmon(ch, &cs)
	}
	if c.xpu != nil {
		c.collectXPU(ch, &cs)
	}

	s, at, err := c.provider.Latest()
	fresh := err == nil && c.now().Sub(at) <= c.maxAge
	if fresh {
		cs.Utilization = f64(intelUtilization(s))
		cs.ClockGraphics = f64(s.FrequencyActual * 1e6)
		if cs.PowerDraw == nil && s.PowerGPU != nil {
			cs.PowerDraw = s.PowerGPU
		}
	}
	c.common.emit(ch, cs)
	if fresh {
		c.common.emitExporter(ch, "intel", at, false)
	} else {
		c.common.emitExporter(ch, "intel", time.Time{}, true)
	}

	if !fresh {
		if c.legacy {
			ch <- prometheus.MustNewConstMetric(c.collectError, prometheus.GaugeValue, 1)
		}
		return
	}

	gauge := func(d *prometheus.Desc, v float64, labels ...string) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, v, labels...)
	}

	for name, e := range s.Engines {
		gauge(c.engineBusy, e.Busy/100, name)
		gauge(c.engineSema, e.Sema/100, name)
		gauge(c.engineWait, e.Wait/100, name)
	}
	gauge(c.freqRequested, s.FrequencyRequested*1e6)
	if s.HasRC6 {
		gauge(c.rc6, s.RC6/100)
	}
	gauge(c.interrupts, s.Interrupts)
	if s.PowerGPU != nil {
		gauge(c.powerGPU, *s.PowerGPU)
	}
	if s.PowerPackage != nil {
		gauge(c.powerPackage, *s.PowerPackage)
	}
	if s.IMCReads != nil {
		gauge(c.imcRead, *s.IMCReads*1048576)
	}
	if s.IMCWrites != nil {
		gauge(c.imcWrite, *s.IMCWrites*1048576)
	}

	if c.legacy {
		gauge(c.freqActual, s.FrequencyActual*1e6)
		gauge(c.collectTime, float64(at.Unix()))
		gauge(c.collectError, 0)
	}
}

// intelUtilization estimates the fraction of the sampling period during which
// any engine was busy, which is what nvidia-smi's utilization.gpu means.
// intel_gpu_top only reports busy time per engine, and engines overlap, so the
// true value lies between the busiest engine and the sum of all engines
// (capped at 1). The time the GPU was out of RC6 (1 - RC6) estimates it
// directly but reads a little high: the GPU stays awake briefly after work,
// and reads 1 when RC6 is disabled. It is therefore clamped to the engine
// bounds. Without RC6 the busiest engine (the lower bound) is used.
func intelUtilization(s intel.Sample) float64 {
	lo, sum := 0.0, 0.0
	for _, e := range s.Engines {
		b := e.Busy / 100
		lo = max(lo, b)
		sum += b
	}
	if !s.HasRC6 {
		return lo
	}
	return min(max(1-s.RC6/100, lo), min(1, sum))
}
