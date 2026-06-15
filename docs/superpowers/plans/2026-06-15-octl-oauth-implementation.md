# octl OAuth2 Flow Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a working OAuth2 authorization-code flow to `octl`: `octl auth` runs a PKCE browser flow to mint tokens, a pluggable `TokenStore` (encrypted file or OS keychain) persists the refresh token outside `config.toml`, a real `OuraRefresher` keeps the access token fresh, and `sync`/`backfill` work in `oauth2` mode.

**Architecture:** Extends `internal/auth/` (already holds `Tokens`, `Refresher`, `OAuthProvider`, AES-256-GCM crypto). New: a `TokenStore` interface with `FileStore` (encrypted, key from `OURA_ENCRYPTION_KEY`) and `KeychainStore` (`zalando/go-keyring`, no cgo); PKCE helpers; the authorization-URL builder, localhost callback handler, and code exchange; the real Oura token-endpoint `Refresher`; and a `NewOAuthProvider` that loads from a store and persists rotated tokens back. The CLI's `buildClient` gains an `oauth2` branch and `octl auth` runs the flow. Bottom-up, TDD; the browser/server glue and cobra wiring are verified by build, not unit tests.

**Tech Stack:** Go 1.26, existing deps + `github.com/zalando/go-keyring` (pure Go: shells to `security` on macOS, dbus on Linux). Oura OAuth endpoints: authorize `https://cloud.ouraring.com/oauth/authorize`, token `https://api.ouraring.com/oauth/token`.

**Spec:** `docs/superpowers/specs/2026-06-10-oura-tool-design.md` (OAuth2 flow + TokenStore sections)

**Worktree:** `~/dev/oura (worktree)`, branch `feat/octl-oauth`.

---

## Prerequisites

- Go at `~/.pixi/bin/go` (1.26.4). Prefix every bash command with `export PATH="$HOME/.pixi/bin:$PATH"`.
- `cd ~/dev/oura (worktree)` for all work.
- Existing packages (config, store, datasets, oura, auth, sync, export, cli) build and pass tests.

## File Structure

| Path | Responsibility |
|---|---|
| `internal/config/config.go` (modify) | Add `StorageConfig.TokenBackend`, `AuthConfig.Scope`, `Overrides.TokenBackend`, env+flag wiring, `TokenStorePath()` |
| `internal/auth/tokenstore.go` (create) | `TokenStore` interface, `ErrNoTokens`, `FileStore` (encrypted), `NewTokenStore` factory |
| `internal/auth/tokenstore_keychain.go` (create) | `KeychainStore` over `zalando/go-keyring` |
| `internal/auth/refresher.go` (create) | `OuraRefresher` (real token-endpoint POST) + shared `postTokenForm` |
| `internal/auth/pkce.go` (create) | `PKCE` generation (S256), `randString` |
| `internal/auth/oauthflow.go` (create) | `AuthorizeURL`, `ExchangeCode`, `callbackHandler`, `RunBrowserFlow`, `FlowConfig` |
| `internal/auth/oauth.go` (modify) | `NewOAuthProvider` constructor; bump skew to 5 min |
| `internal/cli/cli.go` (modify) | `--token-backend` flag; `buildOAuthClient` helper |
| `internal/cli/auth_cmd.go` (modify) | `oauth2` branch runs the flow, saves to store |
| `internal/cli/sync_cmd.go` (modify) | `buildClient` takes full config, gains `oauth2` branch |

---

### Task 1: Config additions (token_backend, scope)

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/config/config_test.go`:
```go
func TestTokenBackendAndScopeDefaultsAndOverride(t *testing.T) {
	d := Defaults()
	if d.Storage.TokenBackend != "file" {
		t.Fatalf("default token_backend = %q, want file", d.Storage.TokenBackend)
	}
	if d.Auth.Scope == "" {
		t.Fatal("default scope must be set")
	}
	got := Resolve(d, map[string]string{"OURA_TOKEN_BACKEND": "keychain"}, Overrides{})
	if got.Storage.TokenBackend != "keychain" {
		t.Fatalf("env override token_backend = %q, want keychain", got.Storage.TokenBackend)
	}
	got = Resolve(d, nil, Overrides{TokenBackend: "file"})
	if got.Storage.TokenBackend != "file" {
		t.Fatalf("flag override token_backend = %q, want file", got.Storage.TokenBackend)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `export PATH="$HOME/.pixi/bin:$PATH" && go test ./internal/config/ -run TestTokenBackend -v`
Expected: FAIL (unknown field `TokenBackend` / `Scope`).

- [ ] **Step 3: Add the fields, defaults, override, and path helper**

In `internal/config/config.go`, add `Scope` to `AuthConfig` and `TokenBackend` to `StorageConfig`:
```go
// in AuthConfig, after RedirectURI:
	Scope string `toml:"scope"`
// in StorageConfig, after EncryptionKey:
	TokenBackend string `toml:"token_backend"` // "file" (default) | "keychain"
```

Add `TokenBackend` to `Overrides` (after `StoragePath`):
```go
	TokenBackend string
```

In `Defaults()`, set the new defaults:
```go
	return Config{
		Auth: AuthConfig{
			Mode:  "token",
			Scope: "personal daily heartrate workout tag session spo2",
		},
		Storage: StorageConfig{TokenBackend: "file"},
		Collection: CollectionConfig{
			Enabled:     []string{"daily_sleep", "sleep", "daily_readiness", "daily_activity"},
			OverlapDays: 3,
		},
		Logging: LoggingConfig{Level: "info"},
	}
```

In `Resolve`, add one line alongside the other `set(...)` calls:
```go
	set(&c.Storage.TokenBackend, env["OURA_TOKEN_BACKEND"], flags.TokenBackend)
```

In `EnvMap`, add `"OURA_TOKEN_BACKEND"` to the `keys` slice.

Add a token-store path helper near `DefaultConfigPath`:
```go
// TokenStorePath is where the encrypted OAuth token file lives (file backend).
func TokenStorePath() (string, error) {
	return xdg.DataFile("oura/tokens.enc")
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `export PATH="$HOME/.pixi/bin:$PATH" && go test ./internal/config/ -v`
Expected: PASS (all config tests, including the new one).

- [ ] **Step 5: Commit**

```bash
export PATH="$HOME/.pixi/bin:$PATH"
git add internal/config/
git commit -m "feat(config): token_backend + oauth scope settings"
```

---

### Task 2: TokenStore interface + encrypted FileStore

**Files:**
- Create: `internal/auth/tokenstore.go`
- Test: `internal/auth/tokenstore_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/auth/tokenstore_test.go`:
```go
package auth

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileStoreRoundTripEncrypted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tokens.enc")
	key := DeriveKey("unit-test-key")
	s := NewFileStore(path, key)

	want := Tokens{AccessToken: "a", RefreshToken: "r", Expiry: time.Unix(1000, 0).UTC()}
	if err := s.Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// On-disk bytes must be ciphertext, not the plaintext token.
	raw, _ := os.ReadFile(path)
	if len(raw) == 0 || string(raw) == "a" || contains(raw, "refresh") {
		t.Fatalf("file is not encrypted: %q", raw)
	}
	// File must be owner-only.
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("perms = %v, want 0600", fi.Mode().Perm())
	}

	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != want {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}
}

func TestFileStoreLoadMissingIsErrNoTokens(t *testing.T) {
	s := NewFileStore(filepath.Join(t.TempDir(), "absent.enc"), DeriveKey("k"))
	if _, err := s.Load(); !errors.Is(err, ErrNoTokens) {
		t.Fatalf("missing file err = %v, want ErrNoTokens", err)
	}
}

func contains(b []byte, sub string) bool {
	return len(b) >= len(sub) && (string(b) == sub || indexOf(string(b), sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `export PATH="$HOME/.pixi/bin:$PATH" && go test ./internal/auth/ -run TestFileStore -v`
Expected: FAIL (undefined: NewFileStore / ErrNoTokens).

- [ ] **Step 3: Implement the interface, ErrNoTokens, and FileStore**

Create `internal/auth/tokenstore.go`:
```go
package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// ErrNoTokens signals that no OAuth tokens are stored yet (run `octl auth`).
var ErrNoTokens = errors.New("no oauth tokens stored")

// TokenStore persists OAuth tokens between runs, outside config.toml.
type TokenStore interface {
	Load() (Tokens, error) // returns ErrNoTokens when none stored
	Save(Tokens) error
}

// FileStore keeps tokens in an AES-256-GCM encrypted file at 0600.
type FileStore struct {
	path string
	key  []byte
}

func NewFileStore(path string, key []byte) *FileStore { return &FileStore{path: path, key: key} }

func (s *FileStore) Load() (Tokens, error) {
	ct, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return Tokens{}, ErrNoTokens
	}
	if err != nil {
		return Tokens{}, fmt.Errorf("read token file: %w", err)
	}
	plain, err := Decrypt(s.key, ct)
	if err != nil {
		return Tokens{}, fmt.Errorf("decrypt token file (wrong OURA_ENCRYPTION_KEY?): %w", err)
	}
	var t Tokens
	if err := json.Unmarshal(plain, &t); err != nil {
		return Tokens{}, fmt.Errorf("decode tokens: %w", err)
	}
	return t, nil
}

func (s *FileStore) Save(t Tokens) error {
	plain, err := json.Marshal(t)
	if err != nil {
		return fmt.Errorf("encode tokens: %w", err)
	}
	ct, err := Encrypt(s.key, plain)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create token dir: %w", err)
	}
	// Write to a temp file then rename for atomicity; chmod 0600 before rename.
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, ct, 0o600); err != nil {
		return fmt.Errorf("write token file: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("commit token file: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `export PATH="$HOME/.pixi/bin:$PATH" && go test ./internal/auth/ -run TestFileStore -v`
Expected: PASS (round trip, encryption, 0600, ErrNoTokens).

- [ ] **Step 5: Commit**

```bash
export PATH="$HOME/.pixi/bin:$PATH"
git add internal/auth/tokenstore.go internal/auth/tokenstore_test.go
git commit -m "feat(auth): TokenStore interface and encrypted FileStore"
```

---

### Task 3: KeychainStore (zalando/go-keyring)

**Files:**
- Create: `internal/auth/tokenstore_keychain.go`
- Test: `internal/auth/tokenstore_keychain_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/auth/tokenstore_keychain_test.go`:
```go
package auth

import (
	"errors"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

func TestKeychainStoreRoundTrip(t *testing.T) {
	keyring.MockInit() // in-memory keyring; no real OS keychain touched

	s := NewKeychainStore("octl-test")
	if _, err := s.Load(); !errors.Is(err, ErrNoTokens) {
		t.Fatalf("empty keychain err = %v, want ErrNoTokens", err)
	}

	want := Tokens{AccessToken: "a", RefreshToken: "r", Expiry: time.Unix(2000, 0).UTC()}
	if err := s.Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != want {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `export PATH="$HOME/.pixi/bin:$PATH" && go get github.com/zalando/go-keyring@latest && go test ./internal/auth/ -run TestKeychainStore -v`
Expected: FAIL (undefined: NewKeychainStore).

- [ ] **Step 3: Implement KeychainStore**

Create `internal/auth/tokenstore_keychain.go`:
```go
package auth

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/zalando/go-keyring"
)

// keychainUser is the fixed account name under the keychain service entry.
const keychainUser = "oauth"

// KeychainStore keeps tokens in the OS keychain (macOS Keychain, Linux Secret
// Service, Windows Credential Manager) via zalando/go-keyring. No token file
// touches disk.
type KeychainStore struct {
	service string
}

func NewKeychainStore(service string) *KeychainStore { return &KeychainStore{service: service} }

func (s *KeychainStore) Load() (Tokens, error) {
	v, err := keyring.Get(s.service, keychainUser)
	if errors.Is(err, keyring.ErrNotFound) {
		return Tokens{}, ErrNoTokens
	}
	if err != nil {
		return Tokens{}, fmt.Errorf("read keychain (%s): %w", s.service, err)
	}
	var t Tokens
	if err := json.Unmarshal([]byte(v), &t); err != nil {
		return Tokens{}, fmt.Errorf("decode keychain tokens: %w", err)
	}
	return t, nil
}

func (s *KeychainStore) Save(t Tokens) error {
	b, err := json.Marshal(t)
	if err != nil {
		return fmt.Errorf("encode tokens: %w", err)
	}
	if err := keyring.Set(s.service, keychainUser, string(b)); err != nil {
		return fmt.Errorf("write keychain (%s): %w", s.service, err)
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `export PATH="$HOME/.pixi/bin:$PATH" && go test ./internal/auth/ -run TestKeychainStore -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
export PATH="$HOME/.pixi/bin:$PATH"
git add internal/auth/tokenstore_keychain.go internal/auth/tokenstore_keychain_test.go go.mod go.sum
git commit -m "feat(auth): KeychainStore backend over zalando/go-keyring"
```

---

### Task 4: NewTokenStore factory

**Files:**
- Modify: `internal/auth/tokenstore.go`
- Test: `internal/auth/tokenstore_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/auth/tokenstore_test.go`:
```go
func TestNewTokenStoreSelection(t *testing.T) {
	// file backend needs an encryption key
	if _, err := NewTokenStore("file", "/tmp/x.enc", ""); err == nil {
		t.Fatal("file backend without encryption key must error")
	}
	s, err := NewTokenStore("file", "/tmp/x.enc", "k")
	if err != nil {
		t.Fatalf("file backend: %v", err)
	}
	if _, ok := s.(*FileStore); !ok {
		t.Fatalf("want *FileStore, got %T", s)
	}
	// empty backend defaults to file
	if _, err := NewTokenStore("", "/tmp/x.enc", "k"); err != nil {
		t.Fatalf("empty backend should default to file: %v", err)
	}
	s, err = NewTokenStore("keychain", "", "")
	if err != nil {
		t.Fatalf("keychain backend: %v", err)
	}
	if _, ok := s.(*KeychainStore); !ok {
		t.Fatalf("want *KeychainStore, got %T", s)
	}
	if _, err := NewTokenStore("bogus", "", ""); err == nil {
		t.Fatal("unknown backend must error")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `export PATH="$HOME/.pixi/bin:$PATH" && go test ./internal/auth/ -run TestNewTokenStore -v`
Expected: FAIL (undefined: NewTokenStore).

- [ ] **Step 3: Implement the factory**

Add to `internal/auth/tokenstore.go`:
```go
// NewTokenStore builds the configured backend. The file backend requires an
// encryption key (encrypting with a key that sits beside the ciphertext is
// pointless, so the key must come from env / a secrets manager).
func NewTokenStore(backend, filePath, encKey string) (TokenStore, error) {
	switch backend {
	case "", "file":
		if encKey == "" {
			return nil, fmt.Errorf("file token backend requires an encryption key (set OURA_ENCRYPTION_KEY) or use --token-backend keychain")
		}
		return NewFileStore(filePath, DeriveKey(encKey)), nil
	case "keychain":
		return NewKeychainStore("octl"), nil
	default:
		return nil, fmt.Errorf("unknown token_backend %q (want file or keychain)", backend)
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `export PATH="$HOME/.pixi/bin:$PATH" && go test ./internal/auth/ -run TestNewTokenStore -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
export PATH="$HOME/.pixi/bin:$PATH"
git add internal/auth/tokenstore.go internal/auth/tokenstore_test.go
git commit -m "feat(auth): NewTokenStore backend factory"
```

---

### Task 5: OuraRefresher (real token endpoint)

**Files:**
- Create: `internal/auth/refresher.go`
- Test: `internal/auth/refresher_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/auth/refresher_test.go`:
```go
package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestOuraRefresherExchangesAndComputesExpiry(t *testing.T) {
	var gotForm url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		gotForm = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"newA","refresh_token":"newR","expires_in":3600,"token_type":"Bearer"}`))
	}))
	defer srv.Close()

	fixed := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	ref := NewOuraRefresher(srv.URL, "cid", "csecret")
	ref.now = func() time.Time { return fixed }

	tok, err := ref.Refresh(context.Background(), "oldR")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if tok.AccessToken != "newA" || tok.RefreshToken != "newR" {
		t.Fatalf("tokens = %+v", tok)
	}
	if !tok.Expiry.Equal(fixed.Add(3600 * time.Second)) {
		t.Fatalf("expiry = %v, want now+3600s", tok.Expiry)
	}
	if gotForm.Get("grant_type") != "refresh_token" || gotForm.Get("refresh_token") != "oldR" ||
		gotForm.Get("client_id") != "cid" || gotForm.Get("client_secret") != "csecret" {
		t.Fatalf("form = %v", gotForm)
	}
}

// Oura rotates refresh tokens but may omit it; keep the old one so the next refresh works.
func TestOuraRefresherKeepsOldRefreshTokenWhenOmitted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"access_token":"newA","expires_in":3600}`))
	}))
	defer srv.Close()

	ref := NewOuraRefresher(srv.URL, "cid", "csecret")
	tok, err := ref.Refresh(context.Background(), "keepme")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if tok.RefreshToken != "keepme" {
		t.Fatalf("refresh token = %q, want keepme (fallback)", tok.RefreshToken)
	}
}

func TestOuraRefresherErrorsOnNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer srv.Close()
	ref := NewOuraRefresher(srv.URL, "cid", "csecret")
	if _, err := ref.Refresh(context.Background(), "r"); err == nil {
		t.Fatal("non-200 refresh must error loudly")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `export PATH="$HOME/.pixi/bin:$PATH" && go test ./internal/auth/ -run TestOuraRefresher -v`
Expected: FAIL (undefined: NewOuraRefresher).

- [ ] **Step 3: Implement OuraRefresher and the shared token POST**

Create `internal/auth/refresher.go`:
```go
package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultTokenURL is Oura's OAuth token endpoint.
const DefaultTokenURL = "https://api.ouraring.com/oauth/token"

// OuraRefresher implements Refresher against Oura's token endpoint.
type OuraRefresher struct {
	tokenURL     string
	clientID     string
	clientSecret string
	http         *http.Client
	now          func() time.Time
}

func NewOuraRefresher(tokenURL, clientID, clientSecret string) *OuraRefresher {
	return &OuraRefresher{
		tokenURL:     tokenURL,
		clientID:     clientID,
		clientSecret: clientSecret,
		http:         &http.Client{Timeout: 30 * time.Second},
		now:          time.Now,
	}
}

func (r *OuraRefresher) Refresh(ctx context.Context, refreshToken string) (Tokens, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {r.clientID},
		"client_secret": {r.clientSecret},
	}
	tok, err := postTokenForm(ctx, r.http, r.tokenURL, form, r.now)
	if err != nil {
		return Tokens{}, err
	}
	// Oura rotates the refresh token but may omit it; keep the old one.
	if tok.RefreshToken == "" {
		tok.RefreshToken = refreshToken
	}
	return tok, nil
}

// tokenResponse is Oura's token-endpoint JSON.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

// postTokenForm POSTs a form to the token endpoint and maps the response to
// Tokens, computing Expiry from expires_in. Shared by refresh and code exchange.
func postTokenForm(ctx context.Context, hc *http.Client, tokenURL string, form url.Values, now func() time.Time) (Tokens, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return Tokens{}, fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := hc.Do(req)
	if err != nil {
		return Tokens{}, fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return Tokens{}, fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, string(body))
	}
	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return Tokens{}, fmt.Errorf("decode token response: %w", err)
	}
	return Tokens{
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		Expiry:       now().Add(time.Duration(tr.ExpiresIn) * time.Second),
	}, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `export PATH="$HOME/.pixi/bin:$PATH" && go test ./internal/auth/ -run TestOuraRefresher -v`
Expected: PASS (exchange+expiry, rotation fallback, non-200 error).

- [ ] **Step 5: Commit**

```bash
export PATH="$HOME/.pixi/bin:$PATH"
git add internal/auth/refresher.go internal/auth/refresher_test.go
git commit -m "feat(auth): OuraRefresher against the token endpoint with rotation fallback"
```

---

### Task 6: PKCE generation

**Files:**
- Create: `internal/auth/pkce.go`
- Test: `internal/auth/pkce_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/auth/pkce_test.go`:
```go
package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

func TestNewPKCEChallengeMatchesVerifier(t *testing.T) {
	p, err := NewPKCE()
	if err != nil {
		t.Fatalf("NewPKCE: %v", err)
	}
	if len(p.Verifier) < 43 { // 32 bytes base64url = 43 chars
		t.Fatalf("verifier too short: %d", len(p.Verifier))
	}
	sum := sha256.Sum256([]byte(p.Verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if p.Challenge != want {
		t.Fatalf("challenge = %q, want S256(verifier) = %q", p.Challenge, want)
	}
	// Two calls must differ (random verifier).
	p2, _ := NewPKCE()
	if p2.Verifier == p.Verifier {
		t.Fatal("two PKCE verifiers identical; not random")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `export PATH="$HOME/.pixi/bin:$PATH" && go test ./internal/auth/ -run TestNewPKCE -v`
Expected: FAIL (undefined: NewPKCE).

- [ ] **Step 3: Implement PKCE**

Create `internal/auth/pkce.go`:
```go
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// PKCE holds the verifier kept by the client and the challenge sent to Oura.
type PKCE struct {
	Verifier  string
	Challenge string
}

// NewPKCE generates a fresh S256 verifier/challenge pair.
func NewPKCE() (PKCE, error) {
	v, err := randString(32)
	if err != nil {
		return PKCE{}, err
	}
	sum := sha256.Sum256([]byte(v))
	return PKCE{Verifier: v, Challenge: base64.RawURLEncoding.EncodeToString(sum[:])}, nil
}

// randString returns n random bytes base64url-encoded (no padding). Used for the
// PKCE verifier and the OAuth state parameter.
func randString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read random: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `export PATH="$HOME/.pixi/bin:$PATH" && go test ./internal/auth/ -run TestNewPKCE -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
export PATH="$HOME/.pixi/bin:$PATH"
git add internal/auth/pkce.go internal/auth/pkce_test.go
git commit -m "feat(auth): PKCE S256 generation"
```

---

### Task 7: Authorize URL, code exchange, callback handler, browser flow

**Files:**
- Create: `internal/auth/oauthflow.go`
- Test: `internal/auth/oauthflow_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/auth/oauthflow_test.go`:
```go
package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestAuthorizeURLContainsPKCEAndState(t *testing.T) {
	u := AuthorizeURL("https://cloud.ouraring.com/oauth/authorize",
		"cid", "http://localhost:8182/callback", "daily personal", "STATE123", "CHAL")
	parsed, err := url.Parse(u)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	q := parsed.Query()
	checks := map[string]string{
		"response_type": "code", "client_id": "cid",
		"redirect_uri": "http://localhost:8182/callback", "scope": "daily personal",
		"state": "STATE123", "code_challenge": "CHAL", "code_challenge_method": "S256",
	}
	for k, want := range checks {
		if q.Get(k) != want {
			t.Fatalf("%s = %q, want %q", k, q.Get(k), want)
		}
	}
}

func TestCallbackHandlerExtractsCodeAndChecksState(t *testing.T) {
	ch := make(chan callbackResult, 1)
	h := callbackHandler("S1", ch)

	// happy path
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest("GET", "/callback?code=ABC&state=S1", nil))
	got := <-ch
	if got.err != nil || got.code != "ABC" {
		t.Fatalf("happy path: code=%q err=%v", got.code, got.err)
	}

	// state mismatch must error, not yield a code
	ch2 := make(chan callbackResult, 1)
	h2 := callbackHandler("S1", ch2)
	h2(httptest.NewRecorder(), httptest.NewRequest("GET", "/callback?code=ABC&state=EVIL", nil))
	if r := <-ch2; r.err == nil {
		t.Fatal("state mismatch must error")
	}
}

func TestExchangeCodePostsAuthorizationCode(t *testing.T) {
	var form url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		form = r.PostForm
		w.Write([]byte(`{"access_token":"A","refresh_token":"R","expires_in":3600}`))
	}))
	defer srv.Close()

	fixed := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	tok, err := ExchangeCode(context.Background(), srv.URL, "cid", "sec",
		"http://localhost:8182/callback", "CODE", "VERIFIER", func() time.Time { return fixed })
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	if tok.AccessToken != "A" || tok.RefreshToken != "R" {
		t.Fatalf("tokens = %+v", tok)
	}
	if form.Get("grant_type") != "authorization_code" || form.Get("code") != "CODE" ||
		form.Get("code_verifier") != "VERIFIER" || !strings.Contains(form.Get("redirect_uri"), "callback") {
		t.Fatalf("form = %v", form)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `export PATH="$HOME/.pixi/bin:$PATH" && go test ./internal/auth/ -run 'TestAuthorizeURL|TestCallbackHandler|TestExchangeCode' -v`
Expected: FAIL (undefined: AuthorizeURL / callbackHandler / ExchangeCode).

- [ ] **Step 3: Implement the flow pieces**

Create `internal/auth/oauthflow.go`:
```go
package auth

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"time"
)

// DefaultAuthorizeURL is Oura's OAuth authorization endpoint.
const DefaultAuthorizeURL = "https://cloud.ouraring.com/oauth/authorize"

// DefaultRedirectURI is the localhost callback the one-shot server listens on.
const DefaultRedirectURI = "http://localhost:8182/callback"

// FlowConfig parameterizes the interactive authorization-code flow.
type FlowConfig struct {
	AuthorizeURL string // default DefaultAuthorizeURL if empty
	TokenURL     string // default DefaultTokenURL if empty
	ClientID     string
	ClientSecret string
	RedirectURI  string // default DefaultRedirectURI if empty
	Scope        string
}

// AuthorizeURL builds the authorization request URL.
func AuthorizeURL(base, clientID, redirectURI, scope, state, challenge string) string {
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"scope":                 {scope},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	return base + "?" + q.Encode()
}

// ExchangeCode swaps an authorization code for tokens.
func ExchangeCode(ctx context.Context, tokenURL, clientID, clientSecret, redirectURI, code, verifier string, now func() time.Time) (Tokens, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"code_verifier": {verifier},
	}
	return postTokenForm(ctx, &http.Client{Timeout: 30 * time.Second}, tokenURL, form, now)
}

type callbackResult struct {
	code string
	err  error
}

// callbackHandler returns an http handler that validates state, extracts the
// code, and reports the result on ch exactly once.
func callbackHandler(expectedState string, ch chan<- callbackResult) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if e := q.Get("error"); e != "" {
			ch <- callbackResult{err: fmt.Errorf("authorization denied: %s", e)}
			http.Error(w, "authorization failed", http.StatusBadRequest)
			return
		}
		if q.Get("state") != expectedState {
			ch <- callbackResult{err: fmt.Errorf("state mismatch (possible CSRF)")}
			http.Error(w, "state mismatch", http.StatusBadRequest)
			return
		}
		code := q.Get("code")
		if code == "" {
			ch <- callbackResult{err: fmt.Errorf("no code in callback")}
			http.Error(w, "no code", http.StatusBadRequest)
			return
		}
		ch <- callbackResult{code: code}
		fmt.Fprintln(w, "octl: authorized. You can close this tab.")
	}
}

// openURL opens a URL in the user's browser. Overridable in tests.
var openURL = func(u string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", u).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Start()
	default:
		return exec.Command("xdg-open", u).Start()
	}
}

// RunBrowserFlow runs the full PKCE authorization-code flow and returns tokens.
// It opens the browser, serves one callback on the redirect URI, and exchanges
// the code. Not unit-tested (browser + listener); its pieces are tested above.
func RunBrowserFlow(ctx context.Context, cfg FlowConfig) (Tokens, error) {
	if cfg.AuthorizeURL == "" {
		cfg.AuthorizeURL = DefaultAuthorizeURL
	}
	if cfg.TokenURL == "" {
		cfg.TokenURL = DefaultTokenURL
	}
	if cfg.RedirectURI == "" {
		cfg.RedirectURI = DefaultRedirectURI
	}
	pkce, err := NewPKCE()
	if err != nil {
		return Tokens{}, err
	}
	state, err := randString(16)
	if err != nil {
		return Tokens{}, err
	}
	redirect, err := url.Parse(cfg.RedirectURI)
	if err != nil {
		return Tokens{}, fmt.Errorf("parse redirect_uri: %w", err)
	}

	ch := make(chan callbackResult, 1)
	mux := http.NewServeMux()
	mux.HandleFunc(redirect.Path, callbackHandler(state, ch))
	ln, err := net.Listen("tcp", redirect.Host)
	if err != nil {
		return Tokens{}, fmt.Errorf("listen on %s (is the port free?): %w", redirect.Host, err)
	}
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	defer srv.Close()

	authURL := AuthorizeURL(cfg.AuthorizeURL, cfg.ClientID, cfg.RedirectURI, cfg.Scope, state, pkce.Challenge)
	if err := openURL(authURL); err != nil {
		fmt.Printf("Open this URL to authorize:\n%s\n", authURL)
	}

	select {
	case <-ctx.Done():
		return Tokens{}, ctx.Err()
	case <-time.After(2 * time.Minute):
		return Tokens{}, fmt.Errorf("timed out waiting for authorization callback")
	case res := <-ch:
		if res.err != nil {
			return Tokens{}, res.err
		}
		return ExchangeCode(ctx, cfg.TokenURL, cfg.ClientID, cfg.ClientSecret, cfg.RedirectURI, res.code, pkce.Verifier, time.Now)
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `export PATH="$HOME/.pixi/bin:$PATH" && go test ./internal/auth/ -run 'TestAuthorizeURL|TestCallbackHandler|TestExchangeCode' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
export PATH="$HOME/.pixi/bin:$PATH"
git add internal/auth/oauthflow.go internal/auth/oauthflow_test.go
git commit -m "feat(auth): authorize URL, code exchange, callback handler, browser flow"
```

---

### Task 8: NewOAuthProvider (load from store, persist back, 5-min skew)

**Files:**
- Modify: `internal/auth/oauth.go`
- Test: `internal/auth/oauth_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/auth/oauth_test.go`:
```go
// memStore is an in-memory TokenStore for provider tests.
type memStore struct {
	t      Tokens
	loaded bool
	saved  *Tokens
}

func (m *memStore) Load() (Tokens, error) {
	if !m.loaded {
		return Tokens{}, ErrNoTokens
	}
	return m.t, nil
}
func (m *memStore) Save(t Tokens) error { m.saved = &t; return nil }

func TestNewOAuthProviderLoadsAndPersists(t *testing.T) {
	// no tokens yet -> ErrNoTokens surfaces
	empty := &memStore{}
	if _, err := NewOAuthProvider(empty, &fakeRefresher{}); err == nil {
		t.Fatal("provider with no stored tokens should error (run octl auth)")
	}

	// expired token -> Token() refreshes and persists the fresh set to the store
	store := &memStore{loaded: true, t: Tokens{AccessToken: "stale", RefreshToken: "r0",
		Expiry: time.Now().Add(-time.Hour)}}
	p, err := NewOAuthProvider(store, &fakeRefresher{})
	if err != nil {
		t.Fatalf("NewOAuthProvider: %v", err)
	}
	tok, err := p.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok != "fresh-access" {
		t.Fatalf("token = %q, want fresh-access", tok)
	}
	if store.saved == nil || store.saved.AccessToken != "fresh-access" {
		t.Fatalf("refreshed tokens not persisted to store: %+v", store.saved)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `export PATH="$HOME/.pixi/bin:$PATH" && go test ./internal/auth/ -run TestNewOAuthProvider -v`
Expected: FAIL (undefined: NewOAuthProvider).

- [ ] **Step 3: Add the constructor and bump the skew**

In `internal/auth/oauth.go`, change the skew constant:
```go
const expirySkew = 5 * time.Minute
```

Add the constructor (and ensure `import "context"` and `"time"` remain):
```go
// NewOAuthProvider loads tokens from the store and persists refreshed tokens
// back to it. Returns ErrNoTokens (wrapped) if nothing is stored yet.
func NewOAuthProvider(store TokenStore, refresher Refresher) (*OAuthProvider, error) {
	t, err := store.Load()
	if err != nil {
		return nil, err
	}
	return &OAuthProvider{
		tokens:    t,
		refresher: refresher,
		now:       time.Now,
		persist:   store.Save,
	}, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `export PATH="$HOME/.pixi/bin:$PATH" && go test ./internal/auth/ -v`
Expected: PASS (all auth tests, including pre-existing refresh tests; the 5-min skew keeps them green because the fixtures sit an hour off the boundary).

- [ ] **Step 5: Commit**

```bash
export PATH="$HOME/.pixi/bin:$PATH"
git add internal/auth/oauth.go internal/auth/oauth_test.go
git commit -m "feat(auth): NewOAuthProvider store integration, 5-min refresh buffer"
```

---

### Task 9: CLI wiring (token-backend flag, oauth2 in auth + sync)

Cobra wiring is verified by build and runtime smoke, not unit tests.

**Files:**
- Modify: `internal/cli/cli.go`, `internal/cli/auth_cmd.go`, `internal/cli/sync_cmd.go`

- [ ] **Step 1: Add the `--token-backend` global flag**

In `internal/cli/cli.go`, inside `addGlobalFlags`, add:
```go
	f.StringVar(&g.overrides.TokenBackend, "token-backend", "", "oauth token store: file or keychain")
```

- [ ] **Step 2: Add a shared oauth client/store builder**

In `internal/cli/cli.go`, add (it imports `config`, `auth`, `oura` already used in the package; add any missing imports):
```go
// buildTokenStore constructs the configured oauth TokenStore.
func buildTokenStore(c config.Config) (auth.TokenStore, error) {
	path, err := config.TokenStorePath()
	if err != nil {
		return nil, err
	}
	return auth.NewTokenStore(c.Storage.TokenBackend, path, c.Storage.EncryptionKey)
}

// oauthClient builds an Oura client backed by the stored OAuth tokens.
func oauthClient(c config.Config) (*oura.Client, error) {
	store, err := buildTokenStore(c)
	if err != nil {
		return nil, err
	}
	refresher := auth.NewOuraRefresher(auth.DefaultTokenURL, c.Auth.ClientID, c.Auth.ClientSecret)
	prov, err := auth.NewOAuthProvider(store, refresher)
	if err != nil {
		if errors.Is(err, auth.ErrNoTokens) {
			return nil, fmt.Errorf("no oauth tokens stored; run `octl auth` first")
		}
		return nil, err
	}
	return oura.New(prov, ouraBaseURL), nil
}
```
Add `"errors"` to the `cli.go` import block if not present, and ensure `"github.com/cfenogli0/ouractl/internal/auth"` and `"github.com/cfenogli0/ouractl/internal/oura"` are imported there (add them).

- [ ] **Step 3: Replace `buildClient` in sync_cmd.go with a config-driven version**

In `internal/cli/sync_cmd.go`, delete the `configForClient` type and the old `buildClient`, and replace with:
```go
func buildClient(c config.Config) (*oura.Client, error) {
	switch c.Auth.Mode {
	case "token":
		return oura.New(auth.NewStaticProvider(c.Auth.Token), ouraBaseURL), nil
	case "oauth2":
		return oauthClient(c)
	default:
		return nil, fmt.Errorf("unknown auth mode %q", c.Auth.Mode)
	}
}
```
Update the two call sites in `newSyncCmd` and `newBackfillCmd` from `buildClient(configForClient{c.Auth.Mode, c.Auth.Token})` to `buildClient(c)`. Add `"github.com/cfenogli0/ouractl/internal/config"` to the sync_cmd.go imports.

- [ ] **Step 4: Implement the oauth2 branch in auth_cmd.go**

In `internal/cli/auth_cmd.go`, replace the `case "oauth2":` line and its error return with:
```go
			case "oauth2":
				store, err := buildTokenStore(c)
				if err != nil {
					return err
				}
				toks, err := auth.RunBrowserFlow(context.Background(), auth.FlowConfig{
					ClientID:     c.Auth.ClientID,
					ClientSecret: c.Auth.ClientSecret,
					RedirectURI:  c.Auth.RedirectURI, // empty -> DefaultRedirectURI
					Scope:        c.Auth.Scope,
				})
				if err != nil {
					return fmt.Errorf("oauth2 authorization failed: %w", err)
				}
				if err := store.Save(toks); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "oauth2 authorized; tokens stored via %s backend\n", backendName(c))
				return nil
```
Add a small helper at the bottom of `auth_cmd.go`:
```go
func backendName(c config.Config) string {
	if c.Storage.TokenBackend == "" {
		return "file"
	}
	return c.Storage.TokenBackend
}
```
Add `"github.com/cfenogli0/ouractl/internal/config"` to the auth_cmd.go imports.

- [ ] **Step 5: Build, vet, full test suite**

Run:
```bash
export PATH="$HOME/.pixi/bin:$PATH"
go build -o octl ./cmd/octl
go vet ./...
go test ./... -count=1 2>&1 | tail -12
```
Expected: build OK, vet clean, all packages pass.

- [ ] **Step 6: Runtime smoke (no real Oura needed)**

Run:
```bash
export PATH="$HOME/.pixi/bin:$PATH"
# file backend without a key must error clearly
./octl sync --auth-mode oauth2 --client-id x --client-secret y --token-backend file --db /tmp/octl-oauth.db 2>&1 | head -3
# keychain backend selected, but no tokens stored yet -> "run octl auth"
./octl sync --auth-mode oauth2 --client-id x --client-secret y --token-backend keychain --db /tmp/octl-oauth.db 2>&1 | head -3
./octl auth --help 2>&1 | head -3
```
Expected: first prints the "file token backend requires an encryption key" error; second prints "no oauth tokens stored; run `octl auth` first"; auth help renders.

- [ ] **Step 7: Commit**

```bash
export PATH="$HOME/.pixi/bin:$PATH"
git add internal/cli/
git commit -m "feat(cli): oauth2 wiring for auth and sync, --token-backend flag"
```

---

## Self-Review

**Spec coverage (OAuth2 + TokenStore sections):**
- PKCE S256 flow: Task 6 (PKCE), Task 7 (authorize URL, callback, exchange, RunBrowserFlow).
- Authorization-code grant, browser + localhost callback, `state` verified: Task 7.
- Token endpoint exchange + refresh, rotation fallback: Task 5 (refresh), Task 7 (code exchange via shared `postTokenForm`).
- Pluggable `TokenStore`, refresh token never in `config.toml`: Tasks 2 (interface+FileStore), 3 (KeychainStore), 4 (factory).
- FileStore AES-256-GCM, 0600, key from `OURA_ENCRYPTION_KEY`, errors if key unset: Tasks 2, 4.
- KeychainStore via zalando/go-keyring, no cgo: Task 3.
- Backend selection `file` default / `keychain`, env+flag: Task 1 (config), Task 9 (flag + builder).
- Proactive 5-min refresh buffer, load-from-store, persist-back: Task 8.
- Scope default = Oura read set: Task 1 (default), Task 9 (passed into the flow).
- `octl auth` oauth2 + `sync`/`backfill` oauth2: Task 9.

**Testing-bar coverage:** OAuth refresh success + failure + rotation (Task 5); code exchange (Task 7); FileStore encryption/0600/ErrNoTokens (Task 2); KeychainStore round trip via MockInit (Task 3); backend selection incl. file-needs-key (Task 4); PKCE challenge integrity (Task 6); provider load+persist (Task 8). `RunBrowserFlow` and cobra wiring are integration glue (build + smoke), consistent with the spec's "not tested: cobra wiring" boundary.

**Placeholder scan:** none. Every code step shows complete code.

**Type consistency:** `Tokens`, `Refresher`, `OAuthProvider`, `TokenStore`, `FileStore`, `KeychainStore`, `NewTokenStore(backend, filePath, encKey)`, `OuraRefresher`/`NewOuraRefresher`, `postTokenForm(ctx, hc, url, form, now)`, `PKCE`/`NewPKCE`, `AuthorizeURL`, `ExchangeCode(ctx, tokenURL, id, secret, redirect, code, verifier, now)`, `callbackHandler(state, ch)`, `RunBrowserFlow(ctx, FlowConfig)`, `NewOAuthProvider(store, refresher)`, `config.TokenStorePath()`, `config.StorageConfig.TokenBackend`, `config.AuthConfig.Scope`, `oauthClient`/`buildTokenStore`/`buildClient(config.Config)` are used identically across tasks. `DefaultTokenURL`/`DefaultAuthorizeURL`/`DefaultRedirectURI` constants live in the auth package and are referenced by the CLI.

**One integration note for the implementer:** Task 9 changes `buildClient`'s signature, so it must update both call sites in `sync_cmd.go` in the same task or the package will not compile. The smoke test in Step 6 is the guard.
