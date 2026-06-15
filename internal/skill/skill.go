// Package skill embeds the octl Claude Code skill and installs it into a
// Claude Code skills directory, so the binary is self-sufficient (no repo
// checkout needed to get the skill).
package skill

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
)

//go:generate cp ../../skills/octl/SKILL.md SKILL.md

//go:embed SKILL.md
var content string

// Content is the embedded SKILL.md, the single source of truth shipped in the binary.
var Content = content

// Install writes the embedded skill to dir/SKILL.md and returns the file path.
// It refuses to overwrite an existing SKILL.md unless force is set, so a manual
// edit is never clobbered silently.
func Install(dir string, force bool) (string, error) {
	path := filepath.Join(dir, "SKILL.md")
	if !force {
		if _, err := os.Stat(path); err == nil {
			return "", fmt.Errorf("%s already exists; re-run with --force to overwrite", path)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create skill dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write skill: %w", err)
	}
	return path, nil
}
