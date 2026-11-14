package amd

import (
	"fmt"
	"os"
	"path/filepath"
)

// FakeCard writes a fake amdgpu card into a test sysfs tree under sysRoot, laid
// out like the kernel's: the PCI device lives under /sys/devices and
// /sys/class/drm/card<N>/device links to it. device and hwmon map file names
// to contents; a nil hwmon creates no hwmon directory. It is for tests.
func FakeCard(sysRoot string, card int, slot string, device, hwmon map[string]string) error {
	pci := filepath.Join(sysRoot, "devices", "pci0000:00", slot)
	write := func(dir string, files map[string]string) error {
		for name, content := range files {
			p := filepath.Join(dir, name)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(p, []byte(content+"\n"), 0o644); err != nil {
				return err
			}
		}
		return nil
	}
	if err := write(pci, device); err != nil {
		return err
	}
	if hwmon != nil {
		if err := write(filepath.Join(pci, "hwmon", fmt.Sprintf("hwmon%d", card)), hwmon); err != nil {
			return err
		}
	}
	cardDir := filepath.Join(sysRoot, "class", "drm", fmt.Sprintf("card%d", card))
	if err := os.MkdirAll(cardDir, 0o755); err != nil {
		return err
	}
	return os.Symlink(pci, filepath.Join(cardDir, "device"))
}

// FakeRDNA3 returns device and hwmon files modelled on an RDNA3 card
// (power1_input, edge/junction/mem temperatures, sclk/mclk).
func FakeRDNA3() (device, hwmon map[string]string) {
	return map[string]string{
		"vendor":               "0x1002",
		"device":               "0x744c",
		"gpu_busy_percent":     "42",
		"mem_busy_percent":     "7",
		"mem_info_vram_used":   "1073741824",
		"mem_info_vram_total":  "25753026560",
		"mem_info_gtt_used":    "16777216",
		"mem_info_gtt_total":   "33554432000",
		"power/runtime_status": "active",
	}, map[string]string{
		"name":         "amdgpu",
		"power1_input": "85000000",
		"power1_cap":   "327000000",
		"temp1_input":  "45000",
		"temp1_label":  "edge",
		"temp2_input":  "52000",
		"temp2_label":  "junction",
		"temp3_input":  "60000",
		"temp3_label":  "mem",
		"fan1_input":   "1200",
		"pwm1":         "51",
		"pwm1_max":     "255",
		"freq1_input":  "2400000000",
		"freq1_label":  "sclk",
		"freq2_input":  "1250000000",
		"freq2_label":  "mclk",
		"in0_input":    "850",
		"in0_label":    "vddgfx",
	}
}
