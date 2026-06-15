package datasets

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func loadRaw(t *testing.T, name string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name+".json"))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal fixture %s: %v", name, err)
	}
	return m
}

func TestKeyExtraction(t *testing.T) {
	cases := []struct{ name, wantID, wantDay string }{
		{"daily_sleep", "ds-2026-06-10", "2026-06-10"},
		{"sleep", "sl-abc123", "2026-06-10"},
		{"daily_readiness", "dr-2026-06-10", "2026-06-10"},
		{"daily_activity", "da-2026-06-10", "2026-06-10"},
	}
	for _, c := range cases {
		d, ok := Get(c.name)
		if !ok {
			t.Fatalf("%s not registered", c.name)
		}
		id, day := d.Key(loadRaw(t, c.name))
		if id != c.wantID || day != c.wantDay {
			t.Fatalf("%s key = (%q,%q), want (%q,%q)", c.name, id, day, c.wantID, c.wantDay)
		}
	}
}

// Guards the Python bug: the sleep score belongs to daily_sleep, not sleep.
func TestSleepScoreComesFromDailySleepNotSleep(t *testing.T) {
	ds, _ := Get("daily_sleep")
	payload, err := ds.Normalize(loadRaw(t, "daily_sleep"))
	if err != nil {
		t.Fatalf("normalize daily_sleep: %v", err)
	}
	var out map[string]any
	json.Unmarshal(payload, &out)
	if out["score"] != float64(82) {
		t.Fatalf("daily_sleep score = %v, want 82", out["score"])
	}

	sl, _ := Get("sleep")
	payload, _ = sl.Normalize(loadRaw(t, "sleep"))
	var sleepOut map[string]any
	json.Unmarshal(payload, &sleepOut)
	if _, present := sleepOut["score"]; present {
		t.Fatal("sleep payload must NOT carry a score field (v2 has none); that was the bug")
	}
}
