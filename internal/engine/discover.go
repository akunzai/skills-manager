package engine

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

var (
	frontmatterNameRegex        = regexp.MustCompile(`(?m)^name:\s*["']?([a-zA-Z0-9_\-\.]+)["']?`)
	frontmatterDescriptionRegex = regexp.MustCompile(`(?m)^description:\s*["']?(.*?)["']?\s*$`)
)

// frontmatterBody returns a SKILL.md's leading `---`-delimited block, or ""
// when the file cannot be read or has none.
func frontmatterBody(skillMdPath string) string {
	contentBytes, err := os.ReadFile(skillMdPath)
	if err != nil {
		return ""
	}
	content := string(contentBytes)
	if !strings.HasPrefix(content, "---") {
		return ""
	}
	parts := strings.SplitN(content, "---", 3)
	if len(parts) < 3 {
		return ""
	}
	return parts[1]
}

func ParseSkillNameFromMD(skillMdPath string) string {
	match := frontmatterNameRegex.FindStringSubmatch(frontmatterBody(skillMdPath))
	if len(match) > 1 {
		return strings.TrimSpace(match[1])
	}
	return ""
}

// ParseSkillDescriptionFromMD reads a Skill's declared description from its
// SKILL.md frontmatter, or "" when it has none.
func ParseSkillDescriptionFromMD(skillMdPath string) string {
	match := frontmatterDescriptionRegex.FindStringSubmatch(frontmatterBody(skillMdPath))
	if len(match) > 1 {
		return strings.TrimSpace(match[1])
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

// DiscoveredSkillDescriptions maps each candidate path in a DiscoveredSkills
// value to that Skill's SKILL.md description, for callers that show it
// (Add's --list) without a second scan.
type DiscoveredSkillDescriptions map[string]string

func DiscoverSkillsInRepo(repoDir, scope string) (DiscoveredSkills, error) {
	discovered, _, err := discoverSkills(repoDir, scope, fileBundleIdentity(repoDir))
	return discovered, err
}

// DiscoverSkillsInRepoWithDescriptions is DiscoverSkillsInRepo plus each
// candidate's description.
func DiscoverSkillsInRepoWithDescriptions(repoDir, scope string) (DiscoveredSkills, DiscoveredSkillDescriptions, error) {
	return discoverSkills(repoDir, scope, fileBundleIdentity(repoDir))
}

// discoverRemoteSkills discovers Skills in a Cache whose sparse checkout holds
// only SKILL.md files (withSkillFiles), so duplicate candidates are
// compared by their committed tree rather than the files on disk.
func discoverRemoteSkills(cache Cache, scope string) (DiscoveredSkills, DiscoveredSkillDescriptions, error) {
	var found DiscoveredSkills
	var descriptions DiscoveredSkillDescriptions
	err := cache.withSkillFiles(func(repoDir string) error {
		var err error
		found, descriptions, err = discoverSkills(repoDir, scope, gitTreeIdentity(repoDir))
		// A committed directory holding no SKILL.md is not on disk at all.
		if errors.Is(err, os.ErrNotExist) {
			if kind, _, gitErr := runGit(repoDir, "cat-file", "-t", "HEAD:"+filepath.ToSlash(filepath.Clean(scope))); gitErr == nil && kind == "tree" {
				found, descriptions, err = DiscoveredSkills{}, DiscoveredSkillDescriptions{}, nil
			}
		}
		return err
	})
	return found, descriptions, err
}

func discoverSkills(repoDir, scope string, identity bundleIdentity) (DiscoveredSkills, DiscoveredSkillDescriptions, error) {
	scanRoot, err := discoveryRoot(repoDir, scope)
	if err != nil {
		return nil, nil, err
	}
	foundPaths := make(map[string][]string)
	rawDescriptions := make(map[string]string)

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
			rawDescriptions[relPathStr] = ParseSkillDescriptionFromMD(path)
		}
		return nil
	})

	if err != nil {
		return nil, nil, err
	}

	found := make(DiscoveredSkills, len(foundPaths))
	descriptions := make(DiscoveredSkillDescriptions, len(rawDescriptions))
	for _, name := range slices.Sorted(maps.Keys(foundPaths)) {
		paths := foundPaths[name]
		slices.Sort(paths)
		candidates := canonicalizeSkillCandidates(name, paths, identity)
		found[name] = candidates
		for _, path := range candidates {
			descriptions[path] = rawDescriptions[path]
		}
	}

	return found, descriptions, nil
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

// bundleIdentity names the content of the Skill bundle at a repository-relative
// path; candidates with equal identities are the same Skill.
type bundleIdentity func(relPath string) (string, error)

// fileBundleIdentity digests the bundle as it is on disk, including file
// modes and symlink targets.
func fileBundleIdentity(repoDir string) bundleIdentity {
	return func(relPath string) (string, error) {
		entries, err := readSkillBundle(filepath.Join(repoDir, filepath.FromSlash(relPath)))
		if err != nil {
			return "", err
		}
		h := sha256.New()
		for _, entry := range entries {
			fmt.Fprintf(h, "%s\x00%o\x00%s\x00%d\x00", entry.path, uint32(entry.mode), entry.linkTarget, len(entry.content))
			h.Write(entry.content)
		}
		return fmt.Sprintf("%x", h.Sum(nil)), nil
	}
}

// gitTreeIdentity is the committed tree ID, which already covers content,
// executable bits, and symlink targets.
func gitTreeIdentity(repoDir string) bundleIdentity {
	return func(relPath string) (string, error) {
		if relPath == "." {
			relPath = ""
		}
		stdout, stderr, err := runGit(repoDir, "rev-parse", "HEAD:"+relPath)
		if err != nil {
			return "", gitOpErr("resolve tree", relPath, stdout, stderr, err)
		}
		return stdout, nil
	}
}

func canonicalizeSkillCandidates(name string, paths []string, identity bundleIdentity) []string {
	if len(paths) == 1 {
		return paths
	}
	type bundleGroup struct {
		identity string
		paths    []string
	}
	groups := make([]bundleGroup, 0, len(paths))
	for _, path := range paths {
		id, err := identity(path)
		if err != nil {
			groups = append(groups, bundleGroup{paths: []string{path}})
			continue
		}
		matched := false
		for i := range groups {
			if groups[i].identity != "" && groups[i].identity == id {
				groups[i].paths = append(groups[i].paths, path)
				matched = true
				break
			}
		}
		if !matched {
			groups = append(groups, bundleGroup{identity: id, paths: []string{path}})
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
