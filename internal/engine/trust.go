package engine

import (
	"slices"

	"github.com/akunzai/skills-manager/internal/config"
)

// TrustItem is one Skill `skills trust` was asked to trust: the Source it is
// declared from, why its Cache copy does not verify, and the tree id Trusted
// content would record, or why it cannot be trusted and what clears that.
type TrustItem struct {
	Name    string
	Source  string
	Reason  string
	Tree    string
	Refused string
	Next    string
}

// PlanTrust observes the Scope as Sync would and says, for each of names,
// what trusting it would record. Only a Skill its signature blocks can be
// trusted; a missing trust root is not a verdict, and Update clears it.
func PlanTrust(cfg *config.Config, configPath, skillsDir, cacheDir string, names []string) ([]TrustItem, error) {
	plan, err := PlanSync(cfg, configPath, skillsDir, cacheDir)
	if err != nil {
		return nil, err
	}
	items := make([]TrustItem, 0, len(names))
	for _, name := range names {
		item := TrustItem{Name: name}
		i := slices.IndexFunc(plan.Items, func(planned SyncPlanItem) bool { return planned.Name == name })
		switch {
		case i < 0:
			item.Refused = "not declared in Config"
		case plan.Items[i].Kind != config.SkillRemote:
			item.Refused = "not from a remote Source"
		case plan.Items[i].Unverified != "":
			item.Refused = "its content is already trusted"
		case plan.Items[i].Block != SyncBlockSignature:
			item.Refused = "its signature does not block it"
		case plan.Items[i].tree == "":
			item.Refused, item.Next = plan.Items[i].BlockReason, plan.Items[i].BlockNext
		default:
			planned := plan.Items[i]
			item.Source, item.Reason, item.Tree = planned.Source, planned.BlockReason, planned.tree
		}
		items = append(items, item)
	}
	return items, nil
}

// ApplyTrust records the Trusted content of every item PlanTrust did not
// refuse, and saves Config. It Materializes nothing; Sync does.
func ApplyTrust(cfg *config.Config, configPath string, items []TrustItem) error {
	changed := false
	for _, item := range items {
		if item.Refused != "" {
			continue
		}
		config.TrustContent(cfg, item.Source, item.Name, item.Tree)
		changed = true
	}
	if !changed {
		return nil
	}
	return config.SaveConfig(cfg, configPath)
}

// RevokeTrust removes the Trusted content of names and saves Config. It
// returns the names that had any.
func RevokeTrust(cfg *config.Config, configPath string, names []string) ([]string, error) {
	var revoked []string
	for _, name := range names {
		if config.RevokeTrust(cfg, name) {
			revoked = append(revoked, name)
		}
	}
	if len(revoked) == 0 {
		return nil, nil
	}
	return revoked, config.SaveConfig(cfg, configPath)
}
