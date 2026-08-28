# Product Design

Long-term principles for user-visible CLI output, interactive prompts, and the README.

## Product thesis

Skills Manager is the quiet control point for skills shared across coding agents. Users should always be able to tell where a skill comes from, which scope owns it, and where it is available.

The experience is compact, ordered, and recognizable through language and information structure rather than decoration.

## Voice

Write like an opinionated operator with the restraint of a craftsperson.

- Use active voice, sentence case, concrete verbs, and stable product terms.
- Put personality in the README, help text, and interactive empty states.
- Keep errors, warnings, confirmations, tables, JSON, and non-interactive output functional.
- State a recovery or next action only when it helps the user continue.
- Keep an action's verb consistent from prompt through result.

Avoid emoji, emotional reactions, memes, stacked punctuation, filler, and claims that the tool makes AI workflows magical or effortless.

## Product language

Use terms that describe what the user controls:

- **Scope** is the active Global or Project configuration.
- **Availability** describes where a skill can be used; symlinks are an implementation detail.
- **Automatically available** agents read the central skills directory directly.
- **Follow defaults** applies the configured default-agent policy to a skill.
- **Include** and **exclude** are persistent per-skill overrides.
- **Drift** is a difference between declared availability and filesystem state.
- **Sync** reconciles filesystem state with declared configuration.

When availability changes, connect the action to its result without turning it into a decorative status card:

```text
Added 3 skills.
Available in Claude Code and Codex. Excluded from Copilot.
```

## Output

Default output leaves one durable result. Show transient progress only for slow operations; reserve detailed steps for `--verbose`.

```text
result
supporting availability or scope, when relevant
next action, only when required
```

Commands that report on the state of a Scope express that state through their exit code rather than through an error: `0` when the Scope matches its Config, `1` when it does not, `2` when the work could not be completed. `1` prints what stands in the way and the next action, and is not styled as an error. See ADR-0002.

Errors use the smallest useful subset:

```text
Error: what could not be completed
Cause: why it failed, when known
Next: the concrete recovery action, when available
```

Color reinforces status or hierarchy but never carries meaning alone. Machine-readable output contains no presentation formatting and preserves its documented contract.

## Icons and terminal capability

Interactive terminals use standard Unicode marks with a deterministic fallback:

```text
standard Unicode marks -> plain text for non-TTY output or TERM=dumb
```

Emoji are not a fallback. Use only widely supported Unicode marks when their meaning is clear; otherwise use short text labels.

No font probing or icon configuration is performed. Honor `NO_COLOR`. Emit no ANSI styling for non-TTY output or `TERM=dumb`.

## Interaction

Ask only for decisions that flags have not supplied. Keep non-interactive operation and `--yes` as stable shortcuts.

- Default to Global scope for compatibility.
- Present Follow defaults before per-skill availability customization.
- Show universal agents as Automatically available, not selectable targets.
- Confirm only consequential or multi-item changes.
- Keep configuration inspectable through commands; do not require hand-editing JSON for core availability.

Availability is declarative. Include and exclude cannot contain the same agent. Saving a policy must either reconcile installed state or clearly direct the user to the reconciliation step.

Do not add a full-screen management TUI until cross-scope management, bulk availability changes, or policy editing proves frequent enough to justify it.

## README

The README's first job is to get a new user through installation and one successful operation within 60 seconds. Its second job is to explain why the tool exists.

Use a plain heading, a short value proposition, one install path, one quick-start path, and one real terminal demo. Keep comprehensive reference material in command help, the schema, or focused docs. Document released behavior, not plans.

The demo uses fixed fixture data, dimensions, theme, timing, and output paths. It must not expose local usernames or home paths. Commit only `docs/assets/demo.gif`; regeneration rules live in the release SOP.
