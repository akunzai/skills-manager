# Deepen only where the rule lives, not where the wording lives

An architecture review in September 2026 proposed five deepenings. Each shipped in a narrower form, because the wider form failed the deletion test: removing the existing code would have moved complexity rather than concentrated it. The pattern the four rejections share is this. When several commands differ only in how they word or present an outcome, that difference is the product, not duplication. Only the rule underneath belongs in the engine, and only once.

Rejected, with where the narrower form landed:

- **Doctor reports a flat list of findings in place of `DoctorReport`'s fields** (#195). The CLI groups Doctor's lines by Agent and by Skill, so a flat list would only move that grouping into the CLI. Kept instead: one definition of a warning (`DoctorWarningKinds`), counted from the final diagnosis.
- **Add applies through a `SyncPlan` restricted to the added Skills** (#198). Add overwrites what the user already agreed to overwrite, and it records Baselines only for remote Sources. Carrying that into the plan as a Force or "just declared" option would move Add's semantics into Sync rather than remove them. Kept instead: `planDeclaredRemoteItem`, a shared `applyItem`, and one `SyncTally`.
- **One exit-code adapter for every command** (#200). ADR-0002's mapping is three lines; what differs between sync, update, add, doctor, prune, rm, and outdated is their sentences, by design. Kept instead: one `SyncSummary` for the two commands that shared the counts, sync and update.
- **The engine owns prune's whole item list, keys and groups included** (#203). Keys, grouping, and which items start selected are the prompt's presentation. Kept instead: `PrunePlan.Select`, which owns the one safety rule, that removing a real directory needs the user's selection.

A later review the same month proposed one more, rejected on the same test:

- **Freshness classification takes every observed fact, so no status is rewritten after it.** `classifyRemoteSkill` compares Cache, Scope, and Baseline content; `attachScopeObservations` then overrides its answer for a Source never fetched, a subpath the sparse checkout does not cover, an unreadable Scope state, and a Skill gone at the Cache head. Those four need the Cache and Baselines, which the classifier does not hold, and they already sit together in one function that `InspectFreshness`'s tests exercise. Moving them in would add four parameters and move the ordering, not remove it. Kept instead: the overrides where they are.

An October 2026 review proposed one more, rejected because the two sites decide different things:

- **Adopt hands a blocked remote Skill to `applyRemoteItem`, as Sync does.** Sync leaves a blocked Skill untouched: no Availability, no Baseline. Adopt applies Availability and records no Baseline, because the copy it adopts is already the user's and its Agents already read it. A blocked Skill, such as one its Signature does not vouch for (ADR-0010), and a copy that differs from the Cache both end in `AdoptDeclaredWithoutBaseline`. Routing the block through `applyRemoteItem` would drop the Agent links Adopt keeps, so it would change Adopt rather than share a rule. Kept instead: the branches in `adoption.apply`, reading the block from the planned item's `Block`.

A second October 2026 review proposed one more, rejected because the rule already lives in the engine:

- **Update's outcome becomes a `Convergence`, as Doctor's did.** `PreparedUpdate.Finish` already decides Update's five `UpdateState`s and their precedence (a refresh failure, then a Sync failure, then pending Sync, then pending refresh), and `update_run_test.go` tests it. The CLI's switch on `State` only picks Update's sentences and hands the Sync half to `reportSyncOutcome`; a refresh failure exits 2 through `ExitCode`'s default for an uncoded error, which is ADR-0002's mapping, not a coincidence. A `Convergence()` beside `State` would leave that switch in place, so it would add a layer rather than remove one. Kept instead: `UpdateState`, decided in `Finish`.

Revisit when a second frontend, such as a `--json` for doctor or prune, needs the same grouping or wording the CLI now owns. Presentation would then have two adapters, and a shared seam for it would be real.

This does not reopen ADR-0001 (Add's Source kind switch) or ADR-0002 (exit codes express state).
