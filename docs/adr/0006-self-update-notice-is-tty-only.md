# Self-update notice is TTY-only and never installs

Other commands may mention a newer CLI release at most once per 24 hours on an interactive terminal. They never replace the binary; that remains `skills self-update`. The notice is skipped for `--json`, non-TTY, `TERM=dumb`, `self-update` itself, and `SKILLS_SKIP_SELF_UPDATE_CHECK`. The switch is an environment variable, not Config: `skills.json` declares a Scope's Skills, Sources, and Availability, not this CLI's own binary.

Auto-installing from an incidental command, prompting with a default yes, and storing the switch in `settings` were rejected: replacing the running binary is a consequential change the user must ask for, and mixing CLI-binary preferences into a Scope Config invites the next reader to treat them as skill policy.
