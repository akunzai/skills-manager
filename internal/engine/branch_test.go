package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
)

func TestAddBranch(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Remote["pinned/repo"] = config.RemoteRepo{Branch: "dev", Skills: map[string]string{"a": "a"}}
	cfg.Remote["default/repo"] = config.RemoteRepo{Skills: map[string]string{"b": "b"}}
	tests := []struct {
		name, key, requested, want, wantErr string
	}{
		{"new Source keeps the request", "new/repo", "v1.0.0", "v1.0.0", ""},
		{"new Source on the default branch", "new/repo", "", "", ""},
		{"same branch", "pinned/repo", "dev", "dev", ""},
		{"no request follows the declared branch", "pinned/repo", "", "dev", ""},
		{"a different branch is refused", "pinned/repo", "main", "", `already declared on branch "dev"`},
		{"a branch on a default-branch Source is refused", "default/repo", "dev", "", "already declared on its default branch"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := AddBranch(cfg, tt.key, tt.requested)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) || !strings.Contains(err.Error(), "skills rm") {
					t.Fatalf("err = %v; want it to contain %q and name skills rm", err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("AddBranch = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

func TestRemoteRepoCommitResolvesBranchesAndTags(t *testing.T) {
	origin := t.TempDir()
	if err := os.WriteFile(filepath.Join(origin, "SKILL.md"), []byte("# v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, origin, "init")
	mustGit(t, origin, "config", "user.email", "test@example.com")
	mustGit(t, origin, "config", "user.name", "test")
	mustGit(t, origin, "add", ".")
	mustGit(t, origin, "commit", "-m", "v1")
	v1 := mustGit(t, origin, "rev-parse", "HEAD")
	mustGit(t, origin, "tag", "light")
	mustGit(t, origin, "tag", "-a", "annotated", "-m", "annotated")
	mustGit(t, origin, "commit", "--allow-empty", "-m", "v2")
	head := mustGit(t, origin, "rev-parse", "HEAD")
	branch := mustGit(t, origin, "symbolic-ref", "--short", "HEAD")
	url := localFileURL(origin)

	for ref, want := range map[string]string{branch: head, "light": v1, "annotated": v1} {
		got, err := remoteRepoCommit("owner/repo", url, ref)
		if err != nil || got != want {
			t.Errorf("remoteRepoCommit(%q) = %q, %v; want %q", ref, got, err, want)
		}
	}
	if _, err := remoteRepoCommit("owner/repo", url, "missing"); err == nil || !strings.Contains(err.Error(), "branch or tag missing not found") {
		t.Errorf("missing ref err = %v", err)
	}
}

// A Cache on an annotated tag checks out the tagged commit, so a Source
// declared on a tag reads as up to date rather than forever outdated.
func TestObserveTreatsACacheOnATagAsUpToDate(t *testing.T) {
	origin := t.TempDir()
	writeLocalGitSkill(t, origin, "sample")
	mustGit(t, origin, "tag", "-a", "v1.0.0", "-m", "v1.0.0")
	mustGit(t, origin, "commit", "--allow-empty", "-m", "after the tag")
	repo := config.RemoteRepo{URL: localFileURL(origin), Branch: "v1.0.0", Skills: map[string]string{"sample": "sample"}}
	cacheDir := t.TempDir()
	if _, err := NewCache("owner/repo", repo.URL, repo.Branch, cacheDir).Refresh(false, "sample"); err != nil {
		t.Fatal(err)
	}
	if status := observeRemoteSource("owner/repo", repo, cacheDir); status.RemoteStatus != RemoteUpToDate {
		t.Fatalf("status = %q (%s); want %q", status.RemoteStatus, status.Error, RemoteUpToDate)
	}
	if _, err := NewCache("owner/repo", repo.URL, repo.Branch, cacheDir).Refresh(true, "sample"); err != nil {
		t.Fatalf("refreshing a tag Cache: %v", err)
	}
}
