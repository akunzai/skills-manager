package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// writeCLILocalSkillWithDescription is writeCLILocalSkill plus a frontmatter
// description, for assertions that --list surfaces it.
func writeCLILocalSkillWithDescription(t *testing.T, root, name, description string) string {
	t.Helper()
	dir := filepath.Join(root, "local", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: " + description + "\n---\n# " + name + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// snapshotTree hashes every file under root (symlinks by target, regular
// files by content), skipping any path under skip. It reports an empty
// snapshot for a root that does not exist, so "before" and "after" compare
// equal when neither is there.
func snapshotTree(t *testing.T, root string, skip ...string) map[string]string {
	t.Helper()
	snapshot := make(map[string]string)
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return snapshot
	}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		for _, s := range skip {
			if rel == s || strings.HasPrefix(rel, s+string(filepath.Separator)) {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, linkErr := os.Readlink(path)
			if linkErr != nil {
				return linkErr
			}
			snapshot[rel] = "symlink:" + target
		case info.IsDir():
			snapshot[rel] = "dir"
		default:
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			sum := sha256.Sum256(data)
			snapshot[rel] = "file:" + hex.EncodeToString(sum[:])
		}
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", root, err)
	}
	return snapshot
}

func TestCLIAddListLocalSymlinkPrintsNameSubpathAndDescription(t *testing.T) {
	resetRootCmdFlags()
	t.Cleanup(resetRootCmdFlags)
	home := isolateHome(t)
	configFile := filepath.Join(home, ".agents", "skills.json")
	skillsDir := filepath.Join(home, ".agents", "skills")
	cacheDir := filepath.Join(home, ".agents", "cache")

	source := writeCLILocalSkillWithDescription(t, home, "sample", "Does the sample thing.")

	out, err := runCLI(t, "add", "--symlink", source, "--list", "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir)
	if err != nil {
		t.Fatalf("add --list failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "sample") || !strings.Contains(out, "Does the sample thing.") {
		t.Fatalf("expected listed skill name and description, got:\n%s", out)
	}
	if _, statErr := os.Stat(configFile); !os.IsNotExist(statErr) {
		t.Fatalf("--list must not create Config, got err = %v", statErr)
	}
	if _, statErr := os.Stat(skillsDir); !os.IsNotExist(statErr) {
		t.Fatalf("--list must not create the Scope skills directory, got err = %v", statErr)
	}
}

func TestCLIAddListJSONShape(t *testing.T) {
	resetRootCmdFlags()
	t.Cleanup(resetRootCmdFlags)
	home := isolateHome(t)
	configFile := filepath.Join(home, ".agents", "skills.json")
	skillsDir := filepath.Join(home, ".agents", "skills")
	cacheDir := filepath.Join(home, ".agents", "cache")

	source := writeCLILocalSkillWithDescription(t, home, "sample", "Does the sample thing.")

	out, err := runCLI(t, "add", "--symlink", source, "--list", "--json", "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir)
	if err != nil {
		t.Fatalf("add --list --json failed: %v\n%s", err, out)
	}

	var rows []map[string]string
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("--json output did not parse: %v\n%s", err, out)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %#v; want exactly one", rows)
	}
	want := map[string]string{"name": "sample", "path": ".", "description": "Does the sample thing."}
	if !reflect.DeepEqual(rows[0], want) {
		t.Fatalf("row = %#v; want %#v", rows[0], want)
	}
}

func TestCLIAddListPathNarrowsDiscovery(t *testing.T) {
	resetRootCmdFlags()
	t.Cleanup(resetRootCmdFlags)
	home := isolateHome(t)
	configFile := filepath.Join(home, ".agents", "skills.json")
	skillsDir := filepath.Join(home, ".agents", "skills")
	cacheDir := filepath.Join(home, ".agents", "cache")

	root := filepath.Join(home, "repo")
	writeSkillAt := func(rel, name string) {
		dir := filepath.Join(root, rel)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		content := "---\nname: " + name + "\ndescription: desc-" + name + "\n---\n"
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeSkillAt("included", "in-scope")
	writeSkillAt("excluded", "out-of-scope")

	out, err := runCLI(t, "add", "--symlink", root, "--path", "included", "--list", "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir)
	if err != nil {
		t.Fatalf("add --list --path failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "in-scope") {
		t.Fatalf("expected in-scope skill listed, got:\n%s", out)
	}
	if strings.Contains(out, "out-of-scope") {
		t.Fatalf("--path did not narrow discovery, got:\n%s", out)
	}
}

func TestCLIAddListEmptyResultExitsOne(t *testing.T) {
	resetRootCmdFlags()
	t.Cleanup(resetRootCmdFlags)
	home := isolateHome(t)
	configFile := filepath.Join(home, ".agents", "skills.json")
	skillsDir := filepath.Join(home, ".agents", "skills")
	cacheDir := filepath.Join(home, ".agents", "cache")

	empty := filepath.Join(home, "empty-source")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, "add", "--symlink", empty, "--list", "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir)
	if err == nil || ExitCode(err) != 1 {
		t.Fatalf("error = %v (exit %d); want exit 1\n%s", err, ExitCode(err), out)
	}
	if !strings.Contains(out, "No Skills found") {
		t.Fatalf("expected a 'No Skills found' state message, got:\n%s", out)
	}
}

func TestCLIAddListSourceCannotBeFetchedExitsTwo(t *testing.T) {
	resetRootCmdFlags()
	t.Cleanup(resetRootCmdFlags)
	home := isolateHome(t)
	configFile := filepath.Join(home, ".agents", "skills.json")
	skillsDir := filepath.Join(home, ".agents", "skills")
	cacheDir := filepath.Join(home, ".agents", "cache")

	missingOrigin := filepath.Join(home, "does-not-exist")
	out, err := runCLI(t, "add", "owner/repo", "--url", missingOrigin, "--list", "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir)
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("error = %v (exit %d); want exit 2\n%s", err, ExitCode(err), out)
	}
}

func TestCLIAddListRemoteSourceFetchesCacheOnly(t *testing.T) {
	resetRootCmdFlags()
	t.Cleanup(resetRootCmdFlags)
	home := isolateHome(t)
	configFile := filepath.Join(home, ".agents", "skills.json")
	skillsDir := filepath.Join(home, ".agents", "skills")
	cacheDir := filepath.Join(home, ".agents", "cache")

	origin := filepath.Join(home, "origin.git")
	writeCLIGitSkill(t, origin, "sample")

	out, err := runCLI(t, "add", "owner/repo", "--url", origin, "--list", "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir)
	if err != nil {
		t.Fatalf("add --list on a remote Source failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "sample") {
		t.Fatalf("expected sample skill listed, got:\n%s", out)
	}
	if _, statErr := os.Stat(cacheDir); statErr != nil {
		t.Fatalf("expected the Cache to be populated by --list: %v", statErr)
	}
	if _, statErr := os.Stat(configFile); !os.IsNotExist(statErr) {
		t.Fatalf("--list must not create Config, got err = %v", statErr)
	}
	if _, statErr := os.Stat(skillsDir); !os.IsNotExist(statErr) {
		t.Fatalf("--list must not create the Scope skills directory, got err = %v", statErr)
	}
}

// TestCLIAddListDoesNotMutateExistingConfigOrScope seeds Config and the Scope
// skills directory with an unrelated Skill, then asserts a byte-identical
// snapshot of the whole isolated home (except the Cache, which --list is
// allowed to populate) before and after listing a different Source.
func TestCLIAddListDoesNotMutateExistingConfigOrScope(t *testing.T) {
	resetRootCmdFlags()
	t.Cleanup(resetRootCmdFlags)
	home := isolateHome(t)
	configFile := filepath.Join(home, ".agents", "skills.json")
	skillsDir := filepath.Join(home, ".agents", "skills")
	cacheDir := filepath.Join(home, ".agents", "cache")

	existing := writeCLILocalSkill(t, home, "existing")
	if _, err := runCLI(t, "add", "--symlink", existing, "--skill", "existing", "--yes", "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir); err != nil {
		t.Fatalf("seed add failed: %v", err)
	}
	source := writeCLILocalSkillWithDescription(t, home, "listed", "Listed only.")

	cacheRel, err := filepath.Rel(home, cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, home, cacheRel)

	resetSubcommandFlags()
	out, err := runCLI(t, "add", "--symlink", source, "--list", "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir)
	if err != nil {
		t.Fatalf("add --list failed: %v\n%s", err, out)
	}

	after := snapshotTree(t, home, cacheRel)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("--list mutated existing state under %s\nbefore = %#v\nafter  = %#v", home, before, after)
	}
}

func TestCLIAddListUsageErrors(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"skill", []string{"--skill", "sample"}},
		{"all", []string{"--all"}},
		{"agent", []string{"--agent", "continue"}},
		{"agent shorthand", []string{"-a", "continue"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetRootCmdFlags()
			t.Cleanup(resetRootCmdFlags)
			home := isolateHome(t)
			configFile := filepath.Join(home, ".agents", "skills.json")
			skillsDir := filepath.Join(home, ".agents", "skills")
			cacheDir := filepath.Join(home, ".agents", "cache")

			source := writeCLILocalSkill(t, home, "sample")
			args := append([]string{"add", "--symlink", source, "--list", "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir}, tc.args...)
			_, err := runCLI(t, args...)
			if err == nil {
				t.Fatal("expected a usage error")
			}
		})
	}

	t.Run("command", func(t *testing.T) {
		resetRootCmdFlags()
		t.Cleanup(resetRootCmdFlags)
		home := isolateHome(t)
		configFile := filepath.Join(home, ".agents", "skills.json")
		skillsDir := filepath.Join(home, ".agents", "skills")
		cacheDir := filepath.Join(home, ".agents", "cache")

		_, err := runCLI(t, "add", "--command", "echo hi", "--skill", "sample", "--list", "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir)
		if err == nil {
			t.Fatal("expected --list combined with --command to be a usage error")
		}
	})
}
