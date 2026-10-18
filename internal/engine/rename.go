package engine

import (
	"cmp"
	"path/filepath"
	"slices"
)

// Replacement is the Skill a Source declares in place of a removed one.
type Replacement struct {
	Name    string
	Subpath string
}

// findReplacements walks the SKILL.md files checked out in repoDir and
// returns, for each name in removed, the Skill that declares it replaces that
// name. accept filters the candidate subpaths, so Freshness can ignore
// SKILL.md files an interrupted discovery left outside the sparse checkout.
func findReplacements(repoDir string, removed []string, accept func(subpath string) bool) map[string]Replacement {
	found := make(map[string]Replacement)
	if len(removed) == 0 {
		return found
	}
	_ = walkSkillFiles(repoDir, committedSymlinks(repoDir), func(path string) error {
		frontmatter := readSkillFrontmatter(path)
		if len(frontmatter.Replaces) == 0 {
			return nil
		}
		rel, relErr := filepath.Rel(repoDir, filepath.Dir(path))
		if relErr != nil {
			return nil
		}
		subpath := filepath.ToSlash(rel)
		if accept != nil && !accept(subpath) {
			return nil
		}
		name := cmp.Or(frontmatter.Name, filepath.Base(filepath.Dir(path)))
		for _, old := range frontmatter.Replaces {
			if !slices.Contains(removed, old) {
				continue
			}
			// The shallowest declaration wins, as discovery prefers it.
			if current, ok := found[old]; ok && current.Subpath <= subpath {
				continue
			}
			found[old] = Replacement{Name: name, Subpath: subpath}
		}
		return nil
	})
	return found
}

// removedSubpaths is the declared Skills whose subpath the Cache's commit no
// longer has.
func removedSubpaths(cache Cache, skills map[string]string) []string {
	var removed []string
	for _, name := range sortedSkillKeys(skills) {
		if !cache.atHead(skills[name]).exists() {
			removed = append(removed, name)
		}
	}
	return removed
}

// coverReplacements finds the Skills that replace declared Skills removed
// from the Cache's commit and covers their subpaths. Update runs this after a
// refresh: reading SKILL.md files outside the sparse checkout may download
// them, which Sync must never do (ADR 0004). It returns what it covered.
func coverReplacements(cache Cache, skills map[string]string) (map[string]Replacement, error) {
	removed := removedSubpaths(cache, skills)
	if len(removed) == 0 {
		return nil, nil
	}
	var found map[string]Replacement
	if err := cache.withSkillFiles(func(repoDir string) error {
		found = findReplacements(repoDir, removed, nil)
		return nil
	}); err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, nil
	}
	paths := make([]string, 0, len(found))
	for _, replacement := range found {
		paths = append(paths, replacement.Subpath)
	}
	slices.Sort(paths)
	if err := cache.Cover(paths...); err != nil {
		return nil, err
	}
	return found, nil
}
