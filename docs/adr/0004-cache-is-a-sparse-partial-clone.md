# 4. The Cache is a sparse partial clone

Date: 2026-09-18

## Status

Accepted.

## Context

The Cache was a shallow clone with a full working tree. A Source that ships
Skills alongside unrelated material — a monorepo with deep test fixtures,
generated snapshots, or large assets — made every Cache download, store, and
check out all of it. On Windows, paths over 260 characters in that material
failed `skills update` until `core.longpaths` was enabled.

A Scope declares specific Skill subpaths. Nothing outside them is ever
Materialized.

## Decision

The Cache is a blobless, sparse clone in cone mode:
`git clone --depth 1 --filter=blob:none --sparse`, then
`git sparse-checkout add <subpath>...` for the Skills a Scope declares. File
contents are downloaded only for checked-out paths. A Skill at the repository
root (`.`) disables the sparse checkout for that Cache, since the cone would be
the whole tree anyway. A `skills-manager.sparseCache` git config key marks
every Cache this release cloned or converted, which is what tells a root
Skill's deliberate full checkout apart from an earlier release's full clone.
`core.longpaths` stays, because a declared Skill can itself hold long paths.

The Cache stays a working tree. The alternative — keeping only git objects and
extracting each Skill with `git archive` or `cat-file` — would avoid the
working tree entirely, but would redefine the Cache and rewrite Materialize,
Freshness digests, and Cache validation for no additional gain over a sparse
checkout.

**The sparse checkout only grows.** One Cache is shared by the Global Scope and
every Project Scope, and a run sees only its own Config. `update` and `add`
add their Scope's subpaths and never remove another's. A path no Scope declares
any more stays checked out until the Cache is rebuilt; it was already written
successfully once, so it costs disk, not correctness.

**Sync stays offline.** Checking out a new path in a blobless clone downloads
its contents, so Sync never widens the sparse checkout. A declared Skill outside
it is reported like any other Skill missing from the Cache, and Sync names
`skills update`. Freshness reports a Cache at the remote commit that lacks a
declared Skill as `cache_incomplete`, which `update` resolves by adding the path
without fetching, so the two commands cannot send the user back and forth.

**Add discovers from SKILL.md files alone.** Add needs every Skill's `SKILL.md` before the
user picks any. Discovery switches the Cache to non-cone mode with its existing
cone directories plus every `SKILL.md`, walks the tree as before, and restores
the cone. Duplicate candidates are compared by committed tree ID, since their
other files are not on disk. If the process dies in between, the next refresh
reads the cone directories back from the non-cone patterns. Add then
adds the selected Skills, together with those the Scope already declares from
the Source, since the refresh before discovery could not know them.

**Sync checks coverage, not just presence.** A declared Skill whose directory
exists but lies outside the sparse checkout — a root Skill in a narrowed
Cache, or SKILL.md files an interrupted discovery left behind — is missing
from the Cache, so Sync never Materializes a partial copy of it.

**Existing Caches are narrowed in place.** A full Cache from an earlier
release is switched to a sparse checkout on its next fetching refresh, before
the new commit is written. Its objects are kept; only the working tree shrinks.
Doctor's legacy Cache migration copies a sparse Cache's `.git` and rebuilds
its working tree offline, because a local clone of a partial clone fails on
the blobs it never fetched.

Git 2.35 or newer is required: it is the release that let `sparse-checkout set`
switch between cone and non-cone mode. Older git fails with that requirement
stated rather than falling back to a full clone, and Doctor reports it for a
Scope with remote Sources.

## Consequences

A Cache costs roughly the declared Skills plus the repository's trees and
root files, not the whole repository.

This does not make a repository with a name Windows cannot write (`aux`,
`con`, a `:`) usable there, wherever that name sits. Git for Windows rejects
such a path when reading the tree into the index, before the sparse checkout
decides what reaches disk. Turning `core.protectNTFS` off would let the clone
through, but it is the guard against a repository writing into `.git` through
NTFS aliases, and the Cache clones repositories nobody vetted, so it stays on.

The first `update` or `add` against a Source after this lands narrows its
Cache, so another Scope sharing that Cache sees its Skills as `cache_incomplete`
until its own `update` adds them back. Sync in that Scope is blocked for those
Skills in the meantime, and says so.

Cone mode always checks out the files at the repository root and the files
directly inside each parent of a declared directory, and discovery checks out
every `SKILL.md`, so those are written whatever a Scope declares.

A server that does not support partial clone makes git download every blob,
which is the previous behaviour; the sparse checkout still keeps undeclared
paths off disk.

A symlink inside a Skill that points outside it now points at a path the Cache
may not have. Materialize already copied such links verbatim, so the
Materialized Skill was dangling before this change too.
