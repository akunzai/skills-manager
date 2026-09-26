package engine

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
)

func TestPrunePlanSelectExpandsMasterSkillsAndKeepsIndividualLinks(t *testing.T) {
	plan := PrunePlan{
		UntrackedSkills: []string{"orphan"},
		Unconfigured: []ManagedAgentPath{
			{Agent: "augment", Skill: "orphan", Path: "/agents/augment/orphan"},
			{Agent: "continue", Skill: "configured", Path: "/agents/continue/configured"},
			{Agent: "cline", Skill: "other", Path: "/agents/cline/other"},
		},
	}

	selected := plan.Select(PruneSelection{Masters: []string{"orphan"}, Links: []string{"/agents/continue/configured"}})

	if got, want := selected.UntrackedSkills, []string{"orphan"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("UntrackedSkills = %v; want %v", got, want)
	}
	if got, want := selected.Unconfigured, []ManagedAgentPath{
		{Agent: "augment", Skill: "orphan", Path: "/agents/augment/orphan"},
		{Agent: "continue", Skill: "configured", Path: "/agents/continue/configured"},
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Unconfigured = %v; want %v", got, want)
	}
}

// Stale Baselines and leftover empty Agent directories are prune items like
// any other: the user keeps the ones selected and drops the rest (#179).
func TestPrunePlanSelectKeepsOnlySelectedBaselinesAndEmptyDirs(t *testing.T) {
	plan := PrunePlan{
		StateSkills:    []string{"gone", "kept"},
		EmptyAgentDirs: []AgentDir{{Name: "jazz", Dir: "/agents/jazz/skills"}, {Name: "crush", Dir: "/agents/crush/skills"}},
	}

	selected := plan.Select(PruneSelection{Baselines: []string{"gone"}, EmptyDirs: []string{"/agents/jazz/skills"}})

	if got, want := selected.StateSkills, []string{"gone"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("StateSkills = %v; want %v", got, want)
	}
	if got, want := selected.EmptyAgentDirs, []AgentDir{{Name: "jazz", Dir: "/agents/jazz/skills"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("EmptyAgentDirs = %#v; want %#v", got, want)
	}
}

// An untracked real directory is the user's own content: selecting it is the
// consent to remove it, and Select is the only way to give that consent.
func TestPrunePlanSelectApprovesOnlySelectedRealDirectories(t *testing.T) {
	plan := PrunePlan{UntrackedDirs: []string{"mine", "theirs"}}

	selected := plan.Select(PruneSelection{Masters: []string{"mine"}})

	if got, want := selected.approvedDirs, []string{"mine"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("approvedDirs = %v; want %v", got, want)
	}
	if len(selected.UntrackedDirs) != 0 || len(selected.UntrackedSkills) != 0 {
		t.Fatalf("UntrackedDirs=%v UntrackedSkills=%v; want the approval alone", selected.UntrackedDirs, selected.UntrackedSkills)
	}
	if selected.Empty() {
		t.Fatal("a plan with an approved directory is not empty")
	}
	if !plan.Select(PruneSelection{}).Empty() {
		t.Fatal("selecting nothing leaves nothing to prune")
	}
}

// Apply removes an untracked real directory only with Select's approval, even
// when a caller lists it among the untracked links, or a link it planned to
// remove was replaced by a real directory since.
func TestApplyPrunePlanNeverRemovesAnUnapprovedRealDirectory(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	for _, name := range []string{"mine", "approved"} {
		mustWriteScopeStateTestFile(t, filepath.Join(skillsDir, name, "SKILL.md"), []byte("# "+name+"\n"))
	}
	source := filepath.Join(project, "elsewhere", "orphan")
	mustWriteScopeStateTestFile(t, filepath.Join(source, "SKILL.md"), []byte("# Orphan\n"))
	if err := os.Symlink(source, filepath.Join(skillsDir, "orphan")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	plan, err := BuildPrunePlan(config.DefaultConfig(), skillsDir, true, false)
	if err != nil {
		t.Fatal(err)
	}
	plan = plan.Select(PruneSelection{Masters: []string{"orphan", "approved"}})
	plan.UntrackedSkills = append(plan.UntrackedSkills, "mine")

	result, err := ApplyPrunePlan(plan, skillsDir)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(skillsDir, "mine", "SKILL.md")); err != nil {
		t.Fatalf("an unapproved real directory was removed: %v", err)
	}
	if got, want := result.SkippedSkills, []string{"mine"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("SkippedSkills = %v; want %v", got, want)
	}
	if got, want := result.RemovedSkills, []string{"orphan", "approved"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("RemovedSkills = %v; want %v", got, want)
	}
	for _, name := range []string{"orphan", "approved"} {
		if _, err := os.Lstat(filepath.Join(skillsDir, name)); !os.IsNotExist(err) {
			t.Fatalf("%s still exists: %v", name, err)
		}
	}
}
