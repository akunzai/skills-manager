# 2. Commands express state through exit codes, not errors

Date: 2026-08-29

## Status

Accepted.

## Context

`skills outdated` already documented three exit codes: `0` when everything is
current, `1` when freshness differences are found, and `2` when the check
cannot be completed. Finding a difference is not an error, so `1` says so
without the command reading as broken — and the CLI suppresses the `Error:`
prefix for `1` on purpose.

`skills sync` had no such distinction. Every non-convergence — a protected
local edit, an unknown baseline, an uncached Source, a failed copy, an agent
path another tool had claimed — went through one failure count and exited `2`
with `Sync did not converge: N failure(s)`. A cold Cache on a fresh checkout
reported "the work could not be completed" when in fact nothing had failed;
the user simply needed to run `skills update`.

These two outcomes ask different things of the user. A blocked skill is a
decision: inspect the change, then choose whether to overwrite. A failure is
an investigation. Collapsing them hides which one is in front of you, and
gives CI no way to tell "this scope needs reconciling" from "this scope is
broken".

## Decision

Commands that report on the state of a Scope share one set of exit codes:

| Code | Meaning |
| --- | --- |
| `0` | the Scope matches its Config |
| `1` | it does not — the state is reported with a next action |
| `2` | the work could not be completed |

`1` is a state, not an error: the command prints what stands in the way and
what to do about it, and the `Error:` prefix stays off. `2` is reserved for
work that genuinely failed. When both are present, `2` wins — a real failure
must not be masked by "just needs reconciling".

Sync classifies each declared Skill as done, blocked, or failed. Blocked
covers what the tool deliberately left alone: local drift, an unknown
baseline, a Cache that was never fetched, a check that did not pass, a missing
local Source. Failed covers what broke: a copy, a symlink, an installer, an
unmanaged agent path, a baseline that could not be recorded.

A third command adopting this scheme adopts these codes rather than inventing
its own.

## Consequences

`sync --dry-run` with pending work now exits `1` where it used to exit `2`.
Callers gating on a non-zero exit are unaffected; callers matching `2` exactly
are not.

Deciding whether a new condition is blocked or failed is now a real design
question with a user-visible answer, and has to be answered when the condition
is introduced.
