package amd

import (
	"os"
	"path/filepath"
	"testing"
)

func eq(t *testing.T, name string, got *float64, want float64) {
	t.Helper()
	if got == nil {
		t.Errorf("%s = nil, want %v", name, want)
		return
	}
	if d := *got - want; d > 1e-9 || d < -1e-9 {
		t.Errorf("%s = %v, want %v", name, *got, want)
	}
}

func TestDiscoverAndRead(t *testing.T) {
	sys := t.TempDir()
	dev, hw := FakeRDNA3()
	if err := FakeCard(sys, 1, "0000:03:00.0", dev, hw); err != nil {
		t.Fatal(err)
	}

	cards := Discover(sys)
	if len(cards) != 1 {
		t.Fatalf("found %d cards, want 1", len(cards))
	}
	c := cards[0]
	if c.Index != 1 || c.PCISlot != "0000:03:00.0" || c.Name != "1002:744c" || c.HwmonDir == "" {
		t.Errorf("card = %+v", c)
	}

	st := c.Read()
	if st.Suspended {
		t.Fatal("Suspended for an active card")
	}
	eq(t, "busy", st.BusyPercent, 42)
	eq(t, "mem busy", st.MemBusyPercent, 7)
	eq(t, "vram used", st.VRAMUsed, 1073741824)
	eq(t, "vram total", st.VRAMTotal, 25753026560)
	eq(t, "gtt used", st.GTTUsed, 16777216)
	eq(t, "power", st.PowerWatts, 85)
	eq(t, "power cap", st.PowerCapWatts, 327)
	eq(t, "fan rpm", st.FanRPM, 1200)
	eq(t, "fan ratio", st.FanRatio, 0.2)
	eq(t, "sclk", st.SCLKHz, 2.4e9)
	eq(t, "mclk", st.MCLKHz, 1.25e9)
	eq(t, "voltage", st.VoltageGFX, 0.85)
	if st.Temps["edge"] != 45 || st.Temps["junction"] != 52 || st.Temps["mem"] != 60 {
		t.Errorf("temps = %v", st.Temps)
	}
}

func TestRead_PowerAverageFallbackAndMissingFiles(t *testing.T) {
	sys := t.TempDir()
	dev := map[string]string{"vendor": "0x1002", "device": "0x73bf", "product_name": "AMD Radeon RX 6800 XT"}
	hw := map[string]string{"name": "amdgpu", "power1_average": "120500000"}
	if err := FakeCard(sys, 0, "0000:0a:00.0", dev, hw); err != nil {
		t.Fatal(err)
	}
	c := Discover(sys)[0]
	if c.Name != "AMD Radeon RX 6800 XT" {
		t.Errorf("name = %q", c.Name)
	}
	st := c.Read()
	eq(t, "power", st.PowerWatts, 120.5)
	if st.BusyPercent != nil || st.VRAMUsed != nil || st.FanRPM != nil || st.FanRatio != nil || st.SCLKHz != nil || st.Temps != nil {
		t.Errorf("missing files should be nil: %+v", st)
	}
}

func TestRead_Suspended(t *testing.T) {
	sys := t.TempDir()
	dev, hw := FakeRDNA3()
	dev["power/runtime_status"] = "suspended"
	if err := FakeCard(sys, 0, "0000:03:00.0", dev, hw); err != nil {
		t.Fatal(err)
	}
	st := Discover(sys)[0].Read()
	if !st.Suspended || st.BusyPercent != nil || st.PowerWatts != nil {
		t.Errorf("suspended card read sensors: %+v", st)
	}
}

func TestDiscover_IgnoresOtherVendorsAndConnectors(t *testing.T) {
	sys := t.TempDir()
	if err := FakeCard(sys, 0, "0000:00:02.0", map[string]string{"vendor": "0x8086"}, map[string]string{"name": "i915"}); err != nil {
		t.Fatal(err)
	}
	dev, hw := FakeRDNA3()
	if err := FakeCard(sys, 2, "0000:03:00.0", dev, hw); err != nil {
		t.Fatal(err)
	}
	// A connector directory next to the card.
	if err := os.MkdirAll(filepath.Join(sys, "class", "drm", "card2-DP-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	// An AMD device without amdgpu (e.g. the radeon driver) is skipped.
	if err := FakeCard(sys, 3, "0000:04:00.0", map[string]string{"vendor": "0x1002"}, map[string]string{"name": "radeon"}); err != nil {
		t.Fatal(err)
	}

	cards := Discover(sys)
	if len(cards) != 1 || cards[0].Index != 2 {
		t.Fatalf("cards = %+v, want only card2", cards)
	}
}

func TestDiscover_DriverLinkWithoutHwmon(t *testing.T) {
	sys := t.TempDir()
	if err := FakeCard(sys, 0, "0000:03:00.0", map[string]string{"vendor": "0x1002", "gpu_busy_percent": "5"}, nil); err != nil {
		t.Fatal(err)
	}
	drv := filepath.Join(sys, "bus", "pci", "drivers", "amdgpu")
	if err := os.MkdirAll(drv, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(drv, filepath.Join(sys, "devices", "pci0000:00", "0000:03:00.0", "driver")); err != nil {
		t.Fatal(err)
	}
	cards := Discover(sys)
	if len(cards) != 1 || cards[0].HwmonDir != "" {
		t.Fatalf("cards = %+v", cards)
	}
	eq(t, "busy", cards[0].Read().BusyPercent, 5)
}
