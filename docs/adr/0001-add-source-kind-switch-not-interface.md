# Keep AddSource as a struct+Kind switch, not a Source interface

`internal/engine/add.go`'s `AddSource` is a struct carrying an `AddSourceKind` enum (`remote`/`symlink`/`command`), switched over in four places (`proposedDisplay`, `BuildAddPlan`'s config-recording, `ApplyAddPlan`'s progress-event target, and its reconcile dispatch). An architecture review proposed replacing this with a `Source` interface, one small adapter per kind, to collapse the four switches into polymorphic dispatch.

**Decision: keep the switch.** Three kinds exist today; a fourth isn't confirmed on the roadmap, so two adapters justify today's seam but a third doesn't yet justify restructuring it. More importantly, `BuildAddPlan`'s conflict detection (`add.go:130-186`) isn't a clean per-kind dispatch: it cross-references the skill's *currently declared* kind and identity (from `config.FindSkillSource`, which reads existing `config.RemoteRepo`/`config.LocalEntry` state) against the *proposed* source's kind and identity. That comparison doesn't decompose onto "one adapter, one method" without either an awkward `conflictsWithExisting(existingKind, existingIdentity string) bool` interface method, or a type-switch over the existing declaration left inside `BuildAddPlan` regardless — in which case the interface wouldn't actually contain that complexity, just relocate the other three switches.

## Considered options

- **Source interface** (`ProposedDisplay`/`Record`/`Reconcile` methods, one adapter per kind): rejected — collapses three of the four switches, but the conflict-detection switch is the one that actually carries the most complexity, and it survives regardless of this change.

## Revisit when

- A fourth `AddSourceKind` is actually proposed, not merely hypothetical.
- The conflict-detection switch in `BuildAddPlan` itself becomes a real pain point independent of this decision.
