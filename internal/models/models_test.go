package models

import (
	"path/filepath"
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

func TestIsUniversalAgent(t *testing.T) {
	if !IsUniversalAgent("gemini") {
		t.Errorf("expected gemini to be universal")
	}
	if !IsUniversalAgent("antigravity-cli") {
		t.Errorf("expected antigravity-cli to be universal")
	}
	if !IsUniversalAgent("cursor") {
		t.Errorf("expected cursor to be universal")
	}
	if IsUniversalAgent("claude-code") {
		t.Errorf("expected claude-code to not be universal")
	}
}

func TestParseRepoSource(t *testing.T) {
	tests := []struct {
		raw      string
		wantKey  string
		wantURL  string
		wantType string
		wantBranch string
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
			raw:      "gitlab:group/project",
			wantKey:  "gitlab.com/group/project",
			wantURL:  "https://gitlab.com/group/project.git",
			wantType: "gitlab",
		},
		{
			raw:      "https://github.com/owner/repo/tree/main/skills/foo",
			wantKey:  "owner/repo",
			wantURL:  "https://github.com/owner/repo.git",
			wantType: "github",
			wantBranch: "main",
			wantSubpath: "skills/foo",
		},
		{
			raw:      "https://gitlab.com/group/project/-/tree/v1.0/skills/bar",
			wantKey:  "gitlab.com/group/project",
			wantURL:  "https://gitlab.com/group/project.git",
			wantType: "gitlab",
			wantBranch: "v1.0",
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

func TestGetProjectRootAndAgents(t *testing.T) {
	projDir := filepath.FromSlash("/path/to/my-project")
	skillsDir := filepath.Join(projDir, ".agents", "skills")

	root := GetProjectRootFromSkillsDir(skillsDir)
	if root != projDir {
		t.Errorf("GetProjectRootFromSkillsDir(%q) = %q; want %q", skillsDir, root, projDir)
	}

	agents := GetAgentsForSkillsDir(skillsDir)
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



