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

The same review proposed two more, rejected because nothing is decided twice:

- **One consent policy for `--yes` across prune and rm.** prune under `--yes` never calls `PrunePlan.Select`, so no Untracked real directory is approved and apply leaves every one. The CLI's list of skipped directories is therefore exactly `plan.UntrackedDirs`. rm under `--yes` approves the directory because the user named it. The two commands consent to different things by design, and the safety rule stays where #203 put it. Kept instead: `PrunePlan.Select` and `RemovePlan.ApproveUntrackedDirectories`, each read by its own command.
- **Self-update decides its install in the updater rather than in the CLI.** The upgrade command to suggest is `ClassifyExecutablePath`'s package-manager command, or else `skills self-update`. The check and the once-a-day notice both reach it the same way, in two lines, and Self-update has one frontend. Kept instead: the updater's helpers, with the orchestration in `self-update`'s `RunE`.

Two more from those October reviews, rejected because they would scatter a rule rather than gather it:

- **Split `internal/engine/adopt.go` by concern.** It is long, but it is one deep module: `BuildAdoptPlan`, the plan's `Select`, and its apply sit in front of a per-Skill transaction that undoes its moves and its Config change when a step fails before the declaration. Splitting it would spread that transaction across files without making the interface any smaller. Kept instead: one file.
- **One command runner for load, plan, prompt, apply, and report.** Every command orders those steps the same way, but each step's prompt and report is that command's product, as the rejections above already say. A runner would move the order into a framework and leave every step behind as a callback. Kept instead: each command's `RunE`.

A third October 2026 review proposed one more, rejected because only one rule was shared:

- **One Skill bundle module walks a Skill for digesting, copying, and discovery.** `DigestSkillContent`, `CopySkillFolder`, and local discovery's `readSkillBundle` each walk a Skill, but they record different things on purpose: the digest leaves out file modes and writes symlink targets with forward slashes so Scope state is portable; the copy keeps modes and recreates links, with a Windows fallback; local discovery's identity keeps the executable bit and raw link target, as the Cache's tree ID does. A shared walker would take each of those as a callback. What all three must agree on is which files belong to a Skill, and local discovery did not. Kept instead: `skillFileIgnored`, read by all three walkers. Discovery from a Cache still compares candidates by their committed tree ID, which covers committed bytecode too; leaving it out there would mean hashing each tree's files instead, for Sources that rarely commit them.

The same review proposed one more, rejected because the rule already lives in one place:

- **`skills trust` verifies only the named Skills instead of planning a whole Sync.** Whether a Skill can be trusted is Sync's answer about it: a Skill another block stops first, such as Drift, is not one its signature blocks, and the tree to trust is the one `remotePlanner.verify` reaches after Freshness classifies the Skill. A narrower path would repeat that classification and `reconcile`'s order, and could then disagree with the Sync that Materializes the trusted copy; it would save one local digest and occupancy pass for a rarely run command. Kept instead: `PlanTrust` over `PlanSync`.

The same review also said signature policy changes escape Adopt's undo snapshot, which shares each policy through a shallow copy of Config. That did not hold: every change already replaced the policy rather than changing it in place. Config's mutators now share that step in `updateSignature`, and a test of `cloneConfig` pins it.

Revisit when a second frontend, such as a `--json` for doctor or prune, needs the same grouping or wording the CLI now owns. Presentation would then have two adapters, and a shared seam for it would be real.

This does not reopen ADR-0001 (Add's Source kind switch) or ADR-0002 (exit codes express state).
