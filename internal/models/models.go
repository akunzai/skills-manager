package models

import (
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
)

// Default paths resolved from environment or user home.
func UserHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return home
}

func ResolveEnvPath(envVar, defaultSubpath string) string {
	val := strings.TrimSpace(os.Getenv(envVar))
	if val != "" {
		return ExpandUser(val)
	}
	return ExpandUser(defaultSubpath)
}

func ExpandUser(path string) string {
	if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) || path == "~" {
		home := UserHomeDir()
		if path == "~" {
			return home
		}
		return filepath.Join(home, path[2:])
	}
	return path
}

func hasPrefixPath(path, prefix string) bool {
	if strings.HasPrefix(path, prefix) {
		return true
	}
	if runtime.GOOS == "windows" && len(path) >= len(prefix) && strings.EqualFold(path[:len(prefix)], prefix) {
		return true
	}
	return false
}

func equalPath(p1, p2 string) bool {
	if p1 == p2 {
		return true
	}
	if runtime.GOOS == "windows" && strings.EqualFold(p1, p2) {
		return true
	}
	return false
}

func toSlash(p string) string {
	return strings.ReplaceAll(filepath.ToSlash(p), "\\", "/")
}

// ToTildePath replaces the user's home directory prefix with ~ if applicable.
func ToTildePath(path string) string {
	if path == "" {
		return ""
	}
	if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) || path == "~" {
		return "~" + toSlash(path[1:])
	}
	home := UserHomeDir()
	if home == "" || home == "." {
		return path
	}
	cleanPath := filepath.Clean(path)
	cleanHome := filepath.Clean(home)

	if equalPath(cleanPath, cleanHome) {
		return "~"
	}
	sep := string(filepath.Separator)
	if hasPrefixPath(cleanPath, cleanHome+sep) {
		rel := cleanPath[len(cleanHome):]
		return "~" + toSlash(rel)
	}
	if evalHome, err := filepath.EvalSymlinks(cleanHome); err == nil && evalHome != cleanHome {
		if equalPath(cleanPath, evalHome) {
			return "~"
		}
		if hasPrefixPath(cleanPath, evalHome+sep) {
			rel := cleanPath[len(evalHome):]
			return "~" + toSlash(rel)
		}
	}
	if evalPath, err := filepath.EvalSymlinks(cleanPath); err == nil && evalPath != cleanPath {
		if equalPath(evalPath, cleanHome) {
			return "~"
		}
		if hasPrefixPath(evalPath, cleanHome+sep) {
			rel := evalPath[len(cleanHome):]
			return "~" + toSlash(rel)
		}
	}
	return path
}

func DefaultAgentsDir() string {
	return ResolveEnvPath("AGENTS_HOME", "~/.agents")
}

func DefaultSkillsDir() string {
	return filepath.Join(DefaultAgentsDir(), "skills")
}

func DefaultConfigFile() string {
	return filepath.Join(DefaultAgentsDir(), "skills.json")
}

func DefaultCacheDir() string {
	if custom := os.Getenv("SKILLS_CACHE_DIR"); custom != "" {
		return ExpandUser(custom)
	}
	stateHome := ResolveEnvPath("XDG_STATE_HOME", "~/.local/state")
	return filepath.Join(stateHome, "skills-manager", "repo-cache")
}

// GetProjectRootFromSkillsDir derives the project root directory from a project-scoped skillsDir.
func GetProjectRootFromSkillsDir(skillsDir string) string {
	clean := filepath.Clean(skillsDir)
	parent := filepath.Dir(clean)
	if filepath.Base(clean) == "skills" && filepath.Base(parent) == ".agents" {
		return filepath.Dir(parent)
	}
	if filepath.Base(clean) == "skills" {
		return parent
	}
	return parent
}

// GetProjectKnownAgents returns mapping of agent names to their project-level
// skills directory. Cursor is not here: it reads project .agents/skills
// directly, same as at Global Scope (cursor.com/docs/skills#skill-directories).
func GetProjectKnownAgents(projectRoot string) map[string]string {
	return map[string]string{
		"claude-code": filepath.Join(projectRoot, ".claude", "skills"),
		"continue":    filepath.Join(projectRoot, ".continue", "skills"),
		"cline":       filepath.Join(projectRoot, ".cline", "skills"),
		"firebender":  filepath.Join(projectRoot, ".firebender", "skills"),
		"roo":         filepath.Join(projectRoot, ".roo", "skills"),
		"windsurf":    filepath.Join(projectRoot, ".codeium", "windsurf", "skills"),
	}
}

// IsGlobalSkillsDir reports whether skillsDir is the global skills directory.
func IsGlobalSkillsDir(skillsDir string) bool {
	if skillsDir == "" {
		return true
	}
	absSkills, _ := filepath.Abs(skillsDir)
	absGlobal, _ := filepath.Abs(DefaultSkillsDir())
	return filepath.Clean(absSkills) == filepath.Clean(absGlobal)
}

// GetAgentsForSkillsDir returns the appropriate agent mapping (global or project-scoped).
func GetAgentsForSkillsDir(skillsDir string) map[string]string {
	if skillsDir == "" {
		skillsDir = DefaultSkillsDir()
	}
	if IsGlobalSkillsDir(skillsDir) {
		return GetKnownAgents()
	}
	projectRoot := GetProjectRootFromSkillsDir(skillsDir)
	return GetProjectKnownAgents(projectRoot)
}

// GetUniversalAgentSkillDirs returns skills directories that Automatically
// available agents may have had materialized for them. skills-manager never
// creates these — those agents read the master skills directory directly in
// every Scope they hold that status — but earlier versions and external setup
// scripts did, and links left behind there still have to be cleaned up when a
// skill goes away. An agent that is Automatically available in only one Scope
// (see universalAgentScopes) is a real, actively-managed known dir in the
// other, so it belongs in GetKnownAgents/GetProjectKnownAgents instead.
//
// These paths are conventions, not guarantees. Callers must only ever act on a
// symlink that resolves into the master skills directory, so a path that turns
// out to be wrong simply matches nothing.
func GetUniversalAgentSkillDirs(skillsDir string) map[string]string {
	if skillsDir == "" {
		skillsDir = DefaultSkillsDir()
	}
	if !IsGlobalSkillsDir(skillsDir) {
		root := GetProjectRootFromSkillsDir(skillsDir)
		return map[string]string{
			"codex": filepath.Join(root, ".codex", "skills"),
		}
	}
	xdgConfig := ResolveEnvPath("XDG_CONFIG_HOME", "~/.config")
	return map[string]string{
		"amp":            ExpandUser("~/.amp/skills"),
		"codex":          ExpandUser("~/.codex/skills"),
		"cursor":         ExpandUser("~/.cursor/skills"),
		"gemini-cli":     ExpandUser("~/.gemini/skills"),
		"github-copilot": ExpandUser("~/.copilot/skills"),
		"opencode":       filepath.Join(xdgConfig, "opencode", "skills"),
		"zed":            filepath.Join(xdgConfig, "zed", "skills"),
	}
}

// GetAutomaticallyAvailableAgents returns the Agents that read the central
// skills directory directly in the Scope that skillsDir belongs to.
func GetAutomaticallyAvailableAgents(skillsDir string) []string {
	want := scopeProject
	if IsGlobalSkillsDir(skillsDir) {
		want = scopeGlobal
	}
	agents := make([]string, 0, len(universalAgentScopes))
	for agent, scopes := range universalAgentScopes {
		if agent != "universal" && scopes&want != 0 {
			agents = append(agents, agent)
		}
	}
	slices.Sort(agents)
	return agents
}

// knownAgentSkillDirTemplates lists agents whose global skills directory is a
// fixed path under the user's home directory: pure data, expanded with
// ExpandUser and nothing else. Agents that need a per-agent environment
// override, an XDG_CONFIG_HOME-relative path, or filesystem probing carry
// that logic explicitly in GetKnownAgents instead of hiding it in this table.
var knownAgentSkillDirTemplates = map[string]string{
	"adal":            "~/.adal/skills",
	"aider-desk":      "~/.aider-desk/skills",
	"antigravity-cli": "~/.gemini/antigravity-cli/skills",
	"astrbot":         "~/.astrbot/data/skills",
	"augment":         "~/.augment/skills",
	"bob":             "~/.bob/skills",
	"cline":           "~/.cline/skills",
	"codearts-agent":  "~/.codeartsdoer/skills",
	"codebuddy":       "~/.codebuddy/skills",
	"codemaker":       "~/.codemaker/skills",
	"codestudio":      "~/.codestudio/skills",
	"command-code":    "~/.commandcode/skills",
	"continue":        "~/.continue/skills",
	"cortex":          "~/.snowflake/cortex/skills",
	"crush":           "~/.config/crush/skills",
	"droid":           "~/.factory/skills",
	"firebender":      "~/.firebender/skills",
	"forgecode":       "~/.forge/skills",
	"iflow-cli":       "~/.iflow/skills",
	"inference-sh":    "~/.inferencesh/skills",
	"jazz":            "~/.jazz/skills",
	"junie":           "~/.junie/skills",
	"kilo":            "~/.kilocode/skills",
	"kimchi":          "~/.config/kimchi/harness/skills",
	"kiro-cli":        "~/.kiro/skills",
	"kode":            "~/.kode/skills",
	"lingma":          "~/.lingma/skills",
	"mcpjam":          "~/.mcpjam/skills",
	"minimax-code":    "~/.minimax/skills",
	"moxby":           "~/.moxby/skills",
	"mux":             "~/.mux/skills",
	"neovate":         "~/.neovate/skills",
	"ona":             "~/.ona/skills",
	"openhands":       "~/.openhands/skills",
	"pi":              "~/.pi/agent/skills",
	"pochi":           "~/.pochi/skills",
	"posit-assistant": "~/.posit/assistant/skills",
	"qoder":           "~/.qoder/skills",
	"qoder-cn":        "~/.qoder-cn/skills",
	"qwen-code":       "~/.qwen/skills",
	"reasonix":        "~/.reasonix/skills",
	"roo":             "~/.roo/skills",
	"rovodev":         "~/.rovodev/skills",
	"tabnine-cli":     "~/.tabnine/agent/skills",
	"terramind":       "~/.terramind/skills",
	"tinycloud":       "~/.tinycloud/skills",
	"trae":            "~/.trae/skills",
	"trae-cn":         "~/.trae-cn/skills",
	"windsurf":        "~/.codeium/windsurf/skills",
	"zcode":           "~/.zcode/skills",
	"zencoder":        "~/.zencoder/skills",
}

// GetKnownAgents returns mapping of non-universal agent names to their global skills directory.
func GetKnownAgents() map[string]string {
	xdgConfig := ResolveEnvPath("XDG_CONFIG_HOME", "~/.config")
	claudeHome := ResolveEnvPath("CLAUDE_CONFIG_DIR", "~/.claude")
	vibeHome := ResolveEnvPath("VIBE_HOME", "~/.vibe")
	hermesHome := ResolveEnvPath("HERMES_HOME", "~/.hermes")
	autohandHome := ResolveEnvPath("AUTOHAND_HOME", "~/.autohand")
	grokHome := ResolveEnvPath("GROK_HOME", "~/.grok")

	openclawDir := ExpandUser("~/.openclaw/skills")
	if _, err := os.Stat(ExpandUser("~/.clawdbot")); err == nil {
		openclawDir = ExpandUser("~/.clawdbot/skills")
	} else if _, err := os.Stat(ExpandUser("~/.moltbot")); err == nil {
		openclawDir = ExpandUser("~/.moltbot/skills")
	}

	known := make(map[string]string, len(knownAgentSkillDirTemplates)+8)
	for agent, template := range knownAgentSkillDirTemplates {
		known[agent] = ExpandUser(template)
	}

	known["autohand-code"] = filepath.Join(autohandHome, "skills")
	known["claude-code"] = filepath.Join(claudeHome, "skills")
	known["devin"] = filepath.Join(xdgConfig, "devin", "skills")
	known["goose"] = filepath.Join(xdgConfig, "goose", "skills")
	known["grok"] = filepath.Join(grokHome, "skills")
	known["hermes-agent"] = filepath.Join(hermesHome, "skills")
	known["mistral-vibe"] = filepath.Join(vibeHome, "skills")
	known["openclaw"] = openclawDir

	return known
}

// agentScope is which Scope(s) an Automatically available Agent holds that
// status in. An Agent absent from universalAgentScopes needs a linkable
// directory (GetKnownAgents / GetProjectKnownAgents) in every Scope instead.
type agentScope int

const (
	scopeGlobal agentScope = 1 << iota
	scopeProject
)

const scopeBoth = scopeGlobal | scopeProject

// universalAgentScopes lists, per Agent, the Scope(s) confirmed (against each
// tool's own documentation) to read the master skills directory directly:
// Global ~/.agents/skills, Project .agents/skills. An Agent that reads its own
// dedicated directory instead — in either Scope — is not listed for that
// Scope, even if some other tool with a similar name is.
//
// A handful of entries are Scope-split because the same Agent's Global and
// Project behavior differ:
//   - antigravity-cli: Project reads .agents/skills; Global reads its own
//     ~/.gemini/antigravity-cli/skills instead — a different tool from the
//     Antigravity IDE, which reads ~/.gemini/skills
//     (antigravity.google/docs/cli/plugins#sharing-global-skills).
//   - replit: confirmed only for Project (docs.replit.com); Replit Agent runs
//     in the cloud with no local Global skills directory to read.
var universalAgentScopes = map[string]agentScope{
	"amp":             scopeBoth,
	"antigravity-cli": scopeProject,
	"codex":           scopeBoth,
	"cursor":          scopeBoth,
	"gemini-cli":      scopeBoth,
	"github-copilot":  scopeBoth,
	"kimi-code-cli":   scopeBoth,
	"opencode":        scopeBoth,
	"replit":          scopeProject,
	"warp":            scopeBoth,
	"zed":             scopeBoth,
	"universal":       scopeBoth,
}

var AgentAliases = map[string]string{
	// Non-universal aliases
	"claude":    "claude-code",
	"roo-code":  "roo",
	"vibe":      "mistral-vibe",
	"hermes":    "hermes-agent",
	"autohand":  "autohand-code",
	"aider":     "aider-desk",
	"codearts":  "codearts-agent",
	"iflow":     "iflow-cli",
	"kiro":      "kiro-cli",
	"kilocode":  "kilo",
	"minimax":   "minimax-code",
	"posit":     "posit-assistant",
	"positai":   "posit-assistant",
	"tabnine":   "tabnine-cli",
	"factory":   "droid",
	"forge":     "forgecode",
	"clawdbot":  "openclaw",
	"moltbot":   "openclaw",
	"opendevin": "openhands",
	"qwen":      "qwen-code",
	"zenflow":   "zencoder",
	// Universal aliases
	"gemini":      "gemini-cli",
	"antigravity": "antigravity-cli",
	"kimi":        "kimi-code-cli",
	"kimi-code":   "kimi-code-cli",
	"copilot":     "github-copilot",
}

func NormalizeAgentName(name string) string {
	low := strings.ToLower(strings.TrimSpace(name))
	if canonical, ok := AgentAliases[low]; ok {
		return canonical
	}
	return low
}

// IsUniversalAgent reports whether name is Automatically available in the
// Scope skillsDir belongs to, so it needs no Availability link there.
func IsUniversalAgent(name, skillsDir string) bool {
	norm := NormalizeAgentName(name)
	scopes, ok := universalAgentScopes[norm]
	if !ok {
		return false
	}
	if IsGlobalSkillsDir(skillsDir) {
		return scopes&scopeGlobal != 0
	}
	return scopes&scopeProject != 0
}

type ParsedRepoSource struct {
	SourceKey string `json:"sourceKey"`
	URL       string `json:"url"`
	RepoType  string `json:"repoType"` // "github", "gitlab", "git"
	Branch    string `json:"branch,omitempty"`
	Subpath   string `json:"subpath,omitempty"`
}

var sshURLRegex = regexp.MustCompile(`^git@([^:]+):(.+?)(?:\.git)?$`)

func ParseRepoSource(raw string) ParsedRepoSource {
	raw = strings.TrimSpace(raw)

	// 1. Prefix: gitlab:group/project
	if strings.HasPrefix(strings.ToLower(raw), "gitlab:") {
		repoPath := strings.Trim(strings.TrimPrefix(raw, raw[:7]), "/")
		return ParsedRepoSource{
			SourceKey: "gitlab.com/" + repoPath,
			URL:       "https://gitlab.com/" + repoPath + ".git",
			RepoType:  "gitlab",
		}
	}

	// Prefix: github:owner/repo
	if strings.HasPrefix(strings.ToLower(raw), "github:") {
		repoPath := strings.Trim(strings.TrimPrefix(raw, raw[:7]), "/")
		return ParsedRepoSource{
			SourceKey: repoPath,
			URL:       "https://github.com/" + repoPath + ".git",
			RepoType:  "github",
		}
	}

	// 2. SSH URLs: git@host:group/project.git or ssh://git@host/...
	if strings.HasPrefix(raw, "git@") || strings.HasPrefix(raw, "ssh://") {
		if strings.HasPrefix(raw, "git@") {
			match := sshURLRegex.FindStringSubmatch(raw)
			if match != nil {
				host := match[1]
				path := strings.Trim(match[2], "/")
				isGitHub := strings.EqualFold(host, "github.com") || strings.EqualFold(host, "github")
				repoType := "git"
				if isGitHub {
					repoType = "github"
				} else if strings.Contains(strings.ToLower(host), "gitlab") {
					repoType = "gitlab"
				}
				sourceKey := host + "/" + path
				if repoType == "github" {
					sourceKey = path
				}
				return ParsedRepoSource{
					SourceKey: sourceKey,
					URL:       raw,
					RepoType:  repoType,
				}
			}
		} else if strings.HasPrefix(raw, "ssh://") {
			u, err := url.Parse(raw)
			if err == nil {
				host := u.Hostname()
				if host == "" {
					host = "git"
				}
				path := strings.TrimSuffix(strings.TrimPrefix(u.Path, "/"), ".git")
				isGitHub := strings.Contains(strings.ToLower(host), "github")
				repoType := "git"
				if isGitHub {
					repoType = "github"
				} else if strings.Contains(strings.ToLower(host), "gitlab") {
					repoType = "gitlab"
				}
				sourceKey := host + "/" + path
				if repoType == "github" {
					sourceKey = path
				}
				return ParsedRepoSource{
					SourceKey: sourceKey,
					URL:       raw,
					RepoType:  repoType,
				}
			}
		}
	}

	// 3. HTTP / HTTPS URLs
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		u, err := url.Parse(raw)
		if err == nil {
			host := strings.ToLower(u.Host)
			path := strings.Trim(u.Path, "/")
			repoType := "git"
			if strings.Contains(host, "github") {
				repoType = "github"
			} else if strings.Contains(host, "gitlab") {
				repoType = "gitlab"
			}

			if strings.Contains(host, "github.com") {
				parts := strings.Split(path, "/")
				if len(parts) >= 4 && (parts[2] == "tree" || parts[2] == "blob") {
					owner := parts[0]
					repo := parts[1]
					branch := parts[3]
					var subpath string
					if len(parts) > 4 {
						subpath = strings.Join(parts[4:], "/")
					}
					return ParsedRepoSource{
						SourceKey: owner + "/" + repo,
						URL:       "https://" + host + "/" + owner + "/" + repo + ".git",
						RepoType:  repoType,
						Branch:    branch,
						Subpath:   subpath,
					}
				}
				cleanPath := strings.TrimSuffix(path, ".git")
				return ParsedRepoSource{
					SourceKey: cleanPath,
					URL:       "https://" + host + "/" + cleanPath + ".git",
					RepoType:  repoType,
				}
			} else if strings.Contains(host, "gitlab") {
				if strings.Contains(path, "/-/tree/") || strings.Contains(path, "/-/blob/") {
					sep := "/-/tree/"
					if strings.Contains(path, "/-/blob/") {
						sep = "/-/blob/"
					}
					parts := strings.SplitN(path, sep, 2)
					basePart := strings.TrimSuffix(parts[0], ".git")
					restParts := strings.Split(parts[1], "/")
					branch := restParts[0]
					var subpath string
					if len(restParts) > 1 {
						subpath = strings.Join(restParts[1:], "/")
					}
					return ParsedRepoSource{
						SourceKey: host + "/" + basePart,
						URL:       "https://" + host + "/" + basePart + ".git",
						RepoType:  repoType,
						Branch:    branch,
						Subpath:   subpath,
					}
				} else if strings.Contains(path, "/tree/") || strings.Contains(path, "/blob/") {
					sep := "/tree/"
					if strings.Contains(path, "/blob/") {
						sep = "/blob/"
					}
					parts := strings.SplitN(path, sep, 2)
					basePart := strings.TrimSuffix(parts[0], ".git")
					restParts := strings.Split(parts[1], "/")
					branch := restParts[0]
					var subpath string
					if len(restParts) > 1 {
						subpath = strings.Join(restParts[1:], "/")
					}
					return ParsedRepoSource{
						SourceKey: host + "/" + basePart,
						URL:       "https://" + host + "/" + basePart + ".git",
						RepoType:  repoType,
						Branch:    branch,
						Subpath:   subpath,
					}
				}
				cleanPath := strings.TrimSuffix(path, ".git")
				return ParsedRepoSource{
					SourceKey: host + "/" + cleanPath,
					URL:       "https://" + host + "/" + cleanPath + ".git",
					RepoType:  repoType,
				}
			} else {
				cleanPath := strings.TrimSuffix(path, ".git")
				protocol := "https"
				if strings.HasPrefix(raw, "http://") {
					protocol = "http"
				}
				return ParsedRepoSource{
					SourceKey: host + "/" + cleanPath,
					URL:       protocol + "://" + host + "/" + cleanPath + ".git",
					RepoType:  repoType,
				}
			}
		}
	}

	// 4. Plain shorthand: e.g. "owner/repo" or "gitlab.com/group/project"
	cleanRaw := strings.Trim(strings.TrimSuffix(raw, ".git"), "/")
	if strings.HasPrefix(cleanRaw, "gitlab.com/") {
		return ParsedRepoSource{
			SourceKey: cleanRaw,
			URL:       "https://" + cleanRaw + ".git",
			RepoType:  "gitlab",
		}
	} else if strings.Contains(cleanRaw, "/") && !strings.HasPrefix(cleanRaw, "github.com/") {
		parts := strings.Split(cleanRaw, "/")
		if strings.Contains(parts[0], ".") {
			host := parts[0]
			repoType := "git"
			if strings.Contains(strings.ToLower(host), "gitlab") {
				repoType = "gitlab"
			}
			return ParsedRepoSource{
				SourceKey: cleanRaw,
				URL:       "https://" + cleanRaw + ".git",
				RepoType:  repoType,
			}
		}
		return ParsedRepoSource{
			SourceKey: cleanRaw,
			URL:       "https://github.com/" + cleanRaw + ".git",
			RepoType:  "github",
		}
	}

	cleanKey := strings.TrimPrefix(cleanRaw, "github.com/")
	return ParsedRepoSource{
		SourceKey: cleanKey,
		URL:       "https://github.com/" + cleanKey + ".git",
		RepoType:  "github",
	}
}

type SkillItem struct {
	Name          string   `json:"name"`
	SourceType    string   `json:"sourceType"` // "github", "gitlab", "git", "local_symlink", "local_command", "symlink", "untracked"
	Source        string   `json:"source"`
	Subpath       string   `json:"subpath,omitempty"`
	InstalledPath string   `json:"path,omitempty"`
	IsInstalled   bool     `json:"installed"`
	IsValidSkill  bool     `json:"valid"`
	Agents        []string `json:"agents"`
	Description   string   `json:"description,omitempty"`
	Scope         string   `json:"scope,omitempty"`
}
