package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/BurntSushi/toml"
	"github.com/adrg/xdg"
)

// Config is the fully-resolved tool configuration.
type Config struct {
	Auth       AuthConfig       `toml:"auth"`
	Storage    StorageConfig    `toml:"storage"`
	Collection CollectionConfig `toml:"collection"`
	Logging    LoggingConfig    `toml:"logging"`
}

type AuthConfig struct {
	Mode         string `toml:"mode"` // "token" | "oauth2"
	Token        string `toml:"token"`
	ClientID     string `toml:"client_id"`
	ClientSecret string `toml:"client_secret"`
	RedirectURI  string `toml:"redirect_uri"`
}

type StorageConfig struct {
	Path          string `toml:"path"`
	EncryptionKey string `toml:"encryption_key"`
}

type CollectionConfig struct {
	Enabled     []string `toml:"enabled"`
	OverlapDays int      `toml:"overlap_days"`
}

type LoggingConfig struct {
	Level string `toml:"level"`
}

// Defaults returns the built-in configuration: minimal dataset set, token auth,
// 3-day overlap window. Storage path stays empty here; ResolvePaths fills it
// from XDG so this function has no filesystem dependency (keeps it testable).
func Defaults() Config {
	return Config{
		Auth: AuthConfig{Mode: "token"},
		Collection: CollectionConfig{
			Enabled:     []string{"daily_sleep", "sleep", "daily_readiness", "daily_activity"},
			OverlapDays: 3,
		},
		Logging: LoggingConfig{Level: "info"},
	}
}

// LoadFile starts from Defaults() and overlays values present in the TOML file.
// A missing file is not an error: the tool must run from env/flags alone.
func LoadFile(path string) (Config, error) {
	c := Defaults()
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return c, nil
	} else if err != nil {
		return c, fmt.Errorf("stat config %s: %w", path, err)
	}
	if _, err := toml.DecodeFile(path, &c); err != nil {
		return c, fmt.Errorf("parse config %s: %w", path, err)
	}
	return c, nil
}

// Overrides holds values supplied on the command line. Empty string means unset.
type Overrides struct {
	Mode          string
	Token         string
	ClientID      string
	ClientSecret  string
	RedirectURI   string
	EncryptionKey string
	StoragePath   string
}

// Resolve applies env then flags on top of base (which already has file+default
// merged by LoadFile). Each field is overridden independently and only when the
// higher-priority source provides a non-empty value.
func Resolve(base Config, env map[string]string, flags Overrides) Config {
	c := base
	set := func(dst *string, vals ...string) {
		for _, v := range vals {
			if v != "" {
				*dst = v
			}
		}
	}
	set(&c.Auth.Mode, env["OURA_AUTH_MODE"], flags.Mode)
	set(&c.Auth.Token, env["OURA_TOKEN"], flags.Token)
	set(&c.Auth.ClientID, env["OURA_CLIENT_ID"], flags.ClientID)
	set(&c.Auth.ClientSecret, env["OURA_CLIENT_SECRET"], flags.ClientSecret)
	set(&c.Auth.RedirectURI, env["OURA_REDIRECT_URI"], flags.RedirectURI)
	set(&c.Storage.EncryptionKey, env["OURA_ENCRYPTION_KEY"], flags.EncryptionKey)
	set(&c.Storage.Path, env["OURA_DB_PATH"], flags.StoragePath)
	return c
}

// EnvMap captures the OURA_* environment for Resolve.
func EnvMap() map[string]string {
	keys := []string{
		"OURA_AUTH_MODE", "OURA_TOKEN", "OURA_CLIENT_ID", "OURA_CLIENT_SECRET",
		"OURA_REDIRECT_URI", "OURA_ENCRYPTION_KEY", "OURA_DB_PATH",
	}
	m := make(map[string]string, len(keys))
	for _, k := range keys {
		if v, ok := os.LookupEnv(k); ok {
			m[k] = v
		}
	}
	return m
}

// ResolvePaths fills empty storage path from XDG. Call after Resolve.
func ResolvePaths(c *Config) error {
	if c.Storage.Path == "" {
		p, err := xdg.DataFile("oura/oura.db")
		if err != nil {
			return fmt.Errorf("resolve data path: %w", err)
		}
		c.Storage.Path = p
	}
	return nil
}

// DefaultConfigPath is where the config file lives unless --config overrides it.
func DefaultConfigPath() (string, error) {
	return xdg.ConfigFile("oura/config.toml")
}

// Validate checks the resolved config is internally consistent and runnable.
func Validate(c Config) error {
	switch c.Auth.Mode {
	case "token":
		if c.Auth.Token == "" {
			return fmt.Errorf("auth mode token requires a token (set OURA_TOKEN, --oura-token, or [auth] token)")
		}
	case "oauth2":
		if c.Auth.ClientID == "" || c.Auth.ClientSecret == "" {
			return fmt.Errorf("auth mode oauth2 requires client_id and client_secret")
		}
	default:
		return fmt.Errorf("unknown auth mode %q (want token or oauth2)", c.Auth.Mode)
	}
	if c.Collection.OverlapDays < 0 {
		return fmt.Errorf("overlap_days must be >= 0, got %d", c.Collection.OverlapDays)
	}
	return nil
}
