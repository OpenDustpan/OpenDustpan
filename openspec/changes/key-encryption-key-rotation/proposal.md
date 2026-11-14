---
version: 3
---

## Why

**Persona: operator.** An operator rotates the key-encryption key of the installation after a leak or on schedule. Today the wrapper ID form reserves rotation but no rotation path exists, so the operator cannot replace the key without downtime or manual database edits. This change gives one resumable rotation that keeps reads working, writes an audit entry, and refuses the old key after finish.

The user path this change must make work:

1. **Docs page.** Operator reads the rotation docs page and stages the new key value beside the old key.
2. **API.** Operator starts the rotation through the NEW installation route and watches batch progress.
3. **API.** Operator checks key health during rotation through the API.
4. **E2e.** Operator runs the e2e rotation check with the Pulumi CLI and OpenTofu reads after the rotation.
5. **Server start.** Operator finishes the rotation, removes the old key, and starts each server process with the new key.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Start of a second rotation while one rotation is active. | The server answers 409 with the NEW error code `installation.rotation-active`. Restage both keys and resume, or abort and start again. | `opendustpan serve` |
| Start with the old key alone after the rotation finished. | The server refuses to start and names `DUSTPAN_KEY_ENCRYPTION_KEY` and the NEW variable `DUSTPAN_KEY_ENCRYPTION_KEY_PREVIOUS`. Run `opendustpan serve` again with both keys staged, or with the new key alone after finish. | `opendustpan serve` |

Evidence for the claims above:

- Main seals envelope keys and checks the key-encryption key at upgrade. Proof: Tests `TestEnvelopeRoundTrip` at `pkg/secret/envelope_internal_test.go:51` and `TestUpgradeCheckAnswersKeyEncryptionKey` at `internal/api/server/upgrade_test.go:382` exist on main.
- Main has an audit port and audit read routes. Proof: Package `pkg/audit` at `pkg/audit` and routes `ANY /api/audit/entries` and `ANY /api/audit/check` in the route table exist on main.
- Main has no key-encryption-key rotation route. Proof: Route list on main names `POST /api/dustpan/v1/namespaces/{}/issuer/keys/rotate` and audit and git-host routes, and names no installation key-encryption-key rotation route.

## What Changes

- NEW installation routes to start, show, resume, abort, and finish a key-encryption-key rotation with the NEW action `installation:rotate-key`.
- Dual-read during rotation and single-flip finish; the old key fails after finish.
- Resumable batch rewraps of namespace key versions, the data keys of `@server`, and sign-in secrets; data key values, item keys, and customer keys stay unchanged.
- Audit entries for start and finish with actor, time, and wrapper IDs.

Out of scope:

- Tagging or publishing a release.
- Rotation of customer keys in a KMS.
- Rotation of namespace keys; ADR 217 decision 18 covers that.
- Change of data key or item key values.
- A NEW CLI for rotation; the API routes are the one path.

Depends on:

- none

## Usability

Where the path still costs the person time or surprises them:

- The operator tracks progress from one docs page with one status command.
- Finish states plainly when the old key is safe to remove; before that it warns that both keys are needed.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main round-trips envelope encryption in the secret package. (`pkg/secret/envelope_internal_test.go:51 (func TestEnvelopeRoundTrip)`)
- Main checks the key-encryption key in the upgrade check. (`internal/api/server/upgrade_test.go:382 (func TestUpgradeCheckAnswersKeyEncryptionKey)`)
- Main converts audit entries in the audit HTTP adapter. (`internal/api/audit/audit.go:173 (func toAuditEntry)`)
- Main keeps the audit port with audit-read and tool-operation kinds. (`pkg/audit/audit.go:69 (kind audit-read)`)
- Main tests access-token rotation behavior. (`pkg/accesstoken/accesstoken_test.go:584 (func TestRotate)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 23: Secrets and the built-in key-encryption key** (not owner-tagged).

- Decisions that apply: 1 (extended), 3 (changed), 4 (changed).
- Changed by: ADR 41 changes decisions 2, 3 and 6; ADR 41 extends decision 1; ADR 45 changes decision 6; ADR 47 extends decision 5; ADR 151 extends decision 3; ADR 217 changes decisions 4 and 8; ADR 237 changes decision 2. Chain to the end: ADR 41, ADR 45, ADR 47, ADR 151, ADR 217, ADR 237, ADR 60, ADR 103, ADR 146, ADR 238, ADR 240, ADR 352, ADR 62, ADR 153, ADR 346, ADR 154, ADR 210, ADR 220, ADR 252, ADR 285, ADR 324, ADR 63, ADR 64, ADR 65, ADR 68, ADR 72, ADR 126, ADR 334, ADR 110, ADR 128, ADR 138, ADR 141, ADR 142, ADR 148, ADR 190, ADR 212, ADR 213, ADR 222, ADR 224, ADR 225, ADR 227, ADR 299.
- Current rule: this ADR, with ADR 41, 45, 47, 151, 217, 237.
- Quote: "Source of the built-in key-encryption key. The key comes from the environment variable `DUSTPAN_KEY_ENCRYPTION_KEY`, or from the file that `DUSTPAN_KEY_ENCRYPTION_KEY_FILE` names."
- Quote: "Start-up. If the database holds wrapped keys, and the key is missing or does not match their wrapper ID, the server refuses to start. The error names the variable."
- Quote: "Wrapper ID. The wrapper ID of the built-in key is `builtin-` and the first 16 hexadecimal characters of HMAC-SHA256, keyed with the key-encryption key, over the fixed text"
- Effect: Follows the fixed wrapper ID form and the start check, and builds the planned rewrap follow-up. The new key comes only from the staged file or variable, never from the request body.
- Conflict: none.

**ADR 217: Customer-managed keys for each namespace: the key hierarchy and rotation (owner)** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current), 4 (current), 19 (current), 21 (current).
- Changed by: ADR 220 changes decision 18. Chain to the end: ADR 220, ADR 273, ADR 286, ADR 363.
- Current rule: this ADR, with ADR 220.
- Quote: "Four levels (owner). From the top:  | Level | One for each | Wraps | Where it is kept | | --- | --- | --- | --- | | Key-encryption key | installation | each namespace key version"
- Quote: "A wrapper ID for each level. Each wrapped key records the ID of each wrapper above it and the KMS key version that the KMS answered."
- Quote: "Data keys and item keys do not change at a rotation. A rotation above them changes their wrap only."
- Quote: "The key-encryption key. The rewrap command of ADR 23 decision 4 wraps each namespace key version and each sign-in secret again. It does not touch customer keys."
- Effect: Follows the owner-tagged hierarchy: the rotation rewraps namespace key versions, the data keys of `@server`, and sign-in secrets under the new key-encryption key and leaves data keys, item keys, and customer keys unchanged.
- Conflict: none.

**ADR 238: `opendustpan serve` needs decisions: an explicit key and database, `key generate` and `doctor`** (not owner-tagged).

- Decisions that apply: 1 (current), 3 (changed), 5 (current).
- Changed by: ADR 315 extends; ADR 356 changes decision 3. Chain to the end: ADR 315, ADR 356.
- Current rule: this ADR, with ADR 315, 356.
- Quote: "An explicit key. `serve` needs `DUSTPAN_KEY_ENCRYPTION_KEY`, `--key-encryption-key-file` (`DUSTPAN_KEY_ENCRYPTION_KEY_FILE`), or a KMS key-encryption key when that setting exists"
- Quote: "The errors for a missing key or database. `serve` checks the key first, then the database, and reports only the first missing one."
- Quote: "`serve` refuses dev data. `serve` refuses to start in each of these cases: - The key file starts with the dev header of ADR 237 decision 10."
- Effect: Keeps explicit key source, first-missing error order, and refusal of dev key files during rotation.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does rotation change data keys or item keys? No. ADR 23 decision 4 keeps data key values fixed and ADR 217 decision 19 changes only their wrap. Item keys stay wrapped by namespace keys; ADR 151 decision 9 keeps counting their wrapper IDs in the start check.
- What happens to customer-managed namespace keys? No. ADR 217 decision 21 rewraps each namespace key version and each sign-in secret again and does not touch customer keys.
- How is the new key delivered? No new source. ADR 23 decision 1 and ADR 238 decision 1 allow only the variable, the key file, or a KMS key. The request carries no key bytes; the operator stages the new key in the key file or variable before start and the server derives the wrapper ID by HMAC per ADR 23 decision 4.
- How do start and restore behave mid-rotation? ADR 23 decision 3 refuses a start with a missing or mismatched key. While a rotation is active the check needs both wrapper IDs, and a restore of a backup made before rotation needs the key that wraps it.
- Does the narrowed scope satisfy the roadmap row without a CLI? Yes. Rewrapping each namespace key version changes the wrap chain of every per-stack data key and item key under it, so no per-stack record needs a direct rewrap. No NEW CLI is added; the API routes are the one path.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
