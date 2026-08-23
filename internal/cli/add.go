package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/engine"
	"github.com/akunzai/skills-manager/internal/models"
	"github.com/akunzai/skills-manager/internal/presentation"
	"github.com/akunzai/skills-manager/internal/tui"
	"github.com/spf13/cobra"
)

const repositoryRootGroup = "Repository root"

func shouldPromptForDiscoveredSkills(skillCount int, interactive, skipConfirmation bool) bool {
	return skillCount > 0 && interactive && !skipConfirmation
}

func selectionSkillsDirs(cmd *cobra.Command) []string {
	if cmd.Flags().Changed("global") || cmd.Flags().Changed("project") || cmd.Flags().Changed("skills-dir") {
		return []string{ResolveScope().SkillsDir}
	}

	dirs := []string{models.DefaultSkillsDir()}
	cwd, err := os.Getwd()
	if err != nil {
		return dirs
	}
	_, projectDir := getProjectPaths(cwd)
	return append(dirs, projectDir)
}

func markInstalledSkills(options []tui.SelectOption, skillsDirs []string) {
	for i := range options {
		for _, skillsDir := range skillsDirs {
			if _, err := os.Stat(filepath.Join(skillsDir, options[i].Key)); err == nil {
				options[i].Installed = true
				options[i].Selected = true
				break
			}
		}
	}
}

func prepareAddTarget(cmd *cobra.Command, skip bool, agents []string) (string, string, *config.Config, []string, error) {
	scope := ResolveScope()
	if tui.IsTerminal() && !skip && !cmd.Flags().Changed("global") && !cmd.Flags().Changed("project") {
		choice, err := tui.PromptSelect("Choose a scope:", []tui.SelectOption{
			{Key: "global", Title: "Global"},
			{Key: "project", Title: "Project"},
		}, 0)
		if err != nil {
			return "", "", nil, nil, err
		}
		if choice == "" {
			return "", "", nil, nil, fmt.Errorf("add cancelled")
		}
		scope = resolveScopeFor(choice == "project")
	}

	configPath, skillsDir := scope.ConfigPath, scope.SkillsDir
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return "", "", nil, nil, err
	}
	if len(agents) > 0 {
		agents, err = validateAgentNames(agents, skillsDir)
		if err != nil {
			return "", "", nil, nil, err
		}
	}
	return configPath, skillsDir, cfg, agents, nil
}

func groupDiscoveredSkills(discovered map[string]string) (tui.GroupedItems, bool) {
	byDirectory := make(map[string][]tui.SelectOption)
	for name, skillPath := range discovered {
		directory := filepath.ToSlash(filepath.Dir(skillPath))
		byDirectory[directory] = append(byDirectory[directory], tui.SelectOption{Key: name, Title: name})
	}
	if len(byDirectory) <= 1 {
		return nil, false
	}

	directories := make([]string, 0, len(byDirectory))
	for directory := range byDirectory {
		directories = append(directories, directory)
	}
	sort.Strings(directories)

	labels := groupLabels(directories)
	groups := make(tui.GroupedItems, len(directories))
	for _, directory := range directories {
		options := byDirectory[directory]
		sort.Slice(options, func(i, j int) bool {
			return options[i].Key < options[j].Key
		})
		groups[labels[directory]] = options
	}

	return groups, true
}

func groupLabels(directories []string) map[string]string {
	depths := make(map[string]int, len(directories))
	for _, directory := range directories {
		depths[directory] = 1
	}

	for {
		labels := make(map[string]string, len(directories))
		duplicates := make(map[string][]string)
		for _, directory := range directories {
			label := repositoryRootGroup
			if directory != "." {
				parts := strings.Split(directory, "/")
				depth := depths[directory]
				if depth > len(parts) {
					depth = len(parts)
				}
				label = strings.Join(parts[len(parts)-depth:], "/")
				if label == repositoryRootGroup {
					label = "./" + label
				}
			}
			labels[directory] = label
			duplicates[label] = append(duplicates[label], directory)
		}

		resolved := true
		for _, directories := range duplicates {
			if len(directories) < 2 {
				continue
			}
			resolved = false
			for _, directory := range directories {
				depths[directory]++
			}
		}
		if resolved {
			return labels
		}
	}
}

func discoveryResult(discovered map[string]string, err error, sourceKey string) (map[string]string, error) {
	if err != nil {
		return nil, fmt.Errorf("discover skills in %s: %w", sourceKey, err)
	}
	if len(discovered) == 0 {
		return nil, fmt.Errorf("no SKILL.md found in %s", sourceKey)
	}
	return discovered, nil
}

func isLocalPath(raw string) bool {
	if strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "~") ||
		strings.HasPrefix(raw, "./") || strings.HasPrefix(raw, "../") ||
		strings.HasPrefix(raw, `.\`) || strings.HasPrefix(raw, `..\`) {
		return true
	}
	// Windows drive letter paths: e.g. C:\foo or C:/foo
	if len(raw) >= 3 && ((raw[0] >= 'a' && raw[0] <= 'z') || (raw[0] >= 'A' && raw[0] <= 'Z')) && raw[1] == ':' && (raw[2] == '/' || raw[2] == '\\') {
		return true
	}
	if !strings.HasPrefix(raw, "git@") && !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") &&
		!strings.HasPrefix(raw, "github:") && !strings.HasPrefix(raw, "gitlab:") {
		expanded := models.ExpandUser(raw)
		if info, err := os.Stat(expanded); err == nil && info.IsDir() {
			return true
		}
	}
	return false
}

type conflictItem struct {
	name       string
	currentSrc string
	newSrc     string
}

type replacementSource struct {
	kind string // "symlink", "remote", "command"
	key  string
	path string
}

func (s replacementSource) display(subpath string) string {
	switch s.kind {
	case "command":
		return fmt.Sprintf("[command] %s", s.key)
	case "symlink":
		p := s.path
		if subpath != "" && subpath != "." {
			p = filepath.Join(s.path, filepath.FromSlash(subpath))
		}
		return fmt.Sprintf("[symlink] %s", models.ToTildePath(p))
	default:
		return fmt.Sprintf("[remote] %s (%s)", s.key, subpath)
	}
}

func confirmSkillReplacements(
	cfg *config.Config,
	skillsDir string,
	skillsToInstall map[string]string,
	src replacementSource,
	flagYes bool,
	out io.Writer,
) error {
	var conflicts []conflictItem
	for name, subpath := range skillsToInstall {
		newSrcDisplay := src.display(subpath)
		localSkillSource := src.path
		if src.kind == "symlink" && subpath != "" && subpath != "." {
			localSkillSource = filepath.Join(src.path, filepath.FromSlash(subpath))
		}
		isLocal := src.kind == "symlink"

		cat, srcKey, found := config.FindSkillSource(cfg, name)
		if found {
			if cat == "remote" {
				if src.kind != "remote" || srcKey != src.key {
					conflicts = append(conflicts, conflictItem{
						name:       name,
						currentSrc: fmt.Sprintf("[remote] %s", srcKey),
						newSrc:     newSrcDisplay,
					})
				}
			} else if cat == "local" {
				if entry, ok := cfg.Local[name]; ok {
					if entry.Type == "command" {
						if src.kind != "command" || entry.Command != src.key {
							conflicts = append(conflicts, conflictItem{
								name:       name,
								currentSrc: fmt.Sprintf("[command] %s", entry.Command),
								newSrc:     newSrcDisplay,
							})
						}
					} else {
						if !isLocal || (entry.Source != models.StoreLocalSourcePath(localSkillSource, skillsDir) && models.ToTildePath(entry.Source) != models.ToTildePath(localSkillSource)) {
							conflicts = append(conflicts, conflictItem{
								name:       name,
								currentSrc: fmt.Sprintf("[symlink] %s", models.ToTildePath(entry.Source)),
								newSrc:     newSrcDisplay,
							})
						}
					}
				}
			}
		} else {
			targetPath := filepath.Join(skillsDir, name)
			if fi, err := os.Lstat(targetPath); err == nil {
				current := "[untracked directory]"
				if fi.Mode()&os.ModeSymlink != 0 {
					if target, err := os.Readlink(targetPath); err == nil {
						current = fmt.Sprintf("[symlink] %s", models.ToTildePath(target))
					} else {
						current = "[symlink]"
					}
				}
				conflicts = append(conflicts, conflictItem{
					name:       name,
					currentSrc: current,
					newSrc:     newSrcDisplay,
				})
			}
		}
	}

	if len(conflicts) > 0 && !flagYes {
		if !tui.IsTerminal() {
			return fmt.Errorf("refusing to overwrite %d existing skill(s) without a terminal; rerun with --yes", len(conflicts))
		}
		sort.Slice(conflicts, func(i, j int) bool {
			return conflicts[i].name < conflicts[j].name
		})
		fmt.Fprintf(out, "\n%sWarning: The following %d skill(s) already exist and will be overwritten:%s\n", colorYellow, len(conflicts), colorReset)
		for _, c := range conflicts {
			fmt.Fprintf(out, "  • %s%s%s: %s -> %s\n", colorBold, c.name, colorReset, c.currentSrc, c.newSrc)
		}
		fmt.Fprintln(out)
		confirmed, err := tui.PromptConfirm("Do you want to proceed with overwriting these skills?", false)
		if err != nil {
			return err
		}
		if !confirmed {
			return fmt.Errorf("operation cancelled by user")
		}
	}
	return nil
}

func newAddCmd() *cobra.Command {
	var (
		flagSkills      []string
		flagAll         bool
		flagPath        string
		flagURL         string
		flagBranch      string
		flagAgents      []string
		flagSymlink     string
		flagCommand     string
		flagCheck       string
		flagDescription string
		flagYes         bool
	)

	cmd := &cobra.Command{
		Use:   "add [source] [skills...]",
		Short: "Add a new skill (remote git, local symlink, or CLI command)",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Past flag parsing, every failure below is a runtime problem rather
			// than misuse, so reporting it with a usage dump would mislead.
			cmd.SilenceUsage = true
			if len(args) == 0 && flagSymlink == "" && flagCommand == "" && tui.IsTerminal() && !flagYes {
				source, err := tui.PromptInput("Source repository or local path")
				if err != nil {
					return err
				}
				if source == "" {
					return fmt.Errorf("source is required")
				}
				args = []string{source}
			}
			cacheDir := ResolveScope().CacheDir

			// Positional skills arguments override or append to --skill
			if len(args) > 1 {
				flagSkills = append(flagSkills, args[1:]...)
			}

			// 1. Local Symlink Mode (via --symlink flag or positional local path)
			if flagSymlink != "" || (flagCommand == "" && len(args) > 0 && isLocalPath(args[0])) {
				localPath := flagSymlink
				if localPath == "" {
					localPath = args[0]
				}

				sourcePath := models.ExpandUser(localPath)
				absSourcePath, err := filepath.Abs(sourcePath)
				if err != nil {
					absSourcePath = sourcePath
				}
				stat, err := os.Stat(absSourcePath)
				if err != nil || !stat.IsDir() {
					return fmt.Errorf("local source path does not exist or is not a directory: %s", models.ToTildePath(sourcePath))
				}

				out := cmd.OutOrStdout()
				fmt.Fprintf(out, "%sScanning local directory: %s%s%s...\n", colorCyan, colorBold, models.ToTildePath(absSourcePath), colorReset)

				discovered, err := engine.DiscoverSkillsInRepo(absSourcePath)
				if err != nil {
					return fmt.Errorf("discover skills in %s: %w", models.ToTildePath(sourcePath), err)
				}
				if len(discovered) == 0 {
					if _, err := os.Stat(filepath.Join(absSourcePath, "SKILL.md")); err == nil {
						skillName := engine.ParseSkillNameFromMD(filepath.Join(absSourcePath, "SKILL.md"))
						if skillName == "" {
							skillName = filepath.Base(absSourcePath)
						}
						discovered[skillName] = "."
					} else {
						return fmt.Errorf("no SKILL.md found in %s", models.ToTildePath(sourcePath))
					}
				}

				src := &localInstallSource{absSourcePath: absSourcePath, description: flagDescription}
				return runAddPipeline(cmd, discovered, src, flagAll, flagPath, flagSkills, flagYes, flagAgents)
			}

			// 2. Command Skill Mode
			if flagCommand != "" {
				if len(flagSkills) == 0 && len(args) == 0 {
					cmd.SilenceUsage = false
					return fmt.Errorf("--skill <name> or skill name argument is required when adding a command skill")
				}
				skillName := ""
				if len(flagSkills) > 0 {
					skillName = flagSkills[0]
				} else {
					skillName = args[0]
				}
				src := &commandInstallSource{
					command:     flagCommand,
					check:       flagCheck,
					description: flagDescription,
				}
				return runAddPipeline(cmd, map[string]string{skillName: "."}, src, false, "", []string{skillName}, flagYes, flagAgents)
			}

			// 3. Remote Git Repository Mode
			if len(args) == 0 {
				cmd.SilenceUsage = false
				return fmt.Errorf("source repository or --symlink/--command required")
			}
			rawSource := args[0]

			parsed := models.ParseRepoSource(rawSource)
			sourceKey := parsed.SourceKey
			cloneURL := flagURL
			if cloneURL == "" {
				cloneURL = parsed.URL
			}
			branch := flagBranch
			if branch == "" {
				branch = parsed.Branch
			}
			subpathOverride := flagPath
			if subpathOverride == "" {
				subpathOverride = parsed.Subpath
			}
			repoType := parsed.RepoType

			out := cmd.OutOrStdout()
			errOut := cmd.ErrOrStderr()
			if presentation.For(errOut).Plain {
				fmt.Fprintf(out, "%sFetching Source: %s%s%s...\n", colorCyan, colorBold, sourceKey, colorReset)
			}
			progress := presentation.StartProgress(errOut, "Fetching Source: "+sourceKey+"...")
			repoDir, discovered, err := engine.PrepareRemoteSource(sourceKey, config.RemoteRepo{URL: cloneURL, Branch: branch}, cacheDir)
			progress.Stop()
			if err != nil {
				return err
			}

			discovered, err = discoveryResult(discovered, nil, sourceKey)
			if err != nil {
				return err
			}

			var storedURL string
			if flagURL != "" || repoType != "github" || !strings.HasPrefix(cloneURL, "https://github.com/") {
				storedURL = cloneURL
			}

			src := &remoteInstallSource{repoDir: repoDir, sourceKey: sourceKey, repoType: repoType, storedURL: storedURL}
			return runAddPipeline(cmd, discovered, src, flagAll, subpathOverride, flagSkills, flagYes, flagAgents)
		},
	}

	cmd.Flags().StringSliceVarP(&flagSkills, "skill", "s", nil, "Specific skill name(s)")
	cmd.Flags().BoolVar(&flagAll, "all", false, "Install all skills found in the repository")
	cmd.Flags().StringVar(&flagPath, "path", "", "Relative path within repo")
	cmd.Flags().StringVar(&flagURL, "url", "", "Custom Git clone URL")
	cmd.Flags().StringVar(&flagBranch, "branch", "", "Git branch or tag")
	cmd.Flags().StringSliceVarP(&flagAgents, "agent", "a", nil, "Persistently include agents for added skills")
	cmd.Flags().StringVar(&flagSymlink, "symlink", "", "Path to local skill directory for symlink install")
	cmd.Flags().StringVar(&flagCommand, "command", "", "Command to install the skill")
	cmd.Flags().StringVar(&flagCheck, "check", "", "Command to check before installing command skill")
	cmd.Flags().StringVar(&flagDescription, "description", "", "Description of the skill")
	cmd.Flags().BoolVarP(&flagYes, "yes", "y", false, "Skip confirmation prompts")

	return cmd
}

func promptAddAvailability(cfg *config.Config, skills map[string]string, _, skillsDir string, skip bool, explicitAgents []string) error {
	if skip || len(explicitAgents) > 0 || !tui.IsTerminal() {
		return nil
	}
	choice, err := tui.PromptSelect("Agent availability:", []tui.SelectOption{
		{Key: "defaults", Title: "Follow defaults (recommended)"},
		{Key: "custom", Title: "Customize"},
	}, 0)
	if err != nil {
		return err
	}
	if choice == "" {
		return fmt.Errorf("add cancelled")
	}
	if choice == "defaults" {
		for skill := range skills {
			config.FollowDefaults(cfg, skill)
		}
		return nil
	}

	known := models.GetAgentsForSkillsDir(skillsDir)
	agents := make([]string, 0, len(known))
	for agent := range known {
		agents = append(agents, agent)
	}
	sort.Strings(agents)
	skillNames := make([]string, 0, len(skills))
	for skill := range skills {
		skillNames = append(skillNames, skill)
	}
	sort.Strings(skillNames)
	firstSkill := skillNames[0]
	baseline := make(map[string]struct{})
	for _, agent := range engine.DesiredAgents(firstSkill, cfg, skillsDir) {
		baseline[agent] = struct{}{}
	}
	for _, skill := range skillNames[1:] {
		other := engine.DesiredAgents(skill, cfg, skillsDir)
		if !sameAgentSelection(baseline, other) {
			return fmt.Errorf("selected skills have different availability; configure them individually with skills agents")
		}
	}
	options := make([]tui.SelectOption, 0, len(agents))
	for _, agent := range agents {
		_, selected := baseline[agent]
		options = append(options, tui.SelectOption{Key: agent, Title: agent, Selected: selected})
	}
	selected, err := tui.PromptMultiSelect("Select agents where these skills should be available:", options)
	if err != nil {
		return err
	}
	if selected == nil {
		return fmt.Errorf("operation cancelled by user")
	}
	selectedSet := make(map[string]struct{}, len(selected))
	for _, agent := range selected {
		selectedSet[agent] = struct{}{}
	}
	for skill := range skills {
		config.FollowDefaults(cfg, skill)
		skillBaseline := make(map[string]struct{})
		for _, agent := range engine.DesiredAgents(skill, cfg, skillsDir) {
			skillBaseline[agent] = struct{}{}
		}
		var include, exclude []string
		for _, agent := range agents {
			_, chosen := selectedSet[agent]
			_, defaulted := skillBaseline[agent]
			if chosen && !defaulted {
				include = append(include, agent)
			}
			if !chosen && defaulted {
				exclude = append(exclude, agent)
			}
		}
		if err := config.IncludeSkillAgents(cfg, skill, include...); err != nil {
			return err
		}
		if err := config.ExcludeSkillAgents(cfg, skill, exclude...); err != nil {
			return err
		}
	}
	return nil
}

func sameAgentSelection(want map[string]struct{}, got []string) bool {
	if len(want) != len(got) {
		return false
	}
	for _, agent := range got {
		if _, ok := want[agent]; !ok {
			return false
		}
	}
	return true
}
