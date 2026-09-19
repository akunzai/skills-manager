package models

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestNormalizeAgentName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"claude", "claude-code"},
		{"Claude-Code", "claude-code"},
		{"gemini", "gemini-cli"},
		{"antigravity", "antigravity-cli"},
		{"vibe", "mistral-vibe"},
		{"muse", "muse-code"},
		{"roo-code", "roo"},
		{"unknown-agent", "unknown-agent"},
	}

	for _, tt := range tests {
		got := NormalizeAgentName(tt.input)
		if got != tt.expected {
			t.Errorf("NormalizeAgentName(%q) = %q; want %q", tt.input, got, tt.expected)
		}
	}
}

func TestIsAutomatic(t *testing.T) {
	global := ForSkillsDir("")
	if !global.IsAutomatic("gemini") {
		t.Errorf("expected gemini to be Automatically available at Global Scope")
	}
	if !global.IsAutomatic("cursor") {
		t.Errorf("expected cursor to be Automatically available at Global Scope")
	}
	if global.IsAutomatic("claude-code") {
		t.Errorf("expected claude-code to not be Automatically available")
	}
	// Confirmed live via `copilot skill list`, which lists exactly the
	// contents of ~/.agents/skills.
	if !global.IsAutomatic("github-copilot") {
		t.Errorf("expected github-copilot to be Automatically available at Global Scope")
	}
}

func TestIsAutomaticIsScopeAware(t *testing.T) {
	projectSkillsDir := filepath.Join(t.TempDir(), ".agents", "skills")
	project := ForSkillsDir(projectSkillsDir)
	global := ForSkillsDir("")

	for _, agent := range []string{"antigravity-cli", "replit"} {
		if global.IsAutomatic(agent) {
			t.Errorf("expected %s to not be Automatically available at Global Scope", agent)
		}
		if !project.IsAutomatic(agent) {
			t.Errorf("expected %s to be Automatically available at Project Scope", agent)
		}
	}

	// grok is the inverse split of antigravity-cli: Global reads
	// ~/.agents/skills; Project uses ./.grok/skills.
	if !global.IsAutomatic("grok") {
		t.Errorf("expected grok to be Automatically available at Global Scope")
	}
	if project.IsAutomatic("grok") {
		t.Errorf("expected grok to not be Automatically available at Project Scope")
	}
	if !global.IsAutomatic("muse") || !project.IsAutomatic("muse-code") {
		t.Errorf("expected muse-code to be Automatically available in both Scopes")
	}

	// cline reads its own directory in every Scope (docs.cline.bot), not
	// .agents/skills, so it must never be Automatically available.
	if global.IsAutomatic("cline") || project.IsAutomatic("cline") {
		t.Errorf("expected cline to not be Automatically available in any Scope")
	}
}

func TestAutomaticAgentsAreScopeAwareAndSorted(t *testing.T) {
	project := ForSkillsDir(filepath.Join(t.TempDir(), ".agents", "skills")).Automatic()
	for _, want := range []string{"antigravity-cli", "codex", "muse-code", "replit"} {
		if !slices.Contains(project, want) {
			t.Fatalf("Project Automatically available Agents missing %q: %#v", want, project)
		}
	}
	if slices.Contains(project, "grok") {
		t.Fatalf("grok is Global-only Automatically available: %#v", project)
	}
	if slices.Contains(project, "universal") {
		t.Fatalf("filter alias must not be reported as an Agent: %#v", project)
	}
	if !slices.IsSorted(project) {
		t.Fatalf("Agents are not sorted: %#v", project)
	}

	global := ForSkillsDir("").Automatic()
	for _, want := range []string{"codex", "grok", "muse-code"} {
		if !slices.Contains(global, want) {
			t.Fatalf("Global Automatically available Agents missing %q: %#v", want, global)
		}
	}
	if slices.Contains(global, "antigravity-cli") {
		t.Fatalf("antigravity-cli is Project-only Automatically available: %#v", global)
	}
}

// A typo'd alias target would otherwise resolve to an agent with no
// directory and no error anywhere in the call chain. Every alias must land
// on a Global agent that is known or Automatically available, never both.
func TestAgentAliasesResolveToExactlyOneKnownOrAutomaticGlobalAgent(t *testing.T) {
	global := ForSkillsDir("")
	known := global.KnownDirs()
	for alias, canonical := range agentAliases {
		_, isKnown := known[canonical]
		isAutomatic := global.IsAutomatic(canonical)
		switch {
		case !isKnown && !isAutomatic:
			t.Errorf("alias %q resolves to %q, which is neither a known agent nor Automatically available at Global Scope", alias, canonical)
		case isKnown && isAutomatic:
			t.Errorf("alias %q resolves to %q, which is both a known agent and Automatically available at Global Scope", alias, canonical)
		}
	}
}

// An Agent must never be both a linkable known dir and Automatically
// available in the same Scope — that contradiction is exactly the cursor/cline
// bug this registry fixes. Project Scope only supports a subset of Agents, so
// unlike the Global check this does not require every alias to resolve.
func TestNoAgentIsBothKnownAndAutomaticInSameScope(t *testing.T) {
	projectSkillsDir := filepath.Join(t.TempDir(), ".agents", "skills")
	project := ForSkillsDir(projectSkillsDir)
	for agent := range project.KnownDirs() {
		if project.IsAutomatic(agent) {
			t.Errorf("%s is both a known Project agent dir and Automatically available at Project Scope", agent)
		}
	}

	global := ForSkillsDir("")
	for agent := range global.KnownDirs() {
		if global.IsAutomatic(agent) {
			t.Errorf("%s is both a known Global agent dir and Automatically available at Global Scope", agent)
		}
	}
}

func TestKnownDirsExpandsPlainTemplatesUnderHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	got := ForSkillsDir("").KnownDirs()
	want := filepath.Join(home, ".adal", "skills")
	if got["adal"] != want {
		t.Errorf(`KnownDirs()["adal"] = %q; want %q`, got["adal"], want)
	}
}

// cline and antigravity-cli were previously misclassified as Automatically
// available at Global Scope; they need a real linkable dir there instead
// (docs.cline.bot; antigravity-cli's own dir confirmed live via its skills
// panel — antigravity.google/docs/cli/plugins#sharing-global-skills).
// github-copilot is not here: confirmed live via `copilot skill list` to read
// ~/.agents/skills at Global Scope too, so it stays universal in both.
func TestKnownDirsIncludesReclassifiedGlobalAgents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	got := ForSkillsDir("").KnownDirs()
	for agent, want := range map[string]string{
		"cline":           filepath.Join(home, ".cline", "skills"),
		"antigravity-cli": filepath.Join(home, ".gemini", "antigravity-cli", "skills"),
		"firebender":      filepath.Join(home, ".firebender", "skills"),
	} {
		if got[agent] != want {
			t.Errorf("KnownDirs()[%q] = %q; want %q", agent, got[agent], want)
		}
	}
	if _, ok := got["github-copilot"]; ok {
		t.Errorf("github-copilot should not be a Global known dir; it is Automatically available")
	}
	if _, ok := got["grok"]; ok {
		t.Errorf("grok should not be a Global known dir; it is Automatically available")
	}
	if _, ok := got["muse-code"]; ok {
		t.Errorf("muse-code should not be a Global known dir; it is Automatically available")
	}
}

func TestProjectKnownDirsOmitCursorButIncludeFirebender(t *testing.T) {
	projectRoot := filepath.FromSlash("/path/to/my-project")
	agents := ForSkillsDir(filepath.Join(projectRoot, ".agents", "skills")).KnownDirs()

	if _, ok := agents["cursor"]; ok {
		t.Errorf("cursor should not be a Project linkable dir; it reads .agents/skills directly in both Scopes")
	}
	if _, ok := agents["muse-code"]; ok {
		t.Errorf("muse-code should not be a Project linkable dir; it reads .agents/skills directly")
	}
	wantFirebender := filepath.Join(projectRoot, ".firebender", "skills")
	if agents["firebender"] != wantFirebender {
		t.Errorf(`KnownDirs()["firebender"] = %q; want %q`, agents["firebender"], wantFirebender)
	}
	wantGrok := filepath.Join(projectRoot, ".grok", "skills")
	if agents["grok"] != wantGrok {
		t.Errorf(`KnownDirs()["grok"] = %q; want %q`, agents["grok"], wantGrok)
	}
}

func TestKnownDirsHonorPerAgentEnvOverrides(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	claudeDir := filepath.Join(home, "custom-claude")
	t.Setenv("CLAUDE_CONFIG_DIR", claudeDir)

	got := ForSkillsDir("").KnownDirs()
	want := filepath.Join(claudeDir, "skills")
	if got["claude-code"] != want {
		t.Errorf(`KnownDirs()["claude-code"] = %q; want %q`, got["claude-code"], want)
	}
}

func TestLeftoverRootsHonorGrokAndMuseRoots(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	xdg := filepath.Join(home, "xdg-config")
	t.Setenv("XDG_CONFIG_HOME", xdg)
	grokHome := filepath.Join(home, "custom-grok")
	t.Setenv("GROK_HOME", grokHome)

	got := ForSkillsDir("").LeftoverRoots()
	if got["grok"] != filepath.Join(grokHome, "skills") {
		t.Errorf(`LeftoverRoots()["grok"] = %q; want under GROK_HOME`, got["grok"])
	}
	if got["muse-code"] != filepath.Join(xdg, "muse", "skills") {
		t.Errorf(`LeftoverRoots()["muse-code"] = %q; want under XDG_CONFIG_HOME/muse`, got["muse-code"])
	}
}

func TestForSkillsDirLeftoverRootsOmitKnownDirs(t *testing.T) {
	global := ForSkillsDir("").LeftoverRoots()
	if _, ok := global["grok"]; !ok {
		t.Fatal("Global Grok leftover root missing")
	}
	if _, ok := global["claude-code"]; ok {
		t.Fatal("linkable Global Agent must not be a leftover root")
	}

	projectSkillsDir := filepath.Join(t.TempDir(), ".agents", "skills")
	project := ForSkillsDir(projectSkillsDir)
	if _, ok := project.KnownDirs()["grok"]; !ok {
		t.Fatal("Project Grok is a known dir")
	}
	if _, ok := project.LeftoverRoots()["grok"]; ok {
		t.Fatal("Project Grok known dir must not also be a leftover root")
	}
	if _, ok := project.LeftoverRoots()["codex"]; !ok {
		t.Fatal("Project Codex leftover root missing")
	}
}

func TestKnownDirsProbeOpenclawForks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	if got, want := ForSkillsDir("").KnownDirs()["openclaw"], filepath.Join(home, ".openclaw", "skills"); got != want {
		t.Errorf("openclaw with no fork installed = %q; want %q", got, want)
	}

	if err := os.MkdirAll(filepath.Join(home, ".clawdbot"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, want := ForSkillsDir("").KnownDirs()["openclaw"], filepath.Join(home, ".clawdbot", "skills"); got != want {
		t.Errorf("openclaw with clawdbot installed = %q; want %q", got, want)
	}
}

func TestParseRepoSource(t *testing.T) {
	tests := []struct {
		raw         string
		wantKey     string
		wantURL     string
		wantType    string
		wantBranch  string
		wantSubpath string
	}{
		{
			raw:      "akunzai/skills",
			wantKey:  "akunzai/skills",
			wantURL:  "https://github.com/akunzai/skills.git",
			wantType: "github",
		},
		{
			raw:      "github:owner/my-repo",
			wantKey:  "owner/my-repo",
			wantURL:  "https://github.com/owner/my-repo.git",
			wantType: "github",
		},
		{
			raw:         "microsoft/azure-skills/skills",
			wantKey:     "microsoft/azure-skills",
			wantURL:     "https://github.com/microsoft/azure-skills.git",
			wantType:    "github",
			wantSubpath: "skills",
		},
		{
			raw:      "gitlab:group/project",
			wantKey:  "gitlab.com/group/project",
			wantURL:  "https://gitlab.com/group/project.git",
			wantType: "gitlab",
		},
		{
			raw:         "https://github.com/owner/repo/tree/main/skills/foo",
			wantKey:     "owner/repo",
			wantURL:     "https://github.com/owner/repo.git",
			wantType:    "github",
			wantBranch:  "main",
			wantSubpath: "skills/foo",
		},
		{
			raw:         "https://gitlab.com/group/project/-/tree/v1.0/skills/bar",
			wantKey:     "gitlab.com/group/project",
			wantURL:     "https://gitlab.com/group/project.git",
			wantType:    "gitlab",
			wantBranch:  "v1.0",
			wantSubpath: "skills/bar",
		},
		{
			raw:      "git@github.com:owner/repo.git",
			wantKey:  "owner/repo",
			wantURL:  "git@github.com:owner/repo.git",
			wantType: "github",
		},
	}

	for _, tt := range tests {
		got := ParseRepoSource(tt.raw)
		if got.SourceKey != tt.wantKey {
			t.Errorf("ParseRepoSource(%q).SourceKey = %q; want %q", tt.raw, got.SourceKey, tt.wantKey)
		}
		if got.URL != tt.wantURL {
			t.Errorf("ParseRepoSource(%q).URL = %q; want %q", tt.raw, got.URL, tt.wantURL)
		}
		if got.RepoType != tt.wantType {
			t.Errorf("ParseRepoSource(%q).RepoType = %q; want %q", tt.raw, got.RepoType, tt.wantType)
		}
		if got.Branch != tt.wantBranch {
			t.Errorf("ParseRepoSource(%q).Branch = %q; want %q", tt.raw, got.Branch, tt.wantBranch)
		}
		if got.Subpath != tt.wantSubpath {
			t.Errorf("ParseRepoSource(%q).Subpath = %q; want %q", tt.raw, got.Subpath, tt.wantSubpath)
		}
	}
}

func TestDefaultCacheDir(t *testing.T) {
	cache := DefaultCacheDir()
	if cache == "" {
		t.Fatalf("expected non-empty DefaultCacheDir")
	}
}

func TestProjectRootAndKnownDirs(t *testing.T) {
	projDir := filepath.FromSlash("/path/to/my-project")
	skillsDir := filepath.Join(projDir, ".agents", "skills")

	root := GetProjectRootFromSkillsDir(skillsDir)
	if root != projDir {
		t.Errorf("GetProjectRootFromSkillsDir(%q) = %q; want %q", skillsDir, root, projDir)
	}

	agents := ForSkillsDir(skillsDir).KnownDirs()
	claudePath, ok := agents["claude-code"]
	if !ok {
		t.Fatalf("expected claude-code in project agents")
	}
	expectedClaude := filepath.Join(projDir, ".claude", "skills")
	if claudePath != expectedClaude {
		t.Errorf("claudePath = %q; want %q", claudePath, expectedClaude)
	}
}

func TestIsGlobalSkillsDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AGENTS_HOME", filepath.Join(home, ".agents"))
	globalSkills := filepath.Join(home, ".agents", "skills")

	if !IsGlobalSkillsDir("") {
		t.Error(`IsGlobalSkillsDir("") = false; want true`)
	}
	if !IsGlobalSkillsDir(globalSkills) {
		t.Errorf("IsGlobalSkillsDir(%q) = false; want true", globalSkills)
	}

	projectSkills := filepath.Join(home, "my-project", ".agents", "skills")
	if IsGlobalSkillsDir(projectSkills) {
		t.Errorf("IsGlobalSkillsDir(%q) = true; want false", projectSkills)
	}
}

func TestExpandUser(t *testing.T) {
	home := UserHomeDir()
	if got := ExpandUser("~"); got != home {
		t.Errorf("ExpandUser(~) = %q; want %q", got, home)
	}
	if got := ExpandUser("~/skills"); got != filepath.Join(home, "skills") {
		t.Errorf("ExpandUser(~/skills) = %q; want %q", got, filepath.Join(home, "skills"))
	}
	if got := ExpandUser(`~\skills`); got != filepath.Join(home, "skills") {
		t.Errorf(`ExpandUser(~\skills) = %q; want %q`, got, filepath.Join(home, "skills"))
	}
}

func TestToTildePath(t *testing.T) {
	home := UserHomeDir()
	tests := []struct {
		input    string
		expected string
	}{
		{"", ""},
		{"~", "~"},
		{"~/already/tilde", "~/already/tilde"},
		{`~\windows\slash\tilde`, "~/windows/slash/tilde"},
		{home, "~"},
		{filepath.Join(home, "code", "agent-skills"), "~/code/agent-skills"},
		{"/nonexistent/path/outside/home", "/nonexistent/path/outside/home"},
	}

	for _, tt := range tests {
		got := ToTildePath(tt.input)
		if got != tt.expected {
			t.Errorf("ToTildePath(%q) = %q; want %q", tt.input, got, tt.expected)
		}
	}
}
