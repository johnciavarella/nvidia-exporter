package collector

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// commonNamespace prefixes the vendor-neutral metrics that every backend emits
// under the same names, units and labels, so one dashboard or alert works for
// NVIDIA, AMD and Intel GPUs.
const commonNamespace = "gpu"

// commonLabels are the labels of every per-GPU common metric:
//   - gpu:    index of the GPU on this host (nvidia-smi index, DRM card number
//     for AMD, 0 for Intel)
//   - vendor: nvidia, amd or intel
//   - name:   product name, or PCI "vendor:device" ID when the driver gives none
//   - id:     stable identifier (NVIDIA UUID, otherwise PCI address)
var commonLabels = []string{"gpu", "vendor", "name", "id"}

// gpuStats is one GPU's reading in vendor-neutral units. A nil value means the
// backend or this GPU does not report it, and the metric is omitted.
type gpuStats struct {
	Index, Vendor, Name, ID string

	Utilization       *float64           // 0-1
	MemoryUtilization *float64           // 0-1
	MemoryUsed        *float64           // bytes
	MemoryTotal       *float64           // bytes
	Temps             map[string]float64 // °C by sensor name
	PowerDraw         *float64           // W
	PowerLimit        *float64           // W
	EnergyJoules      *float64           // cumulative J
	FanRatio          *float64           // 0-1
	FanRPM            *float64           // RPM
	ClockGraphics     *float64           // Hz
	ClockMemory       *float64           // Hz
	Voltage           *float64           // V
}

func (s gpuStats) labels() []string { return []string{s.Index, s.Vendor, s.Name, s.ID} }

// commonDescs holds the descriptors of the common metrics.
type commonDescs struct {
	utilization, memUtilization, memUsed, memTotal *prometheus.Desc
	temperature, powerDraw, powerLimit, energy     *prometheus.Desc
	fanRatio, fanRPM, clockGraphics, clockMemory   *prometheus.Desc
	voltage                                        *prometheus.Desc
	collectError, collectTime                      *prometheus.Desc
}

func newCommonDescs() *commonDescs {
	desc := func(sub, name, help string, extra ...string) *prometheus.Desc {
		return prometheus.NewDesc(prometheus.BuildFQName(commonNamespace, sub, name), help,
			append(append([]string{}, commonLabels...), extra...), nil)
	}
	exporter := func(name, help string) *prometheus.Desc {
		return prometheus.NewDesc(prometheus.BuildFQName(commonNamespace, "exporter", name), help, []string{"vendor"}, nil)
	}
	return &commonDescs{
		utilization:    desc("", "utilization_ratio", "GPU utilization (0-1). Intel: the busiest engine."),
		memUtilization: desc("memory", "utilization_ratio", "Memory controller utilization (0-1)"),
		memUsed:        desc("memory", "used_bytes", "GPU memory used in bytes"),
		memTotal:       desc("memory", "total_bytes", "GPU memory total in bytes"),
		temperature:    desc("", "temperature_celsius", "GPU temperature in degrees Celsius by sensor", "sensor"),
		powerDraw:      desc("", "power_draw_watts", "GPU power draw in Watts"),
		powerLimit:     desc("", "power_limit_watts", "GPU power limit in Watts"),
		energy:         desc("", "energy_joules_total", "Cumulative GPU energy in Joules"),
		fanRatio:       desc("", "fan_speed_ratio", "Fan speed (0-1)"),
		fanRPM:         desc("", "fan_speed_rpm", "Fan speed in RPM"),
		clockGraphics:  desc("", "clock_graphics_hz", "Graphics clock in Hz"),
		clockMemory:    desc("", "clock_memory_hz", "Memory clock in Hz"),
		voltage:        desc("", "voltage_volts", "GPU core voltage in Volts"),
		collectError:   exporter("collection_error", "1 if the last collection from the GPU backend failed, 0 otherwise"),
		collectTime:    exporter("last_collection_timestamp_seconds", "Unix timestamp of the last successful collection"),
	}
}

func (d *commonDescs) describe(ch chan<- *prometheus.Desc) {
	for _, x := range []*prometheus.Desc{
		d.utilization, d.memUtilization, d.memUsed, d.memTotal, d.temperature,
		d.powerDraw, d.powerLimit, d.energy, d.fanRatio, d.fanRPM,
		d.clockGraphics, d.clockMemory, d.voltage, d.collectError, d.collectTime,
	} {
		ch <- x
	}
}

// emit sends the metrics of one GPU, skipping values that are nil.
func (d *commonDescs) emit(ch chan<- prometheus.Metric, s gpuStats) {
	l := s.labels()
	opt := func(desc *prometheus.Desc, t prometheus.ValueType, v *float64) {
		if v != nil {
			ch <- prometheus.MustNewConstMetric(desc, t, *v, l...)
		}
	}
	opt(d.utilization, prometheus.GaugeValue, s.Utilization)
	opt(d.memUtilization, prometheus.GaugeValue, s.MemoryUtilization)
	opt(d.memUsed, prometheus.GaugeValue, s.MemoryUsed)
	opt(d.memTotal, prometheus.GaugeValue, s.MemoryTotal)
	opt(d.powerDraw, prometheus.GaugeValue, s.PowerDraw)
	opt(d.powerLimit, prometheus.GaugeValue, s.PowerLimit)
	opt(d.energy, prometheus.CounterValue, s.EnergyJoules)
	opt(d.fanRatio, prometheus.GaugeValue, s.FanRatio)
	opt(d.fanRPM, prometheus.GaugeValue, s.FanRPM)
	opt(d.clockGraphics, prometheus.GaugeValue, s.ClockGraphics)
	opt(d.clockMemory, prometheus.GaugeValue, s.ClockMemory)
	opt(d.voltage, prometheus.GaugeValue, s.Voltage)
	for sensor, v := range s.Temps {
		ch <- prometheus.MustNewConstMetric(d.temperature, prometheus.GaugeValue, v, append(l, sensor)...)
	}
}

// emitExporter sends gpu_exporter_collection_error and, when last is set,
// gpu_exporter_last_collection_timestamp_seconds.
func (d *commonDescs) emitExporter(ch chan<- prometheus.Metric, vendor string, last time.Time, failed bool) {
	v := 0.0
	if failed {
		v = 1
	}
	ch <- prometheus.MustNewConstMetric(d.collectError, prometheus.GaugeValue, v, vendor)
	if !last.IsZero() {
		ch <- prometheus.MustNewConstMetric(d.collectTime, prometheus.GaugeValue, float64(last.Unix()), vendor)
	}
}

func f64(v float64) *float64 { return &v }
