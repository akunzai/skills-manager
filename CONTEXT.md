# Skills Manager

A control point for skills shared across coding agents. Users always know where a skill comes from, which Scope owns it, and where it is available.

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
The declared Skills for one Scope plus what is on its skills directory, classified as missing, untracked, or invalid.
_Avoid_: scan, catalog, listing

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
_Avoid_: stale, orphan, leftover, mismatch (when you mean Availability vs disk)

**Sync**:
Reconciling the selected Scope from its Config and existing Cache, without network access: Materialize declared Skills and apply Availability.
_Avoid_: restore, install (when you mean the whole declared state)

**Materialize**:
Putting one Skill from its Source onto the Scope skills directory (copy, symlink, or command).
_Avoid_: install (when you mean the disk write only), checkout, restore

**Cache**:
The cloned git working copy used to Materialize remote Skills.
_Avoid_: vendor, tmp clone

**Agent**:
A coding harness that can load Skills (Claude Code, Copilot, Codex, …).
_Avoid_: harness (in user-facing copy), tool, IDE

**Update**:
Refreshing remote Sources into the shared Cache only. Does not Materialize Skills or apply Availability. This supersedes the pre-0.8.0 definition recorded in #60.
_Avoid_: upgrade, pull (when you mean this command)

**Doctor**:
Diagnosis and optional repair of one Scope's Skill, Agent directory, and Availability health.
_Avoid_: health check (diagnosis only), fixer (repair only)

**Add**:
Declaring selected Skills from one Source in a Scope's Config, then Materializing them and applying Availability.
_Avoid_: install (when you mean the whole Add), import, register
