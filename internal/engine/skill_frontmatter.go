package engine

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// skillFrontmatter is what a SKILL.md's frontmatter declares about its Skill.
type skillFrontmatter struct {
	Name        string
	Description string
	// Replaces is the Skill names this Skill takes the place of. A Source
	// renames a Skill by declaring the old name in the new Skill's metadata,
	// the Agent Skills spec's string-to-string map (ADR 0007):
	//
	//	metadata:
	//	  replaces: old-name, older-name
	Replaces []string
}

// skillNameRegex is the leading part of a declared name that discovery uses
// as the Skill's name.
var skillNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_\-\.]+`)

// readSkillFrontmatter reads the frontmatter of the SKILL.md at path. Authors
// write it as YAML but often not as valid YAML, such as an unquoted
// description containing ": ", so it is read line by line and leniently: a
// top-level value runs to the end of its line plus any indented continuation,
// '#' starts no comment, and only metadata is read as a nested map, in block
// or flow form. A file that cannot be read or has no frontmatter declares
// nothing.
func readSkillFrontmatter(path string) skillFrontmatter {
	content, err := os.ReadFile(path)
	if err != nil {
		return skillFrontmatter{}
	}
	lines := frontmatterLines(string(content))
	var declared skillFrontmatter
	seen := make(map[string]bool)
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if line == "" || line[0] == ' ' || line[0] == '\t' {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		block := indentedBlock(lines[i+1:])
		i += len(block)
		// As discovery always has, the first declaration of a key wins.
		if seen[key] {
			continue
		}
		seen[key] = true
		switch key {
		case "name":
			declared.Name = skillNameRegex.FindString(strings.TrimLeft(value, `"'`))
		case "description":
			declared.Description = scalarValue(value, block)
		case "metadata":
			// Metadata values are strings; a list declares no Rename.
			if replaces := metadataMap(value, block)["replaces"]; !strings.HasPrefix(replaces, "[") {
				declared.Replaces = splitNames(replaces)
			}
		}
	}
	return declared
}

// frontmatterLines is the frontmatter of a SKILL.md: the lines between a
// first line of "---" and the next line of "---", or nil when there is none.
func frontmatterLines(content string) []string {
	content = strings.TrimPrefix(content, "\xef\xbb\xbf")
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if strings.TrimRight(lines[0], " \t") != "---" {
		return nil
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], " \t") == "---" {
			return lines[1:i]
		}
	}
	return nil
}

// indentedBlock is the leading lines that are indented or blank: the value
// of the key on the line before them.
func indentedBlock(lines []string) []string {
	n := 0
	for n < len(lines) && (strings.TrimSpace(lines[n]) == "" || lines[n][0] == ' ' || lines[n][0] == '\t') {
		n++
	}
	return lines[:n]
}

// scalarValue is a top-level value on one line: a literal or folded block
// (|, >, and their chomping and indentation indicators) and a plain value
// continued on indented lines both join into one line.
func scalarValue(value string, block []string) string {
	parts := []string{}
	if !isBlockIndicator(value) {
		parts = append(parts, value)
	}
	for _, line := range block {
		if line = strings.TrimSpace(line); line != "" {
			parts = append(parts, line)
		}
	}
	return unquote(strings.Join(parts, " "))
}

func isBlockIndicator(value string) bool {
	if value == "" || (value[0] != '|' && value[0] != '>') {
		return false
	}
	return strings.Trim(value[1:], "+-0123456789") == ""
}

// metadataMap reads metadata as the Agent Skills spec's string-to-string map,
// either as key: value lines at one indentation or as a {key: value, ...} flow
// map, which may continue on indented lines. The first declaration of a key
// wins, as it does at the top level.
func metadataMap(value string, block []string) map[string]string {
	var entries []string
	if strings.HasPrefix(value, "{") {
		flow := value
		for _, line := range block {
			flow += " " + strings.TrimSpace(line)
		}
		flow = strings.TrimSuffix(strings.TrimSpace(flow), "}")
		entries = splitFlow(flow[1:])
	} else {
		entries = directEntries(block)
	}
	metadata := make(map[string]string)
	for _, entry := range entries {
		key, value, ok := strings.Cut(strings.TrimSpace(entry), ":")
		if !ok {
			continue
		}
		key = unquote(strings.TrimSpace(key))
		if _, seen := metadata[key]; !seen {
			metadata[key] = unquote(strings.TrimSpace(value))
		}
	}
	return metadata
}

// directEntries is the lines of block at the indentation of its first
// non-blank line, leaving out what nests deeper inside them.
func directEntries(block []string) []string {
	var entries []string
	indent := -1
	for _, line := range block {
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed == "" {
			continue
		}
		if depth := len(line) - len(trimmed); indent < 0 {
			indent = depth
		} else if depth != indent {
			continue
		}
		entries = append(entries, trimmed)
	}
	return entries
}

// splitNames splits a comma-separated list of Skill names.
func splitNames(value string) []string {
	var names []string
	for name := range strings.SplitSeq(value, ",") {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// splitFlow splits the inside of a flow map at each comma that is not inside a
// quoted string or a nested [...] or {...}.
func splitFlow(s string) []string {
	var parts []string
	var quote byte
	depth, start := 0, 0
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '[' || c == '{':
			depth++
		case c == ']' || c == '}':
			depth--
		case c == ',' && depth == 0:
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	return append(parts, s[start:])
}

// unquote removes one pair of matching quotes around value.
func unquote(value string) string {
	if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
		return value[1 : len(value)-1]
	}
	return value
}

const MaxScanDepth = 7

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

// walkSkillFiles calls visit with the path of each SKILL.md under root,
// skipping the directories IgnoredScanDirs names and any more than
// MaxScanDepth levels below root. Entries that cannot be read are skipped.
// remote skips a SKILL.md that is a symlink: in a Cache it is an editor's
// mirror of a Skill elsewhere, cannot be verified (ADR-0010), and breaks once
// Materialized. A local Source is linked in place, so its symlink still works.
func walkSkillFiles(root string, remote bool, visit func(path string) error) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if IgnoredScanDirs[info.Name()] {
				return filepath.SkipDir
			}
			if rel, relErr := filepath.Rel(root, path); relErr == nil && rel != "." && len(strings.Split(filepath.ToSlash(rel), "/")) > MaxScanDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(info.Name(), "SKILL.md") {
			return nil
		}
		if remote && info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		return visit(path)
	})
}
