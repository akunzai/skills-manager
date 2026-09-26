---
name: skills-manager
description: Manage skills across AI agents via the skills CLI. Triggers when adding skills (Git, local symlink, or CLI command), reconciling availability or drift (sync, update, outdated, diff), diagnosing health issues (doctor, adopt, prune), or configuring availability policies.
---

# Skills Manager

A control point for skills shared across AI agents (Claude Code, Google Antigravity, GitHub Copilot CLI, OpenAI Codex, etc.). Manages central skill materialization and per-agent availability through a single source of truth (`skills.json`).

## Core Invariants

- **Update then Sync**: `update` fetches remote sources into the shared cache, then syncs the Scope; `sync` alone reconciles Scope skills from cache offline.
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

Prefer `-p` over `--project` for project-scoped commands. Commit `./.agents/skills.json` to share declared skills with teammates.

Commit `./.agents/skills/` for zero-install onboarding **only when every declared skill comes from a remote Source**: those are materialized as real directories that commit cleanly. A local symlink Source points at a path on one developer's machine, so it does not belong in version control — and a git client that cannot create symbolic links (the default on Windows) checks the committed link out as a text file holding its target path, which the agent then reads as the skill. `skills -p doctor` reports that stub and names the way out.

## Automation Rules for Agents

When calling `skills` in automated scripts or tool calls:
- **Pass `-y` / `--yes`**: Suppresses interactive prompts so commands run deterministically without blocking on stdin.
- **Specify skills explicitly on `add`**: Use `--skill <name>` (or `--all`).
- **Resolve ambiguous repository paths**: If a repository contains duplicate skill names across subdirectories, pass `--path <subpath>` or append the path (e.g. `owner/repo/skills`).
- **Inspect with `--json`**: `skills ls --json` outputs structured inventory; `skills add <source> --list --json` previews a Source's Skills the same way, before choosing `--skill` names.
- **Non-interactive Source Replacement**: When overwriting or migrating an existing skill (e.g. from a remote Git repository to a local CLI command), pass `-y` to automatically accept the replacement plan.
- **Self-update only on request**: Run `skills self-update` only when the human explicitly asks. A TTY notice that a newer release exists is not a request. A Homebrew or Scoop install refuses self-update and names that package manager's upgrade command instead.

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

# Preview a Source's Skills (name, subpath, description) without declaring any
skills add microsoft/azure-skills --path skills --list --json

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

**Pinning a branch or tag**: `skills add <source> --branch <branch-or-tag>` declares the branch in Config, and `update` keeps following it. A Source already declared on another branch is refused; `skills rm` its Skills first.

**Source Replacement & Migration**: If a skill with the same name already exists in Config (or Scope), `skills add` plans a replacement. Pass `-y` to accept the conflict non-interactively; `skills-manager` automatically cleans up the old registration, records the new entry, and reconciles agent availability.

**Completion criterion**: Run `skills ls` (or `skills -p ls`) and verify the skill appears with status `Installed` and expected availability.

### 2. Inspect and Reconcile (`skills outdated` -> `skills update`)

Keep installed skills aligned with remote sources and local configuration.

```sh
# Step 1: Check remote freshness (Exit 0 = current, 1 = outdated/differences found)
skills outdated

# Step 2: Preview the refresh and the reconciliation plan without writing (Exit 0 = synced, 1 = pending work)
skills update --dry-run

# Step 3: Fetch remote changes into the shared Cache, then materialize skills and apply availability
skills update

# Reconcile from the existing Cache only (offline operation)
skills sync

# Project Scope (workspace reconciliation):
skills -p update
```

**Review before updating**: `skills diff <skill>` prints two unified diffs for a remote skill: Upstream (the commit its copy was applied from -> the Cache) and Local (that applied content -> the Scope copy). It is read-only and offline; `--fetch` refreshes the skill's Source into the Cache first without syncing, and `--stat` lists changed files only. Exit `0` = no differences, `1` = differences, `2` = unknown, local, command, or uncached skill. Without a recorded baseline it shows only Upstream, comparing the Scope copy with the Cache.

```sh
skills diff <skill>
skills diff --fetch --stat <skill>
```

**Protected drift**: If local files inside a materialized skill were modified, `skills sync` leaves them untouched and reports them as `Blocked` (exit code `1`). Inspect them with `skills diff <skill>`. To intentionally discard local modifications and overwrite from cache:
```sh
skills sync --force
```

**Renamed upstream**: When a Source renames a Skill and the new `SKILL.md` declares `metadata: {replaces: <old name>}`, `skills update` finds the rename and its Sync migrates it: Config names the new Skill, the new Skill is synced, and the old copy is removed. An edited old copy blocks the rename exactly like protected drift. A Skill removed upstream with no replacement stays blocked; resolve it with `skills rm <name>`, never `--force`.

**Completion criterion**: `skills update` or `skills sync` (with `-p` for a Project) exits `0` (converged).

### 3. Diagnose and Repair Health (`skills doctor`, `skills adopt` & `skills prune`)

Identify and repair broken symlinks, orphaned files, or untracked skills.

```sh
# Diagnose Scope integrity and agent availability health (Exit 0 = healthy, 1 = findings)
skills doctor

# Automatically repair diagnosed issues and rebuild corrupted cache
skills doctor --fix

# Declare untracked skills instead of removing them (preview first)
skills adopt --dry-run
skills adopt --all --yes

# Preview untracked skills and orphaned agent links
skills prune --dry-run

# Remove untracked skills and obsolete links
skills prune --yes

# Project Scope (workspace diagnosis and repair):
skills -p doctor
skills -p doctor --fix
```

**Adopt**: A skill an installer lock file records keeps its place and is declared from that remote Source, with a Baseline only when its copy matches the Source; an edited copy is declared without one, so the next Sync asks before overwriting it. Any other skill, or one that is its own git checkout, moves to `skills-local/<name>` beside the skills directory (or `--to <dir>`) and is declared as a local Source. Exit `0` all adopted, `1` something left for the user, `2` a failure.

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
