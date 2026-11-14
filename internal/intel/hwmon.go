package intel

import (
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/woo/nvidia-exporter/internal/sysfs"
)

// HwmonStats is one reading of the GPU's hwmon sensors. intel_gpu_top does not
// report power on discrete Arc cards, but the i915/xe drivers expose an energy
// counter (and sometimes a power limit, voltage, temperatures and fan speed)
// through hwmon. Optional values are nil when the driver does not provide them.
type HwmonStats struct {
	EnergyJoules float64            // cumulative energy counter (energy1_input, µJ → J)
	PowerWatts   *float64           // derived from two consecutive energy readings
	PowerLimit   *float64           // W (power1_max, falling back to power1_cap)
	VoltageV     *float64           // V (in0_input, mV → V)
	Temps        map[string]float64 // °C by sensor name (temp<N>_label, else temp<N>)
	FanRPM       *float64           // fan1_input
}

// Hwmon reads the hwmon directory of one Intel GPU.
type Hwmon struct {
	dir string
	now func() time.Time

	mu         sync.Mutex
	prevEnergy float64
	prevAt     time.Time
	havePrev   bool
}

// ErrNoHwmon is returned by FindHwmon when no Intel GPU hwmon directory exists.
var ErrNoHwmon = errors.New("no Intel GPU hwmon directory found")

// NewHwmon returns a reader for the given hwmon directory (for example
// /sys/class/drm/card1/device/hwmon/hwmon5).
func NewHwmon(dir string) *Hwmon {
	return &Hwmon{dir: dir, now: time.Now}
}

// Dir returns the hwmon directory being read.
func (h *Hwmon) Dir() string { return h.dir }

// DeviceDir returns the device the hwmon belongs to (its "device" link).
func (h *Hwmon) DeviceDir() string { return filepath.Join(h.dir, "device") }

// FindHwmon locates the hwmon directory of an Intel GPU under sysRoot
// (normally "/sys"). It looks at /class/drm/card*/device, requires PCI vendor
// 0x8086 and an hwmon named i915 or xe, and returns the first match in card
// order. If pciSlot is non-empty (for example "0000:00:10.0") only that device
// matches.
func FindHwmon(sysRoot, pciSlot string) (string, error) {
	devs, _ := filepath.Glob(filepath.Join(sysRoot, "class", "drm", "card[0-9]*", "device"))
	sort.Strings(devs)
	for _, dev := range devs {
		if v, err := sysfs.ReadTrim(filepath.Join(dev, "vendor")); err != nil || v != "0x8086" {
			continue
		}
		if pciSlot != "" {
			if real, err := filepath.EvalSymlinks(dev); err != nil || filepath.Base(real) != pciSlot {
				continue
			}
		}
		mons, _ := filepath.Glob(filepath.Join(dev, "hwmon", "hwmon*"))
		sort.Strings(mons)
		for _, m := range mons {
			if n, _ := sysfs.ReadTrim(filepath.Join(m, "name")); n == "i915" || n == "xe" {
				return m, nil
			}
		}
	}
	return "", ErrNoHwmon
}

// PCISlotFromDevice extracts the PCI address from an intel_gpu_top device
// filter such as "pci:slot=0000:00:10.0" or "sys:/sys/devices/pci0000:00/0000:00:10.0/drm/card1".
// It returns "" when no address can be found (for example "drm:/dev/dri/renderD128").
func PCISlotFromDevice(filter string) string {
	if i := strings.Index(filter, "slot="); i >= 0 {
		s := filter[i+len("slot="):]
		if j := strings.IndexAny(s, ",;"); j >= 0 {
			s = s[:j]
		}
		return s
	}
	if strings.HasPrefix(filter, "sys:") {
		parts := strings.Split(filter, "/")
		for i := len(parts) - 1; i >= 0; i-- {
			p := parts[i]
			if len(p) == 12 && p[4] == ':' && p[7] == ':' && p[10] == '.' {
				return p
			}
		}
	}
	return ""
}

// Read takes one reading. Power is derived from the energy counter delta since
// the previous Read, so the first call (and a call less than 100ms after the
// previous one, or after a counter reset) reports no PowerWatts.
func (h *Hwmon) Read() (HwmonStats, error) {
	uj, err := sysfs.ReadFloat(filepath.Join(h.dir, "energy1_input"))
	if err != nil {
		return HwmonStats{}, err
	}
	at := h.now()
	st := HwmonStats{EnergyJoules: uj / 1e6}

	h.mu.Lock()
	if h.havePrev {
		dt := at.Sub(h.prevAt).Seconds()
		if dt >= 0.1 && st.EnergyJoules >= h.prevEnergy {
			w := (st.EnergyJoules - h.prevEnergy) / dt
			st.PowerWatts = &w
		}
	}
	if !h.havePrev || at.Sub(h.prevAt) >= 100*time.Millisecond {
		h.prevEnergy, h.prevAt, h.havePrev = st.EnergyJoules, at, true
	}
	h.mu.Unlock()

	// Power limit: power1_max, else power1_cap (µW).
	for _, f := range []string{"power1_max", "power1_cap"} {
		if v, err := sysfs.ReadFloat(filepath.Join(h.dir, f)); err == nil && v > 0 {
			w := v / 1e6
			st.PowerLimit = &w
			break
		}
	}
	if v, err := sysfs.ReadFloat(filepath.Join(h.dir, "in0_input")); err == nil {
		x := v / 1000
		st.VoltageV = &x
	}
	if v, err := sysfs.ReadFloat(filepath.Join(h.dir, "fan1_input")); err == nil {
		st.FanRPM = &v
	}
	st.Temps = sysfs.Temps(h.dir)
	return st, nil
}
