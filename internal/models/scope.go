package models

import (
	"os"
	"path/filepath"
	"strings"
)

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
