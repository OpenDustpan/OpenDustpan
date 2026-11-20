---
version: 9
---

Issue: #1010, #1014, #1144.

## Why

**Persona: developer.** A developer keeps one set of config and secrets in a profile and opens it from a workstation or a tool. Main has the profile, its revisions, its reveal route and its document rules. It has no open: no merge of imports, no session, no route to read a merged result. So each tool keeps its own copy.

The user path this change must make work:

1. **API.** Open a static profile with paths, the consumer `api`, `console` or `pulumi`, and a duration through the NEW route POST /api/dustpan/v1/namespaces/{namespace}/profiles/{profile}/open. The answer holds the session ID and its end time, never a value. Repeat the call with the same Idempotency-Key.
2. **API.** Read the values, or one path, with the same principal through the NEW route GET /api/dustpan/v1/namespaces/{namespace}/profile-sessions/{session}.
3. **Docs page.** Follow docs/reference/profiles-api.md to open and read a session.
4. **E2e.** Run the e2e profiles step against the real server over HTTP with a personal access token of a person, on the NEW open route and the NEW session read route.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| The caller lacks profile:open on the namespace, on the open or on a later read of the session | 403. A read after the grant ended also deletes the session. | `GET /api/dustpan/v1/namespaces/{namespace}/profiles/{profile}` |
| The open reaches a provider block | 422 with the NEW code profile.provider and the name of the block. An open of a static profile with no blocks succeeds. | `GET /api/dustpan/v1/namespaces/{namespace}/profiles/{profile}` |
| The open asks a value that is marked never store | 422 with the NEW code profile.never-store and the path. | `POST /api/dustpan/v1/namespaces/{namespace}/profiles/{profile}/open` (NEW route) with a `paths` list that leaves the path out |
| The principal has 100 live sessions | 422 with the code profile.limit. | `GET /api/dustpan/v1/namespaces/{namespace}/profiles/{profile}` (the person reads the revision, and waits for a session to end) |

Evidence for the claims above (each one checked on main at the line):

- Main has no open of a profile. Proof: `rg -n 'func \(s \*Service\) Open' pkg/profile` gives 0 matches. The only secret read is `Service.reveal` (`pkg/profile/service.go:542`), which opens the secrets of one revision.
- Main has no port for profile sessions and no route for them. Proof: `rg -n 'profile-sessions|ErrProvider|ErrNeverStore|ErrFinal' pkg internal --glob '!*_test.go' --glob '!pkg/gate/**' --glob '!pkg/session/**'` gives 0 matches.
- The never-store rule of the document check already exists and a test pins it. Proof: `pkg/profile/document_check.go:271-273` refuses `$neverStore` without `$secret` or `$sealed`, the table of `TestParseDocument` has the case "never store on a plain value" (`pkg/profile/document_test.go:57-61`), and `docs/reference/profiles-api.md:60` documents it.
- The audit kind `access` exists. Proof: `pkg/audit/audit.go:66` (`KindAccess`), and `pkg/audit/audit.go:560` lists it as a known kind.

## What Changes

- NEW `profile.Service.Open` in NEW file `pkg/profile/open.go`: checks `profile:open` on the namespace of the root profile, reads each revision that the open reaches once, merges the imports in list order (a later item wins, a map merges key by key, a list or a scalar replaces, a `null` removes), keeps every `$final` value, evaluates `${ }` values, carries the secret and never-store marks, and refuses a provider block with NEW `profile.ErrProvider`.
- NEW sentinels in `pkg/profile`: `ErrProvider`, `ErrNeverStore`, `ErrFinal`, `ErrReference` and `ErrImportCycle`, each with its own NEW error code and HTTP 422 in `internal/errcode/catalog_profile.go` and `internal/api/profile/profile.go`.
- NEW port `profile.Sessions` with `Put`, `Get`, `Delete` and `Sweep` (the shape of the stopped builder branch, `origin/builder/open-profile-serve-open-sessions`, `pkg/profile/sessions.go`, with the principal owner only), its conformance cases in NEW file `pkg/profile/profiletest/sessions.go`, and one adapter for each dialect. The service seals the result with the data key of the root profile before `Put`, so the store holds only the ciphertext.
- NEW table `profile_open_sessions` in the one baseline of each dialect, edited in place. The table is in `backup.EmptiedTables`, so no backup holds a session.
- NEW route POST /api/dustpan/v1/namespaces/{namespace}/profiles/{profile}/open with `profile:open` and the Idempotency-Key header. The record holds only the session ID.
- NEW route GET /api/dustpan/v1/namespaces/{namespace}/profile-sessions/{session}, for the principal that opened the session.
- One audit entry of the existing kind `access` for each open and each refused open, with no value.
- A NEW port `profile.Evaluator` with one method that computes a `${ }` value of any type, and its adapter in `internal/expr`, because the Evaluator of `internal/expr` today answers only a boolean or a string.
- Docs: the open, the session read, the errors and the session limits in `docs/reference/profiles-api.md`.

Out of scope:

- Provider blocks, the provider port and its adapters, rotators and login blocks. The change `add-profile-provider-port-its` lists this change under "Depends on", so this change lands first and that change replaces the `ErrProvider` refusal in `Service.Open` with the call of the registry. `Service.Open` must not import `pkg/profile/provider`, because that package imports `pkg/profile` (`pkg/profile/provider/run.go:15`).
- The consumers `run`, `opentofu`, `launcher` and `observe`, exports, and the job open with a job token (owner, 2026-11-20: a session belongs to a principal only, and job sessions wait until job opens are specified). `Service.Open` refuses a job token, with a denial and one denied audit entry, and a consumer other than `api`, `console` and `pulumi`.
- The `checks` of a profile (owner, 2026-11-20). They move to gates at the decision point profile.open (ADR 224 decision 5). That gate slice is not built, and no change in `openspec/changes` holds it (proof: `rg -n 'profile[.]open' openspec/changes --glob '*/tasks.md'` finds no task that builds the gate of the decision point). It needs its own change. Until it lands, `Service.Open` refuses a profile that holds a rule in `checks`.
- Expr names other than `values` in an expression (`providers`, `imports`, `profiles`, `context`). `Service.Open` refuses them with `ErrReference`.
- A fresh sign-in for an open (owner, 2026-11-20). An open and a session read need `profile:open` only (ADR 203 decision 11).
- Gates and access requests at the open, attachments, and the console.

## Usability

Where the path still costs the person time or surprises them:

- A provider block names itself in the error.
- A never-store refusal names the path.
- A final refusal names the path and both profiles.
- A session that ended answers 404, and the open answer holds its end time, so a script knows when to open again.

## What main already does

Each fact names its file and line on main. The change specifies only the difference.

- `Service.reveal` opens the secrets of one revision and writes one audit entry of the kind `reveal`. It does not merge imports. It needs a console session with a recent sign-in on the route. (`pkg/profile/service.go:542`, `docs/reference/profiles-api.md:155-158`)
- `Service.require` checks an action on the namespace object. No grant on a single profile exists, so `profile:open` on the namespace covers the root profile and each import, which are all in one namespace. (`pkg/profile/service.go:363-368`, `internal/api/profile/routes.go:110-112`)
- `Sealer` seals under the data key of a scope, and `scopeOf` makes the scope of a profile from its namespace and ID. So a data key of the root profile exists. (`pkg/profile/service.go:22-25`, `pkg/profile/service.go:679`)
- `Service.recordRefused` writes the audit entry of a refused read. (`pkg/profile/service.go:517`)
- The document parser keeps the marks of each entry: `Secret`, `Final`, `NeverStore`. `checkImports` checks only the shape of the import list. `checkChecks` checks only that each rule is present. `refsIn` finds the paths of `values` that an expression reads. (`pkg/profile/document.go:54-67`, `pkg/profile/document_check.go:90`, `pkg/profile/document_check.go:159`)
- The never-store rule of the save exists: `$neverStore` needs `$secret` or `$sealed`. (`pkg/profile/document_check.go:271-273`)
- `pkg/profile` has the sentinels `ErrInvalid`, `ErrNotFound`, `ErrConflict`, `ErrSchemaVersion`, `ErrLimit` and `ErrFreshRequired`. It has no `ErrProvider`, `ErrNeverStore`, `ErrFinal`, `ErrReference` or `ErrImportCycle`. (`pkg/profile/profile.go:25-39`)
- The only consumers in `pkg/profile` are `launcher` and `job-proxy`. (`pkg/profile/exports.go:24-34`)
- `identity.ActionProfileOpen` exists. `audit.KindAccess` exists. (`pkg/identity/identity.go:603`, `pkg/audit/audit.go:66`)
- The route table of `internal/api/profile/routes.go` has the flag `IdempotencyKey`, the keyed rows for the writes, and `serveKeyed`, which runs after the action check. `Call.SecretAnswer` makes the record hold an object ID in place of the answer, but `serveKeyed` passes nil. (`internal/api/profile/routes.go:35`, `internal/api/profile/routes.go:56-63`, `internal/api/profile/routes.go:147`)
- The replay record is keyed by principal, method, path and key. A replay of a secret answer is 409 with the code `request.idempotency-key-replayed-secret` and the object ID. The same key with another body is 422 `request.idempotency-key-reused`. A record lives 24 hours. (`internal/api/idempotent/idempotent.go:240`, `internal/api/idempotent/idempotent.go:253-263`, `internal/api/idempotent/idempotent.go:269-273`)
- `profileErrors` maps the profile sentinels to HTTP, and the codes of the area are in `internal/errcode/catalog_profile.go:1-37`. (`internal/api/profile/profile.go:140-155`, `internal/errcode/catalog_profile.go:1-37`)
- The sqlite baseline already has a table `sessions`, which holds console sessions, and the SDK has the type `session.Sessions`. The new table is `profile_open_sessions` and the new port is `profile.Sessions`, in another package, so no name clashes. (`pkg/store/sqlite/migrations/0168_baseline.sql:368`, `pkg/session/service.go:64`)
- `backup.EmptiedTables` lists tables that a restore empties and a backup leaves out, and `backup.ControlTables` lists tables that a restore keeps. The schema holds the tables of `Tables`, `ControlTables` and `EmptiedTables`, and no other. Each table has a column list in both adapters. (`pkg/backup/coverage.go:81`, `pkg/backup/coverage.go:38`, `pkg/store/sqlite/backup_columns.go:641`)
- `pkg/profile/provider` exists with a registry and a `Run`. It imports `pkg/profile`. (`pkg/profile/provider/registry.go:23`, `pkg/profile/provider/run.go:141`)
- The Evaluator of `internal/expr` has `Compile`, `Bool` and `String`. The result kinds are `bool` and `string`. (`internal/expr/expr.go:119-141`, `internal/selfservice/check.go:129-136`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 200: Profiles: config, secrets and credentials for every tool** (owner-tagged: decision 1).

- Decisions that apply: 1 (current), the error table of the port (ErrNeverStore).
- Changed by: ADR 210 changes decision 9; ADR 222 changes decision 3; ADR 224 changes decision 3; ADR 225 changes decision 3; ADR 286 changes; ADR 347 extends decision 10. Chain to the end: ADR 210, ADR 222, ADR 224, ADR 225, ADR 286, ADR 347, ADR 247, ADR 335, ADR 351, ADR 354, ADR 241, ADR 313, ADR 317, ADR 342, ADR 353, ADR 299, ADR 248, ADR 343, ADR 319, ADR 365, ADR 328, ADR 359, ADR 368.
- Current rule: this ADR, with ADR 210, 222, 224, 225, 286, 347.
- Quote: "A domain package `pkg/profile` (owner). The package is tool-neutral. It imports no tool package, no store adapter and no transport. No tool package imports it."
- Quote: "A value marked never store for a consumer that stores it."
- Effect: Open and the Sessions port live in pkg/profile as tool-neutral code. A session stores its result, so a consumer of a session is a consumer that stores. The open refuses a never-store value for it with ErrNeverStore.
- Conflict: none.

**ADR 201: The profile document, imports and expressions** (not owner-tagged in its decisions).

- Decisions that apply: 3 (current), 4 (changed in part by ADR 228), 6 (current), 12 (current), 13 (current). Decision 11 is superseded by ADR 224 and is NOT implemented here.
- Changed by: ADR 222 changes decisions 4, 8 and 11; ADR 224 supersedes decision 11; ADR 226 changes decision 5; ADR 228 changes decisions 4 and 8; ADR 241 extends decision 7; ADR 286 changes. Chain to the end: ADR 222, ADR 224, ADR 226, ADR 228, ADR 241, ADR 286, ADR 335, ADR 351, ADR 354, ADR 313, ADR 317, ADR 342, ADR 353, ADR 240, ADR 258, ADR 263, ADR 248, ADR 343, ADR 319, ADR 365, ADR 297, ADR 358, ADR 366, ADR 301, ADR 333.
- Current rule: this ADR, with ADR 222, 224, 226, 228, 241, 286.
- Quote: "wins. Maps of `values` and `exports` merge key by key, as JSON Merge"
- Quote: "- **never store**: from `$neverStore` or a block with `neverStore: true`."
- Quote: "also a secret. ADR 209 decision 3 gives what each consumer does with"
- Quote: "An entry holds exactly one of `$value`, `$secret` and `$sealed`. A map"
- Effect: The merge, `$final`, `${ }` of the own tree and the marks are specified. Because an entry holds exactly one of `$value`, `$secret` and `$sealed`, and the save already refuses `$neverStore` on a plain value (`pkg/profile/document_check.go:271-273`), a never-store value is a secret and always comes from `$secret` or `$sealed`. The old draft changed that rule. This version keeps it.
- Conflict: none. The run of `checks` (decision 11) is superseded, see ADR 224.

**ADR 203: Opening a profile: sessions, attachments, delivery, actions and audit** (not owner-tagged).

- Decisions that apply: 1 (extended), 2 (current, without the provider call), 3 (current), 4 (current, direct open only), 9 (current), 11 (current).
- Changed by: ADR 210 changes decision 12; ADR 222 changes decision 5; ADR 225 changes decision 5; ADR 241 extends decision 1; ADR 286 changes; ADR 300 changes decision 5; ADR 353 extends decision 1. Chain to the end: ADR 210, ADR 222, ADR 225, ADR 241, ADR 286, ADR 300, ADR 353, ADR 247, ADR 335, ADR 351, ADR 354, ADR 299, ADR 248, ADR 313, ADR 343, ADR 328, ADR 359, ADR 368.
- Current rule: this ADR, with ADR 210, 222, 225, 241, 286, 300, 353.
- Quote: "An open with the consumer `api`, `console` or"
- Quote: "The store seals the result with the data key of the root profile. A"
- Quote: "- Only the credential that opened a session reads it: the same"
- Quote: "fails with `identity.ErrDenied`, and the server deletes the session."
- Quote: "- Sessions are not in a backup, because they hold credentials that end"
- Quote: "A refused open writes an entry too. A read of a session writes no entry,"
- Quote (decision 11, actions table): "`profile:open` | the profile | Open, read the static secrets of a document, a diff with secrets." The row names no fresh sign-in. So an open and a session read with a personal token need `profile:open` only (owner, 2026-11-20). The reveal differs: it needs a console session with a recent sign-in (`docs/reference/profiles-api.md:155-158`, `pkg/profile/service.go:479-499`). This change keeps the reveal as it is.
- Effect: The change implements the open for the three session consumers, the seal with the key of the root profile, the owner rule (the same principal), the recheck at each read, the exclusion from backups, and one audit entry of kind access for each open and each refused open. Sessions are for a principal. The rule for a job (at most 100 for each job) belongs to the job open, which is out of scope.
- Conflict: none. The old draft added a request field `mode`. Decision 1 has no such field, so this version drops it.

**ADR 209: How each tool reads a profile** (owner-tagged: decision 3).

- Decisions that apply: 3 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "It is the only way for OpenTofu to read a value marked never store."
- Effect: A session consumer stores the value, so the open refuses it. The ephemeral resource of OpenTofu is out of scope.
- Conflict: none.

**ADR 210: The tool-neutral API prefix, paths, actions, versions and style** (decision 6 is owner-tagged).

- Decisions that apply: 6 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "the first request made (for example a token ID or an open-session ID)"
- Quote: "`request.idempotency-key-replayed-secret` and that ID, and never"
- Effect: The open route declares that its replay record holds the session ID. A replay answers 409 with the code request.idempotency-key-replayed-secret and the session ID, as main does for a token (`internal/api/idempotent/idempotent.go:253-263`).
- Conflict: none.

**ADR 224: Gates replace checks and rules** (part of ADR 222, which holds owner decisions).

- Decisions that apply: 5 (current).
- Changed by: ADR 241 extends decision 16; ADR 286 changes; ADR 313 extends decision 1; ADR 317 changes decision 1; ADR 335 extends decisions 9 and 13 and changes decision 16; ADR 342 changes decision 16; ADR 353 extends decision 2 and changes decision 14; ADR 354 extends; ADR 377 changes decision 8 and extends decision 9. None of them changes decision 5. Chain to the end: the same ADRs.
- Current rule: this ADR, with ADR 241, 286, 313, 317, 335, 342, 353, 354, 377.
- Quote: "A profile `check`"
- Quote: "becomes a gate of the profile at `profile.open`."
- Effect: A profile check becomes a gate at the open. This change does not run `checks` and builds no gate. The open refuses a profile that holds a rule in `checks` (owner, 2026-11-20) until the gate slice for `profile.open` lands.
- Conflict: ADR 201 decision 11 (the open runs each check) is superseded by this ADR.

**ADR 228: One Expr package, values only** (decision 1 is owner-tagged).

- Decisions that apply: 1 (current), 2 (current), 5 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Expr never decides whether a change"
- Quote: "package imports `github.com/expr-lang/expr`. depguard holds this rule."
- Effect: `${ }` computes values in the open. `internal/expr` stays the only importer of the library, so the profile reaches Expr through the port `profile.Evaluator`. An Expr value that fails to compute fails the open with `profile.ErrReference` and is never an empty value.
- Conflict: none. The old draft ran `checks` through Expr in the open, which this ADR forbids.

## Open questions

None. Open questions: none. The three questions of the first draft have the owner's answers below.

Decisions (owner, 2026-11-20):

1. **A profile that holds `checks` (owner, 2026-11-20).** The open refuses it with `ErrInvalid` that names the profile, until the gate slice for the decision point profile.open lands (ADR 224 decision 5). No change in `openspec/changes` builds that gate today, so the slice needs its own change. The hostile test "Rule in checks" in NEW TestOpen (pkg/profile/open_test.go) fails when the guard goes (task 2.5).
2. **No fresh sign-in for an open with a personal token (owner, 2026-11-20).** ADR 203 decision 11 gives `profile:open` only. The reveal keeps its fresh sign-in. The docs page states the difference (task 4.2).
3. **Owner kind `principal` only (owner, 2026-11-20).** A job session is out of scope until job opens are specified. A job token is refused: fail closed, one denied audit entry, no read, no store.

Questions considered and answered:

- Which audit kind does an open write? Kind access, per ADR 203 decision 9, and the kind exists on main (`pkg/audit/audit.go:66`). The old draft called it a NEW kind.
- Does an open bind a session to a token or to a principal? To the principal. ADR 203 decision 3: "- Only the credential that opened a session reads it: the same" principal. A second token of the same principal reads the session. Reveal binds nothing, so this is the narrowest rule that the ADR gives for a person.
- Does a key of the root profile exist? Yes: `Sealer` seals under the data key of a scope, and `scopeOf(namespace, profile ID)` makes the scope (`pkg/profile/service.go:22-25`, `pkg/profile/service.go:679`).
- Does `$neverStore` on a plain value save? No. Main refuses it on purpose, a test pins it, and the docs say so. The old task that changed the rule is removed.
- Does the 101st open of a job count? Jobs are out of scope. The limit of 100 live sessions is for each principal.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs, and rewritten against main by the PM. Every quote, path and ADR citation was checked by code.
