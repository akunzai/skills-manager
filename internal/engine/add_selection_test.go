package engine

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
)

func TestResolveAddSelectionPreservesAmbiguousCandidates(t *testing.T) {
	discovered := DiscoveredSkills{
		"duplicate": {"plugins/duplicate", "skills/duplicate"},
		"unique":    {"skills/unique"},
	}

	outcome, err := ResolveAddSelection(discovered, AddSelectionRequest{All: true}, AddSelectionAnswers{})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Kind != AddSelectionNeedsPath || outcome.Skill != "duplicate" {
		t.Fatalf("outcome = %#v, want path selection for duplicate", outcome)
	}
	if !reflect.DeepEqual(outcome.Options, []string{"plugins/duplicate", "skills/duplicate"}) {
		t.Fatalf("options = %v", outcome.Options)
	}

	outcome, err = ResolveAddSelection(discovered, AddSelectionRequest{All: true}, AddSelectionAnswers{
		Paths: map[string]string{"duplicate": "skills/duplicate"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"duplicate": "skills/duplicate", "unique": "skills/unique"}
	if outcome.Kind != AddSelectionResolved || !reflect.DeepEqual(outcome.Skills, want) {
		t.Fatalf("outcome = %#v, want resolved %v", outcome, want)
	}
}

func TestResolveAddSelectionRequiresExplicitMultipleSkillChoice(t *testing.T) {
	discovered := DiscoveredSkills{"one": {"skills/one"}, "two": {"skills/two"}}

	outcome, err := ResolveAddSelection(discovered, AddSelectionRequest{}, AddSelectionAnswers{})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Kind != AddSelectionNeedsSkills || !reflect.DeepEqual(outcome.Options, []string{"one", "two"}) {
		t.Fatalf("outcome = %#v", outcome)
	}
	if outcome.Slots != nil {
		t.Fatalf("slots = %v; without an occupancy Scope nothing is inspected", outcome.Slots)
	}

	outcome, err = ResolveAddSelection(discovered, AddSelectionRequest{}, AddSelectionAnswers{Skills: []string{"two"}})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Kind != AddSelectionResolved || !reflect.DeepEqual(outcome.Skills, map[string]string{"two": "skills/two"}) {
		t.Fatalf("outcome = %#v", outcome)
	}
}

func TestResolveAddSelectionRejectsInvalidExplicitRequestAtomically(t *testing.T) {
	discovered := DiscoveredSkills{"one": {"skills/one"}, "two": {"skills/two"}}

	for _, request := range []AddSelectionRequest{
		{All: true, Skills: []string{"one"}},
		{Skills: []string{"one", "missing"}},
	} {
		outcome, err := ResolveAddSelection(discovered, request, AddSelectionAnswers{})
		if err == nil {
			t.Fatalf("request %#v: outcome = %#v, want error", request, outcome)
		}
	}
}

func TestResolveAddSelectionDoesNotRenameSoleSkill(t *testing.T) {
	discovered := DiscoveredSkills{"original": {"."}}

	if _, err := ResolveAddSelection(discovered, AddSelectionRequest{Skills: []string{"renamed"}}, AddSelectionAnswers{}); err == nil {
		t.Fatal("renamed selection succeeded")
	}
}

func TestResolveAddSelectionReturnsTypedCancellation(t *testing.T) {
	wantReason := AddSelectionEmpty
	outcome, err := ResolveAddSelection(
		DiscoveredSkills{"one": {"one"}, "two": {"two"}},
		AddSelectionRequest{},
		AddSelectionAnswers{CancelReason: wantReason},
	)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Kind != AddSelectionCancelled || outcome.CancelReason != wantReason {
		t.Fatalf("outcome = %#v", outcome)
	}
}

func TestResolveAddSelectionTreatsConfirmedEmptySelectionAsCancellation(t *testing.T) {
	outcome, err := ResolveAddSelection(
		DiscoveredSkills{"one": {"one"}, "two": {"two"}},
		AddSelectionRequest{},
		AddSelectionAnswers{Skills: []string{}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Kind != AddSelectionCancelled || outcome.CancelReason != AddSelectionEmpty {
		t.Fatalf("outcome = %#v", outcome)
	}
}

// slotInspectorFixture inspects against a Config that declares alpha as a
// symlink to declaredPath under a local Source, and an empty skills directory.
func slotInspectorFixture(t *testing.T, declaredPath string) *AddSlotInspector {
	t.Helper()
	source := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Local["alpha"] = config.LocalEntry{Type: "symlink", Source: filepath.Join(source, filepath.FromSlash(declaredPath))}
	return &AddSlotInspector{Config: cfg, SkillsDir: t.TempDir(), Source: NewSymlinkAddSource(source, "")}
}

// The Skills the prompt offers carry the occupancy the plan will act on.
func TestResolveAddSelectionMarksEachOfferedSkillsOccupancy(t *testing.T) {
	discovered := DiscoveredSkills{"alpha": {"alpha"}, "beta": {"beta"}}

	outcome, err := ResolveAddSelection(discovered, AddSelectionRequest{Occupancy: slotInspectorFixture(t, "alpha")}, AddSelectionAnswers{})

	if err != nil {
		t.Fatal(err)
	}
	if outcome.Kind != AddSelectionNeedsSkills {
		t.Fatalf("outcome = %#v; want Skill selection", outcome)
	}
	want := map[string]AddOccupancy{"alpha": AddSlotDeclared, "beta": AddSlotFree}
	got := map[string]AddOccupancy{}
	for name, slot := range outcome.Slots {
		got[name] = slot.Occupancy
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("occupancy = %v; want %v", got, want)
	}
}

// A Skill whose Source paths would mark it differently needs its path before
// the prompt can mark it; one whose paths mark it alike is asked after.
func TestResolveAddSelectionAsksForAPathOnlyWhenItChangesTheMark(t *testing.T) {
	for _, tc := range []struct {
		name     string
		declared string
		want     AddSelectionKind
	}{
		{name: "paths mark alike", declared: "elsewhere/alpha", want: AddSelectionNeedsSkills},
		{name: "one path is the declared Source", declared: "plugins/alpha", want: AddSelectionNeedsPath},
	} {
		t.Run(tc.name, func(t *testing.T) {
			discovered := DiscoveredSkills{"alpha": {"alpha", "plugins/alpha"}, "beta": {"beta"}}

			outcome, err := ResolveAddSelection(discovered, AddSelectionRequest{Occupancy: slotInspectorFixture(t, tc.declared)}, AddSelectionAnswers{})

			if err != nil {
				t.Fatal(err)
			}
			if outcome.Kind != tc.want {
				t.Fatalf("outcome = %#v; want kind %d", outcome, tc.want)
			}
			if tc.want == AddSelectionNeedsPath && (outcome.Skill != "alpha" || !reflect.DeepEqual(outcome.Options, []string{"alpha", "plugins/alpha"})) {
				t.Fatalf("outcome = %#v; want alpha's Source paths", outcome)
			}
		})
	}
}

// Once its path is answered, a Skill is marked by that path and not asked
// again; an answer that is not one of its paths is refused.
func TestResolveAddSelectionMarksAnAnsweredPath(t *testing.T) {
	discovered := DiscoveredSkills{"alpha": {"alpha", "plugins/alpha"}, "beta": {"beta"}}
	for _, tc := range []struct {
		name    string
		path    string
		want    AddOccupancy
		wantErr string
	}{
		{name: "the declared Source", path: "plugins/alpha", want: AddSlotDeclared},
		{name: "another path", path: "alpha", want: AddSlotConflict},
		{name: "not a candidate", path: "other/alpha", wantErr: `Source path "other/alpha" is not a candidate for Skill "alpha"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := AddSelectionRequest{Occupancy: slotInspectorFixture(t, "plugins/alpha")}

			outcome, err := ResolveAddSelection(discovered, request, AddSelectionAnswers{Paths: map[string]string{"alpha": tc.path}})

			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("err = %v; want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if outcome.Kind != AddSelectionNeedsSkills || outcome.Slots["alpha"].Occupancy != tc.want {
				t.Fatalf("outcome = %#v; want Skill selection with alpha marked %d", outcome, tc.want)
			}
		})
	}
}
