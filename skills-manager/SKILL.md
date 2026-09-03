---
name: skills-manager
description: Manage skills across AI coding agents (Claude Code, Antigravity, Copilot, Codex) using the skills CLI. Triggers when discovering or adding skills to a project or global scope, migrating skill sources, changing install methods, reconciling drift with sync, refreshing remote sources with outdated or update, diagnosing or fixing health with doctor or prune, or configuring agent availability.
---

# Skills Manager

A control point for skills shared across coding agents (Claude Code, Google Antigravity, GitHub Copilot CLI, OpenAI Codex, etc.). Manages central skill materialization and per-agent availability through a single source of truth (`skills.json`).

## Core Invariants

- **Two-phase Refresh**: `skills update` refreshes remote sources into local cache only; `skills sync` reconciles Scope skills from cache without network access.
- **Availability via Symlinks & Defaults**: Universal agents read the central skills directory directly; other agents receive symlinks matching declared policy (`defaultAgents`, `include`, `exclude`).
- **Exit-Code Contract (ADR 0002)**:
  | Exit Code | Meaning | Action |
  | --- | --- | --- |
  | `0` | Scope matches Config / All current | Proceed |
  | `1` | Actionable state (drift, blocked skill, dry-run pending, outdated) | Read stdout for recommended disposition; this is not an execution failure |
  | `2` | Execution failure (unreadable config, git clone error, disk write failure) | Inspect error and abort or repair |

## Scope Selection

Choose the active Scope at the beginning of an operation:

| Scope | Flag | Config Path | Skills Directory | Use Case |
| --- | --- | --- | --- | --- |
| **Global** (Default) | (none) / `-g` | `~/.agents/skills.json` | `~/.agents/skills/` | Personal skills across all projects |
| **Project** | `-p` / `--project` | `./.agents/skills.json` | `./.agents/skills/` | Team-shared project skills committed to git |

Every operational command accepts `--project` / `-p`.

## Automation Rules for Agents

When calling `skills` in automated scripts or tool calls:
- **Pass `--yes` (`-y`)**: Suppresses interactive prompts so commands run deterministically without blocking on stdin.
- **Specify skills explicitly on `add`**: Use `--skill <name>` (or `--all`).
- **Resolve ambiguous repository paths**: If a repository contains duplicate skill names across subdirectories, pass `--path <subpath>` or append the path (e.g. `owner/repo/skills`).
- **Use `--json` for inspection**: `skills ls --json` outputs structured inventory.
- **Non-interactive Source Replacement**: When overwriting or migrating an existing skill (e.g. from a remote Git repository to a local CLI command), pass `--yes` (`-y`) to automatically accept the replacement plan.

## Configuration Structure (`skills.json`)

`skills.json` maintains three top-level sections:
- `settings`: Agent availability policies (`defaultAgents`, `availability` per-skill overrides).
- `remote`: Map of Git repositories (`<owner>/<repo>`), each with its `type` ("github", "gitlab", "git") and `skills` map (`<skill-name>: <subpath>`).
- `local`: Map of local skills (`<skill-name>`), with `type: "symlink"` (`source` path) or `type: "command"` (`command`, optional `check` command).

---

## Operations & Workflows

### 1. Discover and Add Skills (`skills add`)

Declare selected skills from a Source into the Scope's `skills.json`, materialize them to the skills directory, and apply agent availability.

```sh
# Add from GitHub repository
skills add akunzai/agent-skills --skill agents-md --yes

# Add multiple skills from a repository
skills add akunzai/agent-skills --skill agents-md --skill writing-for-agents --yes

# Add all skills from a specific subpath
skills add microsoft/azure-skills --path skills --all --yes

# Add a local directory as a symlink
skills add ./my-local-skill --yes

# Add with custom agent availability overrides
skills add akunzai/agent-skills --skill agents-md --agent claude,antigravity --yes

# Add from CLI command installer (with optional pre-check)
skills add --command "playwright-cli install --skills=agents" --check "which playwright-cli" playwright-cli --yes

# Overwrite or migrate an existing skill to a new source (e.g., remote -> CLI command)
skills add --command "playwright-cli install --skills=agents" playwright-cli --yes

# Add to Project scope
skills -p add akunzai/agent-skills --skill agents-md --yes
```

**Source Replacement & Migration**: If a skill with the same name already exists in Config (or Scope), `skills add` plans a replacement. Pass `--yes` to accept the conflict non-interactively; `skills-manager` automatically cleans up the old registration (e.g. from `remote`), records the new entry under `local` (or `remote`), and reconciles agent availability.

**Completion criterion**: Run `skills ls` and verify the skill appears in Inventory with state `Current` and matching availability.

### 2. Inspect and Reconcile (`skills outdated` -> `skills update` -> `skills sync`)

Keep installed skills aligned with remote sources and local configuration.

```sh
# Step 1: Check remote freshness (Exit 0 = current, 1 = outdated/differences found)
skills outdated

# Step 2: Fetch remote changes into the shared Cache (network operation)
skills update

# Step 3: Preview reconciliation plan without writing to disk
skills sync --dry-run

# Step 4: Materialize skills and apply availability (offline operation)
skills sync
```

**Protected drift**: If local files inside a materialized skill were modified, `skills sync` leaves them untouched and reports them as `Blocked` (exit code `1`). To intentionally discard local modifications and overwrite from cache:
```sh
skills sync --force
```

**Completion criterion**: `skills sync` exits `0`.

### 3. Diagnose and Repair Health (`skills doctor` & `skills prune`)

Identify and repair broken symlinks, orphaned files, or untracked skills.

```sh
# Diagnose Scope integrity and agent availability health
skills doctor

# Automatically repair diagnosed issues and rebuild corrupted cache
skills doctor --fix

# Preview untracked skills and orphaned agent links
skills prune --dry-run

# Remove untracked skills and obsolete links
skills prune --yes
```

**Completion criterion**: `skills doctor` reports all checks passing and exits `0`.

### 4. Manage Agent Availability (`skills agents` & `skills config`)

Control which agents have access to which skills.

```sh
# Inspect current availability for a skill
skills agents agents-md

# Set per-skill overrides
skills agents agents-md include antigravity
skills agents agents-md exclude claude
skills agents agents-md follow-defaults

# Manage global default agents
skills config
skills config set defaultAgents claude,antigravity
```

**Completion criterion**: `skills agents <skill>` reflects the desired agent list and symlinks in agent directories match.
