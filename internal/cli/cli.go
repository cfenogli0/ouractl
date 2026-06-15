package cli

import (
	"fmt"
	"time"

	"github.com/cfenogli0/ouractl/internal/config"
	"github.com/cfenogli0/ouractl/internal/datasets"
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
