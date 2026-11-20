---
version: 12
---

Issue: none. The PM directed this change; no issue exists.

## Why

**Persona: operator.** An operator replaces the key-encryption key of the installation after a leak or on a schedule. Main fixes the form of the wrapper ID so that a later rewrap can rotate the key, but no rewrap exists. A database that holds keys under two wrapper IDs is refused at start, and the refusal text names a rewrap command that does not exist. Today the operator can replace the key only with a database edit.

The user path this change must make work (every step is decided, owner, 2026-11-20):

1. **Docs page.** The operator reads the rotation page `docs/guides/key-rotation.md`. It lists what the operator needs, the sign-in over the API and the order of the steps.
2. **Server start.** The operator starts each server process with the new key in `DUSTPAN_KEY_ENCRYPTION_KEY` (or `DUSTPAN_KEY_ENCRYPTION_KEY_FILE`) and the old key in `DUSTPAN_KEY_ENCRYPTION_KEY_OLD` (or `DUSTPAN_KEY_ENCRYPTION_KEY_OLD_FILE`). Each process writes the wrapper IDs that it holds in its instance row.
3. **API.** A site administrator signs in with a local-user password over the API, reads the CSRF value of the session, and starts the rotation with the session cookie, the CSRF header and the `Origin` header (owner, 2026-11-20, round 2). The administrator watches the progress with `opendustpan key-rotation status`. The commands `key-rotation start` and `key-rotation finish` send no request and print this path and the docs page.
4. **API and CLI.** The site administrator resumes a rotation that stopped.
5. **API.** The site administrator, with a fresh authentication step, finishes the rotation when no record wraps under the old key.
6. **Server start.** The operator removes the old key and starts each server process with the new key alone.

A rotation only moves forward: start, resume, finish (owner, 2026-11-20, round 2). To go back to the old key, finish, then start a rotation in the other direction.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Start of the server with one key while a rotation is active. | The server refuses to start, names both key settings and the rotation state. | `opendustpan serve` with both keys staged |
| Start of the server with the old key still staged after finish. | The server refuses to start, names `DUSTPAN_KEY_ENCRYPTION_KEY_OLD` and `DUSTPAN_KEY_ENCRYPTION_KEY_OLD_FILE` and the one variable to remove (owner, 2026-11-20, round 2). | `opendustpan serve` with the new key alone |
| Start of a rotation from a process that holds no old key. | 409 with the code `key-rotation.old-key-missing`; the text names `DUSTPAN_KEY_ENCRYPTION_KEY_OLD`. | `opendustpan serve` with the old key staged |
| Start or finish while a running instance does not hold both wrapper IDs. | 409 with the code `key-rotation.instance-lacks-key`; the text names the instance ID. | Restart that instance with both keys, or `DELETE /api/server/instances/{id}` for a row of a process that is gone |
| Start while a backup runs. | 409 with the code `key-rotation.backup-running`. | Wait for the backup, then repeat the start |
| Backup or restore while a rotation is active. | 409 with the code `backup.key-rotation-active` or `restore.key-rotation-active`. | `opendustpan key-rotation status` |
| Start or finish with an access token. | 403 with the code `mfa.fresh-step-required`. | Sign in over the API as the docs page shows, and repeat the call |
| Finish while a record still holds the old key, or a blob delete is pending. | 409 with the code `key-rotation.incomplete` and the count for each family. | `opendustpan key-rotation resume` |
| Resume while another instance holds the claim. | 409 with the code `key-rotation.claimed`. | Repeat after the lease ends |
| Start of the server with the old key alone after finish. | Unchanged on main: the server refuses to start and names `DUSTPAN_KEY_ENCRYPTION_KEY`. | `opendustpan serve` with the new key |

Evidence for the claims above:

- Main refuses a database that holds keys under more than one wrapper ID, and the text names a rewrap command that does not exist. Proof: `pkg/secret/secret.go:225 (func checkWrapperIDs)` and the test `TestCheckWrapper` at `pkg/secret/builtin_test.go:209 (func TestCheckWrapper)`, subtest "a half-rotated store is refused".
- Main has no rewrap path. Proof: `rg -i rewrap --glob '*.go'` over non-test files on main names only comments and the error text above. No store method updates a stored wrapper ID.
- Main has no rotation table. Proof: the baseline `pkg/store/sqlite/migrations/0168_baseline.sql` and the PostgreSQL baseline hold no such table. The four tables with a `wrapper_id` column are `secret_keys` (`CREATE TABLE` at line 71, column at line 74), `installation_secret_keys` (line 438, column at line 440), `idempotency_server_key` (line 904, column at line 907) and `item_keys` (line 1082, column at line 1088) in the SQLite baseline.
- Main has no record of the wrapper IDs that a running process holds. Proof: `instance_versions` in the SQLite baseline (`CREATE TABLE` at line 1196) holds `id`, `binary_version` and `started_at_ms` only.

## What Changes

Specified now (every part is decided; owner decisions are marked "(owner, 2026-11-20)"; a rule that the reviewer of this change prescribed is marked "(reviewer-prescribed)"):

- NEW rotation record in SQL, one row for each rotation, with the states `started` and `finished`, in the baseline of each dialect. NEW table of the pending blob deletes of a rotation. NEW table of the wrapper IDs of each instance row.
- NEW `secret.Rotating`: a `Wrapper` that wraps under one key by a rule of the stored rotation state and opens a record by its stored wrapper ID, and NEW function `secret.Holds(wrapper, id)`, used by the places that compare a stored wrapper ID with the ID of the wrapper (the data key open, the two start checks, the idempotency key open and the restore check).
- NEW singleton row `key_rotation_state` (always present, with a generation) and NEW write fence: each write of a wrapped record reads that row in its own transaction (`FOR SHARE` on PostgreSQL, the write lock on SQLite) and fails when its generation is stale, or when its wrapper ID is not the new ID while a rotation is started; start and finish take the row `FOR UPDATE` (reviewer-prescribed; round 3).
- NEW page cursors that carry the wrapper ID, so the list routes keep working while both keys are staged (round 3), behind the NEW narrow port `secret.CursorSealer` (seal and open only), so the list handlers never hold a `secret.Wrapper` (round 4).
- NEW rewrap of the four families that the key-encryption key wraps directly: the data keys (`secret_keys`), the installation data keys (`installation_secret_keys`, which hold the sign-in secrets and the other values of `@server`), the item keys (`item_keys` and their blobs) and the idempotency server key. Run as a durable operation. The old item key blob is deleted from a stored list of pending deletes, so a crash leaves no blob behind (reviewer-prescribed).
- NEW start-up check with a table of cases (design decision 10): both keys while a rotation is active, the new key alone after finish, a refusal when the old key is still staged after finish, and a count of the wrapper IDs of the item keys and of the idempotency key.
- NEW audit entries of kind `change` in the scope `@server` for start, resume, finish and for each record that the rewrap cannot open, with the actor and the wrapper IDs.
- A backup is refused while a rotation is active, and a restore is refused while a rotation is active (reviewer-prescribed). A restore needs a key for every wrapper ID of the manifest.
- NEW installation action `installation:rotate-key-encryption-key` (constant `ActionInstallationRotateKeyEncryptionKey`), held by the site administrator only, and a fresh authentication step on start and finish (owner, 2026-11-20, question 1). The name follows `installation:store-check` and `installation:read-gates` at `pkg/identity/identity.go:262` and `pkg/identity/identity.go:266`. The service checks the action itself, with the principal and the authorizer (reviewer-prescribed). A row for it in the table of ADR 216 decision 3 is a task.
- NEW source of the old key: `DUSTPAN_KEY_ENCRYPTION_KEY_OLD` and `DUSTPAN_KEY_ENCRYPTION_KEY_OLD_FILE`, with the rules of the current key. The two variables together are refused. Both are sensitive. The new key writes. The old key only reads (owner, 2026-11-20, question 2).
- NEW REST API under `/api/dustpan/v1/key-rotations` with five routes (start, list, show, resume, finish) and NEW CLI command group `opendustpan key-rotation` with `status`, `resume`, `start` and `finish` (owner, 2026-11-20, question 3 and round 2). Each route declares its action and its object.
- NEW docs page `docs/guides/key-rotation.md` and an e2e step with the real binary and the real CLIs. The page also says that old wraps stay in the database history and in backups.
- The rewrap deletes old item key blobs in the live store only. It never deletes them in a backup target (owner, 2026-11-20, question 4).

Out of scope:

- Tagging or publishing a release.
- Rotation of customer keys in a KMS and of namespace keys. Main has neither a customer key nor a namespace key version, so the data keys and the item keys are wrapped by the key-encryption key directly.
- Change of data key or item key values.
- A console screen for the rotation. The API is complete without it, and the console screen is not a dependency (owner, 2026-11-20, round 2). The console lives in `OpenDustpan/console` and gets its own change.
- A CLI flow that gets a fresh authentication step.
- An abort of a rotation (owner, 2026-11-20, round 2).
- A KMS source for the old key. ADR 238 decision 1 says "or a KMS key-encryption key when that setting exists", and no such setting exists on main.

Depends on:

- none

## Usability

Where the path still costs the person time or surprises them:

- The server names the rotation state and both wrapper IDs in each start refusal, so the operator sees which key is missing.
- Finish says plainly that the old key is safe to remove only after it succeeds. Before that, both keys are needed. After finish, a process that still holds the old key does not start, and the text names the variable to remove.
- The fresh authentication step is a cost. An access token never gives it, so start and finish answer 403 `mfa.fresh-step-required` for a CLI that holds a token alone. The person signs in over the API: `POST /api/dustpan/v1/sign-in/password` answers a session cookie, `GET /api/server/session` answers the CSRF value, and each call to start or finish sends the cookie, the `X-Dustpan-Csrf` header and an `Origin` header that equals the public URL (owner, 2026-11-20, round 2). The docs page shows each request, in the form of `docs/guides/console-sign-in.md` (section "What you need", line 8) and `docs/guides/first-administrator.md` (line 72, the CSRF read); `docs/guides/connect-github.md` has no such section. The commands `key-rotation start` and `key-rotation finish` send no request and print this path and the docs page. The commands `status` and `resume` call the API.
- A backup and a restore stop while a rotation is active. The window is the time of the rewrap. The docs page says to make a backup before the start and another after the finish.
- Old wraps stay in the database history (dead tuples, free pages, the write-ahead log) and in each backup. The docs page says this, so the operator does not treat a rotation as the end of a leaked key. A leaked old key still opens a copy of the database made before the finish.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The start check lists the stored wrapper IDs of `secret_keys` and refuses a key that is not the only one. (`pkg/secret/secret.go:210 (func CheckWrapper)`)
- The comparison of wrapper IDs: each stored ID must equal the `ID` of the wrapper. The error says "no rewrap command exists yet". (`pkg/secret/secret.go:225 (func checkWrapperIDs)`)
- The same check for the installation data keys. (`pkg/secret/installation.go:230 (func CheckInstallationWrapper)`)
- Opening a data key refuses a stored wrapper ID that is not the `ID` of the wrapper before it calls the wrapper. (`pkg/secret/envelope.go:48 (func unwrapKey)`, the comparison at line 55)
- A new data key is wrapped by the one wrapper of the service. (`pkg/secret/envelope.go:14 (func wrapNewKey)`)
- The AAD of a wrapped key binds the scope, not the wrapper ID. The built-in wrapper seals with a random nonce and the AAD, and a wrong key fails with `ErrDecrypt`. (`pkg/secret/builtin.go:73 (method Wrap)`, `pkg/secret/builtin.go:85 (method Unwrap)`)
- The idempotency server key compares its stored wrapper ID with the `ID` of the wrapper in the same way, and a key that this process cannot open is replaced and the replay records are deleted. (`pkg/store/internal/codec/idempotency.go:197 (method Resolve)`, `pkg/store/internal/codec/idempotency.go:266 (method reset)`, `pkg/store/internal/codec/idempotency.go:320 (method open)`)
- The server runs the check of the data keys and of the installation data keys at start. It does not read `ListItemKeyWrapperIDs`. The instance row is written before the key check. (`pkg/dustpan/dustpan.go:2614 (func startUp)`, which calls `registerInstance` and then `secret.CheckWrapper` at line 2625; `pkg/secret/itemkey.go:243 (method ListItemKeyWrapperIDs)`)
- An item key record holds its wrapper ID and the name of its blob. `NewKey` makes the blob key of one write, and its comment names the rewrap to a new version, but no code rewraps an item key. (`pkg/secret/itemkey.go:199 (type ItemKey)`, `pkg/secret/itemkeyblob.go:219 (method NewKey)`)
- The key-encryption key comes from one variable or one file, and the variable is cleared from the environment after the read. When both are set, the value wins and the file is ignored. (`cmd/opendustpan/main.go:1887 (func loadKeyEncryptionKey)`, clear at `cmd/opendustpan/main.go:1893`, `cmd/opendustpan/main.go:1905 (func loadKeyEncryptionKeyFrom)`; the direct wrapper path clears at `cmd/opendustpan/main.go:929 (func directKeyWrapper)`)
- The key file is checked with `os.Stat` and then read with `os.ReadFile`, which follows a symbolic link. (`cmd/opendustpan/main.go:1935 (func readKeyFile)`)
- The server clears the variables of the settings that are marked sensitive, and each secret variable that no setting declares. (`cmd/opendustpan/envclear.go:22 (func clearSensitiveEnv)`, `internal/cli/setting.go:363 (func SensitiveEnvs)`; the setting `DUSTPAN_KEY_ENCRYPTION_KEY` is in `internal/cli/settings.go`)
- A restore proves that the one wrapper names one of the wrapper IDs of the manifest. A manifest with two IDs passes when the wrapper holds only one of them. (`pkg/backup/staging.go:295 (method run of backupKeyCheck)`, the comparison at line 306)
- A backup starts in `begin`, and a restore starts in `Start` with `admitStart`. Neither reads a rotation state. (`pkg/backup/service.go:689 (method begin)`, `pkg/backup/service.go:368 (method Start)`, `pkg/backup/restore_service.go:519 (method admitStart)`)
- The instance version row holds the ID, the binary version and the start time. It is no liveness lease: a process that dies leaves its row, and an administrator removes it with `DELETE /api/server/instances/{id}`. (`pkg/installation/instances.go:29 (type Instance, doc "leaves its row")`, `pkg/dustpan/instances.go:66 (method registerInstance)`, `internal/api/server/instances.go:32`)
- Durable operations with a claim, ordered steps and resume exist. (`pkg/durable/durable.go:1 (package durable)`, `pkg/backup/restore_operation.go:253 (const RestoreOperationKind)`)
- The server audit scope takes an entry through `AppendServer`, and the kind `change` exists. `AppendServer` of SQLite opens its own transaction. The method `inAuditedTx` writes a change and its entry in one transaction. (`pkg/audit/audit.go:815 (method AppendServer)`, `pkg/audit/audit.go:54 (const KindChange)`, `pkg/store/sqlite/audit.go:61 (method AppendServer)`, `pkg/store/sqlite/audit.go:86 (method inAuditedTx)`, `pkg/store/postgres/audit.go:125 (method inAuditedTx)`)
- A service can take the caller and the authorizer, check the action at the point of use and return a sentinel for a denial. (`internal/trustrulesvc/service.go:471 (method authorizeCreate)`, `pkg/identity/identity.go:1138 (type Authorizer)`)
- The installation actions are `server:*`, with `installation:store-check` and `installation:read-gates` as the two `installation:*` constants. No action for the key-encryption key exists. (`pkg/identity/identity.go:262 (const ActionInstallationStoreCheck)`, `pkg/identity/identity.go:266 (const ActionInstallationReadGates)`)
- Only the site administrator holds the actions in `siteOnlyActions`, which are in the administration family and are removed from the account administrator. (`pkg/access/roles.go:150 (func siteOnlyActions)`, `pkg/access/access.go:165` for the family of `server:manage-sign-in`)
- A write that needs a fresh authentication is wrapped by `Handler.fresh`. A request with an access token answers 403 `mfa.fresh-step-required`. (`internal/api/server/freshness.go:221 (method fresh)`, `internal/api/server/freshness.go:112 (func requireFresh)`; first use: `internal/api/server/audit_chain.go:92`)
- A route row declares method, path, scope, actions, resolver and handler; the store check routes are the model. (`internal/api/server/store_checks.go:126 (func row)`, operation labels at `internal/api/server/operations_storecheck.go:15`)
- A command can be a client that sends nothing: the audit chain approve command. A client command takes `Settings: SettingsFor("server", "ca-file")`. (`internal/cli/audit_chain.go:17 (func AuditChainApproveCommand)`, `internal/cli/store_check.go:9`, command table `cmd/opendustpan/commands.go:51`)
- The password sign-in answers a session cookie, and `GET /api/server/session` answers the CSRF value. (`internal/api/server/localusers.go:43 (const localSignInPath)`, `internal/api/server/signin.go:48 (const sessionPath)`, `docs/guides/local-users.md` section "Sign in")
- The upgrade check always answers that a key-encryption key exists. (`internal/api/server/upgrade.go:92`; test `internal/api/server/upgrade_test.go:388 (func TestUpgradeCheckAnswersKeyEncryptionKey)`)

Differences from the earlier text of this change: the earlier draft rewrapped "namespace key versions", which main does not have; it named the tests at wrong lines; it needed a rotation route that the audit of main showed was not decided; and it let the new key arrive in a running process, which cannot read a changed environment (the variable is cleared at start, `cmd/opendustpan/main.go:1893`). This version reads both keys at the start of the process.

## Owner decisions (owner, 2026-11-20)

- **Question 1: the action (owner, 2026-11-20).** A NEW installation action, held by the site administrator only, that needs a fresh authentication step. Name: `installation:rotate-key-encryption-key`, in the area `installation:` as ADR 210 decision 7 and the constants at `pkg/identity/identity.go:262` and `pkg/identity/identity.go:266` show. Its object is `ServerObject`. It joins the administration family and `siteOnlyActions` (`pkg/access/roles.go:150`), so the account administrator and the operator do not hold it. One action covers start, show, resume and finish. The fresh step applies to start and finish. Resume and show need the action only, because resume runs batches of a rotation that the administrator already started and show reads.
- **Question 2: the old key (owner, 2026-11-20).** `DUSTPAN_KEY_ENCRYPTION_KEY_OLD` and `DUSTPAN_KEY_ENCRYPTION_KEY_OLD_FILE`, with the same format, file mode rule and clear rule as ADR 23 decision 1. The process builds `Rotating` when it holds the old key, with the new key as the writer and the old key as the reader only.
- **Question 3: API and CLI (owner, 2026-11-20).** A REST API and a CLI command. The command is a client of the API (ADR 17, ADR 39).
- **Question 4: backup targets (owner, 2026-11-20).** The rewrap does not delete old item key blobs in a backup target. A restore of an older backup needs the old key (ADR 217 decision 21), and retention removes old backups.
- **Round 2, reach without the console (owner, 2026-11-20, "Yes as recommended").** Document the API path: a local-user password sign-in gives a console session with a CSRF value, and the call to start or finish sends the cookie, the CSRF header and `Origin`, as in `docs/guides/console-sign-in.md` "What you need" and `docs/guides/first-administrator.md`. No new CLI flow for a fresh step. The CLI `start` and `finish` print that path and the docs page. The console screen comes later and is not a dependency.
- **Round 2, abort (owner, 2026-11-20, "Yes as recommended").** Remove abort. A rotation only moves forward: start, resume, finish. To go back, finish, then start a rotation in the other direction.
- **Round 2, old key held after finish (owner, 2026-11-20, "Yes as recommended").** The server refuses to start. The error names the variable to remove and one command. There is no warning.

## Reviewer-prescribed rules

An Opus review (rev2) of version 8 found the gaps below. Each rule follows from that review, and none changes an owner decision.

- The wrapper that a process writes with follows the stored rotation state, and a write fence in the store enforces it (design decision 3).
- Each running instance stores the wrapper IDs that it holds, and start and finish refuse while one lacks a key (design decision 8).
- The start-up and restore cases are one table (design decision 10 and the spec requirement "Start-up with a rotation").
- A backup is refused while a rotation is active. This is the simplest rule. The other choice, a delete of the old blobs in a last step at finish, changes the order of the rewrap and leaves the live store with no point where a backup is safe (design decision 9).
- The old blob delete is a stored list, and finish needs the list empty (design decision 5).
- The service checks the action itself (design decision 11).

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

**ADR 364: Unexported fields and golangci-lint before added checks (owner, 2026-11-10)** (owner-tagged).
- Decisions that apply: 1.
- Quote: "The guard against a bug class is Go visibility first. Fields are unexported, so that only a constructor can make a value, and the constructor checks the invariants of the value. Rules that visibility cannot express go into the existing golangci-lint configuration (for example `forbidigo`, `depguard` and `exhaustruct`). This decision adds no custom analyzer and no new tool."
- Effect: the guard against a plain `Unwrap` of a cursor is visibility (the port `secret.CursorSealer`, which has no `Unwrap`) and one `forbidigo` rule for the remaining callers. The change adds no source-scan test, no analyzer and no tool (round 4).
- Conflict: none.

## Open questions

Status: parked on Q-3 for the part that depends on it. Do not start the tasks that Q-3 blocks (the path under a second-factor rule, task 4.4).

Open questions: 1.

**Q-3. What is the API path for a site administrator when the MFA policy asks for a second factor, given that main has no second sign-in step and step-up does not satisfy `Handler.fresh`?**

Facts on main (`bff8277dd`):
- "A password sign-in next to an identity provider needs a second factor. The server cannot ask for a second factor yet. So when you set the switch to `"true"` on an installation with an identity provider, each password sign-in answers `403` with the error code `local-user.second-factor-required`." (`docs/guides/local-users.md:45-48`).
- "The server does not ask for a passkey as a second factor yet." (`docs/guides/passkeys.md:9-10`). A passkey is a sign-in of its own for a local user (`pkg/mfa/mfa.go:5`).
- `Handler.fresh` passes a request only when the console session signed in recently enough: `sessionauth.Fresh(..., caller.Match.Session.AuthTime, window.duration())` (`internal/api/server/freshness.go:205`); an access token gets 403 `mfa.fresh-step-required` (`internal/api/server/freshness.go:215-218`). The step-up factor (TOTP) is not a sign-in and sets no session `AuthTime` that `fresh` reads, as far as the search of `AuthTime` over non-test code finds (sign-in paths only: `devsignin.go:137`, `passkeysignin.go:288`).
- Decision 12 of round 4 said a TOTP second step works from `curl`. That fact is wrong. Decision 12 is withdrawn in the record as "wrong fact, returned to the owner". It is not deleted from the history of this change.

Options:
- Option 1: the page says that under a second-factor rule the site administrator signs in with a passkey (a sign-in of its own that sets `AuthTime`) and shows the calls. Cost: a passkey needs a WebAuthn client, not `curl`; the page needs a check that the passkey sign-in is allowed under the rule, which this change did not verify.
- Option 2 (recommended): the page says that under a second-factor rule the rotation start and finish wait for the console screen, and the page claims no API path for that case. Cost: no operator path under the rule until the console screen exists.
- Option 3: a server change in a later capability: a second sign-in step after the password, or a step-up that satisfies the fresh check. Cost: a new capability with its own change, and a change of the fresh-step meaning, which the owner-tagged ADRs on freshness cover (ADR 56, ADR 227); it needs the owner.
- Recommendation: option 2, because options 1 and 3 add a claim or a capability that this change cannot check. Plain 3 a.m. summary: under a second-factor rule an administrator cannot run start and finish from the command line today. The page says so, and the console screen will give the path.

Tasks that depend on Q-3 stay blocked: the second-factor part of task 4.4 and its e2e step.

All other questions are answered by the owner on 2026-11-20 (section "Owner decisions"):

| Question | Answer (owner, 2026-11-20) | Where it lands |
| --- | --- | --- |
| 1. Which action, which holder, fresh step | New installation action, site administrator only, fresh step | Spec requirement "Authorize a rotation"; tasks 4.1 and 3.3 |
| 2. Source of the old key | `DUSTPAN_KEY_ENCRYPTION_KEY_OLD` and `_OLD_FILE`, mirror of ADR 23 decision 1 | Spec requirement "Stage the old key"; task 4.3 |
| 3. API, command or both | A REST API and a CLI command that is a client of it | Spec requirement "Rotation API and command"; tasks 4.2 and 4.4 |
| 4. Old item key blobs in targets | Not deleted; retention removes old backups | Spec requirement "Keep blobs in targets"; task 4.5 |
| R2-1. How a person reaches start and finish before the console exists | The documented API path with a password sign-in; the CLI prints it | Spec requirement "Rotation API and command"; task 4.4 |
| R2-2. What abort means | There is no abort; a rotation only moves forward | Spec requirement "Finish"; design decision 13 |
| R2-3. A process that holds the old key after finish | The server refuses to start and names the variable | Spec requirement "Start-up with a rotation"; design decision 10 |
| 12 (was Q-1). The API path under an MFA policy | Withdrawn: wrong fact, returned to the owner. Main has no second sign-in step (`docs/guides/local-users.md:45-48`) | Open question Q-3; design decision 14; task 4.4 (blocked part) |
| 13 (was B-c). A restore of a backup from before the rotation | Row 11 refuses only when the stored data holds no old ID; the restored backup runs on a `Rotating` (B, A) server through `POST /api/dustpan/v1/restores`, then a `Rotating` (A, B) server starts (row 12) and runs a new rotation; this keeps ADR 217 decision 21 | Spec requirement "Start-up with a rotation" (rows 11 and 12, scenario "Restore of a backup from before the rotation"); design decision 10; task 1.9 |

Design choices that follow from the answers and that the PM can check (they need no new owner decision): the fresh step applies to start and finish only, and the commands `start` and `finish` send no request, as the audit chain approval does, because an access token never gives a fresh step.
