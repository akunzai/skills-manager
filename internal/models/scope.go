package models

import (
	"os"
	"path/filepath"
	"strings"
)

// Scope represents the active Global or Project configuration, its skills
// directory, Cache directory, Scope root boundary, and its Agent directory registry.
type Scope struct {
	ConfigPath string
	SkillsDir  string
	CacheDir   string
	IsProject  bool
}

// GetProjectPaths resolves the config path and skills directory for a project workspace.
func GetProjectPaths(cwd string) (configPath, skillsDir string) {
	agentsConfigFile := filepath.Join(cwd, ".agents", "skills.json")
	rootConfigFile := filepath.Join(cwd, "skills.json")
	if _, err := os.Stat(agentsConfigFile); err == nil {
		return agentsConfigFile, filepath.Join(cwd, ".agents", "skills")
	}
	if _, err := os.Stat(rootConfigFile); err == nil {
		return rootConfigFile, filepath.Join(cwd, "skills")
	}
	return agentsConfigFile, filepath.Join(cwd, ".agents", "skills")
}

// NewGlobalScope constructs a Scope for the global user configuration.
func NewGlobalScope(configOverride, skillsDirOverride, cacheDirOverride string) Scope {
	configPath := DefaultConfigFile()
	skillsDir := DefaultSkillsDir()
	cacheDir := DefaultCacheDir()

	if configOverride != "" {
		configPath = ExpandUser(configOverride)
	}
	if skillsDirOverride != "" {
		skillsDir = ExpandUser(skillsDirOverride)
	}
	if cacheDirOverride != "" {
		cacheDir = ExpandUser(cacheDirOverride)
	}
	return Scope{
		ConfigPath: configPath,
		SkillsDir:  skillsDir,
		CacheDir:   cacheDir,
		IsProject:  false,
	}
}

// NewProjectScope constructs a Scope for a project repository.
func NewProjectScope(cwd, configOverride, skillsDirOverride, cacheDirOverride string) Scope {
	configPath, skillsDir := GetProjectPaths(cwd)
	cacheDir := DefaultCacheDir()

	if configOverride != "" {
		configPath = ExpandUser(configOverride)
	}
	if skillsDirOverride != "" {
		skillsDir = ExpandUser(skillsDirOverride)
	}
	if cacheDirOverride != "" {
		cacheDir = ExpandUser(cacheDirOverride)
	}
	return Scope{
		ConfigPath: configPath,
		SkillsDir:  skillsDir,
		CacheDir:   cacheDir,
		IsProject:  true,
	}
}

// ResolveScope resolves a Global or Project Scope based on isProject, working directory, and path overrides.
func ResolveScope(isProject bool, cwd, configOverride, skillsDirOverride, cacheDirOverride string) Scope {
	if isProject {
		return NewProjectScope(cwd, configOverride, skillsDirOverride, cacheDirOverride)
	}
	return NewGlobalScope(configOverride, skillsDirOverride, cacheDirOverride)
}

// Root returns the directory parent cleanup must not escape: the project
// checkout in project Scope, the user's home when skillsDir is the global
// skills directory.
func (s Scope) Root() string {
	return ScopeRoot(s.SkillsDir)
}

// StoreLocalSource renders a local Skill Source for skills.json.
func (s Scope) StoreLocalSource(absSource string) string {
	return StoreLocalSourcePath(absSource, s.SkillsDir)
}

// LocalSymlinkTarget returns what the skills-dir symlink should point at.
func (s Scope) LocalSymlinkTarget(absSource string) string {
	return LocalSymlinkTarget(absSource, s.SkillsDir)
}

// ResolveLocalSource turns a skills.json Source back into a usable path.
func (s Scope) ResolveLocalSource(source string) string {
	return ResolveLocalSourcePath(source, s.SkillsDir)
}

// KnownAgents returns the mapping of non-universal agent names to their skills directory in this Scope.
func (s Scope) KnownAgents() map[string]string {
	return GetAgentsForSkillsDir(s.SkillsDir)
}

// UniversalAgentDirs returns skills directories that Automatically available agents may have had materialized.
func (s Scope) UniversalAgentDirs() map[string]string {
	return GetUniversalAgentSkillDirs(s.SkillsDir)
}

// AutomaticallyAvailable returns the agents that read this Scope's central skills directory directly.
func (s Scope) AutomaticallyAvailable() []string {
	return GetAutomaticallyAvailableAgents(s.SkillsDir)
}

// IsUniversalAgent reports whether the named agent is Automatically available in this Scope.
func (s Scope) IsUniversalAgent(name string) bool {
	return IsUniversalAgent(name, s.SkillsDir)
}

// IsProjectScope reports whether skillsDir is project-scoped rather than the
// global skills directory.
func IsProjectScope(skillsDir string) bool {
	return !IsGlobalSkillsDir(skillsDir)
}

// ScopeRoot is the directory parent cleanup must not escape: the project
// checkout in project Scope, the user's home when skillsDir is the global
// skills directory.
func ScopeRoot(skillsDir string) string {
	return GetProjectRootFromSkillsDir(skillsDir)
}

// StoreLocalSourcePath renders a local Skill Source for skills.json. Inside a
// project it returns a path relative to the project root, so the committed
// Config resolves on a teammate's checkout. Anything outside the project, and
// all of global Scope, uses ~/ if inside the user's home, or the absolute path.
func StoreLocalSourcePath(absSource string, skillsDir string) string {
	if !IsProjectScope(skillsDir) {
		return ToTildePath(absSource)
	}
	projectRoot := GetProjectRootFromSkillsDir(skillsDir)
	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return ToTildePath(absSource)
	}
	rel, err := filepath.Rel(absRoot, absSource)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return ToTildePath(absSource)
	}
	return filepath.ToSlash(rel)
}

// LocalSymlinkTarget returns what the skills-dir symlink should point at. A
// Source inside the project is linked relatively so the checkout survives
// being cloned elsewhere; anything else stays absolute.
func LocalSymlinkTarget(absSource string, skillsDir string) string {
	stored := StoreLocalSourcePath(absSource, skillsDir)
	if strings.HasPrefix(stored, "~") || filepath.IsAbs(stored) {
		return absSource
	}
	rel, err := filepath.Rel(skillsDir, absSource)
	if err != nil {
		return absSource
	}
	return rel
}

// ResolveLocalSourcePath turns a skills.json Source back into a usable path,
// interpreting a relative one against the project root.
func ResolveLocalSourcePath(source string, skillsDir string) string {
	expanded := ExpandUser(source)
	if filepath.IsAbs(expanded) {
		return expanded
	}
	base := GetProjectRootFromSkillsDir(skillsDir)
	return filepath.Join(base, filepath.FromSlash(expanded))
}
