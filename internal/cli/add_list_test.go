package cli

import (
	"bytes"
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

// --list declares nothing, so a branch other than the one the Scope declares
// for the Source is listed rather than refused.
func TestCLIAddListListsAnotherBranchOfADeclaredSource(t *testing.T) {
	home := isolateHome(t)
	scope := []string{"--config", filepath.Join(home, ".agents", "skills.json"), "--skills-dir", filepath.Join(home, ".agents", "skills"), "--cache-dir", filepath.Join(home, ".agents", "cache")}

	origin := filepath.Join(home, "origin")
	writeCLIGitSkill(t, origin, "sample")
	defaultBranch := cliRunGit(t, origin, "symbolic-ref", "--short", "HEAD")
	cliRunGit(t, origin, "switch", "-c", "dev")
	if out, err := runCLI(t, append([]string{"add", "owner/repo", "--url", origin, "--branch", "dev", "--skill", "sample", "-y"}, scope...)...); err != nil {
		t.Fatalf("seed add: %v\n%s", err, out)
	}

	out, err := runCLI(t, append([]string{"add", "owner/repo", "--url", origin, "--branch", defaultBranch, "--list"}, scope...)...)
	if err != nil {
		t.Fatalf("add --list on another branch: %v\n%s", err, out)
	}
	if !strings.Contains(out, "sample") {
		t.Fatalf("expected sample listed from %s, got:\n%s", defaultBranch, out)
	}
}

// TestCLIAddListDoesNotMutateExistingConfigOrScope seeds Config and the Scope
// skills directory with an unrelated Skill, then asserts a byte-identical
// snapshot of the whole isolated home (except the Cache, which --list is
// allowed to populate) before and after listing a different Source.
func TestCLIAddListDoesNotMutateExistingConfigOrScope(t *testing.T) {
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

// A monorepo keeps its Skill deep in a package and mirrors it for an editor
// through a symlinked SKILL.md; only the real Skill is offered, also where git
// checks a symlink out as a plain file holding its target (core.symlinks=false,
// the Windows default).
func TestCLIAddListFindsADeepSkillAndSkipsItsSymlinkedMirror(t *testing.T) {
	for _, symlinks := range []string{"true", "false"} {
		t.Run("core.symlinks="+symlinks, func(t *testing.T) {
			home := isolateHome(t)
			scope := []string{"--config", filepath.Join(home, ".agents", "skills.json"), "--skills-dir", filepath.Join(home, ".agents", "skills"), "--cache-dir", filepath.Join(home, ".agents", "cache")}

			origin := filepath.Join(home, "origin")
			real := filepath.Join(origin, "packages", "cli", "plugins", "sentry-cli", "skills", "sentry-cli")
			mirror := filepath.Join(origin, "packages", "cli", ".cursor", "skills", "sentry-cli")
			for _, dir := range []string{real, mirror} {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(real, "SKILL.md"), []byte("---\nname: sentry-cli\ndescription: Use the Sentry CLI.\n---\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("../../../plugins/sentry-cli/skills/sentry-cli/SKILL.md", filepath.Join(mirror, "SKILL.md")); err != nil {
				t.Fatal(err)
			}
			commitCLIGitRepo(t, origin)
			t.Setenv("GIT_CONFIG_COUNT", "1")
			t.Setenv("GIT_CONFIG_KEY_0", "core.symlinks")
			t.Setenv("GIT_CONFIG_VALUE_0", symlinks)

			var stdout, stderr bytes.Buffer
			cmd := newRootCmd()
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			cmd.SetArgs(append([]string{"add", "owner/repo", "--url", origin, "--list", "--json"}, scope...))
			if err := cmd.Execute(); err != nil {
				t.Fatalf("add --list --json: %v\n%s%s", err, stdout.String(), stderr.String())
			}
			var rows []map[string]string
			if err := json.Unmarshal(stdout.Bytes(), &rows); err != nil {
				t.Fatalf("--json output did not parse: %v\n%s", err, stdout.String())
			}
			want := []map[string]string{{"name": "sentry-cli", "path": "packages/cli/plugins/sentry-cli/skills/sentry-cli", "description": "Use the Sentry CLI."}}
			if !reflect.DeepEqual(rows, want) {
				t.Fatalf("rows = %#v; want %#v", rows, want)
			}
		})
	}
}

// A local Source is linked, not copied or verified, so a Skill whose SKILL.md
// is a symlink (a dotfiles layout) still works and is still offered.
func TestCLIAddListOffersALocalSkillWhoseSkillFileIsASymlink(t *testing.T) {
	home := isolateHome(t)
	scope := []string{"--config", filepath.Join(home, ".agents", "skills.json"), "--skills-dir", filepath.Join(home, ".agents", "skills"), "--cache-dir", filepath.Join(home, ".agents", "cache")}

	dotfiles := filepath.Join(home, "dotfiles")
	if err := os.MkdirAll(dotfiles, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dotfiles, "notes.md"), []byte("---\nname: notes\ndescription: Keep notes.\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(home, "src", "notes")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dotfiles, "notes.md"), filepath.Join(source, "SKILL.md")); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, append([]string{"add", "--symlink", source, "--list"}, scope...)...)
	if err != nil {
		t.Fatalf("add --list: %v\n%s", err, out)
	}
	if !strings.Contains(out, "notes") || !strings.Contains(out, "Keep notes.") {
		t.Fatalf("expected the symlinked local Skill listed, got:\n%s", out)
	}
}

// MaxScanDepth counts from --path, so a Skill too deep for the Source root is
// found once --path narrows discovery toward it.
func TestCLIAddListMeasuresDepthFromPath(t *testing.T) {
	home := isolateHome(t)
	scope := []string{"--config", filepath.Join(home, ".agents", "skills.json"), "--skills-dir", filepath.Join(home, ".agents", "skills"), "--cache-dir", filepath.Join(home, ".agents", "cache")}

	root := filepath.Join(home, "repo")
	skill := filepath.Join(root, "a", "b", "c", "d", "e", "f", "g", "h", "deep")
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: deep\ndescription: Nine levels down.\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if out, err := runCLI(t, append([]string{"add", "--symlink", root, "--list"}, scope...)...); ExitCode(err) != 1 {
		t.Fatalf("listing the root: exit %d; want 1 (nine levels is past MaxScanDepth)\n%s", ExitCode(err), out)
	}
	out, err := runCLI(t, append([]string{"add", "--symlink", root, "--path", "a/b", "--list"}, scope...)...)
	if err != nil {
		t.Fatalf("listing --path a/b: %v\n%s", err, out)
	}
	if !strings.Contains(out, "a/b/c/d/e/f/g/h/deep") {
		t.Fatalf("expected the Skill seven levels below --path listed, got:\n%s", out)
	}
}
