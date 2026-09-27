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
		if err := baselines.Record(skill, "cache", "abc123", signed, ""); err != nil {
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
		got[row["name"].(string)] = row["signed"]
	}
	if got["signed-one"] != true || got["plain"] != false {
		t.Fatalf("signed = %v, want signed-one true and plain false", got)
	}

	resetRootCmdFlags()
	table, err := runCLI(t, "ls", "--config", configFile, "--skills-dir", skillsDir)
	if err != nil {
		t.Fatalf("ls: %v\n%s", err, table)
	}
	// Captured output is not a terminal, so the mark falls back to text.
	var signedRow, plainRow string
	for _, line := range strings.Split(table, "\n") {
		switch {
		case strings.HasPrefix(line, "signed-one"):
			signedRow = line
		case strings.HasPrefix(line, "plain"):
			plainRow = line
		}
	}
	if !strings.Contains(signedRow, "[signed]") || strings.Contains(plainRow, "[signed]") || strings.Contains(table, "unsigned") {
		t.Fatalf("ls should mark only the signed copy:\n%s", table)
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

func TestCLIAddWarnsOfATrustRootItCouldNotFetch(t *testing.T) {
	var out strings.Builder
	printSyncEvent(&out, engine.SyncEvent{Kind: engine.SyncTrustRootFailed, Source: "owner/repo", Err: "fetch Sigstore trust root: unreachable", Next: "update"}, " -p")

	if got := out.String(); !strings.Contains(got, "Warning: fetch Sigstore trust root: unreachable") || !strings.Contains(got, "skills update -p") {
		t.Fatalf("add output = %q; want the failure and update as the way out", got)
	}
}
