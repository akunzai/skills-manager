package models

import (
	"path/filepath"
	"testing"
)

func TestStoreLocalSourcePathGlobalScopeUsesTildePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("AGENTS_HOME", filepath.Join(home, ".agents"))

	globalSkills := filepath.Join(home, ".agents", "skills")
	absSource := filepath.Join(home, "code", "my-skill")

	if got, want := StoreLocalSourcePath(absSource, globalSkills), "~/code/my-skill"; got != want {
		t.Errorf("StoreLocalSourcePath(global) = %q; want %q", got, want)
	}
}

func TestStoreLocalSourcePathProjectScopeInsideProjectIsRelative(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("AGENTS_HOME", filepath.Join(home, ".agents"))

	projectRoot := filepath.Join(home, "workspace", "demo")
	skillsDir := filepath.Join(projectRoot, ".agents", "skills")
	absSource := filepath.Join(projectRoot, "my-skill")

	if got, want := StoreLocalSourcePath(absSource, skillsDir), "my-skill"; got != want {
		t.Errorf("StoreLocalSourcePath(inside project) = %q; want %q", got, want)
	}
}

func TestStoreLocalSourcePathProjectScopeOutsideProjectUsesTildePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("AGENTS_HOME", filepath.Join(home, ".agents"))

	projectRoot := filepath.Join(home, "workspace", "demo")
	skillsDir := filepath.Join(projectRoot, ".agents", "skills")
	absSource := filepath.Join(home, "elsewhere", "my-skill")

	if got, want := StoreLocalSourcePath(absSource, skillsDir), "~/elsewhere/my-skill"; got != want {
		t.Errorf("StoreLocalSourcePath(outside project) = %q; want %q", got, want)
	}
}

func TestLocalSymlinkTargetProjectScopeInsideProjectIsRelative(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("AGENTS_HOME", filepath.Join(home, ".agents"))

	projectRoot := filepath.Join(home, "workspace", "demo")
	skillsDir := filepath.Join(projectRoot, ".agents", "skills")
	absSource := filepath.Join(projectRoot, "my-skill")

	if got, want := LocalSymlinkTarget(absSource, skillsDir), filepath.Join("..", "..", "my-skill"); got != want {
		t.Errorf("LocalSymlinkTarget(inside project) = %q; want %q", got, want)
	}
}

func TestLocalSymlinkTargetGlobalScopeStaysAbsolute(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("AGENTS_HOME", filepath.Join(home, ".agents"))

	globalSkills := filepath.Join(home, ".agents", "skills")
	absSource := filepath.Join(home, "code", "my-skill")

	if got := LocalSymlinkTarget(absSource, globalSkills); got != absSource {
		t.Errorf("LocalSymlinkTarget(global) = %q; want %q", got, absSource)
	}
}

func TestResolveLocalSourcePathAbsoluteSourceReturnedAsIs(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "my-skill")
	if got := ResolveLocalSourcePath(abs, "/anything"); got != abs {
		t.Errorf("ResolveLocalSourcePath(absolute) = %q; want %q", got, abs)
	}
}

func TestResolveLocalSourcePathRelativeSourceResolvesAgainstProjectRoot(t *testing.T) {
	projectRoot := filepath.Join(t.TempDir(), "demo")
	skillsDir := filepath.Join(projectRoot, ".agents", "skills")

	got := ResolveLocalSourcePath("my-skill", skillsDir)
	want := filepath.Join(projectRoot, "my-skill")
	if got != want {
		t.Errorf("ResolveLocalSourcePath(relative) = %q; want %q", got, want)
	}
}

func TestResolveLocalSourcePathTildeSourceExpandsToHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	got := ResolveLocalSourcePath("~/elsewhere/my-skill", "/anything")
	want := filepath.Join(home, "elsewhere", "my-skill")
	if got != want {
		t.Errorf("ResolveLocalSourcePath(tilde) = %q; want %q", got, want)
	}
}

func TestScopeRootProjectIsCheckout(t *testing.T) {
	project := filepath.FromSlash("/path/to/my-project")
	skillsDir := filepath.Join(project, ".agents", "skills")
	if got := ScopeRoot(skillsDir); got != project {
		t.Errorf("ScopeRoot(project) = %q; want %q", got, project)
	}
}

func TestScopeRootGlobalIsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("AGENTS_HOME", filepath.Join(home, ".agents"))
	skillsDir := filepath.Join(home, ".agents", "skills")
	if got := ScopeRoot(skillsDir); got != home {
		t.Errorf("ScopeRoot(global) = %q; want %q", got, home)
	}
}

func TestScopeMethods(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("AGENTS_HOME", filepath.Join(home, ".agents"))

	globalScope := NewGlobalScope("", "", "")
	if globalScope.IsProject {
		t.Error("expected globalScope.IsProject to be false")
	}
	if got := globalScope.Root(); got != home {
		t.Errorf("globalScope.Root() = %q, want %q", got, home)
	}
	if !globalScope.IsUniversalAgent("gemini") {
		t.Error("expected gemini to be universal agent in global scope")
	}
	if globalScope.IsUniversalAgent("claude") {
		t.Error("expected claude to not be universal agent in global scope")
	}

	projectDir := filepath.Join(home, "workspace", "my-project")
	projectScope := NewProjectScope(projectDir, "", "", "")
	if !projectScope.IsProject {
		t.Error("expected projectScope.IsProject to be true")
	}
	if got := projectScope.Root(); got != projectDir {
		t.Errorf("projectScope.Root() = %q, want %q", got, projectDir)
	}
	if got := projectScope.StoreLocalSource(filepath.Join(projectDir, "my-skill")); got != "my-skill" {
		t.Errorf("projectScope.StoreLocalSource() = %q, want %q", got, "my-skill")
	}
}
