---
version: 7
---

## Why

**Persona: operator.** An operator replaces the key-encryption key of the installation after a leak or on a schedule. Main fixes the form of the wrapper ID so that a later rewrap can rotate the key, but no rewrap exists. A database that holds keys under two wrapper IDs is refused at start, and the refusal text names a rewrap command that does not exist. Today the operator can replace the key only with a database edit.

The user path this change must make work (steps 1 to 5 are PARKED, see "Parked part"):

1. **Docs page.** The operator reads the rotation page and stages the old and the new key.
2. **API.** The operator starts the rotation and watches the progress.
3. **API.** The operator resumes or aborts a rotation that stopped.
4. **API.** The operator finishes the rotation when no record wraps under the old key.
5. **Server start.** The operator removes the old key and starts each server process with the new key alone.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Start of the server with one key while a rotation is active. | The server refuses to start, names both key settings and the rotation state. | `opendustpan serve` with both keys staged |
| Start of the server with the old key alone after finish. | Unchanged on main: the server refuses to start and names `DUSTPAN_KEY_ENCRYPTION_KEY`. | `opendustpan serve` with the new key |

The names of the new settings and of the routes in this table and in the user path are provisional. They depend on the owner questions in "Parked part".

Evidence for the claims above:

- Main refuses a database that holds keys under more than one wrapper ID, and the text names a rewrap command that does not exist. Proof: `pkg/secret/secret.go:225 (func checkWrapperIDs)` and the test `TestCheckWrapper` at `pkg/secret/builtin_test.go:208`, subtest "a half-rotated store is refused".
- Main has no rewrap path. Proof: `rg -i rewrap --glob '*.go'` over non-test files on main names only comments and the error text above. No store method updates a stored wrapper ID.
- Main has no rotation table. Proof: the baseline `pkg/store/sqlite/migrations/0168_baseline.sql` and the PostgreSQL baseline hold no such table. The four tables with a `wrapper_id` column are `secret_keys` (line 74), `installation_secret_keys` (line 436), `idempotency_server_key` (line 902) and `item_keys` (line 1065) in the SQLite baseline.

## What Changes

Specified now (the engine, with no new route):

- NEW rotation record in SQL, one row for the installation, in the baseline of each dialect.
- NEW `secret.Rotating`: a `Wrapper` that wraps under the new key and opens under the new key or the old key, and NEW function `secret.Holds(wrapper, id)`, used by the places that compare a stored wrapper ID with the ID of the wrapper (the data key open, the two start checks, the idempotency key open and the restore check).
- NEW rewrap of the four families that the key-encryption key wraps directly: the data keys (`secret_keys`), the installation data keys (`installation_secret_keys`, which hold the sign-in secrets and the other values of `@server`), the item keys (`item_keys` and their blobs) and the idempotency server key. Run as a durable operation with a stored cursor.
- NEW start-up check that needs both keys while a rotation is active and the new key alone after finish. The same check counts the wrapper IDs of the item keys and of the idempotency key, which the start-up check on main does not read.
- NEW audit entries of kind `change` in the scope `@server` for start, finish and abort, with the actor and the wrapper IDs.
- A restore of a backup made under the old key works while both keys are staged. The server refuses a restore while a rotation is active.

Parked (see "Parked part"): the HTTP routes, the authorization action and its holders, the source of the second key and the docs page and e2e step that need them.

Out of scope:

- Tagging or publishing a release.
- Rotation of customer keys in a KMS and of namespace keys. Main has neither a customer key nor a namespace key version, so the data keys and the item keys are wrapped by the key-encryption key directly.
- Change of data key or item key values.
- Deleting old item key blobs from backup targets (parked, see "Owner questions").

Depends on:

- none

## Usability

Where the path still costs the person time or surprises them:

- The server names the rotation state and both wrapper IDs in each start refusal, so the operator sees which key is missing.
- Finish says plainly that the old key is safe to remove only after it succeeds. Before that, both keys are needed.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The start check lists the stored wrapper IDs of `secret_keys` and refuses a key that is not the only one. (`pkg/secret/secret.go:210 (func CheckWrapper)`)
- The comparison of wrapper IDs: each stored ID must equal the `ID` of the wrapper. The error says "no rewrap command exists yet". (`pkg/secret/secret.go:225 (func checkWrapperIDs)`)
- The same check for the installation data keys. (`pkg/secret/installation.go:230 (func CheckInstallationWrapper)`)
- Opening a data key refuses a stored wrapper ID that is not the `ID` of the wrapper before it calls the wrapper. (`pkg/secret/envelope.go:48 (func unwrapKey)`)
- A new data key is wrapped by the one wrapper of the service. (`pkg/secret/envelope.go:14 (func wrapNewKey)`)
- The idempotency server key compares its stored wrapper ID with the `ID` of the wrapper in the same way, and a key that this process cannot open is replaced and the replay records are deleted. (`pkg/store/internal/codec/idempotency.go:320 (func open)`, `pkg/store/internal/codec/idempotency.go:266 (func reset)`)
- The server runs the check of the data keys and of the installation data keys at start. It does not read `ListItemKeyWrapperIDs`. (`pkg/dustpan/dustpan.go:2592 (func startUp)`, `pkg/secret/itemkey.go:243 (method ListItemKeyWrapperIDs)`)
- An item key record holds its wrapper ID and the name of its blob. `NewKey` makes the blob key of one write, and its comment names the rewrap to a new version, but no code rewraps an item key. (`pkg/secret/itemkey.go:199 (type ItemKey)`, `pkg/secret/itemkeyblob.go:219 (func NewKey)`)
- The key-encryption key comes from one variable or one file, and the variable is cleared from the environment after the read. (`cmd/opendustpan/main.go:1737 (func loadKeyEncryptionKey)`)
- A restore proves that the one wrapper names one of the wrapper IDs of the manifest. (`pkg/backup/staging.go:295 (func run)`)
- Durable operations with a claim, ordered steps and resume exist. (`pkg/durable/durable.go:1 (package durable)`, `pkg/backup/restore_operation.go:254 (const RestoreOperationKind)`)
- The server audit scope takes an entry through `AppendServer`, and the kind `change` exists. (`pkg/audit/audit.go:815 (method AppendServer)`, `pkg/audit/audit.go:54 (const KindChange)`)
- The installation actions are `server:*` with one `installation:*` action pair for gates and one for the store check. No action for the key-encryption key exists. (`pkg/identity/identity.go:261 (const ActionInstallationStoreCheck)`)
- The upgrade check always answers that a key-encryption key exists. (`internal/api/server/upgrade.go:92`; test `internal/api/server/upgrade_test.go:385 (func TestUpgradeCheckAnswersKeyEncryptionKey)`)

Differences from the earlier text of this change: the earlier draft rewrapped "namespace key versions", which main does not have; it named the tests at wrong lines; it needed a rotation route that the audit of main shows is not decided; and it let the new key arrive in a running process, which cannot read a changed environment (the variable is cleared at start, `cmd/opendustpan/main.go:1739`).

## Parked part

The engine above is specified without an answer to these owner questions. The tasks that depend on an answer are marked PARKED and do not start.

- Which action starts, shows, resumes, aborts and finishes a rotation, which role holds it, and whether it needs a fresh authentication step. No ADR decides it. (Report: Owner questions 1.)
- How the second key reaches a server process: a new variable and file for the old key, or another way. ADR 23 decision 1 and ADR 238 decision 1 name the sources of the key. (Owner questions 2.)
- Whether rotation is an API only, a command only, or both. ADR 23 decision 4 calls it a rewrap command; the project rule says each feature has a REST API. (Owner questions 3.)
- Whether old item key blobs in backup targets are deleted. (Owner questions 4.)

## Security change

This change is security code: it handles key material, wrapping, start-up refusal and the audit record of a key change. It needs the Claude security review (rev2) on the exact head of its pull request before the pull request is queued. A new push needs a new verdict. Each layer has its own table of hostile cases in the design, and each guard has a test that fails when the guard is removed.

## ADR check

Each quote is copied from the file in `docs/adr/`. The "Changed by" chain and the "Current rule" come from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)".

**ADR 23: Secrets and the built-in key-encryption key** (not owner-tagged; recorded as the reviewed recommendation of an agent session that the owner allowed to become the decision).

- Decisions that apply: 1 (extended by ADR 41), 3 (extended by ADR 151), 4 (changed by ADR 217).
- Changed by: ADR 41, 45, 47, 151, 217, 237 (INDEX line for ADR 23). Current rule: this ADR, with ADR 41, 45, 47, 151, 217, 237.
- Quote 1: "The key comes from the environment variable `DUSTPAN_KEY_ENCRYPTION_KEY`, or from the file that `DUSTPAN_KEY_ENCRYPTION_KEY_FILE` names."
- Quote 3: "If the database holds wrapped keys, and the key is missing or does not match their wrapper ID, the server refuses to start. The error names the variable."
- Quote 4: "This ADR fixes the form now, so that a later rewrap command can rotate the key-encryption key with no format change. The rotation itself is a follow-up. Data keys never rotate, because their ciphertexts live in the stack config of the users."
- Effect: the engine keeps the wrapper ID form and the refusal. "Data keys never rotate" means the data key values: the rewrap changes only their wrap. The second key source is parked.
- Conflict: none for the engine.

**ADR 217: Customer-managed keys for each namespace: the key hierarchy and rotation (owner)** (all decisions owner-tagged).

- Decisions that apply: 19, 21 (current). Decisions 1 and 18 describe a hierarchy that main does not have.
- Changed by: ADR 220 changes decision 18. Current rule: this ADR, with ADR 220.
- Quote 19: "A rotation above them changes their wrap only."
- Quote 21: "The rewrap command of ADR 23 decision 4 wraps each namespace key version and each sign-in secret again. It does not touch customer keys. A restore of a backup made before a rotation of the key-encryption key needs the old key-encryption key, because the backup holds the wraps under it."
- Effect: main has no namespace key version, so the rewrap acts on the records that the key-encryption key wraps directly today. When the namespace key lands, the rewrap changes to its records in that change. The restore rule holds as written. The text "it does not touch customer keys" holds, because none exists.
- Conflict: none. The wording "namespace key version" in decision 21 is not met on main; this change records the difference and does not edit the ADR.

**ADR 151: Redaction, and an item key for each history item** (not owner-tagged).

- Decisions that apply: 2 (changed by ADR 217), 3 (extends ADR 23 decision 3).
- Changed by: ADR 154, 210, 217, 220, 384. Current rule: this ADR, with ADR 154, 210, 217, 220, 384.
- Quote consequence: "A rotation of the key-encryption key (the rewrap command of ADR 23) must write a new key blob for each item key and delete the old one, in the live store and in each target."
- Effect: the rewrap writes a new blob for each item key and deletes the old blob from the live store. The deletion in each target is parked.
- Conflict: the deletion in each target is open (Owner questions 4).

**ADR 238: `opendustpan serve` needs decisions** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: ADR 315 extends; ADR 356 changes decision 3. Current rule: this ADR, with ADR 315, 356.
- Quote 1: "An explicit key. `serve` needs `DUSTPAN_KEY_ENCRYPTION_KEY`, `--key-encryption-key-file` (`DUSTPAN_KEY_ENCRYPTION_KEY_FILE`), or a KMS key-encryption key when that setting exists"
- Effect: the engine takes two `Wrapper` values from its caller and reads no key source itself. The source of the second key is parked.
- Conflict: none for the engine.

**ADR 216: Namespace administration and the operator scope** (owner-tagged (owner) decisions).

- Decisions that apply: 3 and 4, the table and the list of operator actions.
- Changed by: see INDEX. Current rule: this ADR.
- Quote: "A test in `pkg/access` iterates each action constant of the area `installation:` (and of the old area `server:` while the code has it). It fails for each constant that the table of decision 3 or the list of decision 4 does not name. A new installation action cannot merge without a row."
- Effect: a new rotation action needs a row in that table, and no row exists. This is why the action is parked.

**ADR 210: The tool-neutral API prefix, paths, actions, versions and style** (not owner-tagged).

- Decision that applies: 7: "An action has two parts and one colon. The area is the object kind of the action, one word in lowercase letters". The area `server:` becomes `installation:`.
- Effect: the name of the rotation action follows this grammar once the owner names it.

## Open questions

Not none. Four questions are open and listed in "Parked part". The engine tasks do not depend on them. The PARKED tasks wait for an owner decision.
