package skill

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstallWritesSkill(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "octl")

	path, err := Install(dir, false)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if filepath.Base(path) != "SKILL.md" {
		t.Fatalf("returned path = %q, want .../SKILL.md", path)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != Content {
		t.Fatal("written file does not match embedded Content")
	}
}

func TestInstallRefusesOverwriteWithoutForce(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "octl")
	if _, err := Install(dir, false); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(dir, false); err == nil {
		t.Fatal("second install without force must error rather than clobber")
	}
	if _, err := Install(dir, true); err != nil {
		t.Fatalf("force install should overwrite: %v", err)
	}
}

// The embedded copy must match the repo's canonical skill so the binary never
// ships a stale skill. go:generate keeps them in sync; this test enforces it.
func TestEmbeddedMatchesRepoCanonical(t *testing.T) {
	repo, err := os.ReadFile(filepath.Join("..", "..", "skills", "octl", "SKILL.md"))
	if err != nil {
		t.Skipf("repo canonical not present (%v); skipping drift check", err)
	}
	if string(repo) != Content {
		t.Fatal("internal/skill/SKILL.md has drifted from skills/octl/SKILL.md; run `go generate ./internal/skill`")
	}
}
