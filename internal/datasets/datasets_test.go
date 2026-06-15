package datasets

import "testing"

func TestRegistryDefaultsAndLookup(t *testing.T) {
	def := DefaultEnabled()
	want := []string{"daily_sleep", "sleep", "daily_readiness", "daily_activity"}
	if len(def) != len(want) {
		t.Fatalf("DefaultEnabled = %v, want %v", def, want)
	}
	for _, name := range want {
		d, ok := Get(name)
		if !ok {
			t.Fatalf("default dataset %q not registered", name)
		}
		if d.Endpoint == "" || d.Key == nil || d.Normalize == nil {
			t.Fatalf("dataset %q registered incompletely: %+v", name, d)
		}
	}
	if _, ok := Get("does_not_exist"); ok {
		t.Fatal("Get on unknown dataset should return ok=false")
	}
}
