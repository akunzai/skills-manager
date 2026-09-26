# Adopt moves a Skill with no known Source rather than declaring it in place

`skills adopt` declares Untracked Skills on the Scope skills directory. When an installer lock file records the Skill's git Source, the copy stays where it is and is declared from that remote Source, because Sync already owns copies of remote Skills on that directory. Anything else — no record, a record that names no git repository, or a directory that is its own git checkout — is moved to `skills-local/<name>` beside the skills directory (or `--to`) and declared as a local symlink Source there.

The move exists because the skills directory is Sync's destination, never a Source. A local Source inside it is Illegal-local: Sync would be linking a directory onto itself, and rm or prune removing the link would remove the content. Moving keeps every byte, including a checkout's `.git`, and leaves each Skill with a Source outside the directory Sync writes. A git checkout moves even when a lock file records it, because declaring it remote would let Sync overwrite a working tree with its own history.

Rejected:

- **Declare in place as a local Source.** It is exactly Illegal-local; allowing it would reopen that rule for one command and make removal destructive.
- **Declare in place as a remote Source when nothing records one.** Guessing a repository from the name or content can declare the wrong Source, as ADR-0007 rejects for renames.
- **Write the other installer's lock file** to hand the Skill over. Adopt reads installer lock files and never writes them; it warns instead that the installer may still update the same directory.

This does not reopen ADR-0002: adopt speaks its codes, `0` all adopted, `1` something left for the user, `2` a failure.
