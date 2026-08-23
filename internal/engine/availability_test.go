package engine

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
)

func TestAvailabilitySetManagedAgentsStoresMinimalOverride(t *testing.T) {
	cfg := config.DefaultConfig()
	availability := NewAvailability(cfg, filepath.Join(t.TempDir(), ".agents", "skills"))

	if err := availability.SetManagedAgents("sample", []string{"continue"}); err != nil {
		t.Fatal(err)
	}
	want := config.AvailabilityOverride{Include: []string{"continue"}, Exclude: []string{"claude-code"}}
	if got := cfg.Settings.Availability["sample"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("override = %#v, want %#v", got, want)
	}
	if got := availability.ManagedAgents("sample"); !reflect.DeepEqual(got, []string{"continue"}) {
		t.Fatalf("managed Agents = %#v", got)
	}
}

func TestAvailabilityMutationsRemainConflictFree(t *testing.T) {
	cfg := config.DefaultConfig()
	availability := NewAvailability(cfg, filepath.Join(t.TempDir(), ".agents", "skills"))
	if err := availability.Exclude("sample", "claude"); err != nil {
		t.Fatal(err)
	}
	if err := availability.Include("sample", "claude", "continue"); err != nil {
		t.Fatal(err)
	}
	override := cfg.Settings.Availability["sample"]
	if !reflect.DeepEqual(override.Include, []string{"claude-code", "continue"}) || len(override.Exclude) != 0 {
		t.Fatalf("override = %#v", override)
	}
	if err := availability.Reset("sample", "claude"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Settings.Availability["sample"].Include, []string{"continue"}) {
		t.Fatalf("reset override = %#v", cfg.Settings.Availability["sample"])
	}
	availability.FollowDefaults("sample")
	if _, ok := cfg.Settings.Availability["sample"]; ok {
		t.Fatal("follow-defaults did not clear override")
	}
}

func TestAvailabilityUnknownAgentReferencesIncludesPolicyFields(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Settings.DefaultAgents = []string{"codex", "mystery"}
	cfg.Settings.Availability["sample"] = config.AvailabilityOverride{Include: []string{"wat"}, Exclude: []string{"ghost"}}
	availability := NewAvailability(cfg, filepath.Join(t.TempDir(), ".agents", "skills"))

	want := []UnknownAgentReference{
		{Field: AgentRefDefaultAgents, Agent: "mystery"},
		{Skill: "sample", Field: AgentRefInclude, Agent: "wat"},
		{Skill: "sample", Field: AgentRefExclude, Agent: "ghost"},
	}
	if got := availability.UnknownAgentReferences(); !reflect.DeepEqual(got, want) {
		t.Fatalf("unknown references = %#v, want %#v", got, want)
	}
}

func TestAvailabilitySetManagedAgentsRejectsAutomaticallyAvailableAgent(t *testing.T) {
	cfg := config.DefaultConfig()
	availability := NewAvailability(cfg, filepath.Join(t.TempDir(), ".agents", "skills"))

	if err := availability.SetManagedAgents("sample", []string{"codex"}); err == nil {
		t.Fatal("Automatically available Agent must not become a managed selection")
	}
}

func TestAvailabilityMutationsRejectUnknownAgent(t *testing.T) {
	cfg := config.DefaultConfig()
	availability := NewAvailability(cfg, filepath.Join(t.TempDir(), ".agents", "skills"))

	if err := availability.Include("sample", "mystery"); err == nil {
		t.Fatal("expected unknown Agent mutation to fail")
	}
	if _, ok := cfg.Settings.Availability["sample"]; ok {
		t.Fatal("failed mutation changed Config")
	}
}
