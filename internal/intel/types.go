// Package intel collects GPU statistics from Intel GPUs using intel_gpu_top.
package intel

import "time"

// EngineStats holds the utilisation of one GPU engine (for example
// "Render/3D/0" or "Video/0") over a sampling period. Values are percentages.
type EngineStats struct {
	Busy float64 // Time the engine was busy (%)
	Sema float64 // Time the engine waited on a semaphore (%)
	Wait float64 // Time the engine waited on a wait event (%)
}

// Sample is one sampling period reported by intel_gpu_top.
//
// intel_gpu_top does not report temperature, fan speed or VRAM usage, so those
// are not part of a Sample. The optional values are pointers because the set of
// fields depends on the GPU and the intel_gpu_top version.
type Sample struct {
	Period             time.Duration
	FrequencyActual    float64 // MHz
	FrequencyRequested float64 // MHz
	Interrupts         float64 // interrupts per second
	RC6                float64 // % of the period spent in the RC6 power-saving state
	HasRC6             bool    // false if intel_gpu_top did not report rc6 (RC6 is then 0)
	PowerGPU           *float64
	PowerPackage       *float64 // W
	IMCReads           *float64 // MiB/s of memory-controller reads
	IMCWrites          *float64 // MiB/s of memory-controller writes
	Engines            map[string]EngineStats
}
