package engine

import (
	"fmt"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/models"
)

// NoSkillsError is a Source that offers no Skills. It is the Source's state,
// not a failure to read it (ADR-0002): every command that reads a Source
// reports it the same way and exits 1.
type NoSkillsError struct {
	// Source names the Source as the user sees it: a path, a Source key, or
	// a well-known index URL.
	Source string
	// Index is a well-known index listing none, rather than a directory or
	// repository holding no SKILL.md.
	Index bool
	// Subpath is where discovery was limited to, from --path or the Source
	// string itself; a wider one might offer Skills. Empty for an index.
	Subpath string
}

func (e *NoSkillsError) Error() string {
	if e.Index {
		return fmt.Sprintf("%s lists no Skills", e.Source)
	}
	return fmt.Sprintf("%s has no SKILL.md", e.Source)
}

// NoSkillsInDirectory is a *NoSkillsError when a local directory, named
// display and scanned under subpath, offered nothing; nil otherwise.
func NoSkillsInDirectory(discovered DiscoveredSkills, display, subpath string) error {
	if len(discovered) > 0 {
		return nil
	}
	return &NoSkillsError{Source: display, Subpath: subpath}
}

// NoSkills is a *NoSkillsError when the Source offered nothing under the
// requested subpath, including a well-known index whose every entry was
// Skipped; nil otherwise.
func (in *RemoteIntake) NoSkills() error {
	if len(in.Discovered) > 0 {
		return nil
	}
	if in.spec.RepoType == config.WellKnownType {
		return &NoSkillsError{Source: models.WellKnownIndexURL(in.spec.URL), Index: true}
	}
	return &NoSkillsError{Source: in.spec.SourceKey, Subpath: in.spec.Subpath}
}
