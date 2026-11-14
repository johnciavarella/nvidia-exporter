package collector

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/woo/nvidia-exporter/internal/intel"
	"github.com/woo/nvidia-exporter/internal/nvidia"
)

func TestNvidiaCommon_OmitsNA(t *testing.T) {
	g := nvidia.GPUStats{
		Index: 0, Name: "RTX", UUID: "GPU-1",
		UtilizationGPU: 50, Temperature: 65, PowerDraw: 0, FanSpeed: 0, ClockGraphics: 1800,
		NA: map[string]bool{"power.draw": true, "fan.speed": true},
	}
	s := nvidiaCommon(g)
	if s.PowerDraw != nil || s.FanRatio != nil {
		t.Errorf("N/A fields exported: power=%v fan=%v", s.PowerDraw, s.FanRatio)
	}
	if s.Utilization == nil || *s.Utilization != 0.5 {
		t.Errorf("utilization = %v", s.Utilization)
	}
	if s.ClockGraphics == nil || *s.ClockGraphics != 1.8e9 {
		t.Errorf("clock = %v", s.ClockGraphics)
	}
	if s.Temps["gpu"] != 65 {
		t.Errorf("temps = %v", s.Temps)
	}
}

func TestCollector_LegacyOff(t *testing.T) {
	mock := nvidia.NewMockProvider()
	mock.SetStats([]nvidia.GPUStats{{Index: 0, Name: "Test GPU", UUID: "GPU-u", UtilizationGPU: 50, PerformanceState: 2}})
	c := New(mock, time.Second, testLogger()).SetLegacy(false)
	c.collect(context.Background())

	if n := testutil.CollectAndCount(c, "nvidia_gpu_utilization_ratio"); n != 0 {
		t.Errorf("legacy metric emitted with legacy off")
	}
	expected := `
# HELP gpu_utilization_ratio GPU utilization (0-1). Intel: time out of RC6, bounded by the engine busy times.
# TYPE gpu_utilization_ratio gauge
gpu_utilization_ratio{gpu="0",id="GPU-u",name="Test GPU",vendor="nvidia"} 0.5
# HELP nvidia_gpu_performance_state Performance state (0=P0 max performance, 15=P15 min)
# TYPE nvidia_gpu_performance_state gauge
nvidia_gpu_performance_state{gpu="0",name="Test GPU",uuid="GPU-u"} 2
`
	if err := testutil.CollectAndCompare(c, strings.NewReader(expected), "gpu_utilization_ratio", "nvidia_gpu_performance_state"); err != nil {
		t.Error(err)
	}
	if problems, err := testutil.CollectAndLint(c); err != nil || len(problems) > 0 {
		t.Errorf("lint: %v %v", problems, err)
	}
}

func TestIntelCollector_Common(t *testing.T) {
	now := time.Unix(1700000000, 0)
	total := 4e9
	mock := intel.NewMockProvider()
	mock.SetSample(testSample(), now)
	c := NewIntel(mock, "default", time.Minute).
		WithXPU(fakeXPU{stats: intel.XPUStats{MemoryUsedBytes: 1e9, MemoryTotalBytes: &total}, at: now}).
		SetLegacy(false)
	c.now = func() time.Time { return now.Add(time.Second) }

	const l = `gpu="0",id="default",name="",vendor="intel"`
	expected := `
# HELP gpu_utilization_ratio GPU utilization (0-1). Intel: time out of RC6, bounded by the engine busy times.
# TYPE gpu_utilization_ratio gauge
gpu_utilization_ratio{` + l + `} 0.8
# HELP gpu_clock_graphics_hz Graphics clock in Hz
# TYPE gpu_clock_graphics_hz gauge
gpu_clock_graphics_hz{` + l + `} 1.1805e+09
# HELP gpu_power_draw_watts GPU power draw in Watts
# TYPE gpu_power_draw_watts gauge
gpu_power_draw_watts{` + l + `} 14.25
# HELP gpu_memory_total_bytes GPU memory total in bytes
# TYPE gpu_memory_total_bytes gauge
gpu_memory_total_bytes{` + l + `} 4e+09
# HELP gpu_exporter_collection_error 1 if the last collection from the GPU backend failed, 0 otherwise
# TYPE gpu_exporter_collection_error gauge
gpu_exporter_collection_error{vendor="intel"} 0
`
	if err := testutil.CollectAndCompare(c, strings.NewReader(expected),
		"gpu_utilization_ratio", "gpu_clock_graphics_hz", "gpu_power_draw_watts", "gpu_memory_total_bytes", "gpu_exporter_collection_error"); err != nil {
		t.Error(err)
	}
	for _, name := range []string{"intel_gpu_memory_used_bytes", "intel_gpu_frequency_actual_hz", "intel_exporter_collection_error"} {
		if n := testutil.CollectAndCount(c, name); n != 0 {
			t.Errorf("%s emitted with legacy off", name)
		}
	}
	// Engine metrics have no common equivalent and stay.
	if n := testutil.CollectAndCount(c, "intel_gpu_engine_busy_ratio"); n != 2 {
		t.Errorf("engine series = %d, want 2", n)
	}

	// intel_gpu_top failing: xpu memory is still exported, the error is flagged.
	mock.SetError(errors.New("down"))
	if n := testutil.CollectAndCount(c, "gpu_memory_used_bytes"); n != 1 {
		t.Errorf("memory series while intel_gpu_top fails = %d, want 1", n)
	}
	if n := testutil.CollectAndCount(c, "gpu_utilization_ratio"); n != 0 {
		t.Errorf("utilization exported while intel_gpu_top fails")
	}
}

func TestIntelUtilization(t *testing.T) {
	engines := func(busy ...float64) map[string]intel.EngineStats {
		m := map[string]intel.EngineStats{}
		for i, b := range busy {
			m[string(rune('a'+i))] = intel.EngineStats{Busy: b}
		}
		return m
	}
	cases := []struct {
		name string
		s    intel.Sample
		want float64
	}{
		{"rc6 inside bounds", intel.Sample{RC6: 50, HasRC6: true, Engines: engines(30, 40)}, 0.5},
		{"rc6 disabled, capped at engine sum", intel.Sample{RC6: 0, HasRC6: true, Engines: engines(10, 5)}, 0.15},
		{"awake but idle", intel.Sample{RC6: 20, HasRC6: true, Engines: engines(0, 0)}, 0},
		{"rc6 below busiest engine", intel.Sample{RC6: 90, HasRC6: true, Engines: engines(60, 20)}, 0.6},
		{"overlapping engines saturate at 1", intel.Sample{RC6: 0, HasRC6: true, Engines: engines(90, 80)}, 1},
		{"no rc6: busiest engine", intel.Sample{Engines: engines(30, 70)}, 0.7},
	}
	for _, tc := range cases {
		if got := intelUtilization(tc.s); got < tc.want-1e-9 || got > tc.want+1e-9 {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}
