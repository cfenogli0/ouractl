package datasets

import "sort"

// Dataset is one Oura collection. Key derives the storage primary key and the
// range key; Normalize maps a raw API object to the JSON stored in payload.
type Dataset struct {
	Name      string
	Endpoint  string // path under /v2/usercollection/
	Key       func(raw map[string]any) (id, day string)
	Normalize func(raw map[string]any) ([]byte, error)
}

var registry = map[string]Dataset{}

// Register adds a dataset. Called from normalizers.go init().
func Register(d Dataset) { registry[d.Name] = d }

// Get returns a dataset by name.
func Get(name string) (Dataset, bool) {
	d, ok := registry[name]
	return d, ok
}

// All returns every registered dataset name, sorted.
func All() []string {
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// DefaultEnabled is the minimal collection set used when config does not say.
func DefaultEnabled() []string {
	return []string{"daily_sleep", "sleep", "daily_readiness", "daily_activity"}
}
