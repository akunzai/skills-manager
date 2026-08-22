# Skills Manager

[![CI](https://github.com/akunzai/skills-manager/actions/workflows/ci.yml/badge.svg)](https://github.com/akunzai/skills-manager/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-24292f.svg)](LICENSE)

One source of truth for skills across Claude Code, Codex, Google Antigravity CLI, and other coding agents.

Skills Manager installs skills once, records the result in `skills.json`, and keeps each agent's availability in sync. It ships as a standalone Go binary and uses the system `git` only when a remote repository needs updating.

<p align="center">
  <img src="docs/assets/demo.gif" alt="Installing a skill, listing it, and inspecting its agent availability with Skills Manager" width="880">
</p>

## Install

macOS and Linux:

```sh
curl -fsSL https://raw.githubusercontent.com/akunzai/skills-manager/main/install.sh | bash
```

Windows PowerShell:

```powershell
irm https://raw.githubusercontent.com/akunzai/skills-manager/main/install.ps1 | iex
```

## Start with one skill

```sh
skills add akunzai/agent-skills
skills ls
```

The interactive flow asks where the skill belongs and which agents should see it. For scripts and CI, provide the choices explicitly:

```sh
skills add akunzai/agent-skills --skill agents-md --agent claude --yes
```

`--agent` is persistent policy, not a one-time link. A later `skills sync` restores the same availability.

## Global or project-local

Global is the default. Project mode keeps the declaration beside the code so a team can reproduce it after cloning.

| Scope | Command | Configuration | Installed skills |
| --- | --- | --- | --- |
| Global | `skills …` | `~/.agents/skills.json` | `~/.agents/skills/` |
| Project | `skills --project …` | `./.agents/skills.json` | `./.agents/skills/` |

```sh
skills --project init
skills --project add akunzai/agent-skills --skill agents-md
git add .agents/skills.json
```

Teammates restore the declared state with:

```sh
skills --project sync
```

## Control availability

Defaults cover the common case. Per-skill policy handles the exceptions without hand-editing JSON.

```sh
skills config
skills config set defaultAgents claude,antigravity

skills agents agents-md
skills agents agents-md include antigravity
skills agents agents-md exclude claude
skills agents agents-md follow-defaults
```

Universal agents that read the central skills directory directly do not need links and are reported separately.

## Daily commands

| Intent | Command |
| --- | --- |
| See installed and configured skills | `skills ls` |
| Restore configuration and availability | `skills sync` |
| Preview reconciliation | `skills sync --dry-run` |
| Find available updates | `skills outdated` |
| Update remote skills | `skills update` |
| Diagnose drift | `skills doctor` |
| Repair drift | `skills doctor --fix` |
| Remove undeclared managed items | `skills prune` |
| Remove a skill | `skills rm <skill>` |

Every operational command accepts `--project`. Structured consumers can use `skills ls --json`; interactive terminals use standard Unicode marks, while redirected output and `TERM=dumb` fall back to plain text.

## Configuration

`skills init` creates a schema-backed `skills.json`. Most settings can be managed through `skills config` and `skills agents`; direct editing remains available with `skills config edit`.

```json
{
  "$schema": "https://raw.githubusercontent.com/akunzai/skills-manager/main/skills.schema.json",
  "version": 1,
  "settings": {
    "defaultAgents": ["claude-code"],
    "availability": {
      "agents-md": {
        "include": ["antigravity-cli"]
      }
    }
  },
  "remote": {
    "akunzai/agent-skills": {
      "type": "github",
      "skills": {
        "agents-md": "skills/agents-md"
      }
    }
  },
  "local": {}
}
```

See [`skills.schema.json`](skills.schema.json) for every field.

## More

- Run `skills <command> --help` for flags and examples.
