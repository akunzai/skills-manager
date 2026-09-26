package engine

import (
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/models"
)

// remoteSpec is what the CLI hands Remote intake for `skills add owner/repo
// --url <url>`: the key's parse, with the clone URL overridden.
func remoteSpec(url, branch, subpath string) models.ParsedRepoSource {
	spec := models.ParseRepoSource("owner/repo")
	spec.URL, spec.Branch, spec.Subpath = url, branch, subpath
	return spec
}

// writeBranchedOrigin is an origin with sample on its default branch and
// dev-only on a dev branch. It returns the origin and its default branch.
func writeBranchedOrigin(t *testing.T) (string, string) {
	t.Helper()
	origin := filepath.Join(t.TempDir(), "origin")
	writeLocalGitSkill(t, origin, "sample")
	defaultBranch := mustGit(t, origin, "symbolic-ref", "--short", "HEAD")
	mustGit(t, origin, "switch", "-c", "dev")
	if err := os.MkdirAll(filepath.Join(origin, "dev-only"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(origin, "dev-only", "SKILL.md"), []byte("# Dev only\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, origin, "add", ".")
	mustGit(t, origin, "commit", "-m", "dev")
	mustGit(t, origin, "switch", defaultBranch)
	return origin, defaultBranch
}

func mustPrepareRemoteIntake(t *testing.T, cfg *config.Config, spec models.ParsedRepoSource, cacheDir string) *RemoteIntake {
	t.Helper()
	intake, err := PrepareRemoteIntake(cfg, spec, cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	return intake
}

// Prepare discovers each candidate's description while the Cache's
// transient SKILL.md-only checkout (ADR-0004) still holds the file.
func TestRemoteIntakePrepareDiscoversSkillsWithDescriptions(t *testing.T) {
	t.Parallel()
	origin := filepath.Join(t.TempDir(), "origin")
	writeLocalGitSkill(t, origin, "plain")
	dir := filepath.Join(origin, "sample")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: sample\ndescription: Remote sample skill.\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, origin, "add", ".")
	mustGit(t, origin, "commit", "-m", "sample")

	intake := mustPrepareRemoteIntake(t, config.DefaultConfig(), remoteSpec(origin, "", ""), t.TempDir())

	want := DiscoveredSkills{"plain": {"plain"}, "sample": {"sample"}}
	if !reflect.DeepEqual(intake.Discovered, want) {
		t.Fatalf("Discovered = %#v; want %#v", intake.Discovered, want)
	}
	if got := intake.Descriptions["sample"]; got != "Remote sample skill." {
		t.Fatalf("description = %q; want %q", got, "Remote sample skill.")
	}
}

// Prepare discovers every Skill from its SKILL.md alone, compares duplicate
// candidates by committed tree, and leaves the Cache's cone as it was.
func TestRemoteIntakePrepareDiscoversFromSkillMDOnly(t *testing.T) {
	t.Parallel()
	_, url := writeSparseOrigin(t)
	cacheDir := t.TempDir()
	intake := mustPrepareRemoteIntake(t, config.DefaultConfig(), remoteSpec(url, "", ""), cacheDir)

	want := DiscoveredSkills{"alpha": {"skills/alpha"}, "beta": {"skills/beta"}}
	if !reflect.DeepEqual(intake.Discovered, want) {
		t.Fatalf("Discovered = %#v; want %#v", intake.Discovered, want)
	}
	assertCachePaths(t, NewCache("owner/repo", url, "", cacheDir).dir(), []string{"README.md"}, []string{"skills", "mirror", "fixtures"})
}

func TestRemoteIntakePrepareKeepsDivergentMirrorsApart(t *testing.T) {
	t.Parallel()
	origin, url := writeSparseOrigin(t)
	if err := os.WriteFile(filepath.Join(origin, "mirror", "alpha", "notes.txt"), []byte("diverged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, origin, "commit", "-am", "diverge")

	intake := mustPrepareRemoteIntake(t, config.DefaultConfig(), remoteSpec(url, "", ""), t.TempDir())

	if got := intake.Discovered["alpha"]; !reflect.DeepEqual(got, []string{"mirror/alpha", "skills/alpha"}) {
		t.Fatalf("alpha candidates = %#v", got)
	}
}

func TestRemoteIntakePrepareNarrowsDiscoveryToTheSubpath(t *testing.T) {
	t.Parallel()
	_, url := writeSparseOrigin(t)
	cfg := config.DefaultConfig()

	intake := mustPrepareRemoteIntake(t, cfg, remoteSpec(url, "", "mirror"), t.TempDir())
	if want := (DiscoveredSkills{"alpha": {"mirror/alpha"}}); !reflect.DeepEqual(intake.Discovered, want) {
		t.Fatalf("Discovered = %#v; want %#v", intake.Discovered, want)
	}

	intake, err := PrepareRemoteIntake(cfg, remoteSpec(url, "", "fixtures"), t.TempDir())
	if err != nil {
		t.Fatalf("a committed directory without Skills is not an error: %v", err)
	}
	if len(intake.Discovered) != 0 {
		t.Fatalf("Discovered = %#v; want none", intake.Discovered)
	}

	if _, err := PrepareRemoteIntake(cfg, remoteSpec(url, "", "absent"), t.TempDir()); err == nil {
		t.Fatal("a directory the commit does not have must fail")
	}
}

// With no branch requested, prepare fetches the one the Config declares. A
// different branch is fetched as asked: prepare declares nothing, so there is
// nothing for it to conflict with (Add's --list lists it).
func TestRemoteIntakePrepareFollowsTheDeclaredBranch(t *testing.T) {
	t.Parallel()
	origin, defaultBranch := writeBranchedOrigin(t)
	cfg := config.DefaultConfig()
	cfg.Remote["owner/repo"] = config.RemoteRepo{URL: origin, Branch: "dev", Skills: map[string]string{"sample": "sample"}}
	cacheDir := t.TempDir()

	intake := mustPrepareRemoteIntake(t, cfg, remoteSpec(origin, "", ""), cacheDir)
	if _, ok := intake.Discovered["dev-only"]; !ok {
		t.Fatalf("Discovered = %#v; want the declared dev branch's Skills", intake.Discovered)
	}

	intake = mustPrepareRemoteIntake(t, cfg, remoteSpec(origin, defaultBranch, ""), cacheDir)
	if _, ok := intake.Discovered["dev-only"]; ok {
		t.Fatalf("Discovered = %#v; want the requested %s branch's Skills", intake.Discovered, defaultBranch)
	}
}

func TestRemoteIntakePrepareReportsAFetchFailure(t *testing.T) {
	t.Parallel()
	cacheDir := t.TempDir()
	_, err := PrepareRemoteIntake(config.DefaultConfig(), remoteSpec(filepath.Join(t.TempDir(), "missing"), "", ""), cacheDir)
	if err == nil || !strings.Contains(err.Error(), "owner/repo") {
		t.Fatalf("err = %v; want a failure naming the Source", err)
	}
}

// Declare refuses to re-point a Source the Config already declares on another
// branch, and leaves the Config as it was.
func TestRemoteIntakeDeclareRefusesABranchConflict(t *testing.T) {
	t.Parallel()
	origin, defaultBranch := writeBranchedOrigin(t)
	cfg := config.DefaultConfig()
	cfg.Remote["owner/repo"] = config.RemoteRepo{URL: origin, Branch: "dev", Skills: map[string]string{"dev-only": "dev-only"}}
	intake := mustPrepareRemoteIntake(t, cfg, remoteSpec(origin, defaultBranch, ""), t.TempDir())
	before := cloneRemote(cfg.Remote)

	err := intake.Declare(cfg, map[string]string{"sample": "sample"})
	if err == nil || !strings.Contains(err.Error(), `already declared on branch "dev"`) {
		t.Fatalf("err = %v; want the branch conflict refused", err)
	}
	if !reflect.DeepEqual(cfg.Remote, before) {
		t.Fatalf("Config = %#v; a refused declare must not change it (was %#v)", cfg.Remote, before)
	}
}

// Declare covers the chosen Skills in the Cache, together with those the
// Config already declares from the Source, since prepare's refresh could not
// know them (ADR-0004).
func TestRemoteIntakeDeclareCoversChosenAndDeclaredSkills(t *testing.T) {
	t.Parallel()
	_, url := writeSparseOrigin(t)
	cacheDir := t.TempDir()
	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/repo", "beta", "skills/beta", "git", url)
	intake := mustPrepareRemoteIntake(t, cfg, remoteSpec(url, "", ""), cacheDir)

	if err := intake.Declare(cfg, map[string]string{"alpha": "skills/alpha"}); err != nil {
		t.Fatal(err)
	}

	assertCachePaths(t, NewCache("owner/repo", url, "", cacheDir).dir(),
		[]string{"skills/alpha/notes.txt", "skills/beta/reference.txt"},
		[]string{"mirror", "fixtures"})
	if got := cfg.Remote["owner/repo"].Skills; !reflect.DeepEqual(got, map[string]string{"alpha": "skills/alpha", "beta": "skills/beta"}) {
		t.Fatalf("declared Skills = %#v", got)
	}
}

func TestRemoteIntakeDeclareRecordsTheSourceAndBranch(t *testing.T) {
	t.Parallel()
	origin, _ := writeBranchedOrigin(t)
	cfg := config.DefaultConfig()
	intake := mustPrepareRemoteIntake(t, cfg, remoteSpec(origin, "dev", ""), t.TempDir())

	if err := intake.Declare(cfg, map[string]string{"dev-only": "dev-only"}); err != nil {
		t.Fatal(err)
	}

	want := config.RemoteRepo{Type: "github", URL: origin, Branch: "dev", Skills: map[string]string{"dev-only": "dev-only"}}
	if got := cfg.Remote["owner/repo"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("declared Source = %#v; want %#v", got, want)
	}
}

// An intake read from one Config can be declared into another. When that
// Config names a branch the intake did not read, declaring would record Skills
// Materialized from the wrong Cache, so it is refused.
func TestRemoteIntakeDeclareRefusesABranchItDidNotRead(t *testing.T) {
	t.Parallel()
	origin, _ := writeBranchedOrigin(t)
	intake := mustPrepareRemoteIntake(t, config.DefaultConfig(), remoteSpec(origin, "", ""), t.TempDir())
	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/repo", "dev-only", "dev-only", "github", origin)
	repo := cfg.Remote["owner/repo"]
	repo.Branch = "dev"
	cfg.Remote["owner/repo"] = repo

	err := intake.Declare(cfg, map[string]string{"sample": "sample"})
	if err == nil || !strings.Contains(err.Error(), "--branch dev") {
		t.Fatalf("Declare = %v; want a refusal naming --branch dev", err)
	}
	if _, declared := cfg.Remote["owner/repo"].Skills["sample"]; declared {
		t.Fatal("a refused Declare changed Config")
	}
}

// A URL the key already implies is not stored, however it is spelled: a
// trailing .git or / and the host's case do not make it another URL. Add and
// adopt both declare through here, and installer lock files record URLs
// without .git.
func TestRemoteIntakeDeclareComparesNormalizedURLs(t *testing.T) {
	for _, tc := range []struct {
		url, stored string
	}{
		{url: "https://github.com/owner/repo.git"},
		{url: "https://github.com/owner/repo"},
		{url: "https://github.com/owner/repo/"},
		{url: "https://github.com/owner/repo.git/"},
		{url: "https://GitHub.com/owner/repo.git"},
		{url: "https://github.com/owner/other.git", stored: "https://github.com/owner/other.git"},
		{url: "https://github.com/Owner/repo", stored: "https://github.com/Owner/repo"},
	} {
		t.Run(tc.url, func(t *testing.T) {
			origin := filepath.Join(t.TempDir(), "origin")
			writeLocalGitSkill(t, origin, "sample")
			setGitConfig(t, "url."+localFileURL(origin)+".insteadOf", tc.url)
			cfg := config.DefaultConfig()
			intake := mustPrepareRemoteIntake(t, cfg, remoteSpec(tc.url, "", ""), t.TempDir())

			if err := intake.Declare(cfg, map[string]string{"sample": "sample"}); err != nil {
				t.Fatal(err)
			}

			if got := cfg.Remote["owner/repo"].URL; got != tc.stored {
				t.Fatalf("stored URL = %q; want %q", got, tc.stored)
			}
		})
	}
}

func cloneRemote(remote map[string]config.RemoteRepo) map[string]config.RemoteRepo {
	clone := make(map[string]config.RemoteRepo, len(remote))
	for key, repo := range remote {
		repo.Skills = maps.Clone(repo.Skills)
		clone[key] = repo
	}
	return clone
}
