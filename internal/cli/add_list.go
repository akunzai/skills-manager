package cli

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/akunzai/skills-manager/internal/engine"
	"github.com/akunzai/skills-manager/internal/models"
	"github.com/spf13/cobra"
)

// addListRow is one Skill --list offers: its name, the Source subpath Add
// would declare it from, and its SKILL.md description.
type addListRow struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Description string `json:"description"`
}

// runAddList lists a Source's discoverable Skills without declaring any of
// them: it never writes Config, the Scope skills directory, Scope state, or
// an Agent directory. A remote Source's Cache is still refreshed, exactly as
// Add does (ADR-0004/0005), so a later 'skills add' reuses it.
func runAddList(cmd *cobra.Command, kind engine.AddSourceKind, source, flagPath, flagBranch, flagURL, cacheDir string, jsonOutput bool) error {
	switch kind {
	case engine.AddSourceSymlink:
		return listLocalSkills(cmd, source, flagPath, jsonOutput)
	case engine.AddSourceRemote:
		return listRemoteSkills(cmd, source, flagURL, flagBranch, flagPath, cacheDir, jsonOutput)
	default:
		return fmt.Errorf("--list is not supported for this Source kind")
	}
}

func listLocalSkills(cmd *cobra.Command, localPath, selectionPath string, jsonOutput bool) error {
	sourcePath := models.ExpandUser(localPath)
	absSourcePath, err := filepath.Abs(sourcePath)
	if err != nil {
		absSourcePath = sourcePath
	}
	stat, err := os.Stat(absSourcePath)
	if err != nil || !stat.IsDir() {
		return fmt.Errorf("local source path does not exist or is not a directory: %s", models.ToTildePath(sourcePath))
	}

	discovered, descriptions, err := engine.DiscoverSkillsInRepo(absSourcePath, selectionPath)
	if err != nil {
		return fmt.Errorf("discover skills in %s: %w", models.ToTildePath(sourcePath), err)
	}
	return printAddList(cmd, models.ToTildePath(absSourcePath), discovered, descriptions, jsonOutput)
}

func listRemoteSkills(cmd *cobra.Command, rawSource, flagURL, flagBranch, flagPath, cacheDir string, jsonOutput bool) error {
	intake, key, err := fetchRemoteIntake(cmd, ResolveScope().ConfigPath, rawSource, flagURL, flagBranch, flagPath, cacheDir)
	if err != nil {
		return err
	}
	return printAddList(cmd, key, intake.Discovered, intake.Descriptions, jsonOutput)
}

// printAddList flattens discovered into a stable, name-then-path-sorted row
// list and prints it, following ADR-0002's codes for a read-only browse: 0
// with at least one Skill listed, 1 with none. A discovery or fetch failure
// is reported by the caller's error instead, and defaults to exit code 2.
func printAddList(cmd *cobra.Command, displayName string, discovered engine.DiscoveredSkills, descriptions engine.DiscoveredSkillDescriptions, jsonOutput bool) error {
	var rows []addListRow
	for _, name := range slices.Sorted(maps.Keys(discovered)) {
		paths := slices.Clone(discovered[name])
		slices.Sort(paths)
		for _, path := range paths {
			rows = append(rows, addListRow{Name: name, Path: path, Description: descriptions[path]})
		}
	}

	out := cmd.OutOrStdout()
	switch {
	case jsonOutput:
		data, err := json.MarshalIndent(rows, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(out, string(data))
	case len(rows) == 0:
		fmt.Fprintf(out, "%sNo Skills found in %s.%s\n", colorYellow, displayName, colorReset)
	default:
		fmt.Fprintf(out, "%sSkills in %s:%s\n\n", colorBold, displayName, colorReset)
		for _, row := range rows {
			description := row.Description
			if description == "" {
				description = "(no description)"
			}
			fmt.Fprintf(out, "  %s%s%s %s(%s)%s: %s\n", colorBold, row.Name, colorReset, colorDim, row.Path, colorReset, description)
		}
		fmt.Fprintf(out, "\n%s found.\n", countOf(len(rows), "skill"))
	}

	if len(rows) == 0 {
		return exitError{message: fmt.Sprintf("no Skills found in %s", displayName), code: 1}
	}
	return nil
}
