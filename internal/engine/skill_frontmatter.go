package engine

import (
	"cmp"
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

// splitFrontmatter splits content into its frontmatter lines, the trailing
// content after the closing "---", and whether a UTF-8 BOM was present. When
// content has no valid frontmatter block, ok is false.
func splitFrontmatter(content string) (lines []string, rest string, hasBOM bool, ok bool) {
	hasBOM = strings.HasPrefix(content, "\xef\xbb\xbf")
	content = strings.TrimPrefix(content, "\xef\xbb\xbf")
	allLines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if len(allLines) == 0 || strings.TrimRight(allLines[0], " \t") != "---" {
		return nil, "", false, false
	}
	for i := 1; i < len(allLines); i++ {
		if strings.TrimRight(allLines[i], " \t") == "---" {
			return allLines[1:i], strings.Join(allLines[i+1:], "\n"), hasBOM, true
		}
	}
	return nil, "", false, false
}

// frontmatterLines is the frontmatter of a SKILL.md: the lines between a
// first line of "---" and the next line of "---", or nil when there is none.
func frontmatterLines(content string) []string {
	lines, _, _, ok := splitFrontmatter(content)
	if !ok {
		return nil
	}
	return lines
}

// isTopLevelKey reports whether line is an unindented YAML key matching expectedKey.
func isTopLevelKey(line, expectedKey string) bool {
	if line == "" || line[0] == ' ' || line[0] == '\t' {
		return false
	}
	key, _, ok := strings.Cut(line, ":")
	return ok && strings.TrimSpace(key) == expectedKey
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
// MaxScanDepth levels below root. Entries that cannot be read are skipped, and
// so is any SKILL.md that skip, when given, reports.
func walkSkillFiles(root string, skip func(path string) bool, visit func(path string) error) error {
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
		if skip != nil && skip(path) {
			return nil
		}
		return visit(path)
	})
}

// maxSkillDescription is the longest description the Agent Skills spec allows.
const maxSkillDescription = 1024

// formatFrontmatterDescription sanitizes and truncates a description to
// fit the maxSkillDescription rune limit and formats it as a quoted YAML line.
func formatFrontmatterDescription(description string) string {
	description = strings.ReplaceAll(strings.ReplaceAll(description, "\r\n", " "), "\n", " ")
	if r := []rune(description); len(r) > maxSkillDescription {
		description = string(r[:maxSkillDescription])
	}
	quoted := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(description)
	return `description: "` + quoted + `"`
}

// withSkillFrontmatter is a SKILL.md printed by a command, given frontmatter
// when it has none, or having its frontmatter description overridden when
// description is non-empty. When content has frontmatter and description is empty,
// the printed frontmatter is kept as it is. It reports whether the description
// fell back to the Skill name because content had no frontmatter and no description
// was provided.
func withSkillFrontmatter(content, name, description string) (string, bool) {
	fmLines, rest, hasBOM, ok := splitFrontmatter(content)
	if ok {
		if description == "" {
			return content, false
		}
		return overrideFrontmatterLines(fmLines, rest, description, hasBOM), false
	}
	fellBack := description == ""
	descLine := formatFrontmatterDescription(cmp.Or(description, name))
	return "---\nname: " + name + "\n" + descLine + "\n---\n\n" + content, fellBack
}

// overrideFrontmatterDescription replaces the description in an existing
// frontmatter block with description, or appends it if none was declared.
func overrideFrontmatterDescription(content, description string) string {
	fmLines, rest, hasBOM, ok := splitFrontmatter(content)
	if !ok {
		return content
	}
	return overrideFrontmatterLines(fmLines, rest, description, hasBOM)
}

func overrideFrontmatterLines(lines []string, rest, description string, hasBOM bool) string {
	descLine := formatFrontmatterDescription(description)
	var newFmLines []string
	found := false
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if !found && isTopLevelKey(line, "description") {
			found = true
			newFmLines = append(newFmLines, descLine)
			block := indentedBlock(lines[i+1:])
			i += len(block)
			continue
		}
		newFmLines = append(newFmLines, line)
	}
	if !found {
		newFmLines = append(newFmLines, descLine)
	}

	result := "---\n" + strings.Join(newFmLines, "\n") + "\n---\n" + rest
	if hasBOM {
		result = "\xef\xbb\xbf" + result
	}
	return result
}
