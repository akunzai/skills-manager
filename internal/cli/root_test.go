package cli

import (
	"path/filepath"
	"testing"

	"github.com/akunzai/skills-manager/internal/models"
)

// resolveScope is a pure function of its arguments — no cobra flag parsing,
// no package-level flag vars — so it is tested directly rather than through
// RootCmd.Execute().
func TestResolveScopeGlobalDefault(t *testing.T) {
	scope := resolveScope(false, "/cwd", "", "", "")
	if scope.IsProject {
		t.Errorf("expected Global Scope")
	}
	if scope.ConfigPath != models.DefaultConfigFile() {
		t.Errorf("ConfigPath = %q; want %q", scope.ConfigPath, models.DefaultConfigFile())
	}
	if scope.SkillsDir != models.DefaultSkillsDir() {
		t.Errorf("SkillsDir = %q; want %q", scope.SkillsDir, models.DefaultSkillsDir())
	}
	if scope.CacheDir != models.DefaultCacheDir() {
		t.Errorf("CacheDir = %q; want %q", scope.CacheDir, models.DefaultCacheDir())
	}
}

func TestResolveScopeProject(t *testing.T) {
	cwd := t.TempDir()
	scope := resolveScope(true, cwd, "", "", "")
	if !scope.IsProject {
		t.Errorf("expected Project Scope")
	}
	wantConfig := filepath.Join(cwd, ".agents", "skills.json")
	if scope.ConfigPath != wantConfig {
		t.Errorf("ConfigPath = %q; want %q", scope.ConfigPath, wantConfig)
	}
	wantSkills := filepath.Join(cwd, ".agents", "skills")
	if scope.SkillsDir != wantSkills {
		t.Errorf("SkillsDir = %q; want %q", scope.SkillsDir, wantSkills)
	}
	// Cache is never Scope-dependent: it caches remote git checkouts, shared
	// across Global and Project.
	if scope.CacheDir != models.DefaultCacheDir() {
		t.Errorf("CacheDir = %q; want %q (Cache is not Project-scoped)", scope.CacheDir, models.DefaultCacheDir())
	}
}

func TestResolveScopeOverridesWinRegardlessOfProject(t *testing.T) {
	for _, isProject := range []bool{false, true} {
		scope := resolveScope(isProject, "/cwd", "/custom/config.json", "/custom/skills", "/custom/cache")
		if scope.ConfigPath != "/custom/config.json" {
			t.Errorf("isProject=%v: ConfigPath = %q; want override", isProject, scope.ConfigPath)
		}
		if scope.SkillsDir != "/custom/skills" {
			t.Errorf("isProject=%v: SkillsDir = %q; want override", isProject, scope.SkillsDir)
		}
		if scope.CacheDir != "/custom/cache" {
			t.Errorf("isProject=%v: CacheDir = %q; want override", isProject, scope.CacheDir)
		}
		if scope.IsProject != isProject {
			t.Errorf("IsProject = %v; want %v (an override does not change which Scope was chosen)", scope.IsProject, isProject)
		}
	}
}

func TestResolveScopeExpandsUserInOverrides(t *testing.T) {
	scope := resolveScope(false, "/cwd", "~/custom.json", "", "")
	if scope.ConfigPath != models.ExpandUser("~/custom.json") {
		t.Errorf("ConfigPath = %q; want expanded", scope.ConfigPath)
	}
}
