package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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

func DiscoverSkillsInRepo(repoDir string) (map[string]string, error) {
	found := make(map[string]string)
	foundPaths := make(map[string][]string)

	err := filepath.Walk(repoDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if IgnoredScanDirs[info.Name()] {
				return filepath.SkipDir
			}
			rel, err := filepath.Rel(repoDir, path)
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

	names := make([]string, 0, len(foundPaths))
	for name := range foundPaths {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		paths := foundPaths[name]
		if len(paths) > 1 {
			sort.Strings(paths)
			return nil, fmt.Errorf("duplicate skill name %q found in %s", name, strings.Join(paths, ", "))
		}
		found[name] = paths[0]
	}

	return found, nil
}
