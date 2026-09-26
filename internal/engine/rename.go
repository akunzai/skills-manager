package engine

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// A Source renames a Skill by declaring the old name in the new Skill's
// SKILL.md frontmatter, as the Agent Skills spec's string-to-string metadata
// map allows (ADR 0007):
//
//	metadata:
//	  replaces: old-name, older-name
var (
	frontmatterMetadataRegex = regexp.MustCompile(`^metadata:\s*$`)
	metadataReplacesRegex    = regexp.MustCompile(`^\s+replaces:\s*(.*?)\s*$`)
)

// ParseSkillReplacesFromMD returns the Skill names a SKILL.md declares it
// replaces, or nil when it declares none.
func ParseSkillReplacesFromMD(skillMdPath string) []string {
	content, err := os.ReadFile(skillMdPath)
	if err != nil {
		return nil
	}
	text := strings.ReplaceAll(string(content), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return nil
	}
	frontmatter, _, found := strings.Cut(text[len("---\n"):], "\n---")
	if !found {
		return nil
	}
	inMetadata := false
	for _, line := range strings.Split(frontmatter, "\n") {
		if frontmatterMetadataRegex.MatchString(line) {
			inMetadata = true
			continue
		}
		if !inMetadata {
			continue
		}
		if line != "" && line[0] != ' ' && line[0] != '\t' {
			inMetadata = false
			continue
		}
		match := metadataReplacesRegex.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		var names []string
		for _, name := range strings.Split(strings.Trim(match[1], `"'`), ",") {
			if name = strings.TrimSpace(name); name != "" {
				names = append(names, name)
			}
		}
		return names
	}
	return nil
}

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
	_ = filepath.Walk(repoDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if IgnoredScanDirs[info.Name()] {
				return filepath.SkipDir
			}
			if rel, relErr := filepath.Rel(repoDir, path); relErr == nil && rel != "." && len(strings.Split(filepath.ToSlash(rel), "/")) > MaxScanDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(info.Name(), "SKILL.md") {
			return nil
		}
		replaces := ParseSkillReplacesFromMD(path)
		if len(replaces) == 0 {
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
		name := ParseSkillNameFromMD(path)
		if name == "" {
			name = filepath.Base(filepath.Dir(path))
		}
		for _, old := range replaces {
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
