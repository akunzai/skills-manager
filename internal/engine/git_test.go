package engine

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestEnsureGitRepoConfiguresLongpathsOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("skipping Windows-specific core.longpaths test on non-windows platform")
	}

	root := t.TempDir()
	origin := filepath.Join(root, "origin")
	writeLocalGitSkill(t, origin, "sample")

	cacheDir := filepath.Join(root, "cache")
	repoDir, err := EnsureGitRepo("owner/repo", origin, "", false, cacheDir)
	if err != nil {
		t.Fatalf("EnsureGitRepo failed: %v", err)
	}

	val, _, err := RunGit(repoDir, "config", "--local", "--get", "core.longpaths")
	if err != nil {
		t.Fatalf("git config core.longpaths failed: %v", err)
	}
	if val != "true" {
		t.Fatalf("expected core.longpaths to be 'true', got %q", val)
	}

	// Test update path on existing repo
	// Remove the config key first to simulate an older cache repo
	if _, _, err := RunGit(repoDir, "config", "--local", "--unset", "core.longpaths"); err != nil {
		t.Fatalf("failed to unset core.longpaths: %v", err)
	}

	// Calling EnsureGitRepo again should re-apply core.longpaths on Windows
	if _, err := EnsureGitRepo("owner/repo", origin, "", false, cacheDir); err != nil {
		t.Fatalf("EnsureGitRepo update failed: %v", err)
	}

	valAfter, _, err := RunGit(repoDir, "config", "--local", "--get", "core.longpaths")
	if err != nil {
		t.Fatalf("git config core.longpaths failed after update: %v", err)
	}
	if valAfter != "true" {
		t.Fatalf("expected core.longpaths to be restored to 'true', got %q", valAfter)
	}
}
