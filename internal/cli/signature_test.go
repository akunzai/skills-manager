package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/engine"
)

func TestCLILsShowsWhetherTheAppliedCopyWasSigned(t *testing.T) {
	resetRootCmdFlags()
	home := isolateHome(t)
	configFile := filepath.Join(home, ".agents", "skills.json")
	skillsDir := filepath.Join(home, ".agents", "skills")
	cfg := config.DefaultConfig()
	baselines := engine.OpenBaselines(skillsDir)
	for name, signed := range map[string]bool{"signed-one": true, "plain": false} {
		config.AddRemoteSkillEntry(cfg, "owner/repo", name, name, "github", "")
		scopeCopy := filepath.Join(skillsDir, name)
		if err := os.MkdirAll(scopeCopy, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(scopeCopy, "SKILL.md"), []byte("# "+name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		skill := engine.SkillFreshness{Name: name, Source: "owner/repo", ScopePath: scopeCopy}
		if err := baselines.Record(skill, "cache", "abc123", signed); err != nil {
			t.Fatal(err)
		}
	}
	if err := config.SaveConfig(cfg, configFile); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, "ls", "--json", "--config", configFile, "--skills-dir", skillsDir)
	if err != nil {
		t.Fatalf("ls --json: %v\n%s", err, out)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("parse ls --json: %v\n%s", err, out)
	}
	got := make(map[string]any)
	for _, row := range rows {
		got[row["name"].(string)] = row["signature"]
	}
	if got["signed-one"] != "signed" || got["plain"] != "unsigned" {
		t.Fatalf("signature = %v, want signed-one signed and plain unsigned", got)
	}

	resetRootCmdFlags()
	table, err := runCLI(t, "ls", "--config", configFile, "--skills-dir", skillsDir)
	if err != nil {
		t.Fatalf("ls: %v\n%s", err, table)
	}
	if !strings.Contains(table, "Installed, signed") || !strings.Contains(table, "Installed, unsigned") {
		t.Fatalf("ls table should mark each copy signed or unsigned:\n%s", table)
	}
}

func TestCLIAddRefusesTrustCertForALocalSource(t *testing.T) {
	resetRootCmdFlags()
	home := isolateHome(t)
	local := writeCLILocalSkill(t, home, "mine")
	_, err := runCLI(t, "add", "--symlink", local, "--trust-cert", filepath.Join(home, "root.pem"), "--yes", "--global")
	if err == nil || !strings.Contains(err.Error(), "--trust-cert applies only") {
		t.Fatalf("add error = %v, want --trust-cert refused for a local Source", err)
	}
}

func TestStoredTrustCertIsPortableInsideTheConfigDirectory(t *testing.T) {
	pem, err := os.ReadFile(filepath.Join("..", "signing", "testdata", "nvidia", "nv-agent-root-cert.pem"))
	if err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	configPath := filepath.Join(project, ".agents", "skills.json")
	inside := filepath.Join(project, ".agents", "trust", "root.pem")
	outside := filepath.Join(t.TempDir(), "root.pem")
	for _, path := range []string{inside, outside} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, pem, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if got, err := storedTrustCert(inside, configPath); err != nil || got != "trust/root.pem" {
		t.Fatalf("storedTrustCert(inside) = %q, %v; want trust/root.pem", got, err)
	}
	if got, err := storedTrustCert(outside, configPath); err != nil || !filepath.IsAbs(got) {
		t.Fatalf("storedTrustCert(outside) = %q, %v; want an absolute path", got, err)
	}
	notPEM := filepath.Join(project, "not.pem")
	if err := os.WriteFile(notPEM, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := storedTrustCert(notPEM, configPath); err == nil {
		t.Fatal("storedTrustCert should refuse a file with no PEM certificate")
	}
}
