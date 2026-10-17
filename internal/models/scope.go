package models

import (
	"os"
	"path/filepath"
	"strings"
)

// Scope represents the active Global or Project configuration, its skills
// directory, Cache directory, Scope root boundary, and its Agent directory
// registry. Its kind is the flag choice, never the shape of SkillsDir
// (ADR-0012).
type Scope struct {
	ConfigPath string
	SkillsDir  string
	CacheDir   string
	IsProject  bool
	// Root is the directory the Scope belongs to: the project checkout at
	// Project Scope, the user's home at Global Scope. Project Agent
	// directories, relative local Sources, the installer lock, and the
	// boundary parent cleanup stops at are all under it.
	Root string
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
		Root:       UserHomeDir(),
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
		Root:       cwd,
	}
}

// ResolveScope resolves a Global or Project Scope based on isProject, working directory, and path overrides.
func ResolveScope(isProject bool, cwd, configOverride, skillsDirOverride, cacheDirOverride string) Scope {
	if isProject {
		return NewProjectScope(cwd, configOverride, skillsDirOverride, cacheDirOverride)
	}
	return NewGlobalScope(configOverride, skillsDirOverride, cacheDirOverride)
}

// RootDir is Root, or the user's home for a Global Scope built without one.
// A Project Scope has no such default: guessing one would put its Agent
// directories and relative Sources wherever the process happens to run, so
// it panics instead.
func (s Scope) RootDir() string {
	switch {
	case s.Root != "":
		return s.Root
	case s.IsProject:
		panic("models: Project Scope has no Root")
	default:
		return UserHomeDir()
	}
}

// Agents is the Agent directory knowledge for this Scope.
func (s Scope) Agents() Agents {
	var known map[string]string
	if s.IsProject {
		known = projectKnownAgents(s.RootDir())
	} else {
		known = knownAgents()
	}
	leftover := leftoverAgentSkillDirs(s.IsProject, s.RootDir())
	for agent := range known {
		delete(leftover, agent)
	}
	return Agents{known: known, automatic: automaticallyAvailableAgents(s.IsProject), leftover: leftover}
}

// StoreLocalSource renders a local Skill Source for skills.json. Inside a
// project it returns a path relative to the project root, so the committed
// Config resolves on a teammate's checkout. Anything outside the project, and
// all of Global Scope, uses ~/ if inside the user's home, or the absolute path.
func (s Scope) StoreLocalSource(absSource string) string {
	if !s.IsProject {
		return ToTildePath(absSource)
	}
	absRoot, err := filepath.Abs(s.RootDir())
	if err != nil {
		return ToTildePath(absSource)
	}
	rel, err := filepath.Rel(absRoot, absSource)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return ToTildePath(absSource)
	}
	return filepath.ToSlash(rel)
}

// LocalSourceInsideSkillsDir reports whether absSource is the skills directory
// itself or anything nested under it. A local Source there is occupancy on
// the destination, not a Source to Materialize from.
func LocalSourceInsideSkillsDir(absSource, skillsDir string) bool {
	if absSource == "" || skillsDir == "" {
		return false
	}
	absSource, err1 := filepath.Abs(absSource)
	absDir, err2 := filepath.Abs(skillsDir)
	if err1 != nil || err2 != nil {
		return false
	}
	rel, err := filepath.Rel(absDir, absSource)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
}

// LocalSymlinkTarget returns what the skills-dir symlink should point at. A
// Source inside the project is linked relatively so the checkout survives
// being cloned elsewhere; anything else stays absolute.
func (s Scope) LocalSymlinkTarget(absSource string) string {
	stored := s.StoreLocalSource(absSource)
	if strings.HasPrefix(stored, "~") || filepath.IsAbs(stored) {
		return absSource
	}
	rel, err := filepath.Rel(s.SkillsDir, absSource)
	if err != nil {
		return absSource
	}
	return rel
}

// ResolveLocalSource turns a skills.json Source back into a usable path,
// interpreting a relative one against Root.
func (s Scope) ResolveLocalSource(source string) string {
	expanded := ExpandUser(source)
	if filepath.IsAbs(expanded) {
		return expanded
	}
	return filepath.Join(s.RootDir(), filepath.FromSlash(expanded))
}
