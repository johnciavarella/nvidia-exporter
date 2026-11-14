package collector

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/woo/nvidia-exporter/internal/nvidia"
)

const namespace = "nvidia"

// Collector implements prometheus.Collector for NVIDIA GPU metrics.
type Collector struct {
	provider nvidia.Provider
	interval time.Duration
	logger   *slog.Logger
	legacy   bool
	common   *commonDescs

	mu         sync.RWMutex
	stats      []nvidia.GPUStats
	lastUpdate time.Time
	lastError  error

	// GPU metric descriptors
	gpuUtilization *prometheus.Desc
	memUtilization *prometheus.Desc
	memUsed        *prometheus.Desc
	memTotal       *prometheus.Desc
	temperature    *prometheus.Desc
	powerDraw      *prometheus.Desc
	powerLimit     *prometheus.Desc
	fanSpeed       *prometheus.Desc
	clockGraphics  *prometheus.Desc
	clockMemory    *prometheus.Desc
	perfState      *prometheus.Desc

	// Exporter meta metric descriptors
	collectTimestamp *prometheus.Desc
	collectDuration  *prometheus.Desc
	collectError     *prometheus.Desc
}

// New creates a new Collector.
func New(provider nvidia.Provider, interval time.Duration, logger *slog.Logger) *Collector {
	labels := []string{"gpu", "name", "uuid"}

	return &Collector{
		provider: provider,
		interval: interval,
		logger:   logger,
		legacy:   true,
		common:   newCommonDescs(),

		gpuUtilization: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "gpu", "utilization_ratio"),
			"GPU utilization ratio (0-1)",
			labels, nil,
		),
		memUtilization: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "memory", "utilization_ratio"),
			"Memory utilization ratio (0-1)",
			labels, nil,
		),
		memUsed: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "memory", "used_bytes"),
			"Memory used in bytes",
			labels, nil,
		),
		memTotal: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "memory", "total_bytes"),
			"Memory total in bytes",
			labels, nil,
		),
		temperature: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "gpu", "temperature_celsius"),
			"GPU temperature in Celsius",
			labels, nil,
		),
		powerDraw: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "gpu", "power_draw_watts"),
			"Power draw in Watts",
			labels, nil,
		),
		powerLimit: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "gpu", "power_limit_watts"),
			"Power limit in Watts",
			labels, nil,
		),
		fanSpeed: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "gpu", "fan_speed_ratio"),
			"Fan speed ratio (0-1)",
			labels, nil,
		),
		clockGraphics: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "gpu", "clock_graphics_hz"),
			"Graphics clock in Hz",
			labels, nil,
		),
		clockMemory: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "gpu", "clock_memory_hz"),
			"Memory clock in Hz",
			labels, nil,
		),
		perfState: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "gpu", "performance_state"),
			"Performance state (0=P0 max performance, 15=P15 min)",
			labels, nil,
		),

		collectTimestamp: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "exporter", "last_collection_timestamp_seconds"),
			"Unix timestamp of last successful collection",
			nil, nil,
		),
		collectDuration: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "exporter", "collection_duration_seconds"),
			"Duration of last collection in seconds",
			nil, nil,
		),
		collectError: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "exporter", "collection_error"),
			"1 if last collection failed, 0 otherwise",
			nil, nil,
		),
	}
}

// SetLegacy turns the nvidia_* metrics that duplicate the common gpu_* metrics
// on or off (default on). nvidia_gpu_performance_state has no common
// equivalent and is always exported.
func (c *Collector) SetLegacy(on bool) *Collector {
	c.legacy = on
	return c
}

// Describe implements prometheus.Collector.
func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	c.common.describe(ch)
	ch <- c.perfState
	if !c.legacy {
		return
	}
	ch <- c.gpuUtilization
	ch <- c.memUtilization
	ch <- c.memUsed
	ch <- c.memTotal
	ch <- c.temperature
	ch <- c.powerDraw
	ch <- c.powerLimit
	ch <- c.fanSpeed
	ch <- c.clockGraphics
	ch <- c.clockMemory
	ch <- c.collectTimestamp
	ch <- c.collectDuration
	ch <- c.collectError
}

// Collect implements prometheus.Collector.
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	c.mu.RLock()
	stats := c.stats
	lastUpdate := c.lastUpdate
	lastError := c.lastError
	c.mu.RUnlock()

	// Emit GPU metrics
	for _, gpu := range stats {
		c.common.emit(ch, nvidiaCommon(gpu))
		labels := []string{
			strconv.Itoa(gpu.Index),
			gpu.Name,
			gpu.UUID,
		}
		ch <- prometheus.MustNewConstMetric(c.perfState, prometheus.GaugeValue, float64(gpu.PerformanceState), labels...)
		if !c.legacy {
			continue
		}

		ch <- prometheus.MustNewConstMetric(c.gpuUtilization, prometheus.GaugeValue, gpu.UtilizationGPU/100, labels...)
		ch <- prometheus.MustNewConstMetric(c.memUtilization, prometheus.GaugeValue, gpu.UtilizationMem/100, labels...)
		ch <- prometheus.MustNewConstMetric(c.memUsed, prometheus.GaugeValue, float64(gpu.MemoryUsed), labels...)
		ch <- prometheus.MustNewConstMetric(c.memTotal, prometheus.GaugeValue, float64(gpu.MemoryTotal), labels...)
		ch <- prometheus.MustNewConstMetric(c.temperature, prometheus.GaugeValue, gpu.Temperature, labels...)
		ch <- prometheus.MustNewConstMetric(c.powerDraw, prometheus.GaugeValue, gpu.PowerDraw, labels...)
		ch <- prometheus.MustNewConstMetric(c.powerLimit, prometheus.GaugeValue, gpu.PowerLimit, labels...)
		ch <- prometheus.MustNewConstMetric(c.fanSpeed, prometheus.GaugeValue, gpu.FanSpeed/100, labels...)
		ch <- prometheus.MustNewConstMetric(c.clockGraphics, prometheus.GaugeValue, gpu.ClockGraphics*1e6, labels...)
		ch <- prometheus.MustNewConstMetric(c.clockMemory, prometheus.GaugeValue, gpu.ClockMemory*1e6, labels...)
	}

	c.common.emitExporter(ch, "nvidia", lastUpdate, lastError != nil)
	if !c.legacy {
		return
	}

	// Emit exporter meta metrics
	if !lastUpdate.IsZero() {
		ch <- prometheus.MustNewConstMetric(c.collectTimestamp, prometheus.GaugeValue, float64(lastUpdate.Unix()))
	}

	errorVal := 0.0
	if lastError != nil {
		errorVal = 1.0
	}
	ch <- prometheus.MustNewConstMetric(c.collectError, prometheus.GaugeValue, errorVal)
}

// Run starts the collection loop and blocks until ctx is cancelled.
func (c *Collector) Run(ctx context.Context) {
	c.logger.Info("starting collector", "interval", c.interval)

	// Initial collection
	c.collect(ctx)

	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("collector stopped")
			return
		case <-ticker.C:
			c.collect(ctx)
		}
	}
}

// collect performs a single collection.
func (c *Collector) collect(ctx context.Context) {
	start := time.Now()

	stats, err := c.provider.Gather(ctx)

	duration := time.Since(start)

	c.mu.Lock()
	if err != nil {
		c.logger.Error("collection failed", "error", err, "duration", duration)
		c.lastError = err
		// Keep stale stats on error
	} else {
		c.logger.Debug("collection succeeded", "gpus", len(stats), "duration", duration)
		c.stats = stats
		c.lastUpdate = time.Now()
		c.lastError = nil
	}
	c.mu.Unlock()
}

// nvidiaCommon converts nvidia-smi stats to the common metrics, leaving out
// the fields nvidia-smi reported as [N/A] or [Not Supported].
func nvidiaCommon(g nvidia.GPUStats) gpuStats {
	opt := func(field string, v float64) *float64 {
		if !g.Supported(field) {
			return nil
		}
		return &v
	}
	s := gpuStats{
		Index:             strconv.Itoa(g.Index),
		Vendor:            "nvidia",
		Name:              g.Name,
		ID:                g.UUID,
		Utilization:       opt("utilization.gpu", g.UtilizationGPU/100),
		MemoryUtilization: opt("utilization.memory", g.UtilizationMem/100),
		MemoryUsed:        opt("memory.used", float64(g.MemoryUsed)),
		MemoryTotal:       opt("memory.total", float64(g.MemoryTotal)),
		PowerDraw:         opt("power.draw", g.PowerDraw),
		PowerLimit:        opt("power.limit", g.PowerLimit),
		FanRatio:          opt("fan.speed", g.FanSpeed/100),
		ClockGraphics:     opt("clocks.current.graphics", g.ClockGraphics*1e6),
		ClockMemory:       opt("clocks.current.memory", g.ClockMemory*1e6),
	}
	if g.Supported("temperature.gpu") {
		s.Temps = map[string]float64{"gpu": g.Temperature}
	}
	return s
}
