package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/engine"
	"github.com/akunzai/skills-manager/internal/signing"
)

// unverifiableScope declares one remote Skill, "sample", whose signature
// cannot verify: it is signed with a certificate chain its Source does not
// trust. It returns the Scope flags every command takes.
func unverifiableScope(t *testing.T) (args []string, configFile string) {
	t.Helper()
	isolateHome(t)
	project := t.TempDir()
	configFile = filepath.Join(project, ".agents", "skills.json")
	skillsDir := filepath.Join(project, ".agents", "skills")
	cacheDir := filepath.Join(project, "cache")
	origin := filepath.Join(project, "origin")
	if err := os.MkdirAll(filepath.Join(origin, "sample"), 0o755); err != nil {
		t.Fatal(err)
	}
	sig := `{"verificationMaterial":{"x509CertificateChain":{"certificates":[{"rawBytes":"AA=="}]}}}`
	if err := os.WriteFile(filepath.Join(origin, "sample", signing.FileName), []byte(sig), 0o644); err != nil {
		t.Fatal(err)
	}
	writeCLIGitSkill(t, origin, "sample")
	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/repo", "sample", "sample", "git", origin)
	if err := config.SaveConfig(cfg, configFile); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.NewCache("owner/repo", origin, "", cacheDir).Refresh(false, "sample"); err != nil {
		t.Fatal(err)
	}
	return []string{"--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir}, configFile
}

func trustedTree(t *testing.T, configFile string) string {
	t.Helper()
	cfg, err := config.LoadConfig(configFile)
	if err != nil {
		t.Fatal(err)
	}
	tree, _ := cfg.Remote["owner/repo"].Signature.TrustedTree("sample")
	return tree
}

func TestCLITrustLetsSyncApplyUnverifiedContent(t *testing.T) {
	scope, configFile := unverifiableScope(t)
	useFakePrompter(t, &fakePrompter{interactive: false})
	run := func(args ...string) (string, error) {
		return runCLI(t, append(args, scope...)...)
	}

	if out, err := run("sync"); ExitCode(err) != 1 || !strings.Contains(out, "certificate chain") {
		t.Fatalf("sync = %v; want the signature to block it (exit 1):\n%s", err, out)
	}
	if out, err := run("trust", "sample"); err == nil || trustedTree(t, configFile) != "" {
		t.Fatalf("trust without a terminal or --yes = %v; want a refusal that records nothing:\n%s", err, out)
	}

	out, err := run("trust", "sample", "--yes")
	if err != nil || !strings.Contains(out, "Trusted 1 skill") || trustedTree(t, configFile) == "" {
		t.Fatalf("trust --yes = %v; want sample trusted:\n%s", err, out)
	}

	out, err = run("sync")
	if err != nil || !strings.Contains(out, "Trusted unverified: sample") {
		t.Fatalf("sync = %v; want trusted content applied with a note (exit 0):\n%s", err, out)
	}

	table, err := run("ls")
	if err != nil {
		t.Fatalf("ls: %v\n%s", err, table)
	}
	if !strings.Contains(table, "[unverified]") || strings.Contains(table, "[signed]") {
		t.Fatalf("ls should mark sample unverified:\n%s", table)
	}
	raw, err := run("ls", "--json")
	if err != nil {
		t.Fatalf("ls --json: %v\n%s", err, raw)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		t.Fatalf("parse ls --json: %v\n%s", err, raw)
	}
	if len(rows) != 1 || rows[0]["unverified"] != true || rows[0]["signed"] != false {
		t.Fatalf("ls --json = %v; want sample unverified", rows)
	}

	if out, err := run("trust", "--revoke", "sample"); err != nil || trustedTree(t, configFile) != "" {
		t.Fatalf("trust --revoke = %v; want the Trusted content gone:\n%s", err, out)
	}
	if out, err := run("sync", "--dry-run"); ExitCode(err) != 1 {
		t.Fatalf("sync after revoke = %v; want it blocked again (exit 1):\n%s", err, out)
	}
}

func TestCLITrustRefusesASkillItsSignatureDoesNotBlock(t *testing.T) {
	scope, configFile := unverifiableScope(t)

	out, err := runCLI(t, append([]string{"trust", "missing", "--yes"}, scope...)...)

	if ExitCode(err) != 2 || !strings.Contains(out, "not declared in Config") || trustedTree(t, configFile) != "" {
		t.Fatalf("trust missing = %v; want a refusal (exit 2):\n%s", err, out)
	}
}

func TestCLITrustAsksOnATerminal(t *testing.T) {
	for _, answer := range []bool{false, true} {
		scope, configFile := unverifiableScope(t)
		fp := &fakePrompter{interactive: true, answers: []fakeAnswer{
			confirmAnswer("although its signature does not verify", answer),
		}}
		useFakePrompter(t, fp)

		out, err := runCLI(t, append([]string{"trust", "sample"}, scope...)...)

		if err != nil || len(fp.asked) == 0 {
			t.Fatalf("trust on a terminal = %v, asked %v:\n%s", err, fp.asked, out)
		}
		if !strings.Contains(out, "certificate chain") {
			t.Fatalf("trust should show why the signature fails before asking:\n%s", out)
		}
		if (trustedTree(t, configFile) != "") != answer {
			t.Fatalf("answer %v recorded %q", answer, trustedTree(t, configFile))
		}
	}
}
