// Package sysfs has small helpers for reading Linux sysfs and hwmon files,
// shared by the GPU backends that read the kernel driver directly.
package sysfs

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ReadTrim returns the content of a sysfs file without surrounding whitespace.
func ReadTrim(path string) (string, error) {
	b, err := os.ReadFile(path)
	return strings.TrimSpace(string(b)), err
}

// ReadFloat reads a sysfs file holding one number.
func ReadFloat(path string) (float64, error) {
	s, err := ReadTrim(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(s, 64)
}

// OptFloat reads a sysfs file holding one number and returns nil if the file
// is missing or unreadable, or does not hold a number.
func OptFloat(path string) *float64 {
	v, err := ReadFloat(path)
	if err != nil {
		return nil
	}
	return &v
}

// Temps reads every temp<N>_input in an hwmon directory and returns degrees
// Celsius by sensor name (temp<N>_label if present, else temp<N>).
func Temps(hwmonDir string) map[string]float64 {
	temps := map[string]float64{}
	inputs, _ := filepath.Glob(filepath.Join(hwmonDir, "temp[0-9]*_input"))
	for _, in := range inputs {
		v, err := ReadFloat(in)
		if err != nil {
			continue
		}
		base := strings.TrimSuffix(in, "_input")
		name, _ := ReadTrim(base + "_label")
		if name == "" {
			name = filepath.Base(base)
		}
		temps[name] = v / 1000
	}
	return temps
}

// PCISlot returns the PCI address (for example "0000:03:00.0") of a PCI device
// directory such as /sys/class/drm/card1/device, or "" if it cannot be resolved.
func PCISlot(deviceDir string) string {
	real, err := filepath.EvalSymlinks(deviceDir)
	if err != nil {
		return ""
	}
	return filepath.Base(real)
}

// PCIID returns "vendor:device" (for example "1002:744c") of a PCI device
// directory, or "" if the IDs cannot be read.
func PCIID(deviceDir string) string {
	v, err1 := ReadTrim(filepath.Join(deviceDir, "vendor"))
	d, err2 := ReadTrim(filepath.Join(deviceDir, "device"))
	if err1 != nil || err2 != nil {
		return ""
	}
	return strings.TrimPrefix(v, "0x") + ":" + strings.TrimPrefix(d, "0x")
}
