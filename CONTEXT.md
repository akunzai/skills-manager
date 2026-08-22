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
The declared set of Skills, Sources, Availability, and hooks for one Scope (`skills.json`).
_Avoid_: settings file, manifest (except the JSON Schema)

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
Reconciling filesystem state with declared Config: materialize missing Skills and apply Availability.
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
Refreshing remote Cache to a new commit, then Syncing those Skills.
_Avoid_: upgrade, pull (when you mean this command)
