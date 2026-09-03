package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
)

func TestGuideCmd_Stdout(t *testing.T) {
	resetRootCmdFlags()

	var buf bytes.Buffer
	RootCmd.SetOut(&buf)
	RootCmd.SetArgs([]string{"guide"})

	if err := RootCmd.Execute(); err != nil {
		t.Fatalf("guide command failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "name: skills-manager") {
		t.Errorf("expected frontmatter name: skills-manager in output, got: %s", out)
	}
	if !strings.Contains(out, "Core Invariants") {
		t.Errorf("expected Core Invariants in output")
	}
	if !strings.Contains(out, "skills sync") {
		t.Errorf("expected skills sync in output")
	}
}

func TestGuideCmd_InstallProject(t *testing.T) {
	resetRootCmdFlags()
	flagGlobal = false
	_ = RootCmd.PersistentFlags().Set("global", "false")

	tmpProjectDir := t.TempDir()
	oldWd, _ := os.Getwd()
	_ = os.Chdir(tmpProjectDir)
	defer func() { _ = os.Chdir(oldWd) }()

	var buf bytes.Buffer
	RootCmd.SetOut(&buf)
	RootCmd.SetArgs([]string{"guide", "--install", "-p"})

	if err := RootCmd.Execute(); err != nil {
		t.Fatalf("guide --install -p failed: %v", err)
	}

	skillMd := filepath.Join(tmpProjectDir, ".agents", "skills", "skills-manager", "SKILL.md")
	data, err := os.ReadFile(skillMd)
	if err != nil {
		t.Fatalf("expected installed SKILL.md at %s: %v", skillMd, err)
	}
	if !strings.Contains(string(data), "name: skills-manager") {
		t.Errorf("expected SKILL.md to contain frontmatter")
	}

	cfgPath := filepath.Join(tmpProjectDir, ".agents", "skills.json")
	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("load config %s: %v", cfgPath, err)
	}
	entry, ok := cfg.Local["skills-manager"]
	if !ok {
		t.Fatalf("expected skills-manager in cfg.Local")
	}
	if entry.Type != "command" || entry.Command != "skills guide --install --project" {
		t.Errorf("unexpected local entry: %+v", entry)
	}

	claudeLink := filepath.Join(tmpProjectDir, ".claude", "skills", "skills-manager")
	if _, err := os.Lstat(claudeLink); err != nil {
		t.Errorf("expected claude availability link %s: %v", claudeLink, err)
	}
}

func TestGuideCmd_InstallGlobal(t *testing.T) {
	resetRootCmdFlags()
	home := isolateHome(t)

	var buf bytes.Buffer
	RootCmd.SetOut(&buf)
	RootCmd.SetArgs([]string{"guide", "--install"})

	if err := RootCmd.Execute(); err != nil {
		t.Fatalf("guide --install failed: %v", err)
	}

	skillMd := filepath.Join(home, ".agents", "skills", "skills-manager", "SKILL.md")
	if _, err := os.Stat(skillMd); err != nil {
		t.Fatalf("expected installed SKILL.md at %s: %v", skillMd, err)
	}

	cfgPath := filepath.Join(home, ".agents", "skills.json")
	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("load config %s: %v", cfgPath, err)
	}
	entry, ok := cfg.Local["skills-manager"]
	if !ok {
		t.Fatalf("expected skills-manager in cfg.Local")
	}
	if entry.Type != "command" || entry.Command != "skills guide --install" {
		t.Errorf("unexpected local entry: %+v", entry)
	}
}

func TestGuideCmd_SingleSourceOfTruth(t *testing.T) {
	repoSkill, err := os.ReadFile("../../skills-manager/SKILL.md")
	if err != nil {
		t.Fatalf("failed to read skills-manager/SKILL.md: %v", err)
	}

	if string(repoSkill) != embeddedGuideSkill {
		t.Errorf("embedded_skill.md and skills-manager/SKILL.md differ; they must be identical")
	}
}
