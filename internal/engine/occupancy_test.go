package engine

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
)

// globalAgentDirs returns an Availability for the global Scope whose defaults
// select agents, so their directories are the configured ones.
func globalAgentDirs(t *testing.T, skillsDir string, agents ...string) *Availability {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Settings.DefaultAgents = agents
	return NewAvailability(cfg, skillsDir)
}

func agentHealthFor(t *testing.T, observation Occupancy, agent string) AgentHealth {
	t.Helper()
	for _, health := range observation.Agents {
		if health.Name == agent {
			return health
		}
	}
	t.Fatalf("no health for %s in %#v", agent, observation.Agents)
	return AgentHealth{}
}

func TestObserveOccupancyClassifiesConfiguredEntries(t *testing.T) {
	home, skillsDir := globalSkillsHome(t, "healthy")
	mustWriteScopeStateTestFile(t, filepath.Join(skillsDir, "alpha", "SKILL.md"), []byte("# Alpha\n"))
	agentDir := filepath.Join(home, ".config", "goose", "skills")

	plantManagedLink(t, skillsDir, agentDir, "healthy")
	// A managed link whose target has been removed is leftover occupancy of
	// the Skill it names, not an Agent directory finding.
	plantManagedLink(t, skillsDir, agentDir, "removed")
	if err := os.Symlink(filepath.Join(home, "elsewhere-gone"), filepath.Join(agentDir, "foreign")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(agentDir, "manual"), 0755); err != nil {
		t.Fatal(err)
	}
	// A copy this tool made is Availability, not a stray directory.
	if err := replaceManagedCopy(filepath.Join(skillsDir, "alpha"), filepath.Join(agentDir, "alpha")); err != nil {
		t.Fatal(err)
	}

	observation := globalAgentDirs(t, skillsDir, "goose").ObserveOccupancy()

	if got, want := agentHealthFor(t, observation, "goose"), (AgentHealth{
		Name:            "goose",
		Dir:             agentDir,
		UnmanagedBroken: []string{"foreign"},
		Physical:        []string{"manual"},
	}); !reflect.DeepEqual(got, want) {
		t.Fatalf("health = %#v; want %#v", got, want)
	}
	var dangling []string
	for _, path := range observation.Leftover.Paths {
		if path.Dangling {
			dangling = append(dangling, path.Skill)
		}
	}
	if !reflect.DeepEqual(dangling, []string{"removed"}) {
		t.Fatalf("dangling leftover = %#v; want [removed]", dangling)
	}
}

// Claude Code downloads the account's claude.ai skills into its own synced
// directory and reserves the name in any capitalization, so it is the
// Agent's: not a stray directory, not leftover occupancy, and content that
// keeps the directory from being empty. Another Agent has no such reservation.
func TestObserveOccupancyLeavesAgentReservedEntriesAlone(t *testing.T) {
	t.Run("not a stray directory", func(t *testing.T) {
		home, skillsDir := globalSkillsHome(t, "alpha")
		claudeDir := filepath.Join(home, ".claude", "skills")
		gooseDir := filepath.Join(home, ".config", "goose", "skills")
		for _, dir := range []string{
			filepath.Join(claudeDir, "Synced", "account"),
			filepath.Join(claudeDir, "manual"),
			filepath.Join(gooseDir, "synced"),
		} {
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
		}

		observation := globalAgentDirs(t, skillsDir, "claude", "goose").ObserveOccupancy()

		if got := agentHealthFor(t, observation, "claude-code").Physical; !reflect.DeepEqual(got, []string{"manual"}) {
			t.Fatalf("claude-code Physical = %#v; want [manual]", got)
		}
		if got := agentHealthFor(t, observation, "goose").Physical; !reflect.DeepEqual(got, []string{"synced"}) {
			t.Fatalf("goose Physical = %#v; want [synced]", got)
		}
	})

	t.Run("not leftover occupancy", func(t *testing.T) {
		home, skillsDir := globalSkillsHome(t, "synced")
		// Claude Code's own synced/ is a real directory it fills itself. A
		// managed link on that name is this tool's leftover (#178).
		if err := os.MkdirAll(filepath.Join(home, ".claude", "skills", "synced", "account"), 0755); err != nil {
			t.Fatal(err)
		}
		gooseLink := plantManagedLink(t, skillsDir, filepath.Join(home, ".config", "goose", "skills"), "synced")

		observation := globalAgentDirs(t, skillsDir, "claude", "goose").ObserveOccupancy()

		if len(observation.Leftover.Paths) != 1 || observation.Leftover.Paths[0].Path != gooseLink {
			t.Fatalf("Leftover = %#v; want only the goose link", observation.Leftover.Paths)
		}
	})

	t.Run("not a link this tool did not make", func(t *testing.T) {
		home, skillsDir := globalSkillsHome(t, "synced")
		claudeDir := filepath.Join(home, ".claude", "skills")
		if err := os.MkdirAll(claudeDir, 0755); err != nil {
			t.Fatal(err)
		}
		// A Synced link pointing outside the skills directory is the
		// Agent's own, whatever its case.
		if err := os.Symlink(filepath.Join(home, "elsewhere"), filepath.Join(claudeDir, "Synced")); err != nil {
			t.Fatal(err)
		}

		observation := globalAgentDirs(t, skillsDir, "claude").ObserveOccupancy()

		if len(observation.Leftover.Paths) != 0 || len(observation.Unexpected) != 0 {
			t.Fatalf("Leftover = %#v, Unexpected = %#v; want neither", observation.Leftover.Paths, observation.Unexpected)
		}
		if got := agentHealthFor(t, observation, "claude-code"); len(got.UnmanagedBroken) != 0 {
			t.Fatalf("claude-code UnmanagedBroken = %#v; the reserved entry is the Agent's own", got.UnmanagedBroken)
		}
	})

	t.Run("not an empty directory", func(t *testing.T) {
		home, skillsDir := globalSkillsHome(t, "alpha")
		if err := os.MkdirAll(filepath.Join(home, ".claude", "skills", "synced"), 0755); err != nil {
			t.Fatal(err)
		}

		observation := globalAgentDirs(t, skillsDir, "goose").ObserveOccupancy()

		for _, dir := range observation.Leftover.Empty {
			if dir.Name == "claude-code" {
				t.Fatalf("Empty = %#v; synced/ is the Agent's own content", observation.Leftover.Empty)
			}
		}
	})
}

// Only known directories the policy does not select are leftover when empty:
// a configured one is in use, and one holding anything but dot entries is not
// empty.
func TestObserveOccupancyReportsOnlyUnselectedEmptyDirs(t *testing.T) {
	home, skillsDir := globalSkillsHome(t, "alpha")
	goose := filepath.Join(home, ".config", "goose", "skills")
	devin := filepath.Join(home, ".config", "devin", "skills")
	hermes := filepath.Join(home, ".hermes", "skills")
	for _, dir := range []string{goose, devin, hermes} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	mustWriteScopeStateTestFile(t, filepath.Join(devin, ".DS_Store"), []byte("x"))
	mustWriteScopeStateTestFile(t, filepath.Join(hermes, "keep"), []byte("x"))

	observation := globalAgentDirs(t, skillsDir, "goose").ObserveOccupancy()

	if want := []AgentDir{{Name: "devin", Dir: devin}}; !reflect.DeepEqual(observation.Leftover.Empty, want) {
		t.Fatalf("Empty = %#v; want %#v", observation.Leftover.Empty, want)
	}
}

func TestObserveOccupancyReportsUnusableConfiguredDirOnly(t *testing.T) {
	home, skillsDir := globalSkillsHome(t, "alpha")
	goose := filepath.Join(home, ".config", "goose", "skills")
	mustWriteScopeStateTestFile(t, goose, []byte("not a directory"))
	// A leftover root is a convention, not a guarantee: unreadable is skipped.
	mustWriteScopeStateTestFile(t, filepath.Join(home, ".codex", "skills"), []byte("not a directory"))

	observation := globalAgentDirs(t, skillsDir, "claude", "goose").ObserveOccupancy()

	if want := []AgentHealth{{Name: "goose", Dir: goose, Unusable: "not a directory"}}; !reflect.DeepEqual(observation.Agents, want) {
		t.Fatalf("Agents = %#v; want only the unusable goose dir (claude's is missing)", observation.Agents)
	}
	if len(observation.Leftover.Paths) != 0 {
		t.Fatalf("Leftover = %#v; want nothing", observation.Leftover.Paths)
	}
}

func TestObserveOccupancyLeftoverReportsDanglingAutomaticallyAvailablePaths(t *testing.T) {
	home, skillsDir := globalSkillsHome(t, "healthy")
	if err := os.MkdirAll(filepath.Join(skillsDir, "healthy"), 0755); err != nil {
		t.Fatal(err)
	}
	agentDir := filepath.Join(home, ".gemini", "skills")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(filepath.Join(skillsDir, "healthy"), filepath.Join(agentDir, "healthy")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(skillsDir, "removed"), filepath.Join(agentDir, "removed")); err != nil {
		t.Fatal(err)
	}

	occupancy := NewAvailability(config.DefaultConfig(), skillsDir).ObserveOccupancy().Leftover
	var dangling, live []string
	for _, path := range occupancy.Paths {
		if path.Agent != "gemini-cli" {
			continue
		}
		if path.Dangling {
			dangling = append(dangling, path.Skill)
		} else {
			live = append(live, path.Skill)
		}
	}
	if len(dangling) != 1 || dangling[0] != "removed" {
		t.Fatalf("dangling = %v; want [removed]", dangling)
	}
	if len(live) != 1 || live[0] != "healthy" {
		t.Fatalf("live = %v; want [healthy]", live)
	}
}

func TestObserveOccupancyLeftoverReportsAutomaticallyAvailableManagedPaths(t *testing.T) {
	availability, _, skillsDir := projectAvailability(t, "sample")
	project := filepath.Dir(filepath.Dir(skillsDir))
	link := plantManagedLink(t, skillsDir, filepath.Join(project, ".codex", "skills"), "sample")

	got := availability.ObserveOccupancy().Leftover

	if len(got.Paths) != 1 {
		t.Fatalf("Paths = %#v; want the Codex leftover", got.Paths)
	}
	path := got.Paths[0]
	if path.Agent != "codex" || path.Skill != "sample" || path.Path != link || path.Dangling {
		t.Fatalf("path = %#v; want a live Codex leftover for sample", path)
	}
}

func TestObserveOccupancyLeftoverReportsUndeclaredSkillManagedPaths(t *testing.T) {
	availability, _, skillsDir := projectAvailability(t, "sample")
	project := filepath.Dir(filepath.Dir(skillsDir))
	link := plantManagedLink(t, skillsDir, filepath.Join(project, ".claude", "skills"), "orphan")

	got := availability.ObserveOccupancy().Leftover

	if len(got.Paths) != 1 || got.Paths[0].Skill != "orphan" || got.Paths[0].Path != link {
		t.Fatalf("Paths = %#v; want the undeclared Claude leftover", got.Paths)
	}
}

func TestObserveOccupancyLeftoverDoesNotReportDeclaredAvailabilityOnLinkableAgents(t *testing.T) {
	availability, _, _ := projectAvailability(t, "sample")
	if _, err := availability.Apply("sample"); err != nil {
		t.Fatal(err)
	}

	got := availability.ObserveOccupancy().Leftover

	if len(got.Paths) != 0 {
		t.Fatalf("Paths = %#v; declared Availability is Drift, not leftover occupancy", got.Paths)
	}
}

// Unexpected is a managed path of a declared Skill, whatever its master's
// state, on a known Agent its Availability does not select. A desired path, an
// undeclared Skill's path (leftover occupancy), an Agent's reserved entry and
// a dot entry are not.
func TestObserveOccupancyReportsUnexpectedPathsOfDeclaredSkills(t *testing.T) {
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	for _, skill := range []string{"sample", "synced", ".hidden", "undeclared"} {
		if err := os.MkdirAll(filepath.Join(skillsDir, skill), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.DefaultConfig()
	cfg.Settings.DefaultAgents = []string{"continue"}
	for _, skill := range []string{"sample", "synced", ".hidden", "missing"} {
		config.AddRemoteSkillEntry(cfg, "owner/repo", skill, skill, "github", "")
	}
	claude := filepath.Join(project, ".claude", "skills")
	continueDir := filepath.Join(project, ".continue", "skills")
	unexpected := plantManagedLink(t, skillsDir, claude, "sample")
	missing := plantManagedLink(t, skillsDir, claude, "missing")
	// Claude Code's own synced/ is a real directory, never a managed path.
	if err := os.MkdirAll(filepath.Join(claude, "synced", "account"), 0o755); err != nil {
		t.Fatal(err)
	}
	plantManagedLink(t, skillsDir, claude, ".hidden")
	plantManagedLink(t, skillsDir, continueDir, "sample")
	undeclared := plantManagedLink(t, skillsDir, claude, "undeclared")

	observation := NewAvailability(cfg, skillsDir).ObserveOccupancy()

	want := []ManagedAgentPath{
		{Agent: "claude-code", Skill: "missing", Path: missing},
		{Agent: "claude-code", Skill: "sample", Path: unexpected},
	}
	if !reflect.DeepEqual(observation.Unexpected, want) {
		t.Fatalf("Unexpected = %#v; want %#v", observation.Unexpected, want)
	}
	if len(observation.Leftover.Paths) != 1 || observation.Leftover.Paths[0].Path != undeclared {
		t.Fatalf("Leftover = %#v; want only the undeclared Skill", observation.Leftover.Paths)
	}
}

func TestObserveOccupancyLeftoverReportsEmptyUnselectedAgentDirs(t *testing.T) {
	availability, _, skillsDir := projectAvailability(t, "sample")
	project := filepath.Dir(filepath.Dir(skillsDir))
	continueDir := filepath.Join(project, ".continue", "skills")
	if err := os.MkdirAll(continueDir, 0o755); err != nil {
		t.Fatal(err)
	}

	got := availability.ObserveOccupancy().Leftover

	if len(got.Empty) == 0 {
		t.Fatal("expected leftover empty Continue dir")
	}
	found := false
	for _, dir := range got.Empty {
		if dir.Name == "continue" && dir.Dir == continueDir {
			found = true
		}
	}
	if !found {
		t.Fatalf("Empty = %#v; want continue at %s", got.Empty, continueDir)
	}
}

func TestRemoveLeftoverSkipsAPathThatChangedSinceTheObservation(t *testing.T) {
	availability, _, skillsDir := projectAvailability(t, "sample")
	project := filepath.Dir(filepath.Dir(skillsDir))
	codexDir := filepath.Join(project, ".codex", "skills")
	link := plantManagedLink(t, skillsDir, codexDir, "sample")
	occupancy := availability.ObserveOccupancy().Leftover
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}

	result := availability.RemoveLeftover(occupancy)

	if skipped, _ := leftoverRepaired(result, RepairSkipped); len(skipped) != 1 || skipped[0].Path != link {
		t.Fatalf("Paths = %#v; want the path that was no longer managed skipped", result.Paths)
	}
	if removed, _ := leftoverRepaired(result, RepairSucceeded); len(removed) != 0 {
		t.Fatalf("removed = %#v; a vanished leftover must not count as removed", removed)
	}
	if occupancy.Paths[0].Repair.Status != RepairNotAttempted {
		t.Fatal("RemoveLeftover wrote its outcome into the observation it was given")
	}
}

func TestRemoveLeftoverRemovesEmptyDirsAndLeftoverPaths(t *testing.T) {
	availability, _, skillsDir := projectAvailability(t, "sample")
	project := filepath.Dir(filepath.Dir(skillsDir))
	codexDir := filepath.Join(project, ".codex", "skills")
	link := plantManagedLink(t, skillsDir, codexDir, "gone")
	continueDir := filepath.Join(project, ".continue", "skills")
	if err := os.MkdirAll(continueDir, 0o755); err != nil {
		t.Fatal(err)
	}

	result := availability.RemoveLeftover(availability.ObserveOccupancy().Leftover)

	removedPaths, removedEmpty := leftoverRepaired(result, RepairSucceeded)
	if len(removedPaths) != 1 || removedPaths[0].Path != link {
		t.Fatalf("removed = %#v; want gone on Codex", removedPaths)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("leftover path still exists: %v", err)
	}
	foundEmpty := false
	for _, dir := range removedEmpty {
		if dir.Name == "continue" {
			foundEmpty = true
		}
	}
	if !foundEmpty {
		t.Fatalf("removed = %#v; want continue", removedEmpty)
	}
}

func TestRemoveLeftoverSkipsEmptyDirThatGainedAnEntry(t *testing.T) {
	availability, _, skillsDir := projectAvailability(t, "sample")
	project := filepath.Dir(filepath.Dir(skillsDir))
	continueDir := filepath.Join(project, ".continue", "skills")
	if err := os.MkdirAll(continueDir, 0o755); err != nil {
		t.Fatal(err)
	}
	occupancy := availability.ObserveOccupancy().Leftover
	if err := os.MkdirAll(filepath.Join(continueDir, "hand-made"), 0o755); err != nil {
		t.Fatal(err)
	}

	result := availability.RemoveLeftover(occupancy)

	if _, removed := leftoverRepaired(result, RepairSucceeded); len(removed) != 0 {
		t.Fatalf("removed = %#v; a directory that is no longer empty was not removed", removed)
	}
	if _, skipped := leftoverRepaired(result, RepairSkipped); len(skipped) != 1 || skipped[0].Dir != continueDir {
		t.Fatalf("skipped = %#v; want continue", skipped)
	}
	if _, err := os.Stat(filepath.Join(continueDir, "hand-made")); err != nil {
		t.Fatalf("hand-made entry: %v", err)
	}
}

func TestLeftoverOccupancyFiltersArePureTransforms(t *testing.T) {
	availability, _, skillsDir := projectAvailability(t, "sample")
	project := filepath.Dir(filepath.Dir(skillsDir))
	plantManagedLink(t, skillsDir, filepath.Join(project, ".codex", "skills"), "sample")
	plantManagedLink(t, skillsDir, filepath.Join(project, ".claude", "skills"), "orphan")
	if err := os.MkdirAll(filepath.Join(project, ".continue", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	full := availability.ObserveOccupancy().Leftover
	if len(full.Paths) != 2 || len(full.Empty) == 0 {
		t.Fatalf("occupancy = %#v; want two paths and empty dirs", full)
	}

	withoutEmpty := full.WithoutEmpty()
	if len(withoutEmpty.Empty) != 0 || len(withoutEmpty.Paths) != 2 {
		t.Fatalf("WithoutEmpty = %#v", withoutEmpty)
	}
	onlyOrphan := full.ForSkills([]string{"orphan"})
	if len(onlyOrphan.Paths) != 1 || onlyOrphan.Paths[0].Skill != "orphan" {
		t.Fatalf("ForSkills = %#v", onlyOrphan)
	}
	if len(onlyOrphan.Empty) != len(full.Empty) {
		t.Fatalf("ForSkills dropped empty dirs: %#v", onlyOrphan.Empty)
	}
}

// Two Agent names can resolve to one directory. A managed path there is
// Leftover occupancy, and the Drift a Sync plan reads agrees with it rather
// than also calling it Unexpected.
func TestObserveOccupancyLeftoverRootWinsOverAKnownAgentSharingIt(t *testing.T) {
	home, skillsDir := globalSkillsHome(t, "alpha")
	t.Setenv("GROK_HOME", filepath.Join(home, ".claude"))
	availability := globalAgentDirs(t, skillsDir, "goose")
	config.AddLocalSymlinkEntry(availability.cfg, "alpha", filepath.Join(skillsDir, "alpha"), "")
	link := plantManagedLink(t, skillsDir, filepath.Join(home, ".claude", "skills"), "alpha")

	occupancy := availability.ObserveOccupancy()

	if len(occupancy.Leftover.Paths) != 1 || occupancy.Leftover.Paths[0].Path != link {
		t.Fatalf("Leftover = %#v; want the shared directory's path", occupancy.Leftover.Paths)
	}
	if len(occupancy.Unexpected) != 0 {
		t.Fatalf("Unexpected = %#v; a leftover path is not also Drift", occupancy.Unexpected)
	}
	if drift := occupancy.Drift("alpha"); len(drift.Unexpected) != 0 {
		t.Fatalf("Drift.Unexpected = %#v; want none", drift.Unexpected)
	}
}

// A real directory on a path Availability selects for a declared Skill is one
// finding: Foreign Drift, which --fix can replace, not also an Unmanaged
// directory.
func TestObserveOccupancyReportsARealDirectoryOnADeclaredPathAsForeignOnly(t *testing.T) {
	home, skillsDir := globalSkillsHome(t, "alpha")
	availability := globalAgentDirs(t, skillsDir, "claude")
	config.AddLocalSymlinkEntry(availability.cfg, "alpha", filepath.Join(skillsDir, "alpha"), "")
	claude := filepath.Join(home, ".claude", "skills")
	for _, name := range []string{"alpha", "hand-made"} {
		if err := os.MkdirAll(filepath.Join(claude, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	occupancy := availability.ObserveOccupancy()

	if got := agentHealthFor(t, occupancy, "claude-code").Physical; !reflect.DeepEqual(got, []string{"hand-made"}) {
		t.Fatalf("Physical = %#v; want only the undeclared directory", got)
	}
	foreign := occupancy.Drift("alpha").Foreign
	if len(foreign) != 1 || foreign[0].Path != filepath.Join(claude, "alpha") || foreign[0].Kind != ForeignAvailabilityDirectory {
		t.Fatalf("Foreign = %#v; want the real directory on alpha's path", foreign)
	}
}

// leftoverRepaired is the paths and empty Agent directories whose Repair has
// status.
func leftoverRepaired(occupancy LeftoverOccupancy, status RepairStatus) ([]LeftoverPath, []AgentDir) {
	var paths []LeftoverPath
	for _, path := range occupancy.Paths {
		if path.Repair.Status == status {
			paths = append(paths, path)
		}
	}
	var empty []AgentDir
	for _, dir := range occupancy.Empty {
		if dir.Repair.Status == status {
			empty = append(empty, dir)
		}
	}
	return paths, empty
}

// Unmanaged is every entry on a linkable Agent directory that this tool did
// not create and declared Availability does not select, configured or not:
// real directories and symlinks the user placed there, dangling or not. What
// the Agent reserves, what this tool manages, a plain file, and a path Drift
// reports as Foreign are not in it.
func TestObserveOccupancyListsUnmanagedPathsOnEveryLinkableAgentDirectory(t *testing.T) {
	home, skillsDir := globalSkillsHome(t, "alpha")
	availability := globalAgentDirs(t, skillsDir, "claude")
	config.AddLocalSymlinkEntry(availability.cfg, "alpha", filepath.Join(skillsDir, "alpha"), "")
	claude := filepath.Join(home, ".claude", "skills")
	goose := filepath.Join(home, ".config", "goose", "skills")
	elsewhere := filepath.Join(home, "src", "linked")
	for _, dir := range []string{
		filepath.Join(claude, "alpha"),   // Foreign Drift of a declared Skill
		filepath.Join(claude, "mine"),    // Unmanaged directory
		filepath.Join(claude, "synced"),  // reserved by the Agent
		filepath.Join(claude, ".hidden"), // not a Skill
		filepath.Join(goose, "alpha"),    // declared, but not selected on goose
		filepath.Join(goose, "theirs"),   // on an Agent directory no policy configures
		elsewhere,
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mustWriteScopeStateTestFile(t, filepath.Join(claude, "notes.txt"), []byte("a file\n"))
	if err := os.Symlink(elsewhere, filepath.Join(claude, "linked")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(filepath.Join(home, "gone"), filepath.Join(claude, "dangling")); err != nil {
		t.Fatal(err)
	}
	plantManagedLink(t, skillsDir, goose, "removed")

	got := availability.ObserveOccupancy().Unmanaged

	want := []UnmanagedAgentPath{
		{Agent: "claude-code", Name: "dangling", Path: filepath.Join(claude, "dangling"), Symlink: true},
		{Agent: "claude-code", Name: "linked", Path: filepath.Join(claude, "linked"), Symlink: true},
		{Agent: "claude-code", Name: "mine", Path: filepath.Join(claude, "mine")},
		{Agent: "goose", Name: "alpha", Path: filepath.Join(goose, "alpha")},
		{Agent: "goose", Name: "theirs", Path: filepath.Join(goose, "theirs")},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Unmanaged = %#v\nwant %#v", got, want)
	}
}
