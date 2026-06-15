package datasets

import (
	"encoding/json"
	"fmt"
)

// str safely reads a string field from a decoded JSON object.
func str(raw map[string]any, key string) string {
	if v, ok := raw[key].(string); ok {
		return v
	}
	return ""
}

// dayKeyed is the common key function for one-per-day datasets that carry both
// an "id" and a "day".
func dayKeyed(raw map[string]any) (string, string) {
	return str(raw, "id"), str(raw, "day")
}

// passthrough re-serializes the raw object unchanged; v1 stores the full
// normalized record and projects fields at read time, so the normalizer's only
// job is to be the explicit, tested place where mapping lives.
func passthrough(raw map[string]any) ([]byte, error) {
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}
	return b, nil
}

func init() {
	// daily_sleep carries the sleep score (v2). sleep does not.
	Register(Dataset{Name: "daily_sleep", Endpoint: "daily_sleep", Key: dayKeyed, Normalize: passthrough})
	Register(Dataset{Name: "sleep", Endpoint: "sleep", Key: dayKeyed, Normalize: passthrough})
	Register(Dataset{Name: "daily_readiness", Endpoint: "daily_readiness", Key: dayKeyed, Normalize: passthrough})
	Register(Dataset{Name: "daily_activity", Endpoint: "daily_activity", Key: dayKeyed, Normalize: passthrough})

	// Opt-in datasets (not in DefaultEnabled). Same day-keyed shape.
	for _, name := range []string{
		"daily_spo2", "daily_stress", "workout", "session",
		"daily_resilience", "daily_cardiovascular_age", "vO2_max",
		"sleep_time", "rest_mode_period", "ring_configuration",
	} {
		Register(Dataset{Name: name, Endpoint: name, Key: dayKeyed, Normalize: passthrough})
	}
}
