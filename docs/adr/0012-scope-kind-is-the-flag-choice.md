# Scope kind is the flag choice

Whether a Scope is Global or Project is decided once, from `-p`/`-g`, and the engine reads it from the `Scope` the CLI resolved. It never infers it from where the skills directory is. A Scope also carries its root: the working directory at Project Scope, the user's home at Global Scope. Everything that depends on the root comes from it: the Agent directories, the installer lock file, where relative local Sources resolve, and how far prune climbs when it removes empty directories.

The engine used to take only a skills directory and infer the rest from its path: the default `~/.agents/skills` meant Global, and anything else meant Project rooted at the directory above it, or above `.agents` for `.agents/skills`. The CLI already treated the flag as the answer: `ls` and `config` report a custom `--skills-dir` without `-p` as Global, and every suggested command keeps the user's flags. The two disagreed whenever `--skills-dir` moved a Scope:

- `skills sync --skills-dir ~/work/skills` linked Skills into `~/work/.claude/skills` rather than `~/.claude/skills`, and stored local Sources relative to `~/work`.
- `skills sync -p --skills-dir ~/.agents/skills` linked a project's Skills into the user's home.

Inferring the kind from the path was rejected because a path cannot say which one the user meant: any directory can be a moved Global skills directory. A skills directory as the engine's only input was rejected because the root is not derivable from it either.

What a Global Scope with a custom `--skills-dir` left behind is not migrated: the old behaviour was the bug, and the setup is rare. Links beside the custom directory lie outside the Scope's root, so nothing cleans them up, and a local Source stored relative to the custom directory's parent now resolves against the home, so Sync blocks it as missing. The release note says how to fix both by hand.
