package collector

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/woo/nvidia-exporter/internal/intel"
)

func ptr(v float64) *float64 { return &v }

func testSample() intel.Sample {
	return intel.Sample{
		Period:             5 * time.Second,
		FrequencyActual:    1180.5,
		FrequencyRequested: 1200,
		Interrupts:         1523.5,
		RC6:                62.5,
		PowerGPU:           ptr(14.25),
		PowerPackage:       ptr(28.5),
		IMCReads:           ptr(100),
		IMCWrites:          ptr(50),
		Engines: map[string]intel.EngineStats{
			"Render/3D/0": {Busy: 37.5, Sema: 1.25, Wait: 2.5},
			"Video/0":     {Busy: 80},
		},
	}
}

func TestIntelCollector_EmitsMetrics(t *testing.T) {
	mock := intel.NewMockProvider()
	now := time.Unix(1700000000, 0)
	mock.SetSample(testSample(), now)

	c := NewIntel(mock, "default", time.Minute)
	c.now = func() time.Time { return now.Add(time.Second) }

	cases := []struct{ metric, expected string }{
		{"intel_gpu_engine_busy_ratio", `
# HELP intel_gpu_engine_busy_ratio Fraction of the sampling period the engine was busy (0-1)
# TYPE intel_gpu_engine_busy_ratio gauge
intel_gpu_engine_busy_ratio{device="default",engine="Render/3D/0"} 0.375
intel_gpu_engine_busy_ratio{device="default",engine="Video/0"} 0.8
`},
		{"intel_gpu_frequency_actual_hz", `
# HELP intel_gpu_frequency_actual_hz Actual GPU frequency in Hz
# TYPE intel_gpu_frequency_actual_hz gauge
intel_gpu_frequency_actual_hz{device="default"} 1.1805e+09
`},
		{"intel_gpu_rc6_ratio", `
# HELP intel_gpu_rc6_ratio Fraction of the sampling period the GPU spent in the RC6 power-saving state (0-1)
# TYPE intel_gpu_rc6_ratio gauge
intel_gpu_rc6_ratio{device="default"} 0.625
`},
		{"intel_gpu_power_gpu_watts", `
# HELP intel_gpu_power_gpu_watts GPU power draw in Watts
# TYPE intel_gpu_power_gpu_watts gauge
intel_gpu_power_gpu_watts{device="default"} 14.25
`},
		{"intel_gpu_imc_read_bytes_per_second", `
# HELP intel_gpu_imc_read_bytes_per_second Memory controller read bandwidth in bytes per second
# TYPE intel_gpu_imc_read_bytes_per_second gauge
intel_gpu_imc_read_bytes_per_second{device="default"} 1.048576e+08
`},
		{"intel_exporter_collection_error", `
# HELP intel_exporter_collection_error 1 if there is no fresh intel_gpu_top sample, 0 otherwise
# TYPE intel_exporter_collection_error gauge
intel_exporter_collection_error{device="default"} 0
`},
		{"intel_exporter_last_collection_timestamp_seconds", `
# HELP intel_exporter_last_collection_timestamp_seconds Unix timestamp of the last intel_gpu_top sample
# TYPE intel_exporter_last_collection_timestamp_seconds gauge
intel_exporter_last_collection_timestamp_seconds{device="default"} 1.7e+09
`},
	}
	for _, tc := range cases {
		if err := testutil.CollectAndCompare(c, strings.NewReader(tc.expected), tc.metric); err != nil {
			t.Errorf("%s: %v", tc.metric, err)
		}
	}
}

func TestIntelCollector_OmitsAbsentOptionalMetrics(t *testing.T) {
	mock := intel.NewMockProvider()
	now := time.Now()
	s := testSample()
	s.PowerPackage, s.IMCReads, s.IMCWrites = nil, nil, nil
	mock.SetSample(s, now)

	c := NewIntel(mock, "default", time.Minute)
	for _, name := range []string{
		"intel_gpu_power_package_watts",
		"intel_gpu_imc_read_bytes_per_second",
		"intel_gpu_imc_write_bytes_per_second",
	} {
		if n := testutil.CollectAndCount(c, name); n != 0 {
			t.Errorf("%s: got %d series, want 0 when the GPU does not report it", name, n)
		}
	}
	if n := testutil.CollectAndCount(c, "intel_gpu_power_gpu_watts"); n != 1 {
		t.Errorf("intel_gpu_power_gpu_watts: got %d series, want 1", n)
	}
}

func TestIntelCollector_ErrorHidesGPUMetrics(t *testing.T) {
	mock := intel.NewMockProvider()
	mock.SetSample(testSample(), time.Now())
	mock.SetError(errors.New("intel_gpu_top exited"))

	c := NewIntel(mock, "default", time.Minute)

	if n := testutil.CollectAndCount(c, "intel_gpu_engine_busy_ratio"); n != 0 {
		t.Errorf("got %d engine series while intel_gpu_top is failing, want 0", n)
	}
	expected := `
# HELP intel_exporter_collection_error 1 if there is no fresh intel_gpu_top sample, 0 otherwise
# TYPE intel_exporter_collection_error gauge
intel_exporter_collection_error{device="default"} 1
`
	if err := testutil.CollectAndCompare(c, strings.NewReader(expected), "intel_exporter_collection_error"); err != nil {
		t.Error(err)
	}
}

func TestIntelCollector_NoSampleYet(t *testing.T) {
	c := NewIntel(intel.NewMockProvider(), "default", time.Minute)
	expected := `
# HELP intel_exporter_collection_error 1 if there is no fresh intel_gpu_top sample, 0 otherwise
# TYPE intel_exporter_collection_error gauge
intel_exporter_collection_error{device="default"} 1
`
	if err := testutil.CollectAndCompare(c, strings.NewReader(expected), "intel_exporter_collection_error"); err != nil {
		t.Error(err)
	}
}

func TestIntelCollector_StaleSampleIsAnError(t *testing.T) {
	mock := intel.NewMockProvider()
	at := time.Unix(1700000000, 0)
	mock.SetSample(testSample(), at)

	c := NewIntel(mock, "default", 30*time.Second)
	c.now = func() time.Time { return at.Add(31 * time.Second) }

	if n := testutil.CollectAndCount(c, "intel_gpu_engine_busy_ratio"); n != 0 {
		t.Errorf("got %d engine series for a stale sample, want 0", n)
	}
	expected := `
# HELP intel_exporter_collection_error 1 if there is no fresh intel_gpu_top sample, 0 otherwise
# TYPE intel_exporter_collection_error gauge
intel_exporter_collection_error{device="default"} 1
`
	if err := testutil.CollectAndCompare(c, strings.NewReader(expected), "intel_exporter_collection_error"); err != nil {
		t.Error(err)
	}
}

func TestIntelCollector_Hwmon(t *testing.T) {
	dir := t.TempDir()
	set := func(name, v string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(v+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	set("energy1_input", "10000000")
	set("power1_max", "31250000")
	set("temp1_input", "45000")
	set("temp1_label", "pkg")

	mock := intel.NewMockProvider()
	mock.SetError(errors.New("intel_gpu_top failing"))
	c := NewIntel(mock, "default", time.Minute).WithHwmon(intel.NewHwmon(dir))

	// First scrape: energy and limit, no derived power yet. hwmon is reported
	// even though intel_gpu_top is failing.
	if err := testutil.CollectAndCompare(c, strings.NewReader(`
# HELP intel_gpu_hwmon_energy_joules_total Cumulative GPU energy from the hwmon energy counter in Joules (rate() gives Watts)
# TYPE intel_gpu_hwmon_energy_joules_total counter
intel_gpu_hwmon_energy_joules_total{device="default"} 10
# HELP intel_gpu_hwmon_power_limit_watts GPU power limit in Watts from hwmon (if reported)
# TYPE intel_gpu_hwmon_power_limit_watts gauge
intel_gpu_hwmon_power_limit_watts{device="default"} 31.25
# HELP intel_gpu_hwmon_temperature_celsius GPU temperature in degrees Celsius from hwmon
# TYPE intel_gpu_hwmon_temperature_celsius gauge
intel_gpu_hwmon_temperature_celsius{device="default",sensor="pkg"} 45
# HELP intel_exporter_hwmon_error 1 if the hwmon sensors could not be read, 0 otherwise (only present when hwmon is enabled)
# TYPE intel_exporter_hwmon_error gauge
intel_exporter_hwmon_error{device="default"} 0
# HELP intel_exporter_collection_error 1 if there is no fresh intel_gpu_top sample, 0 otherwise
# TYPE intel_exporter_collection_error gauge
intel_exporter_collection_error{device="default"} 1
`)); err != nil {
		t.Fatal(err)
	}

	// Second scrape after the counter advanced: power is derived.
	time.Sleep(150 * time.Millisecond)
	set("energy1_input", "11000000")
	n, err := testutil.GatherAndCount(prometheusGatherer(c), "intel_gpu_hwmon_power_watts")
	if err != nil || n != 1 {
		t.Errorf("power metric count = %d, err = %v, want 1", n, err)
	}

	// Unreadable hwmon is reported without breaking the rest.
	if err := os.Remove(filepath.Join(dir, "energy1_input")); err != nil {
		t.Fatal(err)
	}
	n, _ = testutil.GatherAndCount(prometheusGatherer(c), "intel_exporter_hwmon_error")
	if n != 1 {
		t.Errorf("hwmon_error count = %d, want 1", n)
	}
}

func prometheusGatherer(c prometheus.Collector) prometheus.Gatherer {
	reg := prometheus.NewRegistry()
	reg.MustRegister(c)
	return reg
}
