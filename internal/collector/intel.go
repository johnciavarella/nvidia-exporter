package collector

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/woo/nvidia-exporter/internal/intel"
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
		hwmonError: prometheus.NewDesc(
			prometheus.BuildFQName(intelNamespace, "exporter", "hwmon_error"),
			"1 if the hwmon sensors could not be read, 0 otherwise (only present when hwmon is enabled)",
			nil, constLabels,
		),
	}
}

// WithHwmon enables the hwmon sensor metrics (power, energy, voltage,
// temperature, fan), which are read at scrape time and do not depend on
// intel_gpu_top. It returns the collector for chaining.
func (c *IntelCollector) WithHwmon(h *intel.Hwmon) *IntelCollector {
	c.hwmon = h
	return c
}

// Describe implements prometheus.Collector.
func (c *IntelCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{
		c.engineBusy, c.engineSema, c.engineWait, c.freqActual, c.freqRequested,
		c.rc6, c.interrupts, c.powerGPU, c.powerPackage, c.imcRead, c.imcWrite,
		c.collectTime, c.collectError,
	} {
		ch <- d
	}
	if c.hwmon != nil {
		for _, d := range []*prometheus.Desc{
			c.hwEnergy, c.hwPower, c.hwPowerLimit, c.hwVoltage, c.hwTemp, c.hwFan, c.hwmonError,
		} {
			ch <- d
		}
	}
}

func (c *IntelCollector) collectHwmon(ch chan<- prometheus.Metric) {
	st, err := c.hwmon.Read()
	if err != nil {
		ch <- prometheus.MustNewConstMetric(c.hwmonError, prometheus.GaugeValue, 1)
		return
	}
	ch <- prometheus.MustNewConstMetric(c.hwmonError, prometheus.GaugeValue, 0)
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
func (c *IntelCollector) Collect(ch chan<- prometheus.Metric) {
	if c.hwmon != nil {
		c.collectHwmon(ch)
	}

	s, at, err := c.provider.Latest()

	if err != nil || c.now().Sub(at) > c.maxAge {
		ch <- prometheus.MustNewConstMetric(c.collectError, prometheus.GaugeValue, 1)
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
	gauge(c.freqActual, s.FrequencyActual*1e6)
	gauge(c.freqRequested, s.FrequencyRequested*1e6)
	gauge(c.rc6, s.RC6/100)
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

	gauge(c.collectTime, float64(at.Unix()))
	gauge(c.collectError, 0)
}
