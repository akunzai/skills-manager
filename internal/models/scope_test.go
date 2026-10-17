package models

import (
	"path/filepath"
	"testing"
)

func TestStoreLocalSourceGlobalScopeUsesTildePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("AGENTS_HOME", filepath.Join(home, ".agents"))

	globalSkills := filepath.Join(home, ".agents", "skills")
	absSource := filepath.Join(home, "code", "my-skill")

	if got, want := (Scope{SkillsDir: globalSkills, Root: home}).StoreLocalSource(absSource), "~/code/my-skill"; got != want {
		t.Errorf("StoreLocalSource(global) = %q; want %q", got, want)
	}
}

func TestStoreLocalSourceProjectScopeInsideProjectIsRelative(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("AGENTS_HOME", filepath.Join(home, ".agents"))

	projectRoot := filepath.Join(home, "workspace", "demo")
	skillsDir := filepath.Join(projectRoot, ".agents", "skills")
	absSource := filepath.Join(projectRoot, "my-skill")

	if got, want := (Scope{SkillsDir: skillsDir, IsProject: true, Root: projectRoot}).StoreLocalSource(absSource), "my-skill"; got != want {
		t.Errorf("StoreLocalSource(inside project) = %q; want %q", got, want)
	}
}

func TestStoreLocalSourceProjectScopeOutsideProjectUsesTildePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("AGENTS_HOME", filepath.Join(home, ".agents"))

	projectRoot := filepath.Join(home, "workspace", "demo")
	skillsDir := filepath.Join(projectRoot, ".agents", "skills")
	absSource := filepath.Join(home, "elsewhere", "my-skill")

	if got, want := (Scope{SkillsDir: skillsDir, IsProject: true, Root: projectRoot}).StoreLocalSource(absSource), "~/elsewhere/my-skill"; got != want {
		t.Errorf("StoreLocalSource(outside project) = %q; want %q", got, want)
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

	if got, want := (Scope{SkillsDir: skillsDir, IsProject: true, Root: projectRoot}).LocalSymlinkTarget(absSource), filepath.Join("..", "..", "my-skill"); got != want {
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

	if got := (Scope{SkillsDir: globalSkills, Root: home}).LocalSymlinkTarget(absSource); got != absSource {
		t.Errorf("LocalSymlinkTarget(global) = %q; want %q", got, absSource)
	}
}

func TestResolveLocalSourceAbsoluteSourceReturnedAsIs(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "my-skill")
	if got := (Scope{IsProject: true, Root: "/anything"}).ResolveLocalSource(abs); got != abs {
		t.Errorf("ResolveLocalSource(absolute) = %q; want %q", got, abs)
	}
}

func TestResolveLocalSourceRelativeSourceResolvesAgainstProjectRoot(t *testing.T) {
	projectRoot := filepath.Join(t.TempDir(), "demo")
	got := (Scope{IsProject: true, Root: projectRoot}).ResolveLocalSource("my-skill")
	want := filepath.Join(projectRoot, "my-skill")
	if got != want {
		t.Errorf("ResolveLocalSource(relative) = %q; want %q", got, want)
	}
}

func TestResolveLocalSourceTildeSourceExpandsToHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	got := (Scope{IsProject: true, Root: "/anything"}).ResolveLocalSource("~/elsewhere/my-skill")
	want := filepath.Join(home, "elsewhere", "my-skill")
	if got != want {
		t.Errorf("ResolveLocalSource(tilde) = %q; want %q", got, want)
	}
}

func TestLocalSourceInsideSkillsDir(t *testing.T) {
	skillsDir := filepath.Join(t.TempDir(), ".agents", "skills")
	inside := filepath.Join(skillsDir, "mine")
	nested := filepath.Join(skillsDir, "mine", "nested")
	sibling := skillsDir + "-backup"
	outside := filepath.Join(filepath.Dir(skillsDir), "local-skills", "mine")

	if !LocalSourceInsideSkillsDir(skillsDir, skillsDir) {
		t.Error("the skills directory itself is inside")
	}
	if !LocalSourceInsideSkillsDir(inside, skillsDir) {
		t.Error("a Skill directory on the skills directory is inside")
	}
	if !LocalSourceInsideSkillsDir(nested, skillsDir) {
		t.Error("a nested path under the skills directory is inside")
	}
	if LocalSourceInsideSkillsDir(outside, skillsDir) {
		t.Error("a sibling of the skills directory is not inside")
	}
	if LocalSourceInsideSkillsDir(sibling, skillsDir) {
		t.Errorf("%q must not count as inside %q", sibling, skillsDir)
	}
	if LocalSourceInsideSkillsDir("", skillsDir) || LocalSourceInsideSkillsDir(inside, "") {
		t.Error("empty paths are not inside")
	}
}

// Scope kind is the flag choice (ADR-0012): a Global Scope whose skills
// directory was moved with --skills-dir still links its home's Agent
// directories and stores local Sources as it would at the default path.
func TestGlobalScopeWithMovedSkillsDirStaysGlobal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("AGENTS_HOME", filepath.Join(home, ".agents"))
	work := filepath.Join(home, "work")
	scope := NewGlobalScope("", filepath.Join(work, "skills"), "")

	if got, want := scope.Agents().KnownDirs()["claude-code"], filepath.Join(home, ".claude", "skills"); got != want {
		t.Errorf("claude-code dir = %q, want %q", got, want)
	}
	if got, want := scope.StoreLocalSource(filepath.Join(work, "my-skill")), "~/work/my-skill"; got != want {
		t.Errorf("StoreLocalSource = %q, want %q", got, want)
	}
	if scope.Root != home {
		t.Errorf("Root = %q, want %q", scope.Root, home)
	}
}

// A Project Scope whose skills directory was moved with --skills-dir keeps
// its Agent directories and relative Sources in the project it runs in.
func TestProjectScopeWithMovedSkillsDirStaysInItsProject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("AGENTS_HOME", filepath.Join(home, ".agents"))
	project := filepath.Join(home, "workspace", "demo")
	scope := NewProjectScope(project, "", filepath.Join(home, ".agents", "skills"), "")

	if got, want := scope.Agents().KnownDirs()["claude-code"], filepath.Join(project, ".claude", "skills"); got != want {
		t.Errorf("claude-code dir = %q, want %q", got, want)
	}
	if got, want := scope.StoreLocalSource(filepath.Join(project, "my-skill")), "my-skill"; got != want {
		t.Errorf("StoreLocalSource = %q, want %q", got, want)
	}
	if scope.Root != project {
		t.Errorf("Root = %q, want %q", scope.Root, project)
	}
}

func TestProjectScopeWithoutRootPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("RootDir of a Project Scope with no Root must panic, not resolve against the working directory")
		}
	}()
	(Scope{IsProject: true}).RootDir()
}
