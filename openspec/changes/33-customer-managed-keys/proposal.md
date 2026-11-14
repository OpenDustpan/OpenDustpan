---
version: 1
---

## Why

**Persona: operator.** Operator installs and configures the server. Operator must meet local key control rules and prove which key seals stacks and workspaces. The design waits for RFC 33, so the user path, errors and key outage behavior are open questions below.

Evidence for the claims above:

- Main has a secret domain package for envelope encryption. Proof: pkg/secret
- Main defines an error code for a missing key-encryption key. Proof: internal/errcode/catalog_secret.go:11

## What Changes

- Record that customer-managed keys are wanted and wait on secrets work
- Keep the current single installation key-encryption key until RFC 33 is accepted

Out of scope:

- KMS connector code
- Per-namespace keys
- Key rotation automation
- Backup and restore rules for customer keys

Depends on:

- secrets (RFC 3, part 2)

## Usability

Where the path still costs the person time or surprises them:

- No user path changes in this coarse change.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main has a test that covers secret form and redaction. (`pkg/accesstoken/accesstoken_test.go:234 (func TestSecretFormAndRedaction)`)
- Main defines the error code secret.key-not-configured. (`internal/errcode/catalog_secret.go:11 (func secret.key-not-configured)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 41: The server reads state and masks secrets** (not owner-tagged).

- Decisions that apply: 10 (changed), 12 (changed).
- Changed by: ADR 60 changes decisions 5 and 13; ADR 103 changes decision 13; ADR 146 extends decision 13; ADR 151 changes decision 10; ADR 238 changes decision 12; ADR 240 changes decisions 9 and 13; ADR 240 extends decision 5; ADR 352 extends decision 10. Chain to the end: ADR 60, ADR 103, ADR 146, ADR 151, ADR 238, ADR 240, ADR 352, ADR 63, ADR 64, ADR 65, ADR 68, ADR 72, ADR 126, ADR 210, ADR 334, ADR 110, ADR 128, ADR 138, ADR 141, ADR 142, ADR 148, ADR 190, ADR 212, ADR 213, ADR 222, ADR 224, ADR 225, ADR 227, ADR 299, ADR 149, ADR 262, ADR 297, ADR 154, ADR 217, ADR 220, ADR 315, ADR 356, ADR 354, ADR 124, ADR 216.
- Current rule: this ADR, with ADR 60, 103, 146, 151, 238, 240, 352.
- Quote: "The server seals OpenTofu state blobs and the `outputs` column with the data key of the workspace scope."
- Quote: "The server needs a key-encryption key to seal state."
- Effect: Keeps envelope sealing under data keys with one installation key-encryption key. A customer-managed top key would change only the key-encryption key source. Decisions 10 and 12 are marked changed in the pack; the current rule reads this ADR with ADR 151 and ADR 238. No conflict.
- Conflict: none.

**ADR 151: Redaction, and an item key for each history item** (not owner-tagged).

- Decisions that apply: 5 (current).
- Changed by: ADR 154 changes decisions 3, 8, 15, 28 and 29; ADR 210 changes; ADR 217 changes decision 2; ADR 220 changes decision 2. Chain to the end: ADR 154, ADR 210, ADR 217, ADR 220, ADR 238, ADR 247, ADR 286, ADR 273, ADR 363, ADR 315, ADR 356.
- Current rule: this ADR, with ADR 154, 210, 217, 220.
- Quote: "The data key of the unit does not change."
- Effect: Keeps unit data keys unchanged. A customer-managed key would plug in at the Wrapper only, after RFC 33 is accepted. Decision 5 is current. No conflict.
- Conflict: none.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. What is the scope of a customer-managed key?
   - Options: One customer-managed top key for the whole installation; One customer-managed key per namespace; Customer-managed keys only for selected namespaces.
   - Provisional decision (owner review 14 Nov): One customer-managed top key for the whole installation
   - Reason: One installation key matches the single key-encryption key of main and follows common KMS use. Per-namespace keys wait for a later step. Benchmark: AWS KMS practice: one customer-managed key with versioned key material and dual control for disable and delete.
   - Persona impact: Operator holds key lifecycle. Developer sees no change. Escape hatch: Keep the current key-encryption key from a file as fallback until the customer key is active.
2. How do rotation and revocation behave?
   - Options: Re-wrap only on next write; Background re-wrap of all wrapped keys after rotation.
   - Provisional decision (owner review 14 Nov): Background re-wrap of all wrapped keys after rotation
   - Reason: Background re-wrap bounds the time old key material stays in use and keeps reads working. Benchmark: Google Cloud KMS practice: rotation makes a new key version and old versions stay readable until re-wrap ends.
   - Persona impact: Operator starts rotation. Rotation never blocks reads. Escape hatch: Keep the prior key version readable until all wrapped keys are re-wrapped.
3. What happens when the customer key is unreachable or revoked, or a backup moves to another installation?
   - Options: Fail closed on outage and revocation with a named error; Allow cached-key reads during a short outage.
   - Provisional decision (owner review 14 Nov): Fail closed on outage and revocation with a named error
   - Reason: Fail closed protects sealed state and item keys. Restore and namespace move rules wait for RFC 33. Benchmark: AWS KMS practice: a failed KMS call fails closed and a revoked key blocks reads until restored.
   - Persona impact: Operator restores key access. Developer sees a plain error that names the one command to check key status. Escape hatch: Refuse writes and reveal, keep masked reads, and keep the prior key version for restores.

Questions considered and answered:

- Does the current key design already allow a customer-managed key? No. ADR 41 decisions 10 and 12 fix sealed state under data keys and one installation key-encryption key. ADR 151 decision 5 keeps the unit data key unchanged. No ADR in the pack decides a customer key, rotation, or revocation.
- What does the waits-on secrets work cover? The roadmap row waits on secrets (RFC 3, part 2). No ADR in the pack decides RFC 33, so scope, rotation, revocation, backup and audit wait for it.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
