# Deepen only where the rule lives, not where the wording lives

An architecture review in September 2026 proposed five deepenings. Each shipped in a narrower form, because the wider form failed the deletion test: removing the existing code would have moved complexity rather than concentrated it. The pattern the four rejections share is this. When several commands differ only in how they word or present an outcome, that difference is the product, not duplication. Only the rule underneath belongs in the engine, and only once.

Rejected, with where the narrower form landed:

- **Doctor reports a flat list of findings in place of `DoctorReport`'s fields** (#195). The CLI groups Doctor's lines by Agent and by Skill, so a flat list would only move that grouping into the CLI. Kept instead: one definition of a warning (`DoctorWarningKinds`), counted from the final diagnosis.
- **Add applies through a `SyncPlan` restricted to the added Skills** (#198). Add overwrites what the user already agreed to overwrite, and it records Baselines only for remote Sources. Carrying that into the plan as a Force or "just declared" option would move Add's semantics into Sync rather than remove them. Kept instead: `planDeclaredRemoteItem`, a shared `applyItem`, and one `SyncTally`.
- **One exit-code adapter for every command** (#200). ADR-0002's mapping is three lines; what differs between sync, update, add, doctor, prune, rm, and outdated is their sentences, by design. Kept instead: one `SyncSummary` for the two commands that shared the counts, sync and update.
- **The engine owns prune's whole item list, keys and groups included** (#203). Keys, grouping, and which items start selected are the prompt's presentation. Kept instead: `PrunePlan.Select`, which owns the one safety rule, that removing a real directory needs the user's selection.

Revisit when a second frontend, such as a `--json` for doctor or prune, needs the same grouping or wording the CLI now owns. Presentation would then have two adapters, and a shared seam for it would be real.

This does not reopen ADR-0001 (Add's Source kind switch) or ADR-0002 (exit codes express state).
