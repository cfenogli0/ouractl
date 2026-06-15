package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/cfenogli0/ouractl/internal/auth"
	"github.com/cfenogli0/ouractl/internal/config"
	"github.com/cfenogli0/ouractl/internal/datasets"
	"github.com/cfenogli0/ouractl/internal/oura"
	"github.com/spf13/cobra"
)

type globalFlags struct {
	configPath string
	overrides  config.Overrides
}

func addGlobalFlags(cmd *cobra.Command, g *globalFlags) {
	f := cmd.PersistentFlags()
	f.StringVar(&g.configPath, "config", "", "config file path (default XDG ~/.config/oura/config.toml)")
	f.StringVar(&g.overrides.Mode, "auth-mode", "", "auth mode: token or oauth2")
	f.StringVar(&g.overrides.Token, "oura-token", "", "Oura personal access token")
	f.StringVar(&g.overrides.ClientID, "client-id", "", "OAuth2 client id")
	f.StringVar(&g.overrides.ClientSecret, "client-secret", "", "OAuth2 client secret")
	f.StringVar(&g.overrides.RedirectURI, "redirect-uri", "", "OAuth2 redirect URI")
	f.StringVar(&g.overrides.EncryptionKey, "encryption-key", "", "token store encryption key")
	f.StringVar(&g.overrides.StoragePath, "db", "", "SQLite database path")
	f.StringVar(&g.overrides.TokenBackend, "token-backend", "", "oauth token store: file or keychain")
}

func (g *globalFlags) load() (config.Config, error) {
	path := g.configPath
	if path == "" {
		p, err := config.DefaultConfigPath()
		if err != nil {
			return config.Config{}, err
		}
		path = p
	}
	base, err := config.LoadFile(path)
	if err != nil {
		return config.Config{}, err
	}
	c := config.Resolve(base, config.EnvMap(), g.overrides)
	if err := config.ResolvePaths(&c); err != nil {
		return config.Config{}, err
	}
	return c, nil
}

// NewRootCmd builds the full command tree. main.go calls this.
func NewRootCmd(version string) *cobra.Command {
	g := &globalFlags{}
	root := &cobra.Command{
		Use:           "octl",
		Short:         "octl pulls Oura Ring data into a local store and exports it",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
	}
	addGlobalFlags(root, g)
	root.AddCommand(
		newConfigCmd(g),
		newStatusCmd(g),
		newAuthCmd(g),
		newSyncCmd(g),
		newBackfillCmd(g),
		newExportCmd(g),
	)
	return root
}

func mustValidate(c config.Config) error {
	if err := config.Validate(c); err != nil {
		return fmt.Errorf("config invalid: %w", err)
	}
	return nil
}

// today returns the current date as YYYY-MM-DD.
func today() string { return time.Now().Format("2006-01-02") }

// enabledDatasets resolves config names into dataset values, erroring on any
// unknown name so a typo in config fails loud rather than silently skipping.
func enabledDatasets(names []string) ([]datasets.Dataset, error) {
	out := make([]datasets.Dataset, 0, len(names))
	for _, n := range names {
		d, ok := datasets.Get(n)
		if !ok {
			return nil, fmt.Errorf("unknown dataset %q in collection.enabled", n)
		}
		out = append(out, d)
	}
	return out, nil
}

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

// persistAuthSettings writes the NON-SECRET auth settings that subsequent
// commands need (mode, client_id, redirect_uri, scope, token_backend) into the
// config file, so `octl auth` makes the chosen mode sticky. Secrets resolved
// from env or flags (client_secret, encryption_key) are never written; secrets
// already present in the file are preserved. Returns the path written.
func (g *globalFlags) persistAuthSettings(c config.Config) (string, error) {
	path := g.configPath
	if path == "" {
		p, err := config.DefaultConfigPath()
		if err != nil {
			return "", err
		}
		path = p
	}
	fileCfg, err := config.LoadFile(path) // file values only (+ defaults); preserves file secrets
	if err != nil {
		return "", err
	}
	fileCfg.Auth.Mode = c.Auth.Mode
	fileCfg.Auth.ClientID = c.Auth.ClientID
	fileCfg.Auth.RedirectURI = c.Auth.RedirectURI
	fileCfg.Auth.Scope = c.Auth.Scope
	fileCfg.Storage.TokenBackend = c.Storage.TokenBackend
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("create config dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", fmt.Errorf("open config for write: %w", err)
	}
	defer f.Close()
	if err := toml.NewEncoder(f).Encode(fileCfg); err != nil {
		return "", fmt.Errorf("write config: %w", err)
	}
	return path, nil
}

// guardReauth refuses to overwrite an existing token store that the current
// credentials cannot read, unless force is set. The common cause is a rotated
// OURA_ENCRYPTION_KEY: silently re-authing would orphan tokens that are still
// valid under the original key. A missing store (first auth) or a readable store
// (legitimate re-consent) is allowed.
func guardReauth(store auth.TokenStore, force bool) error {
	if force {
		return nil
	}
	_, err := store.Load()
	if err == nil || errors.Is(err, auth.ErrNoTokens) {
		return nil
	}
	return fmt.Errorf("an existing token store could not be read with the current credentials (%w); "+
		"if you rotated OURA_ENCRYPTION_KEY or switched backends, re-run `octl auth` with --force to overwrite and re-authorize", err)
}
