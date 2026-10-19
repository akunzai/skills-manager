# Cache is the git seam; Config and Freshness stay outside it

Callers talk to a `Cache` — one remote Source's working copy — through `Refresh` and `Cover`. Git stays in that module's implementation: no exported `RunGit` / `EnsureGitRepo`, and no fake git adapter (tests use real local origins). Paths are arguments, not read from Config. `observe` returns SHAs, the working-copy path, uncovered subpaths, and errors; Freshness still classifies `RemoteStatus`.

A git port, a Cache that holds Config, and Observe returning a `FreshnessRepository` were rejected: one adapter does not justify a seam, Config is a Scope declaration, and status is Freshness language.

This does not reopen ADR-0004 (sparse partial clone) or ADR-0001 (AddSource stays a Kind switch).

## Revisited: the well-known Source

A second Cache kind, the WellKnownCache, arrived with #319. Freshness, Sync planning, Update, and Remote intake now read either kind through one unexported interface (ADR-0013). Two adapters justify the seam this ADR rejected for one. The seam sits between Cache kinds, not around git. Git still lives only in `Cache`, and Config and Freshness's classification stay outside both.
