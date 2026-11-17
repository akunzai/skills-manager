package cli

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/akunzai/skills-manager/internal/config"
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
	case engine.AddSourceRemote, engine.AddSourceWellKnown:
		return listRemoteSkills(cmd, engine.RemoteIntakeSpec(kind, source, flagURL, flagBranch, flagPath), cacheDir, jsonOutput)
	default:
		return fmt.Errorf("--list is not supported for this Source kind")
	}
}

func listLocalSkills(cmd *cobra.Command, localPath, selectionPath string, jsonOutput bool) error {
	absSourcePath, displayPath, err := localSourceDir(localPath)
	if err != nil {
		return err
	}

	discovered, descriptions, err := engine.DiscoverSkillsInRepo(absSourcePath, selectionPath)
	if err != nil {
		return fmt.Errorf("discover skills in %s: %w", displayPath, err)
	}
	display := models.ToTildePath(absSourcePath)
	return printAddList(cmd, display, discovered, descriptions, jsonOutput, engine.NoSkillsInDirectory(discovered, display, selectionPath))
}

func listRemoteSkills(cmd *cobra.Command, spec models.ParsedRepoSource, cacheDir string, jsonOutput bool) error {
	cfg, err := config.LoadConfig(ResolveScope(cmd).ConfigPath)
	if err != nil {
		return err
	}
	intake, err := fetchRemoteIntake(cmd, cfg, spec, cacheDir)
	if err != nil {
		return err
	}
	return printAddList(cmd, spec.SourceKey, intake.Discovered, intake.Descriptions, jsonOutput, intake.NoSkills())
}

// printAddList flattens discovered into a stable, name-then-path-sorted row
// list and prints it, following ADR-0002's codes for a read-only browse: 0
// with at least one Skill listed, 1 when none is the Source's state (a
// *engine.NoSkillsError in noSkills), stated on stdout with the list it stands for. A
// discovery or fetch failure is reported by the caller's error instead, and
// defaults to exit code 2.
func printAddList(cmd *cobra.Command, displayName string, discovered engine.DiscoveredSkills, descriptions engine.DiscoveredSkillDescriptions, jsonOutput bool, noSkills error) error {
	rows := []addListRow{}
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
	case noSkills != nil:
		state, next := noSkillsState(noSkills)
		writeState(out, state, next)
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

	if noSkills != nil {
		return exitError{message: noSkills.Error(), code: 1}
	}
	return nil
}
