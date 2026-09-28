# Configuration

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
  "local": {
    "skills-manager": {
      "type": "command",
      "command": "skills guide --install",
      "description": "Skills Manager CLI guide for AI agents"
    }
  }
}
```

See [`skills.schema.json`](../skills.schema.json) for every field.
