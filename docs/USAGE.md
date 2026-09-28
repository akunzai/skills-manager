# Usage

## Discovery scopes

Add the repository root by default. Equivalent copies of a Skill are collapsed
and the conventional `skills/<name>` path is preferred:

```sh
skills add microsoft/azure-skills
```

For precise discovery or automation, append a collection path or pass `--path`:

```sh
skills add microsoft/azure-skills/skills
skills add microsoft/azure-skills --path skills
```

GitHub tree URLs also set the branch and discovery scope. If same-name Skills
have different contents, interactive Add asks which Source path to use;
non-interactive Add requires an explicit scope.

To see what a Source offers before declaring anything, add `--list` (`--json`
for scripts). It honors `--path` and `--branch` the same way and changes
nothing: no Config, Scope skills directory, or Agent directory is written.

```sh
skills add microsoft/azure-skills --path skills --list
```

## Global or project-local

Global is the default. Project mode keeps the declaration beside the code so a team can reproduce it after cloning.

| Scope | Command | Configuration | Installed skills |
| --- | --- | --- | --- |
| Global | `skills …` | `~/.agents/skills.json` | `~/.agents/skills/` |
| Project | `skills -p …` | `./.agents/skills.json` | `./.agents/skills/` |

```sh
skills -p init
skills -p add akunzai/agent-skills --skill agents-md
git add .agents/skills.json
```

Teammates restore the declared state with:

```sh
skills -p update
```

Update uses the Project Config to refresh its declared remote Sources in the shared Cache, then reconciles Project Skills from that Cache as `skills -p sync` would. Sync alone does the reconciliation without network access.

Materialized Project Skills are ordinary team-owned files. Commit `.agents/skills/` when every declared skill comes from a remote Source—those are real directories that commit cleanly. Keep it out of version control once a skill is declared from a local directory: that source is a path on one machine, and a git client without symlink support checks the committed link out as a text file instead of the skill.

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
| Browse a Source's Skills before adding any | `skills add <source> --list` |
| Reconcile the selected Scope from its existing Cache | `skills sync` |
| Preview reconciliation | `skills sync --dry-run` |
| Inspect remote → Cache → Scope freshness | `skills outdated` |
| Show a remote skill's upstream and local changes | `skills diff <skill>` |
| Refresh remote Sources, then sync the Scope | `skills update` |
| Diagnose drift | `skills doctor` |
| Repair diagnosed health issues | `skills doctor --fix` |
| Declare untracked or agent-directory skills, keeping their content | `skills adopt` |
| Remove undeclared managed items | `skills prune` |
| Remove a skill | `skills rm <skill>` |
| Print or install AI agent guide | `skills guide [--install]` |

Every operational command accepts `-p` (or `--project`). Structured consumers can use `skills ls --json`; interactive terminals use standard Unicode marks, while redirected output and `TERM=dumb` fall back to plain text.

For remote Skills, the normal flow is `skills outdated`, then `skills update`. When nothing changed, Update prints one line and asks nothing, so it fits a shell startup file. Sync, including the one Update runs, protects known local changes and unknown baselines; inspect them first with `skills diff <skill>`, or explicitly overwrite with `skills sync --force`. `skills diff` shows what the Source changed since the copy was applied and what was changed locally; it reads the existing Cache unless `--fetch` refreshes the Source first, and exits `0` with nothing to show, `1` with differences, and `2` when it cannot compare. `sync --dry-run` is a non-mutating freshness gate: it reports what Sync would do without writing anything, and without running a skill's own commands.

When upgrading from a legacy branchless Cache layout, `skills doctor --fix` removes affected Cache entries without touching the network; the next `skills update` fetches a branch-aware Cache in their place. Doctor does not run Sync automatically.

`skills update`, `skills sync`, `skills outdated`, and `skills doctor` share one set of exit codes: `0` when the Scope matches its Config, `1` when it does not, and `2` when the work could not be completed. A skill Sync deliberately left alone — protected local changes, an unknown baseline, an uncached Source — is reported as a blocked skill with a next action, and exits `1`. Only a genuine failure, such as a copy that did not complete or an agent path Sync does not manage, exits `2`. Doctor reads the same way: a finding it reports with a next action exits `1`, while Cache recovery artifacts or a repair that failed under `--fix` exit `2`. All of them report state and next actions rather than command errors.
