# Renames are declared by the publisher

A Source renames a Skill by writing `metadata: {replaces: <old name>}` in the new Skill's `SKILL.md`. The Agent Skills spec permits a string-to-string `metadata` map, so the declaration needs nothing beyond the spec. Update notices that a declared Skill's subpath is absent at the fetched commit. It then checks out every `SKILL.md` (the same `withSkillFiles` pass Add uses) and covers the Skill that declares it replaces the missing one. Freshness derives `renamed` offline from the Cache: trees are present in a blobless clone, and the covered `SKILL.md` is on disk. Sync then moves the declaration to the new name, carrying the Source and any Availability override, Materializes the new Skill, and removes the old copy along with its Availability and baseline.

Because detection only reads the Cache, there is no separate rename record. The Cache is shared, so a Scope whose own Update found its Cache already current still follows a rename another Scope's Update fetched; Update checks for removed subpaths on every Cache it can read, not only the ones it refreshed.

The old copy is protected like any Scope copy. Drift or an unknown baseline blocks the whole rename, and `--force` or the unknown-baseline answer lifts it, so Config never names a Skill Sync did not write. An undeclared directory already occupying the new name refuses the rename whatever the decision, because Sync never overwrites Untracked occupancy. A Skill whose subpath is gone with no replacement is `removed_upstream`, and the advice names `skills rm` rather than `skills update`, which could not help.

Sync writing Config is new. It is limited to applying a Rename, it happens only after the plan is previewed, and Config is saved before the old copy is removed, so an interrupted Sync leaves the new name declared and the next Sync completes it.

Rejected:

- **Heuristic matching** by content similarity or name. A wrong guess Materializes a different Skill under the user's declaration, and only the publisher knows the intent.
- **A user-side mapping in `skills.json`**. It puts the burden on every consumer instead of the one publisher. It may come later as an override for Sources that never declare their renames.
- **The refresh rewriting Config**. A refresh writes the shared Cache but reads only the Scope it runs in, so other Scopes sharing the Cache would never see the rename. The rewrite belongs to Sync, which every Scope runs for itself, through `skills sync` or the Sync that ends `skills update`.
- **Recording renames in the Cache's git config**. That is a second source of truth for something the Cache content already states.

This extends ADR-0004 (sparse partial clone), whose "Sync stays offline" rule is why detection lives in Update. It does not reopen ADR-0005: the new code reaches git only through Cache and the existing sparse-checkout helpers.
