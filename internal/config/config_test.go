package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultsAreMinimalAndSafe(t *testing.T) {
	c := Defaults()
	if c.Auth.Mode != "token" {
		t.Fatalf("default auth mode = %q, want token", c.Auth.Mode)
	}
	if c.Collection.OverlapDays != 3 {
		t.Fatalf("default overlap_days = %d, want 3", c.Collection.OverlapDays)
	}
	want := []string{"daily_sleep", "sleep", "daily_readiness", "daily_activity"}
	if len(c.Collection.Enabled) != len(want) {
		t.Fatalf("default enabled = %v, want %v", c.Collection.Enabled, want)
	}
	for i := range want {
		if c.Collection.Enabled[i] != want[i] {
			t.Fatalf("default enabled[%d] = %q, want %q", i, c.Collection.Enabled[i], want[i])
		}
	}
}

func TestLoadFileOverlaysDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(`
[auth]
mode = "oauth2"
client_id = "abc"

[collection]
enabled = ["daily_sleep"]
overlap_days = 5
`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if c.Auth.Mode != "oauth2" || c.Auth.ClientID != "abc" {
		t.Fatalf("auth not loaded: %+v", c.Auth)
	}
	if c.Collection.OverlapDays != 5 || len(c.Collection.Enabled) != 1 {
		t.Fatalf("collection not loaded: %+v", c.Collection)
	}
	if c.Logging.Level != "info" {
		t.Fatalf("logging default lost: %q", c.Logging.Level)
	}
}

func TestLoadFileMissingReturnsDefaults(t *testing.T) {
	c, err := LoadFile(filepath.Join(t.TempDir(), "absent.toml"))
	if err != nil {
		t.Fatalf("missing file should be non-fatal: %v", err)
	}
	if c.Auth.Mode != "token" {
		t.Fatalf("want defaults on missing file, got %+v", c.Auth)
	}
}

func TestResolvePrecedence(t *testing.T) {
	base := Defaults()
	base.Auth.Token = "from-file"
	base.Auth.ClientID = "file-client"

	env := map[string]string{
		"OURA_TOKEN":     "from-env",
		"OURA_AUTH_MODE": "oauth2",
	}
	flags := Overrides{Token: "from-flag"}

	got := Resolve(base, env, flags)

	if got.Auth.Token != "from-flag" {
		t.Fatalf("token = %q, want from-flag", got.Auth.Token)
	}
	if got.Auth.Mode != "oauth2" {
		t.Fatalf("mode = %q, want oauth2", got.Auth.Mode)
	}
	if got.Auth.ClientID != "file-client" {
		t.Fatalf("client_id = %q, want file-client", got.Auth.ClientID)
	}
}

func TestValidate(t *testing.T) {
	ok := Defaults()
	ok.Auth.Token = "t"
	if err := Validate(ok); err != nil {
		t.Fatalf("valid token config rejected: %v", err)
	}
	bad := Defaults()
	if Validate(bad) == nil {
		t.Fatal("token mode without token should fail validation")
	}
	oauthMissing := Defaults()
	oauthMissing.Auth.Mode = "oauth2"
	if Validate(oauthMissing) == nil {
		t.Fatal("oauth2 without client creds should fail validation")
	}
}
