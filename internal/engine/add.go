package engine

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/models"
)

// AddSourceKind represents the kind of Source being added.
type AddSourceKind string

const (
	AddSourceRemote  AddSourceKind = "remote"
	AddSourceSymlink AddSourceKind = "symlink"
	AddSourceCommand AddSourceKind = "command"
)

// AddSource describes where a Skill is obtained and how it should be configured.
type AddSource struct {
	Kind        AddSourceKind
	Key         string        // repository source key, local directory path, or command
	Remote      *RemoteIntake // the prepared remote Source
	LocalPath   string        // absolute path to local skill directory
	Command     string        // installer command
	Check       string        // command pre-check
	Description string        // description of the skill
}

// AddSourceSpec is the already-parsed CLI facts used to classify an Add Source.
type AddSourceSpec struct {
	Positional string
	Symlink    string
	Command    string
}

// ClassifyAddKind decides the Add Source kind from parsed flags and the
// positional argument. The returned string is what the matching constructor
// consumes: a local path, a command, or a remote Source key.
func ClassifyAddKind(spec AddSourceSpec) (AddSourceKind, string, error) {
	if spec.Symlink != "" || (spec.Command == "" && spec.Positional != "" && isLocalPath(spec.Positional)) {
		return AddSourceSymlink, cmp.Or(spec.Symlink, spec.Positional), nil
	}
	if spec.Command != "" {
		return AddSourceCommand, spec.Command, nil
	}
	if spec.Positional == "" {
		return "", "", fmt.Errorf("source repository or --symlink/--command required")
	}
	return AddSourceRemote, spec.Positional, nil
}

func isLocalPath(raw string) bool {
	if strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "~") ||
		strings.HasPrefix(raw, "./") || strings.HasPrefix(raw, "../") ||
		strings.HasPrefix(raw, `.\`) || strings.HasPrefix(raw, `..\`) {
		return true
	}
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

// AddBranch is the branch a remote Source is declared on in cfg. A
// Source already declared keeps its branch: asking for a different one is
// refused rather than silently re-pointing the Skills already declared from
// it, and asking for none follows the declared one.
func AddBranch(cfg *config.Config, key, requested string) (string, error) {
	repo, declared := cfg.Remote[key]
	if !declared || requested == repo.Branch {
		return requested, nil
	}
	if requested == "" {
		return repo.Branch, nil
	}
	return "", branchConflictError{key: key, declared: repo.Branch, requested: requested}
}

// branchConflictError is AddBranch refusing a branch other than the one a
// Source is declared on: a state for the user to resolve, not a failure.
type branchConflictError struct{ key, declared, requested string }

func (e branchConflictError) Error() string {
	current := "its default branch"
	if e.declared != "" {
		current = fmt.Sprintf("branch %q", e.declared)
	}
	return fmt.Sprintf("Source %s is already declared on %s; remove its Skills with 'skills rm' before adding it on %q", e.key, current, e.requested)
}

func NewRemoteAddSource(intake *RemoteIntake) AddSource {
	return AddSource{
		Kind:   AddSourceRemote,
		Key:    intake.spec.SourceKey,
		Remote: intake,
	}
}

func NewSymlinkAddSource(absPath, description string) AddSource {
	return AddSource{
		Kind:        AddSourceSymlink,
		Key:         absPath,
		LocalPath:   absPath,
		Description: description,
	}
}

func NewCommandAddSource(command, check, description string) AddSource {
	return AddSource{
		Kind:        AddSourceCommand,
		Key:         command,
		Command:     command,
		Check:       check,
		Description: description,
	}
}

func (s AddSource) proposedDisplay(subpath, skillsDir string) string {
	switch s.Kind {
	case AddSourceCommand:
		return fmt.Sprintf("[command] %s", s.Command)
	case AddSourceSymlink:
		p := s.LocalPath
		if subpath != "" && subpath != "." {
			p = filepath.Join(s.LocalPath, filepath.FromSlash(subpath))
		}
		return fmt.Sprintf("[symlink] %s", models.ToTildePath(p))
	default:
		if subpath != "" && subpath != "." {
			return fmt.Sprintf("[remote] %s (%s)", s.Key, subpath)
		}
		return fmt.Sprintf("[remote] %s", s.Key)
	}
}

// AddConflict records an existing skill declaration or filesystem entry that
// will be replaced by the Add plan.
type AddConflict struct {
	Skill       string
	CurrentSrc  string
	ProposedSrc string
}

// AddAvailabilityKind is how ApplyAddPlan writes per-Skill Availability.
type AddAvailabilityKind uint8

const (
	AddAvailabilityPreserve AddAvailabilityKind = iota
	AddAvailabilityFollowDefaults
	AddAvailabilityInclude
	AddAvailabilitySetManaged
)

// AddAvailabilityIntent is the Availability decision for one Add plan.
// Agents is the include list when Kind is Include, or the selected set when
// Kind is SetManaged.
type AddAvailabilityIntent struct {
	Kind   AddAvailabilityKind
	Agents []string
}

// AddPlan is the calculated set of Skills to record in Config, Materialize, and link.
type AddPlan struct {
	Source       AddSource
	Skills       map[string]string // skill name -> subpath
	Conflicts    []AddConflict
	Availability AddAvailabilityIntent
	ConfigPath   string
	SkillsDir    string
}

// BuildAddPlan inspects existing Config and filesystem Inventory to calculate
// skill replacements and build an AddPlan.
func BuildAddPlan(
	cfg *config.Config,
	configPath string,
	skillsDir string,
	source AddSource,
	skills map[string]string,
	availability AddAvailabilityIntent,
) AddPlan {
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	if skillsDir == "" {
		skillsDir = models.DefaultSkillsDir()
	}
	if configPath == "" {
		configPath = models.DefaultConfigFile()
	}

	plan := AddPlan{
		Source:       source,
		Skills:       skills,
		Availability: availability,
		ConfigPath:   configPath,
		SkillsDir:    skillsDir,
	}

	for _, name := range sortedSkillKeys(skills) {
		subpath := skills[name]
		newSrcDisplay := source.proposedDisplay(subpath, skillsDir)
		cat, srcKey, found := config.FindSkillSource(cfg, name)
		if found {
			if cat == "remote" {
				if source.Kind != AddSourceRemote || srcKey != source.Key {
					plan.Conflicts = append(plan.Conflicts, AddConflict{
						Skill:       name,
						CurrentSrc:  fmt.Sprintf("[remote] %s", srcKey),
						ProposedSrc: newSrcDisplay,
					})
				}
			} else if cat == "local" {
				if entry, ok := cfg.Local[name]; ok {
					if entry.Type == "command" {
						if source.Kind != AddSourceCommand || entry.Command != source.Command {
							plan.Conflicts = append(plan.Conflicts, AddConflict{
								Skill:       name,
								CurrentSrc:  fmt.Sprintf("[command] %s", entry.Command),
								ProposedSrc: newSrcDisplay,
							})
						}
					} else {
						localSkillSource := source.LocalPath
						if subpath != "" && subpath != "." {
							localSkillSource = filepath.Join(source.LocalPath, filepath.FromSlash(subpath))
						}
						stored := models.StoreLocalSourcePath(localSkillSource, skillsDir)
						if source.Kind != AddSourceSymlink || (entry.Source != stored && models.ToTildePath(entry.Source) != models.ToTildePath(localSkillSource)) {
							plan.Conflicts = append(plan.Conflicts, AddConflict{
								Skill:       name,
								CurrentSrc:  fmt.Sprintf("[symlink] %s", models.ToTildePath(entry.Source)),
								ProposedSrc: newSrcDisplay,
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
				plan.Conflicts = append(plan.Conflicts, AddConflict{
					Skill:       name,
					CurrentSrc:  current,
					ProposedSrc: newSrcDisplay,
				})
			}
		}
	}
	return plan
}

// AddSkillEvent is emitted during ApplyAddPlan for UI progress reporting:
// once before a Skill is applied, with an empty Outcome, and once after, with
// the Outcome it reached.
type AddSkillEvent struct {
	Name    string
	Subpath string
	Kind    AddSourceKind
	Target  string
	Outcome SyncOutcome
}

// AddResult records the outcome of applying an AddPlan. Every Skill in
// AddedSkills is declared in Config; Blocked and Failed count those that could
// not be applied, as Sync counts them, and Events says why.
type AddResult struct {
	SyncTally
	AddedSkills []string
	ConfigPath  string
	Events      []SyncEvent
	// StateError is why the Scope state could not be read. The Skills are
	// applied but their Baselines are not recorded, which counts as failed.
	StateError string
	// StateWarning is why the Scope state could not be read when the added
	// Skills have no Baseline to record: a warning, not a failure (ADR-0002).
	StateWarning string
}

// ApplyAddPlan records all selected Skills in Config (a remote Source's
// through its RemoteIntake), saves Config,
// Materializes each Skill, and applies Availability. It returns an error only
// when it fails before any Skill is applied; after that, each Skill's outcome
// is in the result and one that fails does not stop the rest.
func ApplyAddPlan(plan AddPlan, cfg *config.Config, onProgress func(AddSkillEvent)) (AddResult, error) {
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	availability := NewAvailability(cfg, plan.SkillsDir)
	names := sortedSkillKeys(plan.Skills)

	resolvedLocal := func(subpath string) string {
		resolved := plan.Source.LocalPath
		if subpath != "" && subpath != "." {
			resolved = filepath.Join(resolved, filepath.FromSlash(subpath))
		}
		return resolved
	}
	if plan.Source.Kind == AddSourceSymlink {
		for _, name := range names {
			resolved := resolvedLocal(plan.Skills[name])
			if models.LocalSourceInsideSkillsDir(resolved, plan.SkillsDir) {
				return AddResult{}, fmt.Errorf("local source %s is inside the skills directory", models.ToTildePath(resolved))
			}
		}
	}

	if plan.Source.Kind == AddSourceRemote {
		if err := plan.Source.Remote.Declare(cfg, plan.Skills); err != nil {
			return AddResult{}, err
		}
	}

	for _, name := range names {
		subpath := plan.Skills[name]
		switch plan.Source.Kind {
		case AddSourceSymlink:
			config.AddLocalSymlinkEntry(cfg, name, models.StoreLocalSourcePath(resolvedLocal(subpath), plan.SkillsDir), plan.Source.Description)
		case AddSourceCommand:
			config.AddLocalCommandEntry(cfg, name, plan.Source.Command, plan.Source.Check, plan.Source.Description)
		}
		switch plan.Availability.Kind {
		case AddAvailabilityFollowDefaults:
			availability.FollowDefaults(name)
		case AddAvailabilityInclude:
			if err := availability.Include(name, plan.Availability.Agents...); err != nil {
				return AddResult{}, err
			}
		case AddAvailabilitySetManaged:
			if err := availability.SetManagedAgents(name, plan.Availability.Agents); err != nil {
				return AddResult{}, err
			}
		}
	}

	if err := config.SaveConfig(cfg, plan.ConfigPath); err != nil {
		return AddResult{}, err
	}

	occupancy := availability.ObserveOccupancy()
	result := AddResult{AddedSkills: names, ConfigPath: plan.ConfigPath}
	emit := func(ev SyncEvent) { result.Events = append(result.Events, ev) }
	baselines := OpenBaselines(plan.SkillsDir)
	for _, name := range names {
		subpath := plan.Skills[name]
		target := ""
		if plan.Source.Kind == AddSourceSymlink {
			target = resolvedLocal(subpath)
		} else if plan.Source.Kind == AddSourceCommand {
			target = plan.Source.Command
		}
		event := AddSkillEvent{
			Name:    name,
			Subpath: subpath,
			Kind:    plan.Source.Kind,
			Target:  target,
		}
		if onProgress != nil {
			onProgress(event)
		}
		var item SyncPlanItem
		switch plan.Source.Kind {
		case AddSourceRemote:
			item = planDeclaredRemoteItem(plan.Source.Key, plan.Source.Remote.cache, SkillFreshness{
				Name:      name,
				Source:    plan.Source.Key,
				Subpath:   subpath,
				ScopePath: filepath.Join(plan.SkillsDir, name),
			}, occupancy.Drift(name))
		case AddSourceSymlink, AddSourceCommand:
			item = planLocalItem(cfg, plan.SkillsDir, occupancy.Drift(name), name)
		}
		outcome := applyItem(availability, plan.SkillsDir, item, SyncDecision{}, baselines, emit)
		result.tally(outcome)
		if onProgress != nil {
			event.Outcome = outcome
			onProgress(event)
		}
	}

	// Only a remote Skill has a Baseline to record.
	switch baselines.Verdict(plan.Source.Kind == AddSourceRemote) {
	case StateFail:
		result.StateError = baselines.Err().Error()
		result.tally(SyncFailed)
	case StateWarn:
		result.StateWarning = baselines.Err().Error()
	}
	return result, nil
}
