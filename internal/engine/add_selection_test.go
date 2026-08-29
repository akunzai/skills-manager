package engine

import (
	"reflect"
	"testing"
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
