package engine

import (
	"bytes"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

var frontmatterNameRegex = regexp.MustCompile(`(?m)^name:\s*["']?([a-zA-Z0-9_\-\.]+)["']?`)

func ParseSkillNameFromMD(skillMdPath string) string {
	contentBytes, err := os.ReadFile(skillMdPath)
	if err != nil {
		return ""
	}
	content := string(contentBytes)
	if strings.HasPrefix(content, "---") {
		parts := strings.SplitN(content, "---", 3)
		if len(parts) >= 3 {
			frontmatter := parts[1]
			match := frontmatterNameRegex.FindStringSubmatch(frontmatter)
			if len(match) > 1 {
				return strings.TrimSpace(match[1])
			}
		}
	}
	return ""
}

const MaxScanDepth = 5

var IgnoredScanDirs = map[string]bool{
	".git":         true,
	".hg":          true,
	".svn":         true,
	"node_modules": true,
	"vendor":       true,
	".venv":        true,
	"venv":         true,
	"__pycache__":  true,
	"dist":         true,
	"build":        true,
	"target":       true,
	".cache":       true,
	".next":        true,
	".nuxt":        true,
	".turbo":       true,
}

type DiscoveredSkills map[string][]string

func DiscoverSkillsInRepo(repoDir, scope string) (DiscoveredSkills, error) {
	scanRoot, err := discoveryRoot(repoDir, scope)
	if err != nil {
		return nil, err
	}
	foundPaths := make(map[string][]string)

	err = filepath.Walk(scanRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if IgnoredScanDirs[info.Name()] {
				return filepath.SkipDir
			}
			rel, err := filepath.Rel(scanRoot, path)
			if err == nil && rel != "." {
				depth := len(strings.Split(filepath.ToSlash(rel), "/"))
				if depth > MaxScanDepth {
					return filepath.SkipDir
				}
			}
		}
		if !info.IsDir() && strings.EqualFold(info.Name(), "SKILL.md") {
			skillDir := filepath.Dir(path)
			relPath, err := filepath.Rel(repoDir, skillDir)
			if err != nil {
				return nil
			}
			relPathStr := filepath.ToSlash(relPath)

			name := ParseSkillNameFromMD(path)
			if name == "" {
				if relPathStr == "." {
					name = filepath.Base(repoDir)
				} else {
					name = filepath.Base(skillDir)
				}
			}
			foundPaths[name] = append(foundPaths[name], relPathStr)
		}
		return nil
	})

	if err != nil {
		return nil, err
	}

	found := make(DiscoveredSkills, len(foundPaths))
	for _, name := range slices.Sorted(maps.Keys(foundPaths)) {
		paths := foundPaths[name]
		slices.Sort(paths)
		found[name] = canonicalizeSkillCandidates(repoDir, name, paths)
	}

	return found, nil
}

func discoveryRoot(repoDir, scope string) (string, error) {
	cleanScope := filepath.Clean(filepath.FromSlash(scope))
	if scope == "" || cleanScope == "." {
		return repoDir, nil
	}
	if filepath.IsAbs(cleanScope) || cleanScope == ".." || strings.HasPrefix(cleanScope, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("discovery scope %q escapes repository", scope)
	}
	root := filepath.Join(repoDir, cleanScope)
	info, err := os.Lstat(root)
	if err != nil {
		return "", fmt.Errorf("discovery scope %q: %w", scope, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("discovery scope %q is not a directory", scope)
	}
	return root, nil
}

type bundleEntry struct {
	path       string
	mode       os.FileMode
	content    []byte
	linkTarget string
}

func canonicalizeSkillCandidates(repoDir, name string, paths []string) []string {
	type bundleGroup struct {
		entries []bundleEntry
		paths   []string
	}
	groups := make([]bundleGroup, 0, len(paths))
	for _, path := range paths {
		entries, err := readSkillBundle(filepath.Join(repoDir, filepath.FromSlash(path)))
		if err != nil {
			groups = append(groups, bundleGroup{paths: []string{path}})
			continue
		}
		matched := false
		for i := range groups {
			if groups[i].entries != nil && equalBundleEntries(groups[i].entries, entries) {
				groups[i].paths = append(groups[i].paths, path)
				matched = true
				break
			}
		}
		if !matched {
			groups = append(groups, bundleGroup{entries: entries, paths: []string{path}})
		}
	}

	candidates := make([]string, 0, len(groups))
	for _, group := range groups {
		candidates = append(candidates, canonicalSkillPath(name, group.paths))
	}
	slices.Sort(candidates)
	return candidates
}

func readSkillBundle(root string) ([]bundleEntry, error) {
	var entries []bundleEntry
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == "." {
			return err
		}
		entry := bundleEntry{path: filepath.ToSlash(rel), mode: info.Mode().Type() | (info.Mode() & 0o111)}
		switch {
		case info.Mode().IsRegular():
			entry.content, err = os.ReadFile(path)
		case info.IsDir():
		case info.Mode()&os.ModeSymlink != 0:
			entry.linkTarget, err = os.Readlink(path)
		default:
			return fmt.Errorf("unsupported bundle entry %s", path)
		}
		if err != nil {
			return err
		}
		entries = append(entries, entry)
		return nil
	})
	return entries, err
}

func equalBundleEntries(a, b []bundleEntry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].path != b[i].path || a[i].mode != b[i].mode || a[i].linkTarget != b[i].linkTarget || !bytes.Equal(a[i].content, b[i].content) {
			return false
		}
	}
	return true
}

func canonicalSkillPath(name string, paths []string) string {
	want := filepath.ToSlash(filepath.Join("skills", name))
	return slices.MinFunc(paths, func(a, b string) int {
		if (a == want) != (b == want) {
			if a == want {
				return -1
			}
			return 1
		}
		if depth := strings.Count(a, "/") - strings.Count(b, "/"); depth != 0 {
			return depth
		}
		if length := len(a) - len(b); length != 0 {
			return length
		}
		return strings.Compare(a, b)
	})
}
