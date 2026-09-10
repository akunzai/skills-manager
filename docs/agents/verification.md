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
  clone only. Path handling, the PowerShell installer, and the Store
  App Execution Alias stub case are covered by CI's Ubuntu runner and by
  release builds, not locally.
- `skills self-update` end to end: it downloads a published GitHub
  release asset and replaces the running binary. Only the version
  comparison and asset matching are unit-tested.

A gap you could have closed is not a gap. Run the check whose dependency
you have already seen running, and report a check you skipped as untried,
rather than recording it here as one this repo cannot run.
