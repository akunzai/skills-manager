# Pull requests

**This file is English throughout**, sample blocks included.

Write PR titles, descriptions, and comments in **English**.
**Git commit messages are English**, imperative, subject under 72
characters — they live in history and get searched by tooling.

## Preparing

- Work on a feature branch. Never prepare a request from `main`.
- **Name the branch with a category prefix.** It is the only input the
  release-note labeler reads: `feat/`, `fix/`, `docs/`, `ci/`, `build/`,
  `chore/`, `release/`, `refactor/`, `test/`, `perf/`. An unrecognized
  prefix leaves labels untouched. See `docs/agents/release.md`.
- Use a concise descriptive title with **no Conventional Commit prefix**.
  The title is what becomes the release-note entry, so write it for a
  reader. One request may also carry more than one kind of change, and
  it still gets one label.
- Run `mise run check` before opening anything. See
  `docs/agents/verification.md`.
- **Do not open a request, draft included, without the developer asking.**

## Description shape

1. A plain-language opening: what changed and why, as a reviewer who did
   not write it would need it.
2. A visual the forge renders inline, chosen by what changed:

   | Change | Visual |
   | --- | --- |
   | Flow or state transition | Mermaid `flowchart` / `stateDiagram` |
   | Cross-service or API interaction | Mermaid `sequenceDiagram` |
   | Data model | Mermaid `erDiagram` |
   | CLI output, prompt, or README demo | Before/after terminal capture |
   | Multi-step interactive prompt | Short recording |
   | Engine or library only | None; test output instead |

   Pair before and after. At most one diagram unless it is such a pair.

   Upload the file with the repeatable `--attach` flag —
   `gh pr create --attach './after.png#After'`, and the same flag on
   `gh pr edit` and `gh pr comment`. Alt text follows the path after `#`,
   and a path the body already references as `![alt](./after.png)` is
   rewritten to point at the uploaded asset. Only when capture is
   genuinely impossible, leave a named placeholder comment such as
   `<!-- recording pending: the add prompt with two sources -->`.
3. A collapsed technical trailer holding affected paths, implementation
   notes, verification commands, and log excerpts:

```markdown
<details>
<summary>Technical details</summary>

affected paths, implementation notes, the commands run, log excerpts

</details>
```

**No personally identifiable information in any attachment**, whatever
you end up attaching. `docs/agents/verification.md`'s capture rules say
what that means here: a terminal capture carries the developer's
username, home paths, and their real skills configuration.

## Tests land with the behaviour

- **Product logic**: `internal/`, `cmd/`. A change here lands with its
  tests in the same request. `internal/cli/cli_test.go` and
  `internal/engine/sync_plan_test.go` are the two worked examples to
  follow.
- **Exempt**: `docs/`, `*.md`, `.github/`, `scripts/`, `website/`,
  `skills-manager/`, `mise.toml`, `.goreleaser.yaml`, and dependency
  bumps with no behaviour change.
- **Structurally untestable** code — a terminal-only interactive path, a
  platform branch that cannot run on the developer's machine — is
  declared in the description, naming what covers it instead.

A user-visible change to CLI output, a prompt, or a command's exit code
lands with the assertion that pins it. The exit-code contract in
`docs/adr/0002-exit-codes-express-state.md` is behaviour, not
presentation.

No coverage threshold. The reviewer judges whether the new behaviour is
actually exercised.

## Review readiness

Nothing unverified enters review. `skills-manager` is a standalone CLI
with no deployed environment, so there is one order: verify locally per
`docs/agents/verification.md`, then open the request with the evidence.

State in the description which paths were verified and which were not,
with the reason. `verification.md`'s "Not verified" section lists what
this repo cannot cover from a single developer's clone; a change landing
in one of those areas says so.
