package nvidia

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// SMIProvider collects GPU statistics using nvidia-smi.
type SMIProvider struct {
	// Path to the nvidia-smi binary.
	NvidiaSMIPath string
}

// NewSMIProvider creates a new SMIProvider with the given nvidia-smi path.
func NewSMIProvider(nvidiaSMIPath string) *SMIProvider {
	return &SMIProvider{
		NvidiaSMIPath: nvidiaSMIPath,
	}
}

// queryFields are the nvidia-smi --query-gpu fields we request.
var queryFields = []string{
	"index",
	"name",
	"uuid",
	"utilization.gpu",
	"utilization.memory",
	"memory.used",
	"memory.total",
	"temperature.gpu",
	"power.draw",
	"power.limit",
	"fan.speed",
	"clocks.current.graphics",
	"clocks.current.memory",
	"pstate",
}

// Gather collects GPU statistics by executing nvidia-smi.
func (p *SMIProvider) Gather(ctx context.Context) ([]GPUStats, error) {
	args := []string{
		"--query-gpu=" + strings.Join(queryFields, ","),
		"--format=csv,noheader,nounits",
	}

	cmd := exec.CommandContext(ctx, p.NvidiaSMIPath, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("nvidia-smi failed: %w: %s", err, stderr.String())
	}

	return ParseCSV(stdout.Bytes())
}

// ParseCSV parses nvidia-smi CSV output into GPUStats.
func ParseCSV(data []byte) ([]GPUStats, error) {
	var stats []GPUStats

	scanner := bufio.NewScanner(bytes.NewReader(data))
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		gpu, err := parseLine(line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNum, err)
		}
		stats = append(stats, gpu)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading csv: %w", err)
	}

	return stats, nil
}

// parseLine parses a single CSV line into GPUStats.
func parseLine(line string) (GPUStats, error) {
	fields := strings.Split(line, ", ")
	if len(fields) != len(queryFields) {
		return GPUStats{}, fmt.Errorf("expected %d fields, got %d", len(queryFields), len(fields))
	}

	var gpu GPUStats
	for i, f := range fields {
		if isNA(strings.TrimSpace(f)) {
			if gpu.NA == nil {
				gpu.NA = map[string]bool{}
			}
			gpu.NA[queryFields[i]] = true
		}
	}

	// index
	idx, err := parseIntOrNA(fields[0])
	if err != nil {
		return GPUStats{}, fmt.Errorf("parsing index: %w", err)
	}
	gpu.Index = idx

	// name
	gpu.Name = strings.TrimSpace(fields[1])

	// uuid
	gpu.UUID = strings.TrimSpace(fields[2])

	// utilization.gpu (%)
	gpu.UtilizationGPU, err = parseFloatOrNA(fields[3])
	if err != nil {
		return GPUStats{}, fmt.Errorf("parsing utilization.gpu: %w", err)
	}

	// utilization.memory (%)
	gpu.UtilizationMem, err = parseFloatOrNA(fields[4])
	if err != nil {
		return GPUStats{}, fmt.Errorf("parsing utilization.memory: %w", err)
	}

	// memory.used (MiB -> bytes)
	memUsedMiB, err := parseFloatOrNA(fields[5])
	if err != nil {
		return GPUStats{}, fmt.Errorf("parsing memory.used: %w", err)
	}
	gpu.MemoryUsed = mibToBytes(memUsedMiB)

	// memory.total (MiB -> bytes)
	memTotalMiB, err := parseFloatOrNA(fields[6])
	if err != nil {
		return GPUStats{}, fmt.Errorf("parsing memory.total: %w", err)
	}
	gpu.MemoryTotal = mibToBytes(memTotalMiB)

	// temperature.gpu (Celsius)
	gpu.Temperature, err = parseFloatOrNA(fields[7])
	if err != nil {
		return GPUStats{}, fmt.Errorf("parsing temperature.gpu: %w", err)
	}

	// power.draw (Watts)
	gpu.PowerDraw, err = parseFloatOrNA(fields[8])
	if err != nil {
		return GPUStats{}, fmt.Errorf("parsing power.draw: %w", err)
	}

	// power.limit (Watts)
	gpu.PowerLimit, err = parseFloatOrNA(fields[9])
	if err != nil {
		return GPUStats{}, fmt.Errorf("parsing power.limit: %w", err)
	}

	// fan.speed (%)
	gpu.FanSpeed, err = parseFloatOrNA(fields[10])
	if err != nil {
		return GPUStats{}, fmt.Errorf("parsing fan.speed: %w", err)
	}

	// clocks.current.graphics (MHz)
	gpu.ClockGraphics, err = parseFloatOrNA(fields[11])
	if err != nil {
		return GPUStats{}, fmt.Errorf("parsing clocks.current.graphics: %w", err)
	}

	// clocks.current.memory (MHz)
	gpu.ClockMemory, err = parseFloatOrNA(fields[12])
	if err != nil {
		return GPUStats{}, fmt.Errorf("parsing clocks.current.memory: %w", err)
	}

	// pstate (P0-P15 -> int 0-15)
	gpu.PerformanceState, err = parsePState(fields[13])
	if err != nil {
		return GPUStats{}, fmt.Errorf("parsing pstate: %w", err)
	}

	return gpu, nil
}

// parseFloatOrNA parses a float value, returning 0 for "[Not Supported]" or "[N/A]".
func parseFloatOrNA(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if isNA(s) {
		return 0, nil
	}
	return strconv.ParseFloat(s, 64)
}

// parseIntOrNA parses an int value, returning 0 for "[Not Supported]" or "[N/A]".
func parseIntOrNA(s string) (int, error) {
	s = strings.TrimSpace(s)
	if isNA(s) {
		return 0, nil
	}
	return strconv.Atoi(s)
}

// parsePState converts "P0"-"P15" to integer 0-15, returning 0 for N/A.
func parsePState(s string) (int, error) {
	s = strings.TrimSpace(s)
	if isNA(s) {
		return 0, nil
	}
	if !strings.HasPrefix(s, "P") {
		return 0, fmt.Errorf("invalid pstate format: %s", s)
	}
	return strconv.Atoi(s[1:])
}

// isNA returns true if the value represents a missing/unsupported metric.
func isNA(s string) bool {
	s = strings.ToLower(s)
	return s == "[not supported]" || s == "[n/a]" || s == "n/a" || s == ""
}

// mibToBytes converts MiB to bytes.
func mibToBytes(mib float64) uint64 {
	return uint64(mib * 1024 * 1024)
}
