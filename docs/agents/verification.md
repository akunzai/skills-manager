# Verification

How an agent exercises a change in this repo before it reaches review.
Human setup narrative lives in `README.md`; this file holds only what an
agent needs.

## Starting the environment

```sh
mise run check
```

<!-- drift:forge github -->
<!-- drift:entrypoint-cmd mise run check -->

This is the project's own gate, and it is what CI runs, in CI's order:
`gofmt -l .`, `go vet ./...`, `go test -race ./...`, `go build
./cmd/skills`. It never prompts. A step needing a human aborts non-zero
naming the prerequisite — see Human prerequisites below.

The task is a POSIX shell script and does not run on Windows. There is no
second script for a platform nobody develops on: CI's Windows job runs
`go test ./...` and `go build ./cmd/skills` directly, and so should you if
you ever hold a Windows machine.

**Proof it ran**: `go run ./cmd/skills version` prints `skills-manager
<version>`. `skills-manager` is a CLI, so the proof is the built binary
answering, not a process that stays up.

## Checks

`mise tasks` lists the tasks and their descriptions; nothing here copies
them. What the task file does not hold:

| What | Command |
| --- | --- |
| The gate CI also runs | `mise run check` |
| Tasks and what each one does | `mise tasks` |
| One package while iterating | `go test -race ./internal/engine/...` |
| One test by name | `go test -race -run TestCLISyncExitCodes ./internal/cli/` |
| Verbose suite, as CI prints it | `go test -v -race ./...` |
| Exercise the CLI without installing | `go run ./cmd/skills <command>` |

Tests must not reach the network. `ObserveFreshness` shells out to
`git ls-remote` when a remote Source fixture has no explicit branch, so
fixtures always name one.

## Verifying the interactive prompts

`internal/tui/prompt.go` drives raw terminal mode, so `go test` cannot
exercise what a real TTY does with arrow keys, `Space`, and a redrawn
list. Where a change touches a prompt, drive the built binary in a pane
instead of asserting from the unit tests alone.

Only when `herdr` is available. Check both, and skip this path silently
when either fails — it is an optional aid, never a gate:

```sh
command -v herdr >/dev/null && [ "${HERDR_ENV:-}" = 1 ]
```

Then split a pane beside the calling one, keeping the developer's focus
where it is, and drive the binary there:

```sh
herdr pane split --current --direction right --cwd "$PWD" --no-focus
# read the new id from .result.pane.pane_id
herdr pane run <pane> "cd <scratch> && clear"
herdr pane run <pane> "./skills add --symlink ./src --config ./scope/skills.json --skills-dir ./scope/skills --cache-dir ./scope/cache"
herdr pane wait-output <pane> --source visible --match "Space to toggle" --timeout 20000
herdr pane send-text <pane> " "
herdr pane send-keys <pane> down
herdr pane send-text <pane> " "
herdr pane read <pane> --source visible --lines 10
herdr pane send-keys <pane> enter
```

Four things this repo has already been caught by:

- **Always pass `--config`, `--skills-dir` and `--cache-dir` into a
  scratch directory.** A prompt driven without them writes to the
  developer's real `~/.agents/skills.json` and links into their real
  agent directories. A skills directory outside the global one is
  Project Scope, so the agent links land beside it rather than under
  `$HOME`.
- **Wait on `--source visible`, not the default.** `wait-output`
  searches the snapshot immediately and matches output that is already
  there, so a previous run's prompt in scrollback satisfies the wait and
  the keys then arrive before the new prompt has drawn. Run `clear`
  first, and keep the command line short enough not to wrap — a wrapped
  absolute path fills the viewport and hides the prompt.
- **`Space` goes through `pane send-text " "`.** `pane send-keys <pane>
  space` returns success but does not toggle the checkbox.
- **Wait for each prompt, not for the final line.** `skills add` asks
  three questions in sequence — skill selection, Scope, Agent
  availability. Waiting for `Added` alone times out while an unanswered
  prompt sits on screen.

Close the pane when done, and verify the result on disk rather than from
the prompt's own output: the checkbox state is what the prompt drew, the
symlinks are what the command did.

## Human prerequisites

Run once, by a person. `mise run check` fails until they are done.

- [ ] Install `mise` (https://mise.jdx.dev), then `mise install` in the
      clone to get Go and `tcut`.
- [ ] Have `git` on `PATH`. The CLI shells out to it for every remote
      Source operation, and the engine tests build real local
      repositories.

## Ports

Not applicable. `skills-manager` has no listening service, so several
agents can run the gate in the same clone at once.

## Capturing evidence

- Terminal recording: `mise run demo` drives `tcut` over
  `scripts/demo.video.ts` and writes `website/demo.gif`. Regenerate it
  only under the rules in `docs/agents/release.md`; commit only that
  file.
- Test output: paste the failing package and test name, not the whole
  `-v` run.

**This document is where the capture rules live**, and
`docs/agents/pull-request.md` points here rather than restating them. A
terminal capture on the developer's own machine carries their username,
home paths, and whatever their real `~/.agents/skills.json` declares.
The demo therefore runs on fixed fixture data, dimensions, theme,
timing, and output paths, and must expose no local username or home
path. Assert on the frame, a marker, or fixture data rather than on
whatever the terminal happened to be showing.

For a change behind a mode switch or an exit-code contract, confirm the
far end: assert the exit code (`docs/adr/0002-exit-codes-express-state.md`)
and the on-disk result, not that the command printed something.

## Not verified

- Windows and Linux behaviour: the gate runs on the developer's macOS
  clone only. Linux is covered by CI's Ubuntu runner, which runs the
  whole gate. Windows has its own CI job, but a narrower one — the test
  suite and the build, without the formatter, the vet pass, or the race
  detector (`.github/workflows/ci.yml` says why). Neither runner covers
  the PowerShell installer, the Store App Execution Alias stub case, or
  whether an Agent reads a Skill that Availability copied rather than
  linked.

  A platform branch is only in this list when nothing reaches it. The
  Windows symbolic-link privilege branch is not: `engine.CreateSymbolicLink`
  exists so a test can hand it the Windows errno on any platform, and
  `internal/engine/copy_fallback_test.go` does.
- `skills self-update` end to end: it downloads a published GitHub
  release asset and replaces the running binary. Only the version
  comparison and asset matching are unit-tested.

A gap you could have closed is not a gap. Run the check whose dependency
you have already seen running, and report a check you skipped as untried,
rather than recording it here as one this repo cannot run.
