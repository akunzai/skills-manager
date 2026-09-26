package engine

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
)

// In the adopt fixture's Project Scope, claude-code is the default Agent and
// continue is one the defaults do not cover.
const (
	adoptDefaultAgent = "claude-code"
	adoptOtherAgent   = "continue"
)

func (f adoptFixture) agentDir(agent string) string {
	switch agent {
	case adoptDefaultAgent:
		return filepath.Join(f.root, ".claude", "skills")
	case adoptOtherAgent:
		return filepath.Join(f.root, ".continue", "skills")
	}
	panic("unknown fixture agent " + agent)
}

// onAgent writes a real Skill directory straight onto an Agent directory.
func (f adoptFixture) onAgent(t *testing.T, agent, name, content string) string {
	t.Helper()
	dir := filepath.Join(f.agentDir(agent), name)
	mustWriteScopeStateTestFile(t, filepath.Join(dir, "SKILL.md"), []byte(content))
	return dir
}

// symlinkOnAgent places a user symlink to target on an Agent directory.
func (f adoptFixture) symlinkOnAgent(t *testing.T, agent, name, target string) string {
	t.Helper()
	if err := os.MkdirAll(f.agentDir(agent), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(f.agentDir(agent), name)
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	return link
}

func (f adoptFixture) plan(t *testing.T, cfg *config.Config, from string) AdoptPlan {
	t.Helper()
	plan, err := BuildAdoptPlan(cfg, f.scope(), AdoptOptions{LockPath: f.lockPath, From: from})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func (f adoptFixture) saved(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.LoadConfig(f.configPath)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// assertSettled is the Verification every case ends with: Sync would change
// nothing for name, and Doctor finds no issue.
func (f adoptFixture) assertSettled(t *testing.T, name string) {
	t.Helper()
	cfg := f.saved(t)
	plan, err := PlanSync(cfg, f.configPath, f.skillsDir, f.cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range plan.Items {
		if item.Name == name && !item.Drift.Empty() {
			t.Fatalf("sync --dry-run reports Drift for %s: %#v", name, item.Drift)
		}
	}
	if summary := plan.Summary(SyncDecision{}); !summary.Converged() {
		t.Fatalf("sync --dry-run = %#v; want converged", summary)
	}
	outcome, err := NewDoctorWithCache(cfg, f.skillsDir, f.cacheDir).Run(false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Remaining != 0 {
		t.Fatalf("doctor finds %d issue(s): %#v", outcome.Remaining, outcome.Report)
	}
}

func assertManagedLink(t *testing.T, f adoptFixture, agent, name string) {
	t.Helper()
	path := filepath.Join(f.agentDir(agent), name)
	if !isManagedSkillPath(path, name, f.skillsDir) {
		t.Fatalf("%s must now be Availability of %s", path, name)
	}
}

func agentItem(t *testing.T, plan AdoptPlan, name string) AdoptItem {
	t.Helper()
	for _, item := range plan.Items {
		if item.Name == name && item.OnAgentDirectories() {
			return item
		}
	}
	t.Fatalf("plan %#v offers no Agent-directory Skill %s", plan.Items, name)
	return AdoptItem{}
}

func TestAdoptMovesARealDirectoryFromAnAgentDirectoryAndLinksItBack(t *testing.T) {
	f := newAdoptFixture(t)
	f.onAgent(t, adoptDefaultAgent, "mine", "# Mine\n")
	cfg := config.DefaultConfig()

	plan := f.plan(t, cfg, "")
	item := agentItem(t, plan, "mine")
	if item.Action != AdoptMoveLocal || item.Block != "" {
		t.Fatalf("planned %#v; want a move to a local Source", item)
	}
	result := ApplyAdoptPlan(plan, cfg, f.scope())

	assertAdoptStates(t, result, AdoptAdopted)
	moved := filepath.Join(f.root, ".agents", "skills-local", "mine")
	if data, err := os.ReadFile(filepath.Join(moved, "SKILL.md")); err != nil || string(data) != "# Mine\n" {
		t.Fatalf("moved content = %q, %v", data, err)
	}
	if entry := f.saved(t).Local["mine"]; entry.Source != ".agents/skills-local/mine" {
		t.Fatalf("declared = %#v; want the moved directory as a local Source", entry)
	}
	if _, ok := f.saved(t).Settings.Availability["mine"]; ok {
		t.Fatal("a Skill found only where defaults reach needs no override")
	}
	assertManagedLink(t, f, adoptDefaultAgent, "mine")
	f.assertSettled(t, "mine")
}

func TestAdoptDeclaresALockRecordedRealDirectoryFromAnAgentDirectory(t *testing.T) {
	f := newAdoptFixture(t)
	f.onAgent(t, adoptDefaultAgent, "sample", "# Sample\n")
	f.lock(t, map[string]string{"sample": "sample/SKILL.md"})
	cfg := config.DefaultConfig()

	result := f.adopt(t, cfg)

	assertAdoptStates(t, result, AdoptAdopted)
	if got := result.Skills[0]; got.Action != AdoptDeclareRemote {
		t.Fatalf("adopted = %#v; want a remote declaration", got)
	}
	if _, ok := OpenBaselines(f.skillsDir).Applied("sample"); !ok {
		t.Fatal("no Baseline recorded for an identical copy")
	}
	if !isRealDir(filepath.Join(f.skillsDir, "sample")) {
		t.Fatal("the copy must now be a real directory on the skills directory")
	}
	if subpath := f.saved(t).Remote["owner/repo"].Skills["sample"]; subpath != "sample" {
		t.Fatalf("declared subpath = %q; want sample", subpath)
	}
	assertManagedLink(t, f, adoptDefaultAgent, "sample")
	f.assertSettled(t, "sample")
}

func TestAdoptDeclaresAUserSymlinkTargetWithoutMovingIt(t *testing.T) {
	f := newAdoptFixture(t)
	target := filepath.Join(f.root, "src", "linked")
	mustWriteScopeStateTestFile(t, filepath.Join(target, "SKILL.md"), []byte("# Linked\n"))
	f.symlinkOnAgent(t, adoptOtherAgent, "linked", target)
	cfg := config.DefaultConfig()

	plan := f.plan(t, cfg, "")
	if item := agentItem(t, plan, "linked"); item.Action != AdoptLinkSource || item.Block != "" {
		t.Fatalf("planned %#v; want its target declared as a local Source", item)
	}
	result := ApplyAdoptPlan(plan, cfg, f.scope())

	assertAdoptStates(t, result, AdoptAdopted)
	saved := f.saved(t)
	if entry := saved.Local["linked"]; entry.Type != "symlink" || entry.Source != "src/linked" {
		t.Fatalf("declared = %#v; want the symlink's target as a local Source", entry)
	}
	if got := saved.Settings.Availability["linked"].Include; !reflect.DeepEqual(got, []string{adoptOtherAgent}) {
		t.Fatalf("Include = %v; want the Agent it was found under", got)
	}
	if data, err := os.ReadFile(filepath.Join(target, "SKILL.md")); err != nil || string(data) != "# Linked\n" {
		t.Fatalf("the target must stay where it is: %q, %v", data, err)
	}
	assertManagedLink(t, f, adoptOtherAgent, "linked")
	f.assertSettled(t, "linked")
}

func TestAdoptRefusesAUserSymlinkIntoTheSkillsDirectoryOrDangling(t *testing.T) {
	f := newAdoptFixture(t)
	f.untracked(t, "inner", "# Inner\n")
	intoScope := f.symlinkOnAgent(t, adoptDefaultAgent, "alias", filepath.Join(f.skillsDir, "inner"))
	dangling := f.symlinkOnAgent(t, adoptDefaultAgent, "gone", filepath.Join(f.root, "nowhere"))
	cfg := config.DefaultConfig()

	plan := f.plan(t, cfg, "")
	for name, want := range map[string]string{"alias": "inside the skills directory", "gone": "does not exist"} {
		if item := agentItem(t, plan, name); !strings.Contains(item.Block, want) {
			t.Fatalf("%s planned %#v; want it refused as %q", name, item, want)
		}
	}
	result := ApplyAdoptPlan(plan.Select([]string{"alias", "gone"}), cfg, f.scope())

	assertAdoptStates(t, result, AdoptSkipped, AdoptSkipped)
	for _, link := range []string{intoScope, dangling} {
		if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("a refused symlink must stay: %v", err)
		}
	}
	if _, err := os.Stat(f.configPath); !os.IsNotExist(err) {
		t.Fatalf("nothing adopted, so Config must not be written: %v", err)
	}
}

func TestAdoptTakesIdenticalCopiesOnceAndLinksTheOthers(t *testing.T) {
	f := newAdoptFixture(t)
	f.onAgent(t, adoptDefaultAgent, "dup", "# Dup\n")
	f.onAgent(t, adoptOtherAgent, "dup", "# Dup\n")
	cfg := config.DefaultConfig()

	result := f.adopt(t, cfg)

	assertAdoptStates(t, result, AdoptAdopted)
	if got := f.saved(t).Settings.Availability["dup"].Include; !reflect.DeepEqual(got, []string{adoptOtherAgent}) {
		t.Fatalf("Include = %v; want the Agent defaults do not cover", got)
	}
	assertManagedLink(t, f, adoptDefaultAgent, "dup")
	assertManagedLink(t, f, adoptOtherAgent, "dup")
	f.assertSettled(t, "dup")
}

func TestAdoptRefusesDifferingCopiesListingEveryPath(t *testing.T) {
	f := newAdoptFixture(t)
	first := f.onAgent(t, adoptDefaultAgent, "split", "# One\n")
	second := f.onAgent(t, adoptOtherAgent, "split", "# Two\n")
	cfg := config.DefaultConfig()

	plan := f.plan(t, cfg, "")
	item := agentItem(t, plan, "split")
	for _, want := range []string{first, second, "--from"} {
		if !strings.Contains(item.Block, want) {
			t.Fatalf("Block = %q; want it to name %s", item.Block, want)
		}
	}
	result := ApplyAdoptPlan(plan, cfg, f.scope())

	assertAdoptStates(t, result, AdoptSkipped)
	for _, dir := range []string{first, second} {
		if !isRealDir(dir) {
			t.Fatalf("%s must stay where it is", dir)
		}
	}
}

func TestAdoptFromPicksOneCopyAndExcludesTheOthers(t *testing.T) {
	f := newAdoptFixture(t)
	kept := f.onAgent(t, adoptDefaultAgent, "split", "# One\n")
	f.onAgent(t, adoptOtherAgent, "split", "# Two\n")
	cfg := config.DefaultConfig()

	plan := f.plan(t, cfg, "continue")
	result := ApplyAdoptPlan(plan, cfg, f.scope())

	assertAdoptStates(t, result, AdoptAdopted)
	moved := filepath.Join(f.root, ".agents", "skills-local", "split")
	if data, err := os.ReadFile(filepath.Join(moved, "SKILL.md")); err != nil || string(data) != "# Two\n" {
		t.Fatalf("adopted content = %q, %v; want the continue copy", data, err)
	}
	override := f.saved(t).Settings.Availability["split"]
	if !reflect.DeepEqual(override.Include, []string{adoptOtherAgent}) || !reflect.DeepEqual(override.Exclude, []string{adoptDefaultAgent}) {
		t.Fatalf("override = %#v; want continue included and claude-code excluded", override)
	}
	if data, err := os.ReadFile(filepath.Join(kept, "SKILL.md")); err != nil || string(data) != "# One\n" {
		t.Fatalf("the other copy must stay untouched: %q, %v", data, err)
	}
	assertManagedLink(t, f, adoptOtherAgent, "split")
	f.assertSettled(t, "split")

	// The copy left behind on purpose is not offered again.
	if again := f.plan(t, f.saved(t), ""); len(again.Items) != 0 {
		t.Fatalf("plan after --from = %#v; want nothing to adopt", again.Items)
	}
}

func TestAdoptIncludesTheAgentsDefaultsDoNotCover(t *testing.T) {
	f := newAdoptFixture(t)
	f.onAgent(t, adoptOtherAgent, "elsewhere", "# Elsewhere\n")
	cfg := config.DefaultConfig()

	result := f.adopt(t, cfg)

	assertAdoptStates(t, result, AdoptAdopted)
	if got := f.saved(t).Settings.Availability["elsewhere"]; !reflect.DeepEqual(got.Include, []string{adoptOtherAgent}) || len(got.Exclude) != 0 {
		t.Fatalf("override = %#v; want continue included", got)
	}
	assertManagedLink(t, f, adoptOtherAgent, "elsewhere")
	assertManagedLink(t, f, adoptDefaultAgent, "elsewhere")
	f.assertSettled(t, "elsewhere")
}

func TestAdoptNeverOffersAnAgentReservedName(t *testing.T) {
	f := newAdoptFixture(t)
	f.onAgent(t, adoptDefaultAgent, "synced", "# The Agent's own\n")

	if plan := f.plan(t, config.DefaultConfig(), ""); len(plan.Items) != 0 {
		t.Fatalf("plan = %#v; a reserved name is never a candidate", plan.Items)
	}
}

func TestAdoptRefusesANameThatCollidesInTheScope(t *testing.T) {
	f := newAdoptFixture(t)
	f.untracked(t, "both", "# Scope copy\n")
	onAgent := f.onAgent(t, adoptOtherAgent, "both", "# Agent copy\n")
	declaredCopy := f.onAgent(t, adoptOtherAgent, "declared", "# Agent copy\n")
	cfg := config.DefaultConfig()
	config.AddLocalSymlinkEntry(cfg, "declared", filepath.Join(f.root, "src", "declared"), "")

	plan := f.plan(t, cfg, "")
	for name, want := range map[string]string{"both": "Untracked", "declared": "already declared"} {
		if item := agentItem(t, plan, name); !strings.Contains(item.Block, want) {
			t.Fatalf("%s planned %#v; want it refused as %q", name, item, want)
		}
	}
	var agentOnly AdoptPlan
	for _, item := range plan.Items {
		if item.OnAgentDirectories() {
			agentOnly.Items = append(agentOnly.Items, item)
		}
	}
	result := ApplyAdoptPlan(agentOnly, cfg, f.scope())

	assertAdoptStates(t, result, AdoptSkipped, AdoptSkipped)
	for _, dir := range []string{onAgent, declaredCopy} {
		if !isRealDir(dir) {
			t.Fatalf("%s must stay where it is", dir)
		}
	}
}

// A failure before the Skill is declared undoes both moves and the
// Availability overrides saved before the first, so a re-run finds the copy
// where it was and adopts it with the same Availability.
func TestAdoptRestoresTheMovesAndConfigWhenItFailsBeforeDeclaring(t *testing.T) {
	f := newAdoptFixture(t)
	copyPath := f.onAgent(t, adoptOtherAgent, "mine", "# Mine\n")
	// The move directory cannot be created: the run fails after the Skill
	// has left the Agent directory for the skills directory.
	blocker := filepath.Join(f.root, "blocker")
	mustWriteScopeStateTestFile(t, blocker, []byte("a file\n"))
	cfg := config.DefaultConfig()
	plan, err := BuildAdoptPlan(cfg, f.scope(), AdoptOptions{LockPath: f.lockPath, MoveDir: filepath.Join(blocker, "local")})
	if err != nil {
		t.Fatal(err)
	}

	first := ApplyAdoptPlan(plan, cfg, f.scope())

	assertAdoptStates(t, first, AdoptFailed)
	if got := first.Skills[0]; got.Declared || got.Reason == "" {
		t.Fatalf("failed = %#v; want it undeclared, with a reason", got)
	}
	if data, err := os.ReadFile(filepath.Join(copyPath, "SKILL.md")); err != nil || string(data) != "# Mine\n" {
		t.Fatalf("the copy must be back on the Agent directory: %q, %v", data, err)
	}
	if _, err := os.Lstat(filepath.Join(f.skillsDir, "mine")); !os.IsNotExist(err) {
		t.Fatalf("nothing may stay on the skills directory: %v", err)
	}
	if _, ok := cfg.Settings.Availability["mine"]; ok {
		t.Fatal("the Availability override must be rolled back in memory")
	}
	if _, err := os.Stat(f.configPath); !os.IsNotExist(err) {
		t.Fatalf("Config did not exist before, so it must not now: %v", err)
	}

	second := ApplyAdoptPlan(f.plan(t, cfg, ""), cfg, f.scope())

	assertAdoptStates(t, second, AdoptAdopted)
	assertManagedLink(t, f, adoptOtherAgent, "mine")
	f.assertSettled(t, "mine")
}

// Declaring a Skill whose Config cannot be saved undoes its move and leaves
// Config in memory as it was.
func TestAdoptRestoresTheMoveWhenConfigCannotBeSaved(t *testing.T) {
	f := newAdoptFixture(t)
	dir := f.untracked(t, "mine", "# Mine\n")
	blocker := filepath.Join(f.root, "blocker")
	mustWriteScopeStateTestFile(t, blocker, []byte("a file\n"))
	scope := f.scope()
	scope.ConfigPath = filepath.Join(blocker, "skills.json")
	cfg := config.DefaultConfig()
	plan, err := BuildAdoptPlan(cfg, scope, AdoptOptions{LockPath: f.lockPath})
	if err != nil {
		t.Fatal(err)
	}

	result := ApplyAdoptPlan(plan, cfg, scope)

	assertAdoptStates(t, result, AdoptFailed)
	if got := result.Skills[0]; got.Declared || got.Reason == "" {
		t.Fatalf("failed = %#v; want it undeclared, with a reason", got)
	}
	if !isRealDir(dir) {
		t.Fatal("the directory must be moved back onto the skills directory")
	}
	if _, err := os.Lstat(plan.Items[0].MoveTo); !os.IsNotExist(err) {
		t.Fatalf("nothing may stay at the move target: %v", err)
	}
	if _, ok := cfg.Local["mine"]; ok {
		t.Fatal("the declaration must be rolled back in memory")
	}
}

// A copy that changed after the plan is not removed to make room for an
// Availability link. The Skill stays declared and the copy is named.
func TestAdoptKeepsTheDeclarationAndNamesACopyItCouldNotReplace(t *testing.T) {
	f := newAdoptFixture(t)
	f.onAgent(t, adoptDefaultAgent, "dup", "# Dup\n")
	f.onAgent(t, adoptOtherAgent, "dup", "# Dup\n")
	cfg := config.DefaultConfig()
	plan := f.plan(t, cfg, "")
	replaced := agentItem(t, plan, "dup").CopiesWith(AgentCopyReplace)
	if len(replaced) != 1 {
		t.Fatalf("planned %#v; want one copy to replace", plan.Items)
	}
	mustWriteScopeStateTestFile(t, filepath.Join(replaced[0].Path, "SKILL.md"), []byte("# Dup, edited since the plan\n"))

	result := ApplyAdoptPlan(plan, cfg, f.scope())

	assertAdoptStates(t, result, AdoptDeclaredWithCopiesLeft)
	if got := result.Skills[0]; !got.Declared || !reflect.DeepEqual(got.CopiesLeft, []string{replaced[0].Path}) {
		t.Fatalf("outcome = %#v; want it declared with %s left", got, replaced[0].Path)
	}
	if _, ok := f.saved(t).Local["dup"]; !ok {
		t.Fatal("the Skill must stay declared")
	}
	if data, err := os.ReadFile(filepath.Join(replaced[0].Path, "SKILL.md")); err != nil || string(data) != "# Dup, edited since the plan\n" {
		t.Fatalf("the changed copy must stay untouched: %q, %v", data, err)
	}
}

// An Availability link that cannot be made after the Skill is declared keeps
// the declaration: Sync finishes it once the cause is fixed.
func TestAdoptKeepsTheDeclarationWhenAvailabilityFailsAfterIt(t *testing.T) {
	f := newAdoptFixture(t)
	f.untracked(t, "mine", "# Mine\n")
	// The default Agent's directory cannot be created.
	mustWriteScopeStateTestFile(t, filepath.Join(f.root, ".claude"), []byte("a file\n"))
	cfg := config.DefaultConfig()

	result := f.adopt(t, cfg)

	assertAdoptStates(t, result, AdoptFailed)
	if got := result.Skills[0]; !got.Declared || got.Reason == "" {
		t.Fatalf("failed = %#v; want it declared, with a reason", got)
	}
	if entry := f.saved(t).Local["mine"]; entry.Source != ".agents/skills-local/mine" {
		t.Fatalf("declared = %#v; the declaration must stay", entry)
	}
	if !isRealDir(filepath.Join(f.root, ".agents", "skills-local", "mine")) {
		t.Fatal("the moved directory must stay where it is declared")
	}
}
