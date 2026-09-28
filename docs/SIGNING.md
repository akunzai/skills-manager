# Signed skills

A Source can sign each Skill with an [OpenSSF Model Signing](https://github.com/sigstore/model-transparency) signature, `skill.oms.sig`, as [NVIDIA/skills](https://github.com/NVIDIA/skills) and [akunzai/agent-skills](https://github.com/akunzai/agent-skills) do. Skills Manager verifies it before Materializing: every file must match what was signed, and nothing unsigned may be added.

- **Sigstore keyless** signatures need nothing up front. The first signer seen for a Source is recorded in `skills.json` under `signature.sigstore`; review it there, and a later Skill signed by anyone else is refused.
- **Certificate chains** need their trust anchor named once: `skills add NVIDIA/skills --trust-cert nv-agent-root-cert.pem`.
- A Skill that fails verification is not written. The copy already in the Scope stays, and the command exits `1` naming why. Right after a publisher merges, a signature can be stale for a few minutes; run `skills update` again later.
- `skills ls` marks a signed Skill with `✓` after its Source (`[signed]` when output is not a terminal); an unsigned Skill is installed and left unmarked. Set `"signature": {"require": true}` on a Source to refuse unsigned Skills from it. A Skill that was signed is always refused if it arrives unsigned.

- When a Skill does not verify but you have reviewed its content and want it anyway, `skills trust <skill>` records that exact content (its Cache tree id) in `skills.json` under `signature.trusted`, so the decision is reviewed like any other change. `skills sync` then installs it and notes it; `skills ls` marks it `!` (`[unverified]` when output is not a terminal), and `skills doctor` warns about it. Once the Source changes the Skill, the trust no longer applies and it is verified again. `skills trust --revoke <skill>` removes the trust. `--force` still never installs an unverified Skill.

`skills update` fetches the Sigstore trust root next to the Cache, so `skills sync` verifies offline.
