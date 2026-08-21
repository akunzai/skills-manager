# Skills Manager

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go: >=1.27](https://img.shields.io/badge/Go->=1.27-00ADD8.svg?logo=go)](https://golang.org/)

Fast, zero-runtime skills manager for AI coding agents (Claude Code, Codex, GitHub Copilot CLI, Antigravity CLI, etc.).

Deterministic `skills.json` configuration, centralized Git shallow clone caching, and automatic multi-agent symlink synchronization.

---

## ⚡ Quick Install

```bash
# macOS / Linux (bash / zsh)
curl -fsSL https://raw.githubusercontent.com/akunzai/skills-manager/main/install.sh | bash

# Windows (PowerShell)
irm https://raw.githubusercontent.com/akunzai/skills-manager/main/install.ps1 | iex
```

---

## 🚀 Quick Start

```bash
# Install skills from GitHub
skills add akunzai/agent-skills

# List installed skills
skills ls

# Check for updates & upgrade
skills outdated
skills update

# Sync / restore declared skills
skills sync
```

---

## 🌐 Global & Project Scopes

| Scope | Flag | Config File | Skills Directory | Agent Symlinks |
| :--- | :--- | :--- | :--- | :--- |
| **Global** (Default) | `-g` / `--global` | `~/.agents/skills.json` | `~/.agents/skills/` | `~/.claude/skills/`, etc. |
| **Project** | `-p` / `--project` | `./.agents/skills.json` | `./.agents/skills/` | `./.claude/skills/`, etc. |

> **Note**: Git caches are centralized in `~/.local/state/skills-manager/repo-cache`, keeping project workspaces 100% clean.

### Working in Project Scope

Every command accepts `-p` / `--project` to act on the current project instead of your global setup:

```bash
skills -p init                                   # create ./.agents/skills.json
skills -p add akunzai/agent-skills -s agents-md  # install into ./.agents/skills/
skills -p ls                                     # list this project's skills
skills -p outdated                               # check remote repos for updates
skills -p sync                                   # restore declared skills after a clone
skills -p prune                                  # clean items no longer declared in configuration
skills -p doctor --fix                           # verify & repair project symlinks
skills -p rm agents-md                           # remove from this project
```

Commit `.agents/skills.json` to your repository so teammates get the same skills with a single `skills -p sync`.

---

<details>
<summary>📖 <strong>Full Command Reference</strong></summary>

### Adding Skills
```bash
# Interactive selection from GitHub / GitLab / Git URLs
skills add akunzai/agent-skills
skills add gitlab:my-org/my-skills
skills add https://github.com/owner/repo/tree/main/skills/foo

# Local directory or monorepo auto-scan (interactive multi-select)
skills add ~/code/agent-skills
skills add ./local-skills --all
skills add --symlink ~/code/my-skill --skill my-skill

# Specific skill(s) or all skills (with -y / --yes to bypass prompts)
skills add akunzai/agent-skills -s agents-md -s tidy-commits
skills add akunzai/agent-skills --all -y

# Custom CLI installer command
skills add --command "agentsview skills install" --check "which agentsview" --skill finding-history
```

### Managing & Inspecting Skills
```bash
# List skills (table or JSON)
skills ls
skills ls --json
skills ls -a claude
skills ls -s akunzai

# Interactive removal
skills rm
skills rm tidy-commits
```

### Sync & Maintenance
```bash
# Sync / restore declared skills
skills sync
skills sync --force     # force re-fetch & re-link
skills sync --dry-run

# Remove untracked master skills and unconfigured managed links
skills prune             # interactively select items to remove (none preselected)
skills prune --dry-run   # show the plan without changing files
skills prune --yes       # run non-interactively
skills prune --links-only
skills prune --skills-only

# Deprecated: use `skills prune` instead
skills sync --prune --yes

# Check outdated & update
skills outdated
skills update
skills update akunzai/agent-skills

# Health check & auto-repair
skills doctor
skills doctor --fix

# Self-update binary
skills self-update
skills self-update --check
```

</details>

<details>
<summary>⚙️ <strong>Sample Configuration (<code>skills.json</code>)</strong></summary>

Full field reference: [`skills.schema.json`](skills.schema.json).

```json
{
  "$schema": "https://raw.githubusercontent.com/akunzai/skills-manager/main/skills.schema.json",
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
    "my-tool": {
      "type": "symlink",
      "source": "~/tools/my-tool"
    }
  }
}
```

</details>

<details>
<summary>🐚 <strong>Shell Autocompletion</strong></summary>

```bash
# Zsh (add to ~/.zshrc)
source <(skills completion zsh)

# Bash (add to ~/.bashrc)
source <(skills completion bash)

# Fish
skills completion fish | source

# PowerShell
skills completion powershell | Out-String | Invoke-Expression
```

</details>
