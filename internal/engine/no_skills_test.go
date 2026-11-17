package engine

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/models"
	"github.com/akunzai/skills-manager/internal/wellknowntest"
)

func assertNoSkills(t *testing.T, err error, want NoSkillsError) {
	t.Helper()
	got, ok := errors.AsType[*NoSkillsError](err)
	if !ok || *got != want {
		t.Fatalf("err = %#v; want %#v", err, want)
	}
}

func TestNoSkillsInDirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	discovered, _, err := DiscoverSkillsInRepo(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	assertNoSkills(t, NoSkillsInDirectory(discovered, "~/empty", ""), NoSkillsError{Source: "~/empty"})
	assertNoSkills(t, NoSkillsInDirectory(discovered, "~/empty", "docs"), NoSkillsError{Source: "~/empty", Subpath: "docs"})

	if err := os.MkdirAll(filepath.Join(dir, "sample"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sample", "SKILL.md"), []byte("# Sample\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if discovered, _, err = DiscoverSkillsInRepo(dir, ""); err != nil {
		t.Fatal(err)
	}
	if err := NoSkillsInDirectory(discovered, "~/empty", ""); err != nil {
		t.Fatalf("err = %v; a directory with a Skill offers it", err)
	}
}

func TestRemoteIntakeNoSkillsInAGitSource(t *testing.T) {
	t.Parallel()
	origin := filepath.Join(t.TempDir(), "origin")
	writeLocalGitSkill(t, origin, "sample")
	cacheDir := filepath.Join(t.TempDir(), "cache")

	intake := mustPrepareRemoteIntake(t, config.DefaultConfig(), remoteSpec(origin, "", ""), cacheDir)
	if err := intake.NoSkills(); err != nil {
		t.Fatalf("err = %v; the Source offers sample", err)
	}

	mustGit(t, origin, "rm", "-rq", "sample")
	if err := os.WriteFile(filepath.Join(origin, "README.md"), []byte("# Nothing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, origin, "add", ".")
	mustGit(t, origin, "commit", "-qm", "empty")
	intake = mustPrepareRemoteIntake(t, config.DefaultConfig(), remoteSpec(origin, "", ""), filepath.Join(t.TempDir(), "cache"))
	assertNoSkills(t, intake.NoSkills(), NoSkillsError{Source: "owner/repo"})
}

func TestRemoteIntakeNoSkillsInAWellKnownIndex(t *testing.T) {
	odd := wellknowntest.SkillMD("odd", "Odd")
	odd.Type = "bundle"
	for name, entries := range map[string][]wellknowntest.Entry{
		"empty index":         nil,
		"every entry skipped": {odd},
	} {
		t.Run(name, func(t *testing.T) {
			server, _, cacheDir := newWellKnownTestCache(t, entries...)
			spec := models.ParsedRepoSource{SourceKey: "example.com", URL: server.URL, RepoType: config.WellKnownType}

			intake := mustPrepareRemoteIntake(t, config.DefaultConfig(), spec, cacheDir)

			assertNoSkills(t, intake.NoSkills(), NoSkillsError{Source: models.WellKnownIndexURL(server.URL), Index: true})
			if (len(entries) > 0) != (len(intake.Skipped) > 0) {
				t.Fatalf("Skipped = %v; want one warning per skipped entry", intake.Skipped)
			}
		})
	}
}

func TestNoSkillsErrorNamesWhatIsMissing(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		err  NoSkillsError
		want string
	}{
		{NoSkillsError{Source: "owner/repo"}, "owner/repo has no SKILL.md"},
		{NoSkillsError{Source: "https://example.com/.well-known/agent-skills/index.json", Index: true}, "https://example.com/.well-known/agent-skills/index.json lists no Skills"},
	} {
		if got := tc.err.Error(); got != tc.want {
			t.Errorf("Error() = %q; want %q", got, tc.want)
		}
	}
}
