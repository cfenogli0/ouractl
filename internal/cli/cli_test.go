package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cfenogli0/ouractl/internal/auth"
	"github.com/cfenogli0/ouractl/internal/config"
)

// fakeStore lets us simulate the three Load outcomes guardReauth cares about.
type fakeStore struct {
	tokens auth.Tokens
	err    error
}

func (f fakeStore) Load() (auth.Tokens, error) { return f.tokens, f.err }
func (f fakeStore) Save(auth.Tokens) error     { return nil }

func TestGuardReauth(t *testing.T) {
	// First auth (no tokens yet): allowed.
	if err := guardReauth(fakeStore{err: auth.ErrNoTokens}, false); err != nil {
		t.Fatalf("first auth should be allowed, got %v", err)
	}
	// Existing readable tokens: re-auth allowed (refreshing consent is legit).
	if err := guardReauth(fakeStore{tokens: auth.Tokens{AccessToken: "a"}}, false); err != nil {
		t.Fatalf("re-auth with readable store should be allowed, got %v", err)
	}
	// Existing store that can't be decrypted (rotated key): refuse without --force.
	decryptErr := errors.New("decrypt token file (wrong OURA_ENCRYPTION_KEY?)")
	err := guardReauth(fakeStore{err: decryptErr}, false)
	if err == nil {
		t.Fatal("unreadable store must refuse re-auth without --force")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Fatalf("refusal should mention --force, got %v", err)
	}
	// --force overrides the guard.
	if err := guardReauth(fakeStore{err: decryptErr}, true); err != nil {
		t.Fatalf("--force should override the guard, got %v", err)
	}
}

// persistAuthSettings must write the mode and client_id so subsequent commands
// inherit them, but must NEVER write secrets resolved from env/flags.
func TestPersistAuthSettingsExcludesSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	g := &globalFlags{configPath: path}

	c := config.Defaults()
	c.Auth.Mode = "oauth2"
	c.Auth.ClientID = "client-abc"
	c.Auth.ClientSecret = "SUPER-SECRET" // resolved from a flag/env; must not be persisted
	c.Storage.EncryptionKey = "ENC-SECRET" // ditto
	c.Storage.TokenBackend = "keychain"

	if _, err := g.persistAuthSettings(c); err != nil {
		t.Fatalf("persistAuthSettings: %v", err)
	}
	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, `mode = "oauth2"`) || !strings.Contains(s, `client-abc`) || !strings.Contains(s, `keychain`) {
		t.Fatalf("expected mode/client_id/backend persisted, got:\n%s", s)
	}
	if strings.Contains(s, "SUPER-SECRET") || strings.Contains(s, "ENC-SECRET") {
		t.Fatalf("SECRET LEAKED to config file:\n%s", s)
	}

	// And the persisted file makes mode sticky: reloading yields oauth2.
	reloaded, err := config.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Auth.Mode != "oauth2" || reloaded.Storage.TokenBackend != "keychain" {
		t.Fatalf("reloaded config not sticky: %+v", reloaded)
	}
}
