package engine

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"

	"github.com/akunzai/skills-manager/internal/config"
)

// Baselines is one Scope's recorded Baselines: what Sync last applied each
// remote Skill's Scope copy from. Drift protection compares a Scope copy with
// its Baseline, so every command that records, forgets or reads one goes
// through here and shares one rule for a Scope state it cannot read: Err says
// why, recording and forgetting report ErrNotRecorded, the file is never
// rewritten, and Verdict says what that means for the command. Only Reset,
// which doctor --fix calls, replaces it.
type Baselines struct {
	store *scopeStateStore
	state ScopeState
	err   error
}

// ErrNotRecorded is what Record and Forget report when the Scope state could
// not be read: nothing was recorded or forgotten, and the file was left as it
// is. The error wraps why the state could not be read.
var ErrNotRecorded = errors.New("nothing recorded in an unreadable Scope state")

// StateVerdict is what the Scope state means for one command (ADR-0002).
type StateVerdict int

const (
	// StateOK is a readable Scope state.
	StateOK StateVerdict = iota
	// StateWarn is an unreadable Scope state the command needed no Baseline
	// from: the Scope can still match its Config, so the command warns and
	// keeps the exit code its other work earns.
	StateWarn
	// StateFail is an unreadable Scope state the command needed to record,
	// compare, or forget a Baseline in: a failure.
	StateFail
)

func (v StateVerdict) String() string {
	return [...]string{"ok", "warn", "fail"}[v]
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

// Verdict is what the Scope state means for a command, given whether it
// touched a Skill that needed a Baseline recorded, compared, or forgotten.
// Each command decides what needed means; only the verdict is decided here.
func (b *Baselines) Verdict(needed bool) StateVerdict {
	switch {
	case b.err == nil:
		return StateOK
	case needed:
		return StateFail
	default:
		return StateWarn
	}
}

// notRecorded is ErrNotRecorded, saying why.
func (b *Baselines) notRecorded() error {
	return fmt.Errorf("%w: %w", ErrNotRecorded, b.err)
}

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
// from cacheIdentity at commit. An unreadable Scope state records nothing
// and reports ErrNotRecorded.
func (b *Baselines) Record(skill SkillFreshness, cacheIdentity, commit string) error {
	if b.err != nil {
		return b.notRecorded()
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
// left as it is, so forgetting never creates one. An unreadable Scope state
// forgets nothing and reports ErrNotRecorded.
func (b *Baselines) Forget(names ...string) error {
	if b.err != nil {
		return b.notRecorded()
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
