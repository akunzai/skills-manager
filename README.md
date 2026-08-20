# Skills Manager

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Python: >=3.10](https://img.shields.io/badge/Python->=3.10-blue.svg)](https://www.python.org/)

Global skills manager for AI coding agents (Claude Code, Codex, Cursor, Gemini CLI, Cline, Zed, OpenCode, etc.).

Replaces `npx skills` for global/user-level management with a deterministic `skills.json` configuration, fast Git shallow clone caching, local/CLI tool skill support, and multi-agent symlink synchronization.

---

## Installation

```bash
# One-line installer
curl -fsSL https://raw.githubusercontent.com/akunzai/skills-manager/main/install.sh | bash

# Or install from local clone
./install.sh
```

Packages a standalone executable directly to `~/.local/bin/skills`.

---

## Commands

```bash
# List installed skills (table or JSON)
skills ls
skills ls --json
skills ls -a claude
skills ls -s akunzai
skills ls --source local

# Add remote git skill(s) (interactive selection or specific skills)
skills add akunzai/agent-skills
skills add akunzai/agent-skills --skill tidy-commits
skills add akunzai/agent-skills --all

# Add local symlink skill
skills add --symlink ~/.local/share/terminal-browser/app/skills/default/terminal-browser --skill terminal-browser

# Add CLI command skill
skills add --command "agentsview skills install --harness agents" --check "which agentsview" --skill agentsview-finding-history

# Remove skill(s) (interactive selection or specific skills)
skills rm
skills rm ponytail-review
skills rm tidy-commits --agent claude

# Check for updates in remote skill repositories
skills outdated
skills outdated --json

# Update remote skills to latest versions
skills update
skills update akunzai/agent-skills
skills update triage --force
skills update --dry-run

# Sync and restore all skills from ~/.agents/skills.json
skills sync
skills sync --force     # force re-fetch & re-link
skills sync --prune     # remove untracked skills
skills sync --dry-run

# Health check & auto-repair
skills doctor
skills doctor --fix

# Self-update skills CLI binary to latest release
skills self-update
skills self-update --check
skills self-update --version v0.1.1
skills self-update --dry-run
```

---

## Configuration (`~/.agents/skills.json`)

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "version": 1,
  "settings": {
    "defaultAgents": ["claude"]
  },
  "remote": {
    "akunzai/agent-skills": {
      "type": "github",
      "skills": {
        "agents-md": "skills/agents-md",
        "tidy-commits": "skills/tidy-commits"
      }
    }
  },
  "local": {
    "terminal-browser": {
      "type": "symlink",
      "source": "~/.local/share/terminal-browser/app/skills/default/terminal-browser"
    },
    "agentsview-finding-history": {
      "type": "command",
      "check": "which agentsview",
      "command": "agentsview skills install --harness agents"
    }
  }
}
```
