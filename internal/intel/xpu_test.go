package intel

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseXPUStats(t *testing.T) {
	b, err := os.ReadFile("../../testdata/xpu-smi-stats.json")
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseXPUStats(b)
	if err != nil {
		t.Fatal(err)
	}
	if want := 378.34765625 * 1048576; got != want {
		t.Errorf("used = %v, want %v", got, want)
	}

	// Leading warning lines before the JSON are ignored.
	if _, err := parseXPUStats(append([]byte("WARNING: something\n"), b...)); err != nil {
		t.Errorf("with warning prefix: %v", err)
	}

	// Multiple tiles are summed.
	multi := `{"memory":{"used_mib":{"tile_0":{"current":100},"tile_1":{"current":50}}}}`
	if got, _ := parseXPUStats([]byte(multi)); got != 150*1048576 {
		t.Errorf("multi-tile = %v", got)
	}

	for _, bad := range []string{`{}`, `{"memory":{}}`, `not json`, ``} {
		if _, err := parseXPUStats([]byte(bad)); err == nil {
			t.Errorf("parseXPUStats(%q): want error", bad)
		}
	}
}

func TestParseXPUDiscovery(t *testing.T) {
	b, err := os.ReadFile("../../testdata/xpu-smi-discovery.json")
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseXPUDiscovery(b)
	if err != nil || got != 4244635648 {
		t.Errorf("got %v, %v", got, err)
	}
	if got, err := parseXPUDiscovery([]byte(`{"memory_physical_size_byte":"4244635648"}`)); err != nil || got != 4244635648 {
		t.Errorf("string form: got %v, %v", got, err)
	}
	for _, bad := range []string{`{}`, `{"memory_physical_size_byte":0}`, `{"memory_physical_size_byte":"x"}`} {
		if _, err := parseXPUDiscovery([]byte(bad)); err == nil {
			t.Errorf("parseXPUDiscovery(%q): want error", bad)
		}
	}
}

func TestXPU_RunPollsFakeTool(t *testing.T) {
	fake, _ := filepath.Abs("../../testdata/fake-xpu-smi")
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("FAKE_XPU_ARGS_FILE", argsFile)

	x := NewXPU(fake, "0", time.Second, testLogger())
	if _, _, err := x.Latest(); err != ErrNoXPUSample {
		t.Fatalf("before first poll: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go x.Run(ctx)

	deadline := time.Now().Add(5 * time.Second)
	for {
		st, _, err := x.Latest()
		if err == nil {
			if st.MemoryTotalBytes == nil || *st.MemoryTotalBytes != 4244635648 {
				t.Errorf("total = %v", st.MemoryTotalBytes)
			}
			if st.MemoryUsedBytes != 378.34765625*1048576 {
				t.Errorf("used = %v", st.MemoryUsedBytes)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no reading: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	args, _ := os.ReadFile(argsFile)
	for _, want := range []string{"discovery -d 0 -j", "stats -d 0 -j"} {
		if !strings.Contains(string(args), want) {
			t.Errorf("xpu-smi not called with %q; calls:\n%s", want, args)
		}
	}
}

func TestXPU_ReportsToolFailure(t *testing.T) {
	fake, _ := filepath.Abs("../../testdata/fake-xpu-smi")
	t.Setenv("FAKE_XPU_FAIL", "1")
	x := NewXPU(fake, "0", time.Second, testLogger())
	x.poll(context.Background())
	_, _, err := x.Latest()
	if err == nil || err == ErrNoXPUSample || !strings.Contains(err.Error(), "XPU Manager daemon") {
		t.Errorf("err = %v, want the tool's stderr", err)
	}
}
