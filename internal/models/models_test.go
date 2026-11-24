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
	global := (Scope{}).Agents()
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
	project := (Scope{IsProject: true, Root: t.TempDir()}).Agents()
	global := (Scope{}).Agents()

	if global.IsAutomatic("replit") {
		t.Errorf("expected replit to not be Automatically available at Global Scope")
	}
	if !project.IsAutomatic("replit") {
		t.Errorf("expected replit to be Automatically available at Project Scope")
	}

	// grok is Global-only Automatically available: Global reads
	// ~/.agents/skills; Project uses ./.grok/skills.
	if !global.IsAutomatic("grok") {
		t.Errorf("expected grok to be Automatically available at Global Scope")
	}
	if project.IsAutomatic("grok") {
		t.Errorf("expected grok to not be Automatically available at Project Scope")
	}

	for _, agent := range []string{
		"antigravity",
		"antigravity-cli",
		"augment",
		"autohand-code",
		"cline",
		"crush",
		"devin",
		"droid",
		"goose",
		"junie",
		"kilo",
		"mistral-vibe",
		"muse",
		"muse-code",
		"openclaw",
		"openhands",
		"pi",
		"pochi",
		"qwen-code",
		"roo",
		"rovodev",
		"windsurf",
	} {
		if !global.IsAutomatic(agent) || !project.IsAutomatic(agent) {
			t.Errorf("expected %s to be Automatically available in both Scopes (global=%v project=%v)",
				agent, global.IsAutomatic(agent), project.IsAutomatic(agent))
		}
	}

	// firebender and continue still read their own directories in every
	// Scope, so they must never be Automatically available.
	for _, agent := range []string{"firebender", "continue"} {
		if global.IsAutomatic(agent) || project.IsAutomatic(agent) {
			t.Errorf("expected %s to not be Automatically available in any Scope", agent)
		}
	}
}

func TestAutomaticAgentsAreScopeAwareAndSorted(t *testing.T) {
	project := (Scope{IsProject: true, Root: t.TempDir()}).Agents().Automatic()
	for _, want := range []string{"antigravity-cli", "cline", "codex", "goose", "muse-code", "pi", "replit", "roo"} {
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

	global := (Scope{}).Agents().Automatic()
	for _, want := range []string{"antigravity-cli", "cline", "codex", "goose", "grok", "muse-code", "pi", "roo", "windsurf"} {
		if !slices.Contains(global, want) {
			t.Fatalf("Global Automatically available Agents missing %q: %#v", want, global)
		}
	}
	if slices.Contains(global, "replit") {
		t.Fatalf("replit is Project-only Automatically available: %#v", global)
	}
}

// A typo'd alias target would otherwise resolve to an agent with no
// directory and no error anywhere in the call chain. Every alias must land
// on a Global agent that is known or Automatically available, never both.
func TestAgentAliasesResolveToExactlyOneKnownOrAutomaticGlobalAgent(t *testing.T) {
	global := (Scope{}).Agents()
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
	project := (Scope{IsProject: true, Root: t.TempDir()}).Agents()
	for agent := range project.KnownDirs() {
		if project.IsAutomatic(agent) {
			t.Errorf("%s is both a known Project agent dir and Automatically available at Project Scope", agent)
		}
	}

	global := (Scope{}).Agents()
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

	got := (Scope{}).Agents().KnownDirs()
	want := filepath.Join(home, ".adal", "skills")
	if got["adal"] != want {
		t.Errorf(`KnownDirs()["adal"] = %q; want %q`, got["adal"], want)
	}
}

func TestKnownDirsIncludesReclassifiedGlobalAgents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	got := (Scope{}).Agents().KnownDirs()
	for agent, want := range map[string]string{
		"continue":     filepath.Join(home, ".continue", "skills"),
		"firebender":   filepath.Join(home, ".firebender", "skills"),
		"hermes-agent": filepath.Join(home, ".hermes", "skills"),
		"kiro-cli":     filepath.Join(home, ".kiro", "skills"),
	} {
		if got[agent] != want {
			t.Errorf("KnownDirs()[%q] = %q; want %q", agent, got[agent], want)
		}
	}
	for _, automatic := range []string{
		"antigravity-cli",
		"augment",
		"autohand-code",
		"cline",
		"crush",
		"devin",
		"droid",
		"github-copilot",
		"goose",
		"grok",
		"junie",
		"kilo",
		"mistral-vibe",
		"muse-code",
		"openclaw",
		"openhands",
		"pi",
		"pochi",
		"qwen-code",
		"roo",
		"rovodev",
		"windsurf",
	} {
		if _, ok := got[automatic]; ok {
			t.Errorf("%s should not be a Global known dir; it is Automatically available", automatic)
		}
	}
}

func TestProjectKnownDirsOmitCursorButIncludeFirebender(t *testing.T) {
	projectRoot := filepath.FromSlash("/path/to/my-project")
	agents := (Scope{IsProject: true, Root: projectRoot}).Agents().KnownDirs()

	for _, automatic := range []string{"antigravity-cli", "cline", "cursor", "muse-code", "roo", "windsurf"} {
		if _, ok := agents[automatic]; ok {
			t.Errorf("%s should not be a Project linkable dir; it reads .agents/skills directly", automatic)
		}
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

	got := (Scope{}).Agents().KnownDirs()
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
	autohandHome := filepath.Join(home, "custom-autohand")
	t.Setenv("AUTOHAND_HOME", autohandHome)
	vibeHome := filepath.Join(home, "custom-vibe")
	t.Setenv("VIBE_HOME", vibeHome)

	got := (Scope{}).Agents().LeftoverRoots()
	wants := map[string]string{
		"antigravity-cli": filepath.Join(home, ".gemini", "antigravity-cli", "skills"),
		"augment":         filepath.Join(home, ".augment", "skills"),
		"autohand-code":   filepath.Join(autohandHome, "skills"),
		"cline":           filepath.Join(home, ".cline", "skills"),
		"crush":           filepath.Join(home, ".config", "crush", "skills"),
		"devin":           filepath.Join(xdg, "devin", "skills"),
		"droid":           filepath.Join(home, ".factory", "skills"),
		"goose":           filepath.Join(xdg, "goose", "skills"),
		"grok":            filepath.Join(grokHome, "skills"),
		"junie":           filepath.Join(home, ".junie", "skills"),
		"kilo":            filepath.Join(home, ".kilocode", "skills"),
		"mistral-vibe":    filepath.Join(vibeHome, "skills"),
		"muse-code":       filepath.Join(xdg, "muse", "skills"),
		"openhands":       filepath.Join(home, ".openhands", "skills"),
		"pi":              filepath.Join(home, ".pi", "agent", "skills"),
		"pochi":           filepath.Join(home, ".pochi", "skills"),
		"qwen-code":       filepath.Join(home, ".qwen", "skills"),
		"roo":             filepath.Join(home, ".roo", "skills"),
		"rovodev":         filepath.Join(home, ".rovodev", "skills"),
		"windsurf":        filepath.Join(home, ".codeium", "windsurf", "skills"),
	}
	for agent, want := range wants {
		if got[agent] != want {
			t.Errorf(`LeftoverRoots()[%q] = %q; want %q`, agent, got[agent], want)
		}
	}
}

func TestScopeAgentsLeftoverRootsOmitKnownDirs(t *testing.T) {
	global := (Scope{}).Agents().LeftoverRoots()
	if _, ok := global["grok"]; !ok {
		t.Fatal("Global Grok leftover root missing")
	}
	if _, ok := global["claude-code"]; ok {
		t.Fatal("linkable Global Agent must not be a leftover root")
	}

	projectRoot := t.TempDir()
	project := (Scope{IsProject: true, Root: projectRoot}).Agents()
	if _, ok := project.KnownDirs()["grok"]; !ok {
		t.Fatal("Project Grok is a known dir")
	}
	if _, ok := project.LeftoverRoots()["grok"]; ok {
		t.Fatal("Project Grok known dir must not also be a leftover root")
	}
	projectLeftover := project.LeftoverRoots()
	for agent, want := range map[string]string{
		"cline":    filepath.Join(projectRoot, ".cline", "skills"),
		"codex":    filepath.Join(projectRoot, ".codex", "skills"),
		"roo":      filepath.Join(projectRoot, ".roo", "skills"),
		"windsurf": filepath.Join(projectRoot, ".codeium", "windsurf", "skills"),
	} {
		if projectLeftover[agent] != want {
			t.Errorf("Project LeftoverRoots()[%q] = %q; want %q", agent, projectLeftover[agent], want)
		}
	}
}

func TestLeftoverRootsProbeOpenclawForks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	if got, want := (Scope{}).Agents().LeftoverRoots()["openclaw"], filepath.Join(home, ".openclaw", "skills"); got != want {
		t.Errorf("openclaw with no fork installed = %q; want %q", got, want)
	}

	if err := os.MkdirAll(filepath.Join(home, ".clawdbot"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, want := (Scope{}).Agents().LeftoverRoots()["openclaw"], filepath.Join(home, ".clawdbot", "skills"); got != want {
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

func TestParseGistSource(t *testing.T) {
	const id = "f55ddefadc6aa709b1e51b9d9c4f08ef"
	tests := []struct {
		raw     string
		wantID  string
		wantURL string // "" is the HTTPS clone URL the id implies
	}{
		{"https://gist.github.com/someone/" + id, id, ""},
		{"https://gist.github.com/" + id, id, ""},
		{"https://gist.github.com/" + id + ".git", id, ""},
		{"https://Gist.GitHub.com/someone/" + id + "/#file-skill-md", id, ""},
		{"gist:" + id, id, ""},
		{"GIST:" + id, id, ""},
		{"git@gist.github.com:" + id + ".git", id, "git@gist.github.com:" + id + ".git"},
		{"https://gist.github.com/1234567", "1234567", ""},
	}
	for _, tt := range tests {
		got, ok, err := ParseGistSource(tt.raw)
		if !ok || err != nil {
			t.Errorf("ParseGistSource(%q) = ok %v, err %v; want a Gist", tt.raw, ok, err)
			continue
		}
		wantURL := tt.wantURL
		if wantURL == "" {
			wantURL = "https://gist.github.com/" + tt.wantID + ".git"
		}
		want := ParsedRepoSource{SourceKey: "gist.github.com/" + tt.wantID, URL: wantURL, RepoType: "git"}
		if got != want {
			t.Errorf("ParseGistSource(%q) = %+v; want %+v", tt.raw, got, want)
		}
		if parsed := ParseRepoSource(tt.raw); parsed != want {
			t.Errorf("ParseRepoSource(%q) = %+v; want %+v", tt.raw, parsed, want)
		}
	}

	// The key a Gist is declared under implies its HTTPS clone URL, so
	// Config need not record one.
	if implied := ParseRepoSource("gist.github.com/" + id); implied.URL != "https://gist.github.com/"+id+".git" {
		t.Errorf("ParseRepoSource(key).URL = %q", implied.URL)
	}

	for _, raw := range []string{
		"https://gist.github.com/someone/" + id + "/0123abcd",
		"https://gist.github.com/someone/" + id + "/raw/SKILL.md",
		"https://gist.github.com/someone",
		"https://gist.github.com/",
		"gist:",
		"gist:someone/" + id,
	} {
		if _, ok, err := ParseGistSource(raw); !ok || err == nil {
			t.Errorf("ParseGistSource(%q) = ok %v, err %v; want a refused Gist", raw, ok, err)
		}
	}

	for _, raw := range []string{id, "owner/repo", "https://github.com/owner/repo", "gitlab:g/p"} {
		if _, ok, _ := ParseGistSource(raw); ok {
			t.Errorf("ParseGistSource(%q) took it for a Gist", raw)
		}
	}
}

func TestDefaultCacheDir(t *testing.T) {
	cache := DefaultCacheDir()
	if cache == "" {
		t.Fatalf("expected non-empty DefaultCacheDir")
	}
}

func TestProjectScopeKnownDirsAreUnderItsRoot(t *testing.T) {
	projDir := filepath.FromSlash("/path/to/my-project")

	agents := (Scope{IsProject: true, Root: projDir}).Agents().KnownDirs()
	claudePath, ok := agents["claude-code"]
	if !ok {
		t.Fatalf("expected claude-code in project agents")
	}
	expectedClaude := filepath.Join(projDir, ".claude", "skills")
	if claudePath != expectedClaude {
		t.Errorf("claudePath = %q; want %q", claudePath, expectedClaude)
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
