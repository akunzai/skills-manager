# Well-known Sources are a sibling Cache behind one Cache seam

A publisher may serve its Skills over HTTPS through an [Agent Skills Discovery](https://github.com/cloudflare/agent-skills-discovery-rfc) index at `/.well-known/agent-skills/index.json`, with no git repository behind them. Such a Source is a fourth Add Source kind, `well-known`, and its Skills are remote Skills: declared under `remote` with `type: "well-known"`, the base URL as `url`, each Skill mapped to its index entry name, and no `branch`. They are Materialized from a `WellKnownCache`, a sibling of the git `Cache`, which holds the last index and each declared Skill unpacked from an artifact verified against the index's `digest`. A Baseline records the base URL as its Cache identity and the entry digest as the version applied, so Drift protection, Trusted content (ADR-0010), and Renames (ADR-0007) key by digest where a git Source keys by tree id; the Scope state version is unchanged.

Only index v0.2.0 is read, since v0.1.0 carries no digests, and only over `https://`. A URL is a well-known Source when it has no path or ends in the index path; every other URL stays a git Source, decided without a network probe. Add, Update, and outdated reach the network; Sync reads the Cache only (ADR-0004). Content Add will not use — an artifact that does not match its digest, an archive with a member outside its root, a link, no `SKILL.md` at its root, or more than 100 MiB unpacked — is refused with exit `1` before anything is declared.

Freshness, Sync planning, Update, and Remote intake read either Cache through one unexported interface. This revisits ADR-0005, which rejected a git port because one adapter did not justify a seam: two adapters now do, and the seam is between Cache kinds, not around git. Git still lives only in `Cache`, and Config and Freshness's classification still stay outside both. It also revisits ADR-0001: the fourth kind was weighed against a Source interface and the Kind switch kept, because a well-known Source reuses Remote intake whole and adds one case to each switch, while the conflict-detection switch the ADR names is unchanged.

The Cache lives at `<cache dir>/.well-known/<host>`, with a digest of the base URL's path appended when it has one. A Source key never starts with a dot, so it cannot collide with a git Cache, as `well-known/<host>` could with a GitHub owner of that name.

## Considered options

- **Extending the git `Cache` with an HTTP mode.** Every git-specific method would branch on it, and the module ADR-0005 keeps git in would hold a second protocol.
- **Probing a URL to tell a git repository from a well-known index.** Classification would need the network, and a host serving both would be decided by whichever answered.
- **Reading index v0.1.0.** Its `files` arrays carry no digests, so nothing it names can be verified.
