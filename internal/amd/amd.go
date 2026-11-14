// Package amd reads AMD GPU statistics directly from the amdgpu kernel driver
// through sysfs. It needs no ROCm or amd-smi installation and no root.
//
// Per card (/sys/class/drm/card<N>/device):
//
//	gpu_busy_percent, mem_busy_percent        utilization (%)
//	mem_info_vram_{used,total}                VRAM (bytes)
//	mem_info_gtt_{used,total}                 GTT, system memory mapped for the GPU (bytes)
//	product_name                              name (only some cards)
//	power/runtime_status                      "suspended" when the GPU is in runtime PM sleep
//	hwmon/hwmon<M>/ (name "amdgpu"):
//	  power1_input or power1_average (µW), power1_cap (µW)
//	  temp<N>_input/_label (m°C; edge, junction, mem)
//	  fan1_input (RPM), pwm1/pwm1_max
//	  freq<N>_input/_label (Hz; sclk, mclk)
//	  in0_input (mV; vddgfx)
//
// Which files exist depends on the GPU generation and kernel; every value is
// optional.
package amd

import (
	"path/filepath"
	"regexp"
	"sort"
	"strconv"

	"github.com/woo/nvidia-exporter/internal/sysfs"
)

// Card is one amdgpu device.
type Card struct {
	Index     int    // DRM card number
	DeviceDir string // /sys/class/drm/card<N>/device
	HwmonDir  string // "" if the card has no amdgpu hwmon
	PCISlot   string // e.g. "0000:03:00.0"
	Name      string // product_name, else the PCI ID, e.g. "1002:744c"
}

// Stats is one reading of a card. Nil means not reported.
type Stats struct {
	// Suspended is true when the GPU is runtime-suspended. Only Suspended is
	// set then: reading the sensors would wake the GPU (or fail).
	Suspended bool

	BusyPercent    *float64
	MemBusyPercent *float64
	VRAMUsed       *float64 // bytes
	VRAMTotal      *float64 // bytes
	GTTUsed        *float64 // bytes
	GTTTotal       *float64 // bytes
	PowerWatts     *float64
	PowerCapWatts  *float64
	Temps          map[string]float64 // °C by sensor (edge, junction, mem)
	FanRPM         *float64
	FanRatio       *float64 // pwm1 / pwm1_max
	SCLKHz         *float64 // graphics clock
	MCLKHz         *float64 // memory clock
	VoltageGFX     *float64 // V
}

var cardRe = regexp.MustCompile(`^card(\d+)$`)

// Discover returns the amdgpu cards under sysRoot (normally "/sys") in card
// order. A card is an amdgpu device if its PCI vendor is 0x1002 and its driver
// is amdgpu or it has an hwmon named amdgpu.
func Discover(sysRoot string) []Card {
	entries, _ := filepath.Glob(filepath.Join(sysRoot, "class", "drm", "card*"))
	var cards []Card
	for _, e := range entries {
		m := cardRe.FindStringSubmatch(filepath.Base(e))
		if m == nil {
			continue // connector such as card1-DP-1
		}
		dev := filepath.Join(e, "device")
		if v, err := sysfs.ReadTrim(filepath.Join(dev, "vendor")); err != nil || v != "0x1002" {
			continue
		}
		hw := findHwmon(dev)
		if hw == "" && driver(dev) != "amdgpu" {
			continue
		}
		idx, _ := strconv.Atoi(m[1])
		name, _ := sysfs.ReadTrim(filepath.Join(dev, "product_name"))
		if name == "" {
			name = sysfs.PCIID(dev)
		}
		cards = append(cards, Card{
			Index:     idx,
			DeviceDir: dev,
			HwmonDir:  hw,
			PCISlot:   sysfs.PCISlot(dev),
			Name:      name,
		})
	}
	sort.Slice(cards, func(i, j int) bool { return cards[i].Index < cards[j].Index })
	return cards
}

func findHwmon(dev string) string {
	mons, _ := filepath.Glob(filepath.Join(dev, "hwmon", "hwmon*"))
	sort.Strings(mons)
	for _, m := range mons {
		if n, _ := sysfs.ReadTrim(filepath.Join(m, "name")); n == "amdgpu" {
			return m
		}
	}
	return ""
}

func driver(dev string) string {
	target, err := filepath.EvalSymlinks(filepath.Join(dev, "driver"))
	if err != nil {
		return ""
	}
	return filepath.Base(target)
}

// Read takes one reading of the card.
func (c Card) Read() Stats {
	dev := func(name string) *float64 { return sysfs.OptFloat(filepath.Join(c.DeviceDir, name)) }

	if s, _ := sysfs.ReadTrim(filepath.Join(c.DeviceDir, "power", "runtime_status")); s == "suspended" {
		return Stats{Suspended: true}
	}

	st := Stats{
		BusyPercent:    dev("gpu_busy_percent"),
		MemBusyPercent: dev("mem_busy_percent"),
		VRAMUsed:       dev("mem_info_vram_used"),
		VRAMTotal:      dev("mem_info_vram_total"),
		GTTUsed:        dev("mem_info_gtt_used"),
		GTTTotal:       dev("mem_info_gtt_total"),
	}
	if c.HwmonDir == "" {
		return st
	}

	hw := func(name string) *float64 { return sysfs.OptFloat(filepath.Join(c.HwmonDir, name)) }
	scale := func(v *float64, f float64) *float64 {
		if v == nil {
			return nil
		}
		x := *v * f
		return &x
	}

	// Newer kernels/GPUs report instantaneous power1_input, older ones only
	// power1_average.
	st.PowerWatts = scale(hw("power1_input"), 1e-6)
	if st.PowerWatts == nil {
		st.PowerWatts = scale(hw("power1_average"), 1e-6)
	}
	st.PowerCapWatts = scale(hw("power1_cap"), 1e-6)
	if t := sysfs.Temps(c.HwmonDir); len(t) > 0 {
		st.Temps = t
	}
	st.FanRPM = hw("fan1_input")
	if pwm, pmax := hw("pwm1"), hw("pwm1_max"); pwm != nil {
		m := 255.0
		if pmax != nil && *pmax > 0 {
			m = *pmax
		}
		st.FanRatio = scale(pwm, 1/m)
	}
	st.VoltageGFX = scale(hw("in0_input"), 1e-3)

	freqs, _ := filepath.Glob(filepath.Join(c.HwmonDir, "freq[0-9]*_input"))
	for _, in := range freqs {
		label, _ := sysfs.ReadTrim(in[:len(in)-len("_input")] + "_label")
		switch label {
		case "sclk":
			st.SCLKHz = sysfs.OptFloat(in)
		case "mclk":
			st.MCLKHz = sysfs.OptFloat(in)
		}
	}
	return st
}
