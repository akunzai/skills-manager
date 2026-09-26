# Skills Manager

A control point for skills shared across AI agents. Users always know where a skill comes from, which Scope owns it, and where it is available.

## Language

**Skill**:
A named agent instruction bundle whose root contains `SKILL.md`.
_Avoid_: package, plugin, tool

**Source**:
Where a Skill is obtained: a git repository, a local directory, or a command.
_Avoid_: repo (when you mean the Source key), origin, location

**Scope**:
The active Global or Project configuration, its skills directory, and the root that portable local Source paths and prune must not escape.
_Avoid_: mode, environment, workspace

**Config**:
The declared set of Skills, Sources, and Availability for one Scope (`skills.json`).
_Avoid_: settings file, manifest (except the JSON Schema)

**Inventory**:
The declared Skills for one Scope plus what is on its skills directory, classified as missing, untracked, invalid, stub, or illegal-local.
_Avoid_: scan, catalog, listing

**Untracked**:
Occupancy on a Scope skills directory that Config does not declare. A real directory there is occupancy the tool does not manage, not a missing declaration. A leftover symlink on that directory is the same occupancy in another shape.
_Avoid_: orphan (when you mean this occupancy), undeclared (as a noun)

**Stub**:
A declared local-symlink Skill that arrived on the skills directory as a regular file instead of a directory.
_Avoid_: invalid (that is a missing SKILL.md), leftover occupancy

**Illegal-local**:
A declared local symlink whose Source resolves inside the skills directory.
_Avoid_: Untracked, leftover occupancy, Drift

**Availability**:
Where a Skill can be used. Declared by defaults, include, and exclude. Symlinks are not the concept.
_Avoid_: linked agents, dispatch, install targets

**Automatically available**:
Agents that read the central skills directory directly, so they need no per-Skill Availability links.
_Avoid_: universal (in user-facing copy)

**Follow defaults**:
The per-Skill choice to apply the configured default-agent policy instead of include/exclude.
_Avoid_: inherit, default availability

**Include** / **Exclude**:
Persistent per-Skill overrides of which agents get Availability.
_Avoid_: whitelist, blacklist, enable, disable

**Drift**:
A difference between declared Availability and filesystem state.
_Avoid_: stale, orphan, leftover occupancy, mismatch (when you mean Availability vs disk)

**Leftover occupancy**:
On an Agent directory, occupancy this tool's Availability mechanism created (or left behind) that declared Availability does not call for: a managed path on an automatically available Agent, a managed path for a Skill Config does not declare, a managed path on a name the Agent reserves for its own content, or an empty Agent directory the current policy does not select.
_Avoid_: Drift (no declaration to compare), Inventory (wrong directory), stale, orphan

**Unmanaged directory**:
On an Agent directory, a real directory this tool did not create and Config does not declare. The tool leaves it alone and reports it as a warning, not Drift.
_Avoid_: Physical, stray, orphan

**Agent directory occupancy**:
Everything on a Scope's Agent directories, observed once and classified under one rule: Availability paths a declared Skill should have, Unexpected and Foreign paths (Drift), Leftover occupancy, and Unmanaged directories. No path is in two of them. Sync, Doctor, prune, and rm all read this one observation and remove through one step that checks each path again first.
_Avoid_: agent state, agent scan

**Sync**:
Reconciling the selected Scope from its Config and existing Cache, without network access: Materialize declared Skills and apply Availability. Sync writes Config only to apply a Rename.
_Avoid_: restore, install (when you mean the whole declared state)

**Baseline**:
What Sync last applied a remote Skill's copy on the Scope skills directory from: the copy's content digests, its Source, Cache identity and commit. Drift protection compares that copy with its Baseline. Only a remote Skill has one; the Scope state is the file that holds them.
_Avoid_: snapshot, lock

**Sync plan**:
Every declared Skill of one Scope observed once, with the action each takes and what blocks it. The same plan carries from preview through confirmation to apply, so the user's answer is a pure transformation of it rather than a second observation. A Freshness disposition recommends which command to reach for; a Sync plan decides what happens to each Skill.
_Avoid_: diff, changeset, transaction. An Add of selected Skills from one Source is not a Sync plan.

**Rename**:
A Source's declaration, through `metadata.replaces` in a Skill's `SKILL.md`, that the Skill takes the place of a Skill it no longer has. Update covers the new Skill in the Cache; Sync moves the Scope's declaration to it and removes the old copy.
_Avoid_: move, alias, migration (when you mean the declaration)

**Materialize**:
Putting one Skill from its Source onto the Scope skills directory (copy, symlink, or command).
_Avoid_: install (when you mean the disk write only), checkout, restore

**Cache**:
One remote Source's sparse-partial-clone working copy, from which that Source's Skills are Materialized. Every Scope shares the same Cache for a given Source.
_Avoid_: vendor, tmp clone, cache directory (that is where Caches live, not a Cache)

**Freshness**:
The observed relationship between a remote Source, its Cache, and the Materialized Skill in a Scope. Remote Source observation is optional so Freshness can be evaluated without network access.
_Avoid_: update status, sync status, currentness

**Freshness disposition**:
Recommended next actions derived from one Freshness snapshot. Dispositions are ordered and may coexist: no action, Update, Sync, protect Drift, and investigate an incomplete observation. Each includes a structured reason but does not perform the action or own its presentation.
_Avoid_: command, operation, side effect

**Agent**:
A harness that can load Skills (Claude Code, Copilot, Codex, …).
_Avoid_: harness (in user-facing copy), tool, IDE

**Update**:
Refreshing remote Sources into the shared Cache, including covering the replacement of a Renamed Skill, then Syncing the Scope it runs in. The refresh is the only part that reaches the network. This supersedes the Cache-only definition from #85.
_Avoid_: upgrade, pull (when you mean this command), Self-update

**Self-update**:
Replacement of this CLI's own binary by a newer published release.
_Avoid_: Update, upgrade (when you mean this or the Source command)

**Doctor**:
Diagnosis and optional repair of one Scope's Skill, Agent directory, and Availability health.
_Avoid_: health check (diagnosis only), fixer (repair only)

**Add**:
Declaring selected Skills from one Source in a Scope's Config, then Materializing them and applying Availability.
_Avoid_: install (when you mean the whole Add), import, register

**Remote intake**:
Fetching a remote Source into its Cache, discovering its Skills, and declaring the chosen ones in a Scope's Config. Listing a remote Source's Skills stops before the declaration.
_Avoid_: import, install, fetch

**Adopt**:
Declaring Untracked Skills on a Scope skills directory, and Skills that live directly on its Agent directories, in its Config without losing what is on disk, the inverse of prune. A Skill an installer lock file records is declared from that remote Source where it stands, with a Baseline only when its copy matches the Cache; any other Skill moves beside the skills directory and is declared as a local Source. An Unmanaged directory on an Agent directory first moves onto the skills directory and is then adopted the same way; a user's symlink there has its target declared as a local Source. Either way the Skill stays available to each Agent it was found under, and copies that differ from the one adopted stay where they are, their Agents excluded.
_Avoid_: import, register, claim, take over
