package intel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrNoXPUSample is returned by XPU.Latest before the first xpu-smi reading.
var ErrNoXPUSample = errors.New("no xpu-smi reading received yet")

// xpuTimeout bounds one xpu-smi call. `xpu-smi stats` samples for ~0.2s.
const xpuTimeout = 20 * time.Second

// XPUStats is what the exporter takes from Intel's xpu-smi. intel_gpu_top and
// hwmon report no memory use on discrete Arc cards; xpu-smi does.
type XPUStats struct {
	MemoryUsedBytes  float64
	MemoryTotalBytes *float64 // nil until `xpu-smi discovery` has succeeded
}

// XPUProvider returns the most recent xpu-smi reading.
type XPUProvider interface {
	Latest() (XPUStats, time.Time, error)
}

// XPU polls `xpu-smi stats -d <device> -j` on an interval and keeps the newest
// reading. The physical memory size comes from `xpu-smi discovery` and is read
// once, retrying until it succeeds.
type XPU struct {
	Path     string        // xpu-smi binary
	Device   string        // xpu-smi device id, e.g. "0"
	Interval time.Duration // polling period
	Logger   *slog.Logger

	mu    sync.RWMutex
	stats XPUStats
	at    time.Time
	err   error
	total *float64
}

// NewXPU creates an XPU poller. Call Run to start polling.
func NewXPU(path, device string, interval time.Duration, logger *slog.Logger) *XPU {
	if device == "" {
		device = "0"
	}
	return &XPU{Path: path, Device: device, Interval: interval, Logger: logger, err: ErrNoXPUSample}
}

// Latest implements XPUProvider.
func (x *XPU) Latest() (XPUStats, time.Time, error) {
	x.mu.RLock()
	defer x.mu.RUnlock()
	return x.stats, x.at, x.err
}

// Run polls until ctx is cancelled.
func (x *XPU) Run(ctx context.Context) {
	interval := x.Interval
	if interval < time.Second {
		interval = time.Second
	}
	for {
		x.poll(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

func (x *XPU) poll(ctx context.Context) {
	if x.total == nil {
		out, err := x.run(ctx, "discovery", "-d", x.Device, "-j")
		if err == nil {
			var total float64
			if total, err = parseXPUDiscovery(out); err == nil {
				x.total = &total
			}
		}
		if err != nil {
			x.Logger.Warn("xpu-smi discovery failed, memory total unavailable", "error", err)
		}
	}

	out, err := x.run(ctx, "stats", "-d", x.Device, "-j")
	var used float64
	if err == nil {
		used, err = parseXPUStats(out)
	}
	if ctx.Err() != nil {
		return
	}

	x.mu.Lock()
	defer x.mu.Unlock()
	if err != nil {
		x.err = err
		x.Logger.Error("xpu-smi stats failed", "error", err)
		return
	}
	x.stats = XPUStats{MemoryUsedBytes: used, MemoryTotalBytes: x.total}
	x.at = time.Now()
	x.err = nil
}

func (x *XPU) run(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, xpuTimeout)
	defer cancel()
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, x.Path, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("xpu-smi %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// jsonObject returns b from its first '{', dropping any warning lines xpu-smi
// printed before the JSON.
func jsonObject(b []byte) []byte {
	if i := bytes.IndexByte(b, '{'); i > 0 {
		return b[i:]
	}
	return b
}

// parseXPUStats returns the memory in use, in bytes, summed over tiles, from
// the JSON printed by `xpu-smi stats -d N -j`.
func parseXPUStats(b []byte) (float64, error) {
	var s struct {
		Memory struct {
			UsedMiB map[string]struct {
				Current *float64 `json:"current"`
			} `json:"used_mib"`
		} `json:"memory"`
	}
	if err := json.Unmarshal(jsonObject(b), &s); err != nil {
		return 0, fmt.Errorf("parsing xpu-smi stats: %w", err)
	}
	var mib float64
	found := false
	for _, t := range s.Memory.UsedMiB {
		if t.Current != nil {
			mib += *t.Current
			found = true
		}
	}
	if !found {
		return 0, errors.New("xpu-smi stats reported no memory.used_mib")
	}
	return mib * 1048576, nil
}

// parseXPUDiscovery returns memory_physical_size_byte from the JSON printed by
// `xpu-smi discovery -d N -j`. The field is a number on some versions and a
// string on others.
func parseXPUDiscovery(b []byte) (float64, error) {
	var d map[string]any
	if err := json.Unmarshal(jsonObject(b), &d); err != nil {
		return 0, fmt.Errorf("parsing xpu-smi discovery: %w", err)
	}
	switch v := d["memory_physical_size_byte"].(type) {
	case float64:
		if v > 0 {
			return v, nil
		}
	case string:
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			return f, nil
		}
	}
	return 0, errors.New("xpu-smi discovery reported no memory_physical_size_byte")
}
