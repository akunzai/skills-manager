package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
)

func TestConfiguredKnownAgentsSelectsDefaultAgentsOnly(t *testing.T) {
	proj := t.TempDir()
	skillsDir := filepath.Join(proj, ".agents", "skills")

	cfg := config.DefaultConfig()
	got := ConfiguredKnownAgents(cfg, skillsDir)

	if len(got) != 1 {
		t.Fatalf("expected 1 configured agent, got %d: %v", len(got), got)
	}
	want := filepath.Join(proj, ".claude", "skills")
	if got["claude-code"] != want {
		t.Errorf("claude-code dir = %q; want %q", got["claude-code"], want)
	}
	if _, ok := got["continue"]; ok {
		t.Errorf("continue should not be configured when defaultAgents is [claude]")
	}
}

func TestConfiguredKnownAgentsHonorsExcludeAgents(t *testing.T) {
	proj := t.TempDir()
	skillsDir := filepath.Join(proj, ".agents", "skills")

	cfg := config.DefaultConfig()
	cfg.Settings.DefaultAgents = []string{"claude", "continue"}
	cfg.Settings.ExcludeAgents = []string{"continue"}

	got := ConfiguredKnownAgents(cfg, skillsDir)
	if _, ok := got["continue"]; ok {
		t.Fatal("excluded continue should not be configured")
	}
	if _, ok := got["claude-code"]; !ok {
		t.Fatal("expected claude-code to remain configured")
	}
}

func TestLeftoverEmptyAgentDirsIgnoresConfiguredAndNonEmpty(t *testing.T) {
	root := t.TempDir()
	claude := filepath.Join(root, "claude")
	crush := filepath.Join(root, "crush")
	jazz := filepath.Join(root, "jazz")
	_ = os.MkdirAll(claude, 0755)
	_ = os.MkdirAll(crush, 0755)
	_ = os.MkdirAll(jazz, 0755)
	_ = os.WriteFile(filepath.Join(jazz, "keep"), []byte("x"), 0644)

	known := map[string]string{
		"claude-code": claude,
		"crush":       crush,
		"jazz":        jazz,
		"missing":     filepath.Join(root, "nope"),
	}
	configured := map[string]string{"claude-code": claude}

	got := LeftoverEmptyAgentDirs(known, configured)
	if len(got) != 1 {
		t.Fatalf("got %#v; want only crush", got)
	}
	if got[0].Name != "crush" || got[0].Dir != crush {
		t.Fatalf("got %#v; want crush at %s", got[0], crush)
	}
}

func TestRemoveEmptyAgentDirPrunesEmptyParentsAndStopsAtHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	jazzSkills := filepath.Join(home, ".jazz", "skills")
	if err := os.MkdirAll(jazzSkills, 0755); err != nil {
		t.Fatal(err)
	}

	if err := RemoveEmptyAgentDir(jazzSkills); err != nil {
		t.Fatalf("RemoveEmptyAgentDir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".jazz")); !os.IsNotExist(err) {
		t.Fatal("expected ~/.jazz to be removed")
	}
	if _, err := os.Stat(home); err != nil {
		t.Fatal("home directory must remain")
	}
}

func TestRemoveEmptyAgentDirStopsAtXDGConfig(t *testing.T) {
	home := t.TempDir()
	xdg := filepath.Join(home, ".config")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)

	crushSkills := filepath.Join(xdg, "crush", "skills")
	if err := os.MkdirAll(crushSkills, 0755); err != nil {
		t.Fatal(err)
	}

	if err := RemoveEmptyAgentDir(crushSkills); err != nil {
		t.Fatalf("RemoveEmptyAgentDir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(xdg, "crush")); !os.IsNotExist(err) {
		t.Fatal("expected ~/.config/crush to be removed")
	}
	if _, err := os.Stat(xdg); err != nil {
		t.Fatal("XDG config directory must remain")
	}
}

func TestRemoveEmptyAgentDirLeavesNonEmptyParents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	grokSkills := filepath.Join(home, ".grok", "skills")
	if err := os.MkdirAll(grokSkills, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".grok", "config.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := RemoveEmptyAgentDir(grokSkills); err != nil {
		t.Fatalf("RemoveEmptyAgentDir: %v", err)
	}
	if _, err := os.Stat(grokSkills); !os.IsNotExist(err) {
		t.Fatal("expected empty skills dir to be removed")
	}
	if _, err := os.Stat(filepath.Join(home, ".grok", "config.json")); err != nil {
		t.Fatal("sibling files in parent must remain")
	}
}

func TestRemoveEmptyAgentDirDoesNotDeleteNonEmptySkillsDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	skills := filepath.Join(home, ".jazz", "skills")
	if err := os.MkdirAll(skills, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skills, "keep"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := RemoveEmptyAgentDir(skills); err != nil {
		t.Fatalf("RemoveEmptyAgentDir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(skills, "keep")); err != nil {
		t.Fatal("non-empty skills dir must not be removed")
	}
}
