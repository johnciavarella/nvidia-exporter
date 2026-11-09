package intel

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fakeSys(t *testing.T) (sys, mon string) {
	t.Helper()
	sys = t.TempDir()
	pci := filepath.Join(sys, "devices", "pci0000:00", "0000:00:10.0")
	write(t, filepath.Join(pci, "vendor"), "0x8086")
	mon = filepath.Join(pci, "hwmon", "hwmon3")
	write(t, filepath.Join(mon, "name"), "i915")
	if err := os.MkdirAll(filepath.Join(sys, "class", "drm", "card1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(pci, filepath.Join(sys, "class", "drm", "card1", "device")); err != nil {
		t.Fatal(err)
	}
	return sys, mon
}

func TestFindHwmon(t *testing.T) {
	sys, mon := fakeSys(t)
	got, err := FindHwmon(sys, "")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(sys, "class", "drm", "card1", "device", "hwmon", "hwmon3"); got != want {
		t.Errorf("got %s want %s (real %s)", got, want, mon)
	}
	if _, err := FindHwmon(sys, "0000:00:10.0"); err != nil {
		t.Errorf("matching slot: %v", err)
	}
	if _, err := FindHwmon(sys, "0000:03:00.0"); err != ErrNoHwmon {
		t.Errorf("other slot: got %v want ErrNoHwmon", err)
	}
	if _, err := FindHwmon(t.TempDir(), ""); err != ErrNoHwmon {
		t.Errorf("empty sys: got %v want ErrNoHwmon", err)
	}
}

func TestFindHwmon_IgnoresNonIntel(t *testing.T) {
	sys, _ := fakeSys(t)
	write(t, filepath.Join(sys, "devices", "pci0000:00", "0000:00:10.0", "vendor"), "0x10de")
	if _, err := FindHwmon(sys, ""); err != ErrNoHwmon {
		t.Errorf("got %v want ErrNoHwmon", err)
	}
}

func TestPCISlotFromDevice(t *testing.T) {
	cases := map[string]string{
		"":                             "",
		"drm:/dev/dri/renderD128":      "",
		"pci:slot=0000:00:10.0":        "0000:00:10.0",
		"pci:vendor=8086,device=56a6":  "",
		"pci:slot=0000:03:00.0,card=0": "0000:03:00.0",
		"sys:/sys/devices/pci0000:00/0000:00:10.0/drm/card1": "0000:00:10.0",
	}
	for in, want := range cases {
		if got := PCISlotFromDevice(in); got != want {
			t.Errorf("PCISlotFromDevice(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHwmonRead(t *testing.T) {
	_, mon := fakeSys(t)
	write(t, filepath.Join(mon, "energy1_input"), "10000000") // 10 J
	write(t, filepath.Join(mon, "power1_max"), "31250000")
	write(t, filepath.Join(mon, "in0_input"), "743")
	write(t, filepath.Join(mon, "temp1_input"), "45000")
	write(t, filepath.Join(mon, "temp1_label"), "pkg")
	write(t, filepath.Join(mon, "temp2_input"), "50500")
	write(t, filepath.Join(mon, "fan1_input"), "1200")

	h := NewHwmon(mon)
	now := time.Unix(1000, 0)
	h.now = func() time.Time { return now }

	st, err := h.Read()
	if err != nil {
		t.Fatal(err)
	}
	if st.EnergyJoules != 10 || st.PowerWatts != nil {
		t.Errorf("first read: energy=%v power=%v", st.EnergyJoules, st.PowerWatts)
	}
	if st.PowerLimit == nil || *st.PowerLimit != 31.25 {
		t.Errorf("limit = %v", st.PowerLimit)
	}
	if st.VoltageV == nil || *st.VoltageV != 0.743 {
		t.Errorf("voltage = %v", st.VoltageV)
	}
	if st.FanRPM == nil || *st.FanRPM != 1200 {
		t.Errorf("fan = %v", st.FanRPM)
	}
	if st.Temps["pkg"] != 45 || st.Temps["temp2"] != 50.5 {
		t.Errorf("temps = %v", st.Temps)
	}

	// 5 J over 2 s = 2.5 W.
	write(t, filepath.Join(mon, "energy1_input"), "15000000")
	now = now.Add(2 * time.Second)
	st, _ = h.Read()
	if st.PowerWatts == nil || *st.PowerWatts != 2.5 {
		t.Errorf("power = %v, want 2.5", st.PowerWatts)
	}

	// Counter reset: no power, and the baseline restarts.
	write(t, filepath.Join(mon, "energy1_input"), "1000000")
	now = now.Add(time.Second)
	st, _ = h.Read()
	if st.PowerWatts != nil {
		t.Errorf("after reset power = %v, want nil", *st.PowerWatts)
	}
	write(t, filepath.Join(mon, "energy1_input"), "3000000")
	now = now.Add(time.Second)
	st, _ = h.Read()
	if st.PowerWatts == nil || *st.PowerWatts != 2 {
		t.Errorf("power after reset = %v, want 2", st.PowerWatts)
	}
}

func TestHwmonRead_NoEnergy(t *testing.T) {
	_, mon := fakeSys(t)
	if _, err := NewHwmon(mon).Read(); err == nil {
		t.Error("want error without energy1_input")
	}
}
