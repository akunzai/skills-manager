package engine

import (
	"os"
	"path/filepath"
	"regexp"
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

func DiscoverSkillsInRepo(repoDir string) (map[string]string, error) {
	found := make(map[string]string)

	err := filepath.Walk(repoDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() && info.Name() == ".git" {
			return filepath.SkipDir
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
			found[name] = relPathStr
		}
		return nil
	})

	return found, err
}
