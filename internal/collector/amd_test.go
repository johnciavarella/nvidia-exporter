package collector

import (
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/woo/nvidia-exporter/internal/amd"
)

func TestAMDCollector_EmitsCommonMetrics(t *testing.T) {
	sys := t.TempDir()
	dev, hw := amd.FakeRDNA3()
	if err := amd.FakeCard(sys, 1, "0000:03:00.0", dev, hw); err != nil {
		t.Fatal(err)
	}
	c := NewAMD(sys)
	c.now = func() time.Time { return time.Unix(1700000000, 0) }

	const l = `gpu="1",id="0000:03:00.0",name="1002:744c",vendor="amd"`
	expected := `
# HELP gpu_utilization_ratio GPU utilization (0-1). Intel: time out of RC6, bounded by the engine busy times.
# TYPE gpu_utilization_ratio gauge
gpu_utilization_ratio{` + l + `} 0.42
# HELP gpu_memory_used_bytes GPU memory used in bytes
# TYPE gpu_memory_used_bytes gauge
gpu_memory_used_bytes{` + l + `} 1.073741824e+09
# HELP gpu_power_draw_watts GPU power draw in Watts
# TYPE gpu_power_draw_watts gauge
gpu_power_draw_watts{` + l + `} 85
# HELP gpu_temperature_celsius GPU temperature in degrees Celsius by sensor
# TYPE gpu_temperature_celsius gauge
gpu_temperature_celsius{gpu="1",id="0000:03:00.0",name="1002:744c",sensor="edge",vendor="amd"} 45
gpu_temperature_celsius{gpu="1",id="0000:03:00.0",name="1002:744c",sensor="junction",vendor="amd"} 52
gpu_temperature_celsius{gpu="1",id="0000:03:00.0",name="1002:744c",sensor="mem",vendor="amd"} 60
# HELP gpu_clock_graphics_hz Graphics clock in Hz
# TYPE gpu_clock_graphics_hz gauge
gpu_clock_graphics_hz{` + l + `} 2.4e+09
# HELP gpu_exporter_collection_error 1 if the last collection from the GPU backend failed, 0 otherwise
# TYPE gpu_exporter_collection_error gauge
gpu_exporter_collection_error{vendor="amd"} 0
# HELP amd_gpu_runtime_suspended 1 if the GPU is runtime-suspended (sensors are not read so as not to wake it), 0 otherwise
# TYPE amd_gpu_runtime_suspended gauge
amd_gpu_runtime_suspended{gpu="1",id="0000:03:00.0",name="1002:744c"} 0
`
	if err := testutil.CollectAndCompare(c, strings.NewReader(expected),
		"gpu_utilization_ratio", "gpu_memory_used_bytes", "gpu_power_draw_watts", "gpu_temperature_celsius",
		"gpu_clock_graphics_hz", "gpu_exporter_collection_error", "amd_gpu_runtime_suspended"); err != nil {
		t.Error(err)
	}
	if problems, err := testutil.CollectAndLint(c); err != nil || len(problems) > 0 {
		t.Errorf("lint: %v %v", problems, err)
	}
}

func TestAMDCollector_NoCards(t *testing.T) {
	c := NewAMD(t.TempDir())
	expected := `
# HELP gpu_exporter_collection_error 1 if the last collection from the GPU backend failed, 0 otherwise
# TYPE gpu_exporter_collection_error gauge
gpu_exporter_collection_error{vendor="amd"} 1
`
	if err := testutil.CollectAndCompare(c, strings.NewReader(expected), "gpu_exporter_collection_error"); err != nil {
		t.Error(err)
	}
	if n := testutil.CollectAndCount(c, "gpu_utilization_ratio"); n != 0 {
		t.Errorf("got %d utilization series with no cards", n)
	}
}
