package intel

import (
	"encoding/json"
	"time"
)

// rawSample mirrors one object of intel_gpu_top's JSON output (-J).
type rawSample struct {
	Period struct {
		Duration float64 `json:"duration"` // milliseconds
	} `json:"period"`
	Frequency struct {
		Requested float64 `json:"requested"`
		Actual    float64 `json:"actual"`
	} `json:"frequency"`
	Interrupts struct {
		Count float64 `json:"count"`
	} `json:"interrupts"`
	RC6 *struct {
		Value float64 `json:"value"`
	} `json:"rc6"`
	Power struct {
		GPU     *float64 `json:"GPU"`
		Package *float64 `json:"Package"`
	} `json:"power"`
	IMC *struct {
		Reads  float64 `json:"reads"`
		Writes float64 `json:"writes"`
	} `json:"imc-bandwidth"`
	Engines map[string]struct {
		Busy float64 `json:"busy"`
		Sema float64 `json:"sema"`
		Wait float64 `json:"wait"`
	} `json:"engines"`
}

func (r *rawSample) sample() Sample {
	s := Sample{
		Period:             time.Duration(r.Period.Duration * float64(time.Millisecond)),
		FrequencyActual:    r.Frequency.Actual,
		FrequencyRequested: r.Frequency.Requested,
		Interrupts:         r.Interrupts.Count,
		PowerGPU:           r.Power.GPU,
		PowerPackage:       r.Power.Package,
		Engines:            make(map[string]EngineStats, len(r.Engines)),
	}
	if r.RC6 != nil {
		s.RC6, s.HasRC6 = r.RC6.Value, true
	}
	if r.IMC != nil {
		reads, writes := r.IMC.Reads, r.IMC.Writes
		s.IMCReads, s.IMCWrites = &reads, &writes
	}
	for name, e := range r.Engines {
		s.Engines[name] = EngineStats{Busy: e.Busy, Sema: e.Sema, Wait: e.Wait}
	}
	return s
}

// ParseSample decodes a single intel_gpu_top JSON object.
func ParseSample(data []byte) (Sample, error) {
	var raw rawSample
	if err := json.Unmarshal(data, &raw); err != nil {
		return Sample{}, err
	}
	return raw.sample(), nil
}
