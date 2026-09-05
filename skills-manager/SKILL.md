---
name: skills-manager
description: Manage AI agent skills across coding agents via the skills CLI. Triggers when adding skills (Git, local symlink, or CLI command), reconciling availability or drift (sync, update, outdated), diagnosing health issues (doctor, prune), or configuring availability policies.
---

# Skills Manager

A control point for skills shared across coding agents (Claude Code, Google Antigravity, GitHub Copilot CLI, OpenAI Codex, etc.). Manages central skill materialization and per-agent availability through a single source of truth (`skills.json`).

## Core Invariants

- **Two-phase Refresh**: `update` fetches remote sources into local cache; `sync` reconciles Scope skills from cache offline.
- **Availability via Defaults & Symlinks**: Universal agents read the central skills directory directly; non-universal agents receive symlinks per declared policy (`defaultAgents`, `include`, `exclude`).
- **Exit-Code Contract**:
  | Exit Code | Meaning | Agent Action |
  | --- | --- | --- |
  | `0` | Converged: Scope matches Config | Proceed |
  | `1` | Actionable state: drift, blocked overwrite, outdated, or dry-run pending | Inspect stdout for recommended disposition; this is expected state, not a failure |
  | `2` | Execution failure: unreadable config, git error, or disk failure | Inspect error and abort or repair |

## Scope Selection

Choose the active Scope at the beginning of an operation:

| Scope | Flag | Config Path | Skills Directory | Use Case |
| --- | --- | --- | --- | --- |
| **Global** (Default) | (none) / `-g` | `~/.agents/skills.json` | `~/.agents/skills/` | Personal skills shared across all projects |
| **Project** | `-p` / `--project` | `./.agents/skills.json` | `./.agents/skills/` | Team-shared project skills committed to git |

Prefer `-p` over `--project` for project-scoped commands. Commit `./.agents/skills.json` to share declared skills with teammates; optionally commit `./.agents/skills/` for zero-install onboarding.

## Automation Rules for Agents

When calling `skills` in automated scripts or tool calls:
- **Pass `-y` / `--yes`**: Suppresses interactive prompts so commands run deterministically without blocking on stdin.
- **Specify skills explicitly on `add`**: Use `--skill <name>` (or `--all`).
- **Resolve ambiguous repository paths**: If a repository contains duplicate skill names across subdirectories, pass `--path <subpath>` or append the path (e.g. `owner/repo/skills`).
- **Inspect with `--json`**: `skills ls --json` outputs structured inventory.
- **Non-interactive Source Replacement**: When overwriting or migrating an existing skill (e.g. from a remote Git repository to a local CLI command), pass `-y` to automatically accept the replacement plan.

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

# Add a local directory as a live symlink
skills add --symlink ./my-local-skill --yes

# Add with custom agent availability overrides
skills add akunzai/agent-skills --skill agents-md --agent claude,antigravity --yes

# Add from CLI command installer (with prerequisite check)
skills add --command "playwright-cli install --global --skills=agents" --check "which playwright-cli" playwright-cli --yes

# Overwrite or migrate an existing skill to a new source (e.g., remote -> CLI command)
skills add --command "playwright-cli install --global --skills=agents" playwright-cli --yes

# Add to Project scope (workspace installation)
skills -p add akunzai/agent-skills --skill agents-md --yes
skills -p add --command "playwright-cli install --skills=agents" playwright-cli --yes
```

**Source Replacement & Migration**: If a skill with the same name already exists in Config (or Scope), `skills add` plans a replacement. Pass `-y` to accept the conflict non-interactively; `skills-manager` automatically cleans up the old registration, records the new entry, and reconciles agent availability.

**Completion criterion**: Run `skills ls` (or `skills -p ls`) and verify the skill appears with status `Installed` and expected availability.

### 2. Inspect and Reconcile (`skills outdated` -> `skills update` -> `skills sync`)

Keep installed skills aligned with remote sources and local configuration.

```sh
# Step 1: Check remote freshness (Exit 0 = current, 1 = outdated/differences found)
skills outdated

# Step 2: Fetch remote changes into the shared Cache (network operation)
skills update

# Step 3: Preview reconciliation plan without writing to disk (Exit 0 = synced, 1 = pending work)
skills sync --dry-run

# Step 4: Materialize skills and apply availability (offline operation)
skills sync

# Project Scope (workspace reconciliation):
skills -p update
skills -p sync
```

**Protected drift**: If local files inside a materialized skill were modified, `skills sync` leaves them untouched and reports them as `Blocked` (exit code `1`). To intentionally discard local modifications and overwrite from cache:
```sh
skills sync --force
```

**Completion criterion**: `skills sync` (or `skills -p sync`) exits `0` (converged).

### 3. Diagnose and Repair Health (`skills doctor` & `skills prune`)

Identify and repair broken symlinks, orphaned files, or untracked skills.

```sh
# Diagnose Scope integrity and agent availability health (Exit 0 = healthy, 1 = findings)
skills doctor

# Automatically repair diagnosed issues and rebuild corrupted cache
skills doctor --fix

# Preview untracked skills and orphaned agent links
skills prune --dry-run

# Remove untracked skills and obsolete links
skills prune --yes

# Project Scope (workspace diagnosis and repair):
skills -p doctor
skills -p doctor --fix
```

**Completion criterion**: `skills doctor` (or `skills -p doctor`) reports all checks passing and exits `0`.

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

# Project Scope availability overrides:
skills -p agents agents-md include antigravity
```

**Completion criterion**: `skills ls` reflects the target agents under `AGENTS` and `skills doctor` exits `0`.
