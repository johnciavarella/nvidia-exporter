package intel

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func fixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "intel-gpu-top.json"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestParseSample(t *testing.T) {
	raw := `{
		"period": {"duration": 5000.211, "unit": "ms"},
		"frequency": {"requested": 1200.0, "actual": 1180.5, "unit": "MHz"},
		"interrupts": {"count": 1523.5, "unit": "irq/s"},
		"rc6": {"value": 62.5, "unit": "%"},
		"power": {"GPU": 14.25, "Package": 28.5, "unit": "W"},
		"imc-bandwidth": {"reads": 100.0, "writes": 50.5, "unit": "MiB/s"},
		"engines": {
			"Render/3D/0": {"busy": 37.5, "sema": 1.25, "wait": 2.5, "unit": "%"},
			"Video/0": {"busy": 80.0, "sema": 0.0, "wait": 0.0, "unit": "%"}
		}
	}`
	s, err := ParseSample([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}

	if s.Period != 5000211*time.Microsecond {
		t.Errorf("Period = %v", s.Period)
	}
	if s.FrequencyActual != 1180.5 || s.FrequencyRequested != 1200 {
		t.Errorf("frequency = %v / %v", s.FrequencyActual, s.FrequencyRequested)
	}
	if s.Interrupts != 1523.5 || s.RC6 != 62.5 || !s.HasRC6 {
		t.Errorf("interrupts = %v, rc6 = %v", s.Interrupts, s.RC6)
	}
	if s.PowerGPU == nil || *s.PowerGPU != 14.25 || s.PowerPackage == nil || *s.PowerPackage != 28.5 {
		t.Errorf("power = %v / %v", s.PowerGPU, s.PowerPackage)
	}
	if s.IMCReads == nil || *s.IMCReads != 100 || s.IMCWrites == nil || *s.IMCWrites != 50.5 {
		t.Errorf("imc = %v / %v", s.IMCReads, s.IMCWrites)
	}
	if len(s.Engines) != 2 {
		t.Fatalf("got %d engines, want 2", len(s.Engines))
	}
	if e := s.Engines["Render/3D/0"]; e.Busy != 37.5 || e.Sema != 1.25 || e.Wait != 2.5 {
		t.Errorf("Render/3D/0 = %+v", e)
	}
}

func TestParseSample_OptionalFieldsAbsent(t *testing.T) {
	raw := `{"period": {"duration": 1000}, "power": {"GPU": 1.1}, "engines": {}}`
	s, err := ParseSample([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if s.HasRC6 {
		t.Error("HasRC6 set without an rc6 field")
	}
	if s.PowerGPU == nil || *s.PowerGPU != 1.1 {
		t.Errorf("PowerGPU = %v", s.PowerGPU)
	}
	if s.PowerPackage != nil || s.IMCReads != nil || s.IMCWrites != nil {
		t.Errorf("absent fields should be nil: %v %v %v", s.PowerPackage, s.IMCReads, s.IMCWrites)
	}
}

func TestParseSample_Invalid(t *testing.T) {
	if _, err := ParseSample([]byte(`{not json`)); err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestReadStream_KeepsNewestSample(t *testing.T) {
	// The real tool never closes the array; the fixture does, which also works.
	p := NewGPUTopProvider("unused", "", time.Second, testLogger())
	if err := p.readStream(bytes.NewReader(fixture(t))); err != nil {
		t.Fatal(err)
	}

	s, at, err := p.Latest()
	if err != nil {
		t.Fatalf("Latest error = %v", err)
	}
	if time.Since(at) > time.Minute {
		t.Errorf("sample time %v is not recent", at)
	}
	// Second sample in the fixture: idle, no package power.
	if s.FrequencyActual != 300 || s.PowerPackage != nil {
		t.Errorf("expected the second (idle) sample, got %+v", s)
	}
}

func TestReadStream_RejectsNonArray(t *testing.T) {
	p := NewGPUTopProvider("unused", "", time.Second, testLogger())
	if err := p.readStream(strings.NewReader(`{"period": {}}`)); err == nil {
		t.Error("expected error when output is not a JSON array")
	}
}

func TestProvider_BeforeFirstSample(t *testing.T) {
	p := NewGPUTopProvider("unused", "", time.Second, testLogger())
	if _, _, err := p.Latest(); !errors.Is(err, ErrNoSample) {
		t.Errorf("Latest error = %v, want ErrNoSample", err)
	}
}

func TestArgs(t *testing.T) {
	cases := []struct {
		name     string
		device   string
		interval time.Duration
		want     string
	}{
		{"default", "", 10 * time.Second, "-J -s 10000"},
		{"device", "drm:/dev/dri/renderD128", 5 * time.Second, "-J -s 5000 -d drm:/dev/dri/renderD128"},
		{"min interval", "", time.Millisecond, "-J -s 100"},
	}
	for _, tc := range cases {
		p := NewGPUTopProvider("x", tc.device, tc.interval, testLogger())
		if got := strings.Join(p.args(), " "); got != tc.want {
			t.Errorf("%s: args = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func fakeTool(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	path, err := filepath.Abs(filepath.Join("..", "..", "testdata", "fake-intel-gpu-top"))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestProvider_RunStreamsFromProcess(t *testing.T) {
	tool := fakeTool(t)
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("FAKE_INTEL_ARGS_FILE", argsFile)

	p := NewGPUTopProvider(tool, "drm:/dev/dri/renderD128", 5*time.Second, testLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	deadline := time.Now().Add(5 * time.Second)
	for {
		s, _, err := p.Latest()
		if err == nil && s.FrequencyActual == 300 { // the second fixture sample
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no sample from fake intel_gpu_top; last error = %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(args)); got != "-J -s 5000 -d drm:/dev/dri/renderD128" {
		t.Errorf("intel_gpu_top args = %q", got)
	}
}

func TestProvider_ReportsProcessFailure(t *testing.T) {
	tool := fakeTool(t)
	t.Setenv("FAKE_INTEL_FAIL", "1")

	p := NewGPUTopProvider(tool, "", time.Second, testLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	deadline := time.Now().Add(5 * time.Second)
	for {
		_, _, err := p.Latest()
		if err != nil && !errors.Is(err, ErrNoSample) {
			if !strings.Contains(err.Error(), "Permission denied") {
				t.Errorf("error should include intel_gpu_top's stderr, got %v", err)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected a process error, last error = %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
