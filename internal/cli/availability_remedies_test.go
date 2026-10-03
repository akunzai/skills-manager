package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/engine"
)

func TestCLISyncAvailabilityRemediesUseTheSameScope(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		for _, dryRun := range []bool{false, true} {
			t.Run(fmt.Sprintf("foreign=%t/dry-run=%t", foreign, dryRun), func(t *testing.T) {
				isolateHome(t)
				root := t.TempDir()
				skillsDir := filepath.Join(root, ".agents", "skills")
				configPath := filepath.Join(root, ".agents", "skills.json")
				cacheDir := filepath.Join(root, "cache")
				cfg := config.DefaultConfig()
				cfg.Settings.DefaultAgents = []string{"continue"}
				if foreign {
					cfg.Settings.DefaultAgents = append(cfg.Settings.DefaultAgents, "claude-code")
				}
				for _, name := range []string{"alpha", "beta"} {
					config.AddLocalCommandEntry(cfg, name, "exit 0", "", "")
					path := filepath.Join(skillsDir, name)
					if err := os.MkdirAll(path, 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte("# "+name+"\n"), 0o644); err != nil {
						t.Fatal(err)
					}
					if foreign {
						occupied := filepath.Join(root, ".claude", "skills", name)
						if err := os.MkdirAll(occupied, 0o755); err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(filepath.Join(occupied, "keep"), []byte("Foreign data"), 0o644); err != nil {
							t.Fatal(err)
						}
					}
				}
				agentDir := filepath.Join(root, ".continue", "skills")
				if err := os.MkdirAll(filepath.Dir(agentDir), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(agentDir, []byte("keep"), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := config.SaveConfig(cfg, configPath); err != nil {
					t.Fatal(err)
				}
				before, err := os.ReadFile(configPath)
				if err != nil {
					t.Fatal(err)
				}
				args := []string{"sync", "-p", "--config", configPath, "--skills-dir", skillsDir, "--cache-dir", cacheDir}
				if dryRun {
					args = append(args, "--dry-run")
				}
				out, err := runCLI(t, args...)
				if ExitCode(err) != 2 || !strings.Contains(err.Error(), "2 failures") {
					t.Fatalf("err=%v; want two failed Skills\n%s", err, out)
				}
				if !strings.Contains(out, agentDir) || !strings.Contains(out, "inspect Agent directory") {
					t.Fatalf("missing manual inspection:\n%s", out)
				}
				expected := "Next: run 'skills doctor -p --config " + shellWord(configPath) + " --skills-dir " + shellWord(skillsDir) + " --cache-dir " + shellWord(cacheDir) + " --fix'."
				count := 0
				if foreign {
					count = 1
				}
				if got := strings.Count(out, expected); got != count {
					t.Fatalf("Doctor hints=%d; want %d:\n%s", got, count, out)
				}
				if !foreign && strings.Contains(out, "--fix") {
					t.Fatalf("unobservable path was offered --fix:\n%s", out)
				}
				after, err := os.ReadFile(configPath)
				if err != nil || !bytes.Equal(before, after) {
					t.Fatalf("Config changed: %v", err)
				}
				if got, err := os.ReadFile(agentDir); err != nil || string(got) != "keep" {
					t.Fatalf("unobservable content changed: %q, %v", got, err)
				}
				if foreign {
					for _, name := range []string{"alpha", "beta"} {
						if got, err := os.ReadFile(filepath.Join(root, ".claude", "skills", name, "keep")); err != nil || string(got) != "Foreign data" {
							t.Fatalf("Foreign content changed: %q, %v", got, err)
						}
					}
				}
			})
		}
	}
}

func TestReportReconciledPreservesEveryJoinedNextCommand(t *testing.T) {
	var out bytes.Buffer
	reason := errors.Join(
		fmt.Errorf("context: %w", engine.NextCommand{Reason: "Foreign alpha", Command: "doctor --fix"}),
		engine.NextCommand{Reason: "Foreign beta", Command: "doctor --fix"},
		engine.NextCommand{Reason: "Source missing", Command: "update"},
	)
	err := reportReconciled(&out, []engine.AvailabilityOutcome{{Skill: "sample", Err: reason}}, " -p")
	if ExitCode(err) != 2 {
		t.Fatalf("err=%v; want exit 2", err)
	}
	if strings.Count(out.String(), "run 'skills doctor") != 1 || strings.Count(out.String(), "run 'skills update") != 1 {
		t.Fatalf("commands repeated in item reasons:\n%s", out.String())
	}
	for _, next := range []string{"doctor -p --fix", "update -p"} {
		if strings.Count(out.String(), "Next: run 'skills "+next+"'.") != 1 {
			t.Fatalf("missing or repeated %s:\n%s", next, out.String())
		}
	}
}

func TestCLIGuideAvailabilityFailureCarriesScopedRemedy(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	configPath := filepath.Join(root, ".agents", "skills.json")
	skillsDir := filepath.Join(root, ".agents", "skills")
	occupied := filepath.Join(root, ".claude", "skills", "skills-manager")
	if err := os.MkdirAll(occupied, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(occupied, "keep")
	if err := os.WriteFile(marker, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runCLI(t, "guide", "--install", "-p", "--config", configPath, "--skills-dir", skillsDir)
	if ExitCode(err) != 2 || !strings.Contains(out, "Failed to apply availability") {
		t.Fatalf("err=%v:\n%s", err, out)
	}
	expected := "Next: run 'skills doctor -p --config " + shellWord(configPath) + " --skills-dir " + shellWord(skillsDir) + " --fix'."
	if strings.Count(out, expected) != 1 {
		t.Fatalf("missing scoped remedy:\n%s", out)
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "keep" {
		t.Fatalf("Foreign data changed: %q, %v", got, err)
	}
}

func TestCLIAdoptRetainsAvailabilityRemedyAfterDeclaration(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	configPath := filepath.Join(root, ".agents", "skills.json")
	skillsDir := filepath.Join(root, ".agents", "skills")
	original := filepath.Join(skillsDir, "sample")
	if err := os.MkdirAll(original, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(original, "SKILL.md"), []byte("# Sample\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	occupied := filepath.Join(root, ".claude", "skills", "sample")
	if err := os.MkdirAll(occupied, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(occupied, "keep")
	if err := os.WriteFile(marker, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runCLI(t, "adopt", "sample", "--yes", "-p", "--config", configPath, "--skills-dir", skillsDir)
	if ExitCode(err) != 2 || !strings.Contains(out, "It is declared") {
		t.Fatalf("err=%v:\n%s", err, out)
	}
	expected := "Next: run 'skills doctor -p --config " + shellWord(configPath) + " --skills-dir " + shellWord(skillsDir) + " --fix'."
	if strings.Count(out, expected) != 1 {
		t.Fatalf("missing scoped remedy:\n%s", out)
	}
	cfg, err := config.LoadConfig(configPath)
	if err != nil || cfg.Local["sample"].Source == "" {
		t.Fatalf("declaration not kept: %v, %v", cfg, err)
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "keep" {
		t.Fatalf("Foreign data changed: %q, %v", got, err)
	}
}

func TestCLIDoctorFailureDoesNotSuggestRepeatingItsRepair(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	configPath := filepath.Join(root, ".agents", "skills.json")
	skillsDir := filepath.Join(root, ".agents", "skills")
	source := writeCLILocalSkill(t, root, "sample")
	cfg := config.DefaultConfig()
	config.AddLocalSymlinkEntry(cfg, "sample", source, "")
	if err := config.SaveConfig(cfg, configPath); err != nil {
		t.Fatal(err)
	}
	// Materialize only the declared copy; the Foreign Agent directory is never
	// replaced by this non-interactive Doctor invocation.
	if err := os.MkdirAll(filepath.Join(skillsDir, "sample"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "sample", "SKILL.md"), []byte("# sample\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	occupied := filepath.Join(root, ".claude", "skills", "sample")
	if err := os.MkdirAll(occupied, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(occupied, "keep")
	if err := os.WriteFile(marker, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runCLI(t, "doctor", "--fix", "-p", "--config", configPath, "--skills-dir", skillsDir)
	if ExitCode(err) != 2 || !strings.Contains(out, "Remove it manually: rm -rf --") {
		t.Fatalf("err=%v:\n%s", err, out)
	}
	if strings.Contains(out, "run 'skills doctor") && strings.Contains(out, "--fix'") {
		t.Fatalf("Doctor suggested retrying itself:\n%s", out)
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "keep" {
		t.Fatalf("Foreign data changed: %q, %v", got, err)
	}
}
