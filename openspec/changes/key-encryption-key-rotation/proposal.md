---
version: 8
---

Issue: none. The PM directed this change; no issue exists.

## Why

**Persona: operator.** An operator replaces the key-encryption key of the installation after a leak or on a schedule. Main fixes the form of the wrapper ID so that a later rewrap can rotate the key, but no rewrap exists. A database that holds keys under two wrapper IDs is refused at start, and the refusal text names a rewrap command that does not exist. Today the operator can replace the key only with a database edit.

The user path this change must make work (every step is decided, owner, 2026-11-20):

1. **Docs page.** The operator reads the rotation page `docs/guides/key-rotation.md` and stages the new key and the old key.
2. **Server start.** The operator starts each server process with the new key in `DUSTPAN_KEY_ENCRYPTION_KEY` (or `DUSTPAN_KEY_ENCRYPTION_KEY_FILE`) and the old key in `DUSTPAN_KEY_ENCRYPTION_KEY_OLD` (or `DUSTPAN_KEY_ENCRYPTION_KEY_OLD_FILE`).
3. **API and CLI.** A site administrator, with a fresh authentication step, starts the rotation, and watches the progress with `opendustpan key-rotation status`.
4. **API and CLI.** The site administrator resumes or aborts a rotation that stopped.
5. **API.** The site administrator, with a fresh authentication step, finishes the rotation when no record wraps under the old key.
6. **Server start.** The operator removes the old key and starts each server process with the new key alone.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Start of the server with one key while a rotation is active. | The server refuses to start, names both key settings and the rotation state. | `opendustpan serve` with both keys staged |
| Start of a rotation from a process that holds no old key. | 409 with the code `key-rotation.old-key-missing`; the text names `DUSTPAN_KEY_ENCRYPTION_KEY_OLD`. | `opendustpan serve` with the old key staged |
| Start, finish or abort with an access token. | 403 with the code `mfa.fresh-step-required`. | Sign in to the console again and repeat the call (see "Usability") |
| Finish while a record still holds the old key. | 409 with the code `key-rotation.incomplete` and the count for each family. | `opendustpan key-rotation resume` |
| Start of the server with the old key alone after finish. | Unchanged on main: the server refuses to start and names `DUSTPAN_KEY_ENCRYPTION_KEY`. | `opendustpan serve` with the new key |

Evidence for the claims above:

- Main refuses a database that holds keys under more than one wrapper ID, and the text names a rewrap command that does not exist. Proof: `pkg/secret/secret.go:225 (func checkWrapperIDs)` and the test `TestCheckWrapper` at `pkg/secret/builtin_test.go:209 (func TestCheckWrapper)`, subtest "a half-rotated store is refused".
- Main has no rewrap path. Proof: `rg -i rewrap --glob '*.go'` over non-test files on main names only comments and the error text above. No store method updates a stored wrapper ID.
- Main has no rotation table. Proof: the baseline `pkg/store/sqlite/migrations/0168_baseline.sql` and the PostgreSQL baseline hold no such table. The four tables with a `wrapper_id` column are `secret_keys` (line 74), `installation_secret_keys` (line 436), `idempotency_server_key` (line 902) and `item_keys` (line 1065) in the SQLite baseline.

## What Changes

Specified now (every part is decided; owner decisions are marked "(owner, 2026-11-20)"):

- NEW rotation record in SQL, one row for each rotation, in the baseline of each dialect.
- NEW `secret.Rotating`: a `Wrapper` that wraps under the new key and opens under the new key or the old key, and NEW function `secret.Holds(wrapper, id)`, used by the places that compare a stored wrapper ID with the ID of the wrapper (the data key open, the two start checks, the idempotency key open and the restore check).
- NEW rewrap of the four families that the key-encryption key wraps directly: the data keys (`secret_keys`), the installation data keys (`installation_secret_keys`, which hold the sign-in secrets and the other values of `@server`), the item keys (`item_keys` and their blobs) and the idempotency server key. Run as a durable operation with a stored cursor.
- NEW start-up check that needs both keys while a rotation is active and the new key alone after finish. The same check counts the wrapper IDs of the item keys and of the idempotency key, which the start-up check on main does not read.
- NEW audit entries of kind `change` in the scope `@server` for start, resume, finish and abort, with the actor and the wrapper IDs.
- A restore of a backup made under the old key works while both keys are staged. The server refuses a restore while a rotation is active.
- NEW installation action `installation:rotate-key-encryption-key` (constant `ActionInstallationRotateKeyEncryptionKey`), held by the site administrator only, and a fresh authentication step on start, finish and abort (owner, 2026-11-20, question 1). The name follows `installation:store-check` and `installation:read-gates` at `pkg/identity/identity.go:262` and `pkg/identity/identity.go:266`. A row for it in the table of ADR 216 decision 3 is a task.
- NEW source of the old key: `DUSTPAN_KEY_ENCRYPTION_KEY_OLD` and `DUSTPAN_KEY_ENCRYPTION_KEY_OLD_FILE`, with the rules of the current key. The new key writes. The old key only reads. The variable is cleared after the read like the current one (owner, 2026-11-20, question 2).
- NEW REST API under `/api/dustpan/v1/key-rotations` and NEW CLI command group `opendustpan key-rotation`, a client of the API (owner, 2026-11-20, question 3). Each route declares its action and its object.
- NEW docs page `docs/guides/key-rotation.md` and an e2e step with the real binary and the real CLIs.
- The rewrap deletes old item key blobs in the live store only. It never deletes them in a backup target (owner, 2026-11-20, question 4).

Out of scope:

- Tagging or publishing a release.
- Rotation of customer keys in a KMS and of namespace keys. Main has neither a customer key nor a namespace key version, so the data keys and the item keys are wrapped by the key-encryption key directly.
- Change of data key or item key values.
- A console screen for the rotation. The API is complete without it. The console lives in `OpenDustpan/console` and gets its own change.
- A KMS source for the old key. ADR 238 decision 1 says "or a KMS key-encryption key when that setting exists", and no such setting exists on main.

Depends on:

- none

## Usability

Where the path still costs the person time or surprises them:

- The server names the rotation state and both wrapper IDs in each start refusal, so the operator sees which key is missing.
- Finish says plainly that the old key is safe to remove only after it succeeds. Before that, both keys are needed.
- The fresh authentication step is a cost. An access token never gives it, so start, finish and abort answer 403 `mfa.fresh-step-required` for a CLI that holds a token alone. This is the rule of the audit chain approval, whose command sends no request and names the console (`internal/cli/audit_chain.go:16 (func AuditChainApproveCommand)`). The commands `key-rotation start`, `finish` and `abort` follow it: they send no request and print the console path and the route. The commands `status` and `resume` call the API. The console screen is a separate change. The docs page says this in its first lines.
- The old key stays in the environment of each process until the operator removes it after finish. A process that still holds the old key after finish starts and writes a warning that names `DUSTPAN_KEY_ENCRYPTION_KEY_OLD`.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The start check lists the stored wrapper IDs of `secret_keys` and refuses a key that is not the only one. (`pkg/secret/secret.go:210 (func CheckWrapper)`)
- The comparison of wrapper IDs: each stored ID must equal the `ID` of the wrapper. The error says "no rewrap command exists yet". (`pkg/secret/secret.go:225 (func checkWrapperIDs)`)
- The same check for the installation data keys. (`pkg/secret/installation.go:230 (func CheckInstallationWrapper)`)
- Opening a data key refuses a stored wrapper ID that is not the `ID` of the wrapper before it calls the wrapper. (`pkg/secret/envelope.go:48 (func unwrapKey)`)
- A new data key is wrapped by the one wrapper of the service. (`pkg/secret/envelope.go:14 (func wrapNewKey)`)
- The idempotency server key compares its stored wrapper ID with the `ID` of the wrapper in the same way, and a key that this process cannot open is replaced and the replay records are deleted. (`pkg/store/internal/codec/idempotency.go:320 (func open)`, `pkg/store/internal/codec/idempotency.go:266 (func reset)`)
- The server runs the check of the data keys and of the installation data keys at start. It does not read `ListItemKeyWrapperIDs`. (`pkg/dustpan/dustpan.go:2614 (func startUp)`, which calls `secret.CheckWrapper` at line 2625, `pkg/secret/itemkey.go:243 (method ListItemKeyWrapperIDs)`)
- An item key record holds its wrapper ID and the name of its blob. `NewKey` makes the blob key of one write, and its comment names the rewrap to a new version, but no code rewraps an item key. (`pkg/secret/itemkey.go:199 (type ItemKey)`, `pkg/secret/itemkeyblob.go:219 (method NewKey)`)
- The key-encryption key comes from one variable or one file, and the variable is cleared from the environment after the read. (`cmd/opendustpan/main.go:1887 (func loadKeyEncryptionKey)`, clear at `cmd/opendustpan/main.go:1893`; the direct wrapper path clears at `cmd/opendustpan/main.go:929 (func directKeyWrapper)`)
- A restore proves that the one wrapper names one of the wrapper IDs of the manifest. (`pkg/backup/staging.go:295 (func run)`)
- Durable operations with a claim, ordered steps and resume exist. (`pkg/durable/durable.go:1 (package durable)`, `pkg/backup/restore_operation.go:253 (const RestoreOperationKind)`)
- The server audit scope takes an entry through `AppendServer`, and the kind `change` exists. (`pkg/audit/audit.go:815 (method AppendServer)`, `pkg/audit/audit.go:54 (const KindChange)`)
- The installation actions are `server:*`, with `installation:store-check` and `installation:read-gates` as the two `installation:*` constants. No action for the key-encryption key exists. (`pkg/identity/identity.go:262 (const ActionInstallationStoreCheck)`, `pkg/identity/identity.go:266 (const ActionInstallationReadGates)`)
- Only the site administrator holds the actions in `siteOnlyActions`, which are in the administration family and are removed from the account administrator. (`pkg/access/roles.go:150 (func siteOnlyActions)`, `pkg/access/access.go:165` for the family of `server:manage-sign-in`)
- A write that needs a fresh authentication is wrapped by `Handler.fresh`. A request with an access token answers 403 `mfa.fresh-step-required`. (`internal/api/server/freshness.go:221 (method fresh)`, `internal/api/server/freshness.go:112 (func requireFresh)`; first use: `internal/api/server/audit_chain.go:92`)
- A route row declares method, path, scope, actions, resolver and handler; the store check routes are the model. (`internal/api/server/store_checks.go:126 (func row)`, operation labels at `internal/api/server/operations_storecheck.go:15`)
- A command can be a client that sends nothing: the audit chain approve command. A client command takes `Settings: SettingsFor("server", "ca-file")`. (`internal/cli/audit_chain.go:16`, `internal/cli/store_check.go:9`, command table `cmd/opendustpan/commands.go:51`)
- The upgrade check always answers that a key-encryption key exists. (`internal/api/server/upgrade.go:92`; test `internal/api/server/upgrade_test.go:388 (func TestUpgradeCheckAnswersKeyEncryptionKey)`)

Differences from the earlier text of this change: the earlier draft rewrapped "namespace key versions", which main does not have; it named the tests at wrong lines; it needed a rotation route that the audit of main showed was not decided; and it let the new key arrive in a running process, which cannot read a changed environment (the variable is cleared at start, `cmd/opendustpan/main.go:1893`). This version reads both keys at the start of the process.

## Owner decisions (owner, 2026-11-20)

- **Question 1: the action (owner, 2026-11-20).** A NEW installation action, held by the site administrator only, that needs a fresh authentication step. Name: `installation:rotate-key-encryption-key`, in the area `installation:` as ADR 210 decision 7 and the constants at `pkg/identity/identity.go:262` and `pkg/identity/identity.go:266` show. Its object is `ServerObject`. It joins the administration family and `siteOnlyActions` (`pkg/access/roles.go:150`), so the account administrator and the operator do not hold it. One action covers start, show, resume, abort and finish. The fresh step applies to start, finish and abort. Resume and show need the action only, because resume runs batches of a rotation that the administrator already started and show reads.
- **Question 2: the old key (owner, 2026-11-20).** `DUSTPAN_KEY_ENCRYPTION_KEY_OLD` and `DUSTPAN_KEY_ENCRYPTION_KEY_OLD_FILE`, with the same format, file mode rule and clear rule as ADR 23 decision 1. The process builds `Rotating` when it holds the old key, with the new key as the writer and the old key as the reader only.
- **Question 3: API and CLI (owner, 2026-11-20).** A REST API and a CLI command. The command is a client of the API (ADR 17, ADR 39).
- **Question 4: backup targets (owner, 2026-11-20).** The rewrap does not delete old item key blobs in a backup target. A restore of an older backup needs the old key (ADR 217 decision 21), and retention removes old backups.

## Security change

This change is security code: it handles key material, wrapping, start-up refusal and the audit record of a key change. It needs the Claude security review (rev2) on the exact head of its pull request before the pull request is queued. A new push needs a new verdict. Each layer has its own table of hostile cases in the design, and each guard has a test that fails when the guard is removed.

## ADR check

Each quote is copied from the file in `docs/adr/`. The "Changed by" chain and the "Current rule" come from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)".

**ADR 23: Secrets and the built-in key-encryption key** (not owner-tagged; recorded as the reviewed recommendation of an agent session that the owner allowed to become the decision).

- Decisions that apply: 1 (extended by ADR 41), 3 (extended by ADR 151), 4 (changed by ADR 217).
- Changed by: ADR 41, 45, 47, 151, 217, 237 (INDEX line for ADR 23). Current rule: this ADR, with ADR 41, 45, 47, 151, 217, 237.
- Quote 1: "The key comes from the environment variable `DUSTPAN_KEY_ENCRYPTION_KEY`, or from the file that `DUSTPAN_KEY_ENCRYPTION_KEY_FILE` names. The file must not be readable by group or others. The server never reads the key from a command-line flag, because flags show in process lists."
- Quote 3: "If the database holds wrapped keys, and the key is missing or does not match their wrapper ID, the server refuses to start. The error names the variable."
- Quote 4: "This ADR fixes the form now, so that a later rewrap command can rotate the key-encryption key with no format change. The rotation itself is a follow-up. Data keys never rotate, because their ciphertexts live in the stack config of the users."
- Effect: the old key source mirrors decision 1 (owner, 2026-11-20, question 2): a variable and a file, with the same file mode rule, and never a flag. "Data keys never rotate" means the data key values: the rewrap changes only their wrap. The "rewrap command" of decision 4 is the CLI group `opendustpan key-rotation` (owner, 2026-11-20, question 3). The refusal of decision 3 keeps its text and names the old key variable when a rotation is active.
- Conflict: none. A task records the rewrap command name in the new ADR.

**ADR 217: Customer-managed keys for each namespace: the key hierarchy and rotation (owner)** (all decisions owner-tagged).

- Decisions that apply: 19, 21 (current). Decisions 1 and 18 describe a hierarchy that main does not have.
- Changed by: ADR 220 changes decision 18. Current rule: this ADR, with ADR 220.
- Quote 19: "A rotation above them changes their wrap only."
- Quote 21: "The rewrap command of ADR 23 decision 4 wraps each namespace key version and each sign-in secret again. It does not touch customer keys. A restore of a backup made before a rotation of the key-encryption key needs the old key-encryption key, because the backup holds the wraps under it."
- Effect: main has no namespace key version, so the rewrap acts on the records that the key-encryption key wraps directly today. When the namespace key lands, the rewrap changes to its records in that change. Decision 21 is the reason that the rewrap never deletes old item key blobs in a backup target (owner, 2026-11-20, question 4). The restore rule holds as written.
- Conflict: none. The wording "namespace key version" in decision 21 is not met on main; this change records the difference and does not edit the ADR.

**ADR 151: Redaction, and an item key for each history item** (not owner-tagged).

- Decisions that apply: 2 (changed by ADR 217) and the consequence about rotation. The consequence is in the section "Consequences", not in a numbered decision; it extends decision 3 (extends ADR 23 decision 3).
- Changed by: ADR 154, 210, 217, 220, 384. Current rule: this ADR, with ADR 154, 210, 217, 220, 384.
- Quote 1 (consequence): "A rotation of the key-encryption key (the rewrap command of ADR 23) must write a new key blob for each item key and delete the old one, in the live store and in each target. A target that holds a key wrapped by the old key-encryption key stays readable only with that key."
- Quote 2 (ADR 217 decision 21, owner): "A restore of a backup made before a rotation of the key-encryption key needs the old key-encryption key, because the backup holds the wraps under it."
- How the change obeys both: the rewrap writes a new blob for each item key and deletes the old blob in the live store, as the consequence says. It does not delete the old blob in a target, because a target belongs to backups, and a restore of an older backup needs the old blob with the old key (decision 21, owner). The second sentence of the consequence ("stays readable only with that key") already describes this state. Retention removes the old backups and so the old blobs (owner, 2026-11-20, question 4).
- Conflict: the words "and in each target" of the consequence contradict this. The owner decision of 2026-11-20 and ADR 217 decision 21 (owner) win over a consequence that no owner tag covers. A task adds a "Changes:" item in a new ADR and the "Changed by" item in ADR 151, and the Changelog entry that ADR 365 asks for.

**ADR 238: `opendustpan serve` needs decisions** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: ADR 315 extends; ADR 356 changes decision 3. Current rule: this ADR, with ADR 315, 356.
- Quote 1: "An explicit key. `serve` needs `DUSTPAN_KEY_ENCRYPTION_KEY`, `--key-encryption-key-file` (`DUSTPAN_KEY_ENCRYPTION_KEY_FILE`), or a KMS key-encryption key when that setting exists"
- Effect: the new key keeps its sources. The old key adds its own two sources and never a flag. No KMS setting exists, so none is added.
- Conflict: none.

**ADR 216: Namespace administration and the operator scope** (owner-tagged (owner) decisions).

- Decisions that apply: 3 and 4, the table and the list of operator actions; 5, the test of the table.
- Changed by: ADR 273 changes decision 8. Current rule: this ADR, with ADR 273.
- Quote 5: "A test in `pkg/access` iterates each action constant of the area `installation:` (and of the old area `server:` while the code has it). It fails for each constant that the table of decision 3 or the list of decision 4 does not name. A new installation action cannot merge without a row."
- Quote 1: "On a self-hosted installation with the defaults of ADR 215 decision 19, one site administrator holds both scopes."
- Effect: the new action `installation:rotate-key-encryption-key` is an operator-scope action: it "changes how the whole installation runs" and "reads or writes data of more than one namespace" (decision 2). It needs a row in the table of decision 3 in the form of the existing rows (action, scope, namespace action, note): `installation:rotate-key-encryption-key` | operator | none | The key-encryption key wraps the data keys of every namespace. A task adds the row. The task changes no word of an owner decision. Main has no test of decision 5 (`rg 'ADR 216' --glob '*_test.go'` finds no hit); the row is the record.
- Conflict: none.

**ADR 210: The tool-neutral API prefix, paths, actions, versions and style** (not owner-tagged).

- Decision that applies: 7: "An action has two parts and one colon. The area is the object kind of the action, one word in lowercase letters". The name `installation:rotate-key-encryption-key` has the area `installation` and the verb phrase `rotate-key-encryption-key`.
- Effect: the paths of the routes are under `/api/dustpan/v1`, as the store check routes are.

**ADR 17: Every feature has a REST API, authorization and policy** (owner decision of 2026-10-10).

- Quote: "Each feature that OpenDustpan ships has a documented REST API. The console, the CLI and every other client use only that API." and "A test lists every route that the server registers and fails when a route has no declared action."
- Effect: the routes declare `installation:rotate-key-encryption-key` and `ServerObject`.

**ADR 39: Commands never touch the database** (not owner-tagged).

- Quote 1: "`opendustpan serve` is the only command that opens the database, the blob store, a backup target or the key-encryption key. Every other command is a client of the REST API."
- Changed by: ADR 237, 238, 245 change decisions 1 and 4. Current rule: this ADR, with ADR 237, 238, 245.
- Effect: `opendustpan key-rotation` never reads a key. It sends a request with `--server` and a token, and prints the answer. Only `serve` reads the old key.

## Open questions

Open questions: none.

The four questions that this change held open are answered by the owner on 2026-11-20 (section "Owner decisions"):

| Question | Answer (owner, 2026-11-20) | Where it lands |
| --- | --- | --- |
| 1. Which action, which holder, fresh step | New installation action, site administrator only, fresh step | Spec requirement "Authorize a rotation"; tasks 4.1 and 3.3 |
| 2. Source of the old key | `DUSTPAN_KEY_ENCRYPTION_KEY_OLD` and `_OLD_FILE`, mirror of ADR 23 decision 1 | Spec requirement "Stage the old key"; task 4.3 |
| 3. API, command or both | A REST API and a CLI command that is a client of it | Spec requirement "Rotation API and command"; tasks 4.2 and 4.4 |
| 4. Old item key blobs in targets | Not deleted; retention removes old backups | Spec requirement "Keep blobs in targets"; task 4.5 |

Two design choices that follow from the answers and that the PM can check (they need no new owner decision): the fresh step applies to start, finish and abort only, and the commands `start`, `finish` and `abort` send no request, as the audit chain approval does, because an access token never gives a fresh step.
