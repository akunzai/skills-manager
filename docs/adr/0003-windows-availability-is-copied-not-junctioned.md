# 3. Windows Availability is copied, not junctioned

Date: 2026-09-10

## Status

Accepted.

## Context

Creating a symbolic link on Windows needs Developer Mode or an elevated
process. Availability for a non-Automatically-available Agent is a link into
the master skills directory, so on an ordinary Windows machine that link
cannot be made and the Skill is not available at all.

skills-manager already copied the Skill into the Agent directory when
`os.Symlink` failed, but the fallback was invisible, unverified, and fired on
any failure — a path too long or a target on a network volume produced a
silent copy that looked like success.

The comparable tools in this space go the other way. The npm-distributed
`skills` CLI has the same architecture — copy once into a canonical skills
directory, then link out per Agent — and on Windows creates an NTFS junction
with an absolute target, falling back to a full copy on any failure. Another
tool in the same family uses the identical junction pattern. A third school
sidesteps links entirely by generating a per-Agent file from one canonical
source.

## Decision

Copying stays the mechanism. NTFS junctions are rejected.

The fallback becomes decided by the operating system's answer rather than by
the platform: it fires only on ERROR_PRIVILEGE_NOT_HELD with a directory
target, and every other link failure surfaces as the error it is.

Junctions were rejected for four reasons, in descending weight:

1. **Go no longer treats a junction as a link.** Since Go 1.23 a junction
   carries `ModeIrregular` rather than `ModeSymlink` and is not resolved by
   symlink evaluation. The escape hatch for the old behaviour is removed in
   Go 1.27, which this project targets.
2. **Every link-ownership predicate would need re-auditing.** `IsManagedSkillLink`,
   prune, Doctor's health scan, and Availability Drift all ask "is this a
   symlink pointing into the master skills directory?". A junction answers no
   to the first half everywhere.
3. **The privilege assumption is undocumented.** That an unelevated process
   may create a junction is empirically true but not stated by Microsoft.
4. **It does not answer whether Agents find junction-linked Skills.** Both
   Node and Rust report a junction as a link rather than a directory in a
   directory scan, so a scanner written to skip links skips junctions too.

The single-source-of-truth benefit a junction would buy is largely already
spent: Sync rebuilds copies from the master anyway, now skipping the rebuild
when the master's recorded digest is unchanged.

## Consequences

Windows is an honest best-effort platform rather than a silently degraded one.
`sync` says once, at the end, that Availability was applied by copying and
why; Doctor counts the copies it finds at any later time. Enabling Developer
Mode makes the next Sync create links, so earlier copies are not permanent:
applying Availability over an existing copy attempts the link first, beside
the copy, and moves it into place only once it succeeds.

Each copy carries a marker recording the master Skill it came from and that
Skill's content digest, so a routine Sync leaves unchanged copies alone.

The fallback branch compiles and runs on every platform, and its behaviour is
verified by tests everywhere rather than only where it happens to fire.

Normalizing a symlink target before digesting it changes the recorded baseline
of any Skill on Windows that contains a symlink with a multi-segment target.
The first Sync after this lands re-Materializes such a Skill, or reports it as
local Drift if its Cache also moved on — `skills sync --force` clears it. No
other platform is affected, because the normalization is a no-op there.

Only Availability is covered. The same fallback also fires one level up, when
a Skill declared from a local Source cannot be linked into the skills
directory itself and is copied there instead. That copy is a Materialized
Skill, not Availability: its marker names the Source it came from rather than
a master Skill, so `IsManagedSkillCopy` does not claim it, neither the Sync
notice nor Doctor's count mentions it, and every Sync rebuilds it rather than
comparing digests. Making that site verifiable too would need the marker to
carry which of the two it is, which is a decision for whoever needs it.

Availability on Windows costs disk proportional to the number of Agents, and a
Skill edited in the master directory is stale in an Agent directory until the
next Sync. A hand-edited copy is not Drift: nothing reports it, and the next
Sync that finds the Skill changed overwrites it without warning. Edits belong
in the master skills directory, which has a recorded baseline.

This divergence from the junction consensus is deliberate. Whether a given
Agent discovers a junction-linked Skill on a real Windows machine is the one
fact that would most change this decision, and it is not yet known.
