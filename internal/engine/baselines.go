package engine

import (
	"errors"
	"os"
	"reflect"
	"slices"

	"github.com/akunzai/skills-manager/internal/config"
)

// Baselines is one Scope's recorded Baselines: what Sync last applied each
// remote Skill's Scope copy from. Drift protection compares a Scope copy with
// its Baseline, so every command that records, forgets or reads one goes
// through here and shares one rule for a Scope state it cannot read: Err says
// why, recording and forgetting do nothing, and the file is never rewritten.
// Only Reset, which doctor --fix calls, replaces it.
type Baselines struct {
	store *scopeStateStore
	state ScopeState
	err   error
}

// OpenBaselines reads skillsDir's Scope state. It never fails: when the state
// cannot be read, Err reports why and the Baselines are read-only and empty.
func OpenBaselines(skillsDir string) *Baselines {
	store, err := newScopeStateStore(skillsDir)
	if err != nil {
		return &Baselines{err: err}
	}
	state, err := store.Load()
	if err != nil {
		return &Baselines{store: store, err: err}
	}
	return &Baselines{store: store, state: state}
}

// Err is why the Scope state could not be read, or nil.
func (b *Baselines) Err() error { return b.err }

// Applied is the Baseline recorded for name.
func (b *Baselines) Applied(name string) (AppliedSkillState, bool) {
	applied, ok := b.state.Skills[name]
	return applied, ok
}

// CompareScopeCopy compares the Skill's copy on the Scope skills directory
// with its Baseline.
func (b *Baselines) CompareScopeCopy(name, scopePath string) ScopeCopy {
	digests, err := DigestSkillContent(scopePath)
	if errors.Is(err, os.ErrNotExist) || os.IsNotExist(rootPathError(err)) {
		return ScopeCopyAbsent
	}
	applied, _ := b.Applied(name)
	if err != nil || b.err != nil || applied.Source == "" {
		return ScopeCopyUnknown
	}
	if reflect.DeepEqual(digests, applied.ContentDigests) {
		return ScopeCopyClean
	}
	return ScopeCopyDrift
}

// Record makes the Skill's Scope copy, as it is now, its Baseline, applied
// from cacheIdentity at commit.
func (b *Baselines) Record(skill SkillFreshness, cacheIdentity, commit string) error {
	if b.err != nil {
		return nil
	}
	digests, err := DigestSkillContent(skill.ScopePath)
	if err != nil {
		return err
	}
	skill.CacheDigests = digests
	b.state.Skills[skill.Name] = skill.appliedState(cacheIdentity, commit)
	return b.store.Save(b.state)
}

// Forget drops the Baselines of names. A Scope state holding none of them is
// left as it is, so forgetting never creates one.
func (b *Baselines) Forget(names ...string) error {
	if b.err != nil {
		return nil
	}
	forgot := false
	for _, name := range names {
		if _, ok := b.state.Skills[name]; ok {
			delete(b.state.Skills, name)
			forgot = true
		}
	}
	if !forgot {
		return nil
	}
	return b.store.Save(b.state)
}

// Stale names the Baselines whose Skill cfg does not declare as remote,
// sorted. Only a remote Skill has a Baseline, so one now declared local is as
// stale as one not declared at all.
func (b *Baselines) Stale(cfg *config.Config) []string {
	remote := make(map[string]struct{})
	for _, repo := range cfg.Remote {
		for name := range repo.Skills {
			remote[name] = struct{}{}
		}
	}
	var stale []string
	for name := range b.state.Skills {
		if _, ok := remote[name]; !ok {
			stale = append(stale, name)
		}
	}
	slices.Sort(stale)
	return stale
}

// ForgetStale drops every Stale Baseline.
func (b *Baselines) ForgetStale(cfg *config.Config) error {
	return b.Forget(b.Stale(cfg)...)
}

// Reset removes the Scope state, readable or not. It is doctor --fix's repair
// for a state no other command may rewrite.
func (b *Baselines) Reset() error {
	if b.store == nil {
		return b.err
	}
	return b.store.Prune()
}
