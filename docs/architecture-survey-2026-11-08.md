# Architecture survey: duplicated concepts and the deep cuts

Date: 2026-11-08. Commit surveyed: `dd563bf` (`main`). Full history: 1,818 commits.

This survey looks for one kind of defect: one concept that the code
implements in two or more ways, so that a fix in one copy does not reach the
others. For each candidate it gives the evidence, the bug classes, the
structural fix, the cost and the ADRs that apply. It does not propose more
tooling as a fix. A lint rule is listed only where it holds a structural
change in place.

Counts are production code only (no `_test.go`, no `*test` packages, no
`internal/db`, no e2e), unless a line says otherwise. Issue and pull request
numbers come from the read-only GitHub search API. The search API hit its
rate limit once during the survey, so some issue counts are lower bounds.

Some names that reviewers cited (`authorizeRefusal`, `opaqueDenialError`,
`appendBreakGlassRefusal`, `guard.Record`) are not on `main` at `dd563bf`.
They are in open pull requests. The survey uses the copies that are on
`main`.

## Labels

- **Ready**: the fix follows the current ADRs. No owner decision is needed.
- **Needs owner**: the fix needs the owner to decide the order or the scope
  of an owner decision, but it does not contradict one.
- **Needs RFC + owner**: the fix contradicts an owner-tagged ADR decision.
  It is not ready.
- **One-way door**: after the first tag, a later change needs an upgrade
  path (ADR 72, ADR 322 decision 2). Before the first tag, nothing has
  shipped, so every cut here is cheaper now than later.

## Ranked list

The rank is (bugs removed) / (cost). "Bugs" counts the issues and pull
requests in the evidence that the cut makes impossible, plus the live defects
found. "Cost" is in pull requests.

| Rank | Cut | Bugs (cited) | Cost | Label |
| --- | --- | --- | --- | --- |
| 1 | One route boundary for authorization and refusals | 15, 1 live | 6 to 10 PRs | Ready |
| 2 | One request intake: body decoder and origin check | 7 | 2 to 3 PRs | Ready |
| 3 | Every outbound call through `egresshttp`, one webhook signer | 1 live, 4 | 1 to 2 PRs | Ready |
| 4 | One clock: the application clock is the only clock | 9 | 4 to 6 PRs | Ready |
| 5 | Stored group references and one group-change guard | 6 | 3 to 4 PRs | Ready (index); Needs RFC + owner (IDs in Cedar text) |
| 6 | The central table list, and costs derived from it | 23 | 4 to 6 PRs | Needs owner (order, ADR 274 decision 2) |
| 7 | One error registry and a committed-write answer | 9 | 5 to 8 PRs | Ready; One-way door at the tag |
| 8 | One cursor codec and one page-size rule | 3, 1 live | 2 to 3 PRs | Ready; One-way door at the tag |
| 9 | One secret reader and one prefixed-token type | 3 | 2 to 3 PRs | Ready |
| 10 | One backoff and one limiter with a stored variant | 3 | 3 to 4 PRs | Ready (keep ADR 295 decision 4) |
| 11 | Constructors for wide domain structs | 2 (17 merge breaks) | 3 to 5 PRs | Ready |
| 12 | Fakes replaced by the real SQLite store | 2 | 1 + 8 PRs | Ready |
| 13 | ADR 274 areas: store logic once, SQL per dialect | authoring cost | 20 PRs | Needs owner (order); RFC before MySQL |
| 14 | Hand-kept aggregates: baseline hash files | merge conflicts | 1 PR | Needs owner (ADR 322 direction 4) |
| 15 | Audit: record the rule that the code follows | 2 | 2 PRs | Ready (ADR 60 decision 3 is not owner-tagged) |

Rejected as a root cause: audit fan-out (candidate 2), service layering as a
source of bugs (half of candidate 8), and fixes missed in one dialect
(part of candidate 6). See "Rejected".

Order and dependencies:

```
1 ──> 5 (the guard uses the route boundary for its refusal)
1 ──> 7 (the boundary renders through the registry)
2      independent
3      independent
4 ──> 6 (the cost model and the restore read the one clock)
RFC 103 step 8 ──> 6 ──> 13      (ADR 274 decision 2, owner)
8, 9, 10, 11, 12, 14, 15  independent
```

Cuts 1, 2, 3 and 4 stop most of the current bleeding. They need no owner
decision. Cut 6 needs one owner decision on order.

---

## 1. One route boundary for authorization and refusals

PM candidates 1 and part of 11, and the Foremen evidence (a). **Confirmed.**

### Evidence

- The shared pipeline exists: `internal/api/route/enforce.go:102` `Enforce`
  runs Authenticate, Resolve, Authorize, Serve. `route/authorize.go:55`
  `AuthorizeObject` maps a refused read to not-found. The audited check is
  `internal/api/refusal/refusal.go:169` `Recorder.Authorize`.
- Only some APIs use it. The others keep their own loops:
  `internal/api/pulumi/routes.go:817`, `tfe/routes.go:617`,
  `server/routes.go:429`, and an own `authorizeRoute` in triggers, settings
  and objectcontext.
- 54 authorization sites in 34 handler files. 17 of them call the raw
  `Authorizer` and skip the `Recorder`, so a denial writes no audit entry.
  Examples: `tfe/workspaces.go:420,507,737`, `inventory/inventory.go:619`,
  `jobs/logs.go:287` (re-check during a log stream, no entry on denial),
  `server/restores.go:243`.
- 27 more sites in 17 service and store files: `pkg/access/service.go` (5),
  `pkg/trigger/manager.go:501`, `pkg/job/service_start.go:254,304`,
  `pkg/manuallock/service.go:343`, `internal/outputsharesvc/checker.go:156`.
  `pkg/profile/service.go:517` audits its refusal. `pkg/trigger/manager.go`
  does not.
- 20 handlers build their own `refusal.Denial`.
- **Live existence oracle.** `readRefusal` exists four times:
  `route/authorize.go:92` keeps the IP-allowlist refusal as 403. The copies
  `triggers/routes.go:254`, `objectcontext/routes.go:181` and
  `settings/routes.go:264` lack that branch, so they answer 404 to a caller
  outside the allowlist. Jobs, drift and runner answer 403
  `access.ip-not-allowed`. The commit that fixed this (8aa81f08) reached only
  the APIs on the shared helper.
- The route test of ADR 17 is copied into 17 test files
  (`TestRoutesDeclareActions`, `TestRouteTable`), for example
  `jobs/routes_test.go:498`, `pulumi/routes_test.go:583`.
- CSRF and origin: `pkg/session/session.go:473` `CheckCSRF` and the origin
  checks of `internal/sessionauth` cover session routes only. The sign-in
  routes are public routes outside that check: #2970 (sign-in accepts
  cross-site `text/plain` requests).

### Bug classes

Existence oracles (#2954, #2923, #1844). Guards that land on one route and
miss its siblings (#2940, #2877, #2870). Refusals with no audit entry
(#2591, #2466). Refusal builders fixed again and again, each with its own
fail-closed and audit rule (#2831, #2949, #2955, #2894, #2976, #623).

### Structural fix

1. Every API declares its routes as `route.Route[T]` and runs through
   `route.Enforce`. Delete the own loops of pulumi, tfe, server, triggers,
   settings and objectcontext, and the three `readRefusal` copies.
2. The Resolve step is the object loader. It answers one not-found for both
   "absent" and "read refused". A handler gets a resolved, authorized object.
   It never sees the raw `Authorizer`.
3. The `Authorizer` port is reachable from `internal/api` only through the
   `Recorder`, so a denial cannot skip its audit entry. A service that must
   decide (list filters, capability probes) takes a narrow `Checker` that
   the boundary hands it, and the `Checker` records.
4. One route test reads the route tables of every API. Delete the 17 copies.
5. The origin check (`Sec-Fetch-Site`, `Origin`, and a content-type
   allowlist for public state-changing routes) runs in `Enforce` for every
   method that can change state, public routes included.

The first PR is the smallest one: delete the three `readRefusal` copies and
call the shared one. That stops the live oracle.

### Cost and blast radius

6 to 10 PRs, about 1,500 to 3,000 lines changed, mostly in pulumi, tfe,
server, triggers, settings and objectcontext. Blast radius: every adapter.
The Pulumi and TFE wire shapes do not change. Check each tool adapter with
`make e2e`.

### ADRs

| ADR | Decision relied on or changed | Owner-tagged |
| --- | --- | --- |
| 17 | "A test lists every route that the server registers and fails when a route has no declared action." (`0017:57`). Relied on; the test becomes one test. | No |
| 60 decision 3 | "The route table of ADR 17 declares the action of each route, so one middleware writes the entry, not each handler. A denied request and a failed request also write an entry." Relied on: the denial entry moves into the boundary. | No (design after the RFC 22 answers) |
| 210 | Route paths and actions. Not changed. Decision 6 is owner-tagged and not touched. | Decision 6 only |
| 222, 230 | The access and gate model. Not changed: the cut moves where the check runs, not what it decides. | Yes (2026-10-18), not touched |
| 295 decision 5 | "One answer for every refusal" (break-glass route). Consistent. | Yes, not touched |
| 297 decision 15 | "The checks of `Origin` and `Sec-Fetch-Site` of `internal/sessionauth` stay, and the session routes use them too." Extended to public routes; no change of meaning. | Owner direction, PM design accepted by the owner |

Label: **Ready.**

---

## 2. One request intake: body decoder and origin check

Found in the survey. **Confirmed.**

### Evidence

- `internal/textbody/decode.go:135` `Decode` refuses invalid UTF-8 and lone
  surrogates before decoding. 8 files use it.
- `internal/api/route/route.go:132` `DecodeBody` is a second shared helper
  with 9 users. It does not call `textbody`, so the server stores U+FFFD in
  place of what the client sent.
- About 15 local decoders: `server/principals.go:695`,
  `runnerapi/errors.go:294` (no check for a trailing second value),
  `triggers/serve.go:364`, `settings/serve.go:562`,
  `registry/handlers.go:172`, `server/gate_config.go:200`,
  `server/localusers.go:325`, `githost_app.go:125`.
- 14 files call `json.NewDecoder(r.Body)` directly: pulumi (5), tfe (4),
  tofuhttp locks, `server/devsignin.go:57`, maintenance, namespaces.

### Bug classes

Different checks for unknown fields, trailing data and text. Error text that
can echo the body. Commits: #2852, #2780, #1916, #482, #428. Login CSRF
through `text/plain` (#2970) is the same intake gap on public routes.

### Structural fix

`route.DecodeBody` calls `textbody.Decode`, with options for an empty body
and unknown fields. Delete the local decoders. The Pulumi and TFE adapters
keep their wire shapes; only the checks are shared. The origin check is
step 5 of cut 1.

### Cost

2 to 3 PRs, about 600 lines. Blast radius: every request body. Run
`make e2e`.

### ADRs

| ADR | Decision | Owner-tagged |
| --- | --- | --- |
| 250 (CLAUDE.md "Security code") | "Check the input at the boundary. Use an allowlist. Reject an input that is not valid. Do not change it to make it valid." Relied on: U+FFFD substitution violates it. | Yes (whole ADR) |
| 276 | Error codes of a refused body. Not changed. | Decisions 1 to 6, 9, 10 |

Label: **Ready.**

---

## 3. Every outbound call through `egresshttp`, one webhook signer

Found in the survey. **Confirmed, with one live defect.**

### Evidence

- **Live:** `cmd/opendustpan/auditsinks.go:333` builds the audit webhook sink
  with `Client: nil`. `pkg/logsink/webhook/webhook.go:116`
  `noRedirectClient` then makes a bare `http.Client`. That client applies no
  egress rules. The other 12 outbound users go through
  `internal/egress/egresshttp`.
- `pkg/blob/s3/store.go:147` falls back to `http.DefaultClient`.
- Three HMAC-SHA256 webhook signers: `pkg/notify/sign.go:155`,
  `pkg/logsink/webhook/webhook.go:140`, `internal/policy/webhook.go:185`.

### Bug classes

A request to an address that the egress rules refuse (SSRF class). Signers
that drift apart. Commits: #2856, #2063, #2006, #16.

### Structural fix

The constructors of the webhook sink and the S3 store take a required
`egresshttp` client. A nil client is an error, not a default. One package
signs webhook bodies; the three senders call it.

### Cost

1 to 2 PRs, about 300 lines.

### ADRs

| ADR | Decision | Owner-tagged |
| --- | --- | --- |
| 321 | "Each outbound connection of a sign-in method uses the egress rules." Extended to every outbound connection. | Owner direction; the design is PM under delegation |
| 60 decisions 12 and 13 | The built-in webhook sink. Not changed. | No |

Label: **Ready.**

---

## 4. One clock

PM candidate 5 and the Foremen evidence (b). **Confirmed, in a different
shape than the count suggests.**

### Evidence

- There is no `Clock` port. About 143 files take a `Now func() time.Time`
  field. 66 `time.Now()` calls remain in production code.
- Packages that have an injected clock and still call `time.Now()`:
  `internal/api/server/restores.go:214,535`;
  `pkg/audit/ship.go:387,499,509,518`, `checkpoint.go:626`;
  `pkg/dustpan/auditchain.go:546,579,1028,1047`, `dustpan.go:3488,3500`.
- Audit `At:` times from the wall clock in handlers that have a clock:
  `pulumi/updates.go:569`, `pulumi/reveal.go:265`,
  `pulumi/secrets.go:417,444`, `tfe/audit.go:59`, `tfe/overrides.go:92`,
  `tfe/reveal.go:220`, `tofuhttp/audit.go:40,63`, `refusal.go:517,562`,
  `audit/export.go:331`.
- Lease and expiry times from the wall clock:
  `pkg/durable/runner.go:364` (`claimedAt`), `pkg/backup/service.go:387`,
  `internal/api/jobs/logs.go:194,256`, `gatestore/objects.go:327`,
  `store/{sqlite,postgres}/self_service_blueprints.go:78-79`.
- A third clock is the database: 21 lines of the PostgreSQL `queries.sql`
  read `now()` or a related function, 53 more in PostgreSQL Go files; 11
  lines of the SQLite `queries.sql` read the SQLite clock.

### Bug classes

Three clocks (database, application, injected) that disagree. Lease and
expiry skew between instances, audit times that differ from the time of the
change, and tests that depend on the wall clock. PRs: #2829, #2795, #2965,
#2823, #2866, #2844, #2552, #2882, #2871.

### Structural fix

1. One clock port in a neutral package. `dustpan.NewServer` makes it and
   gives it to each service. Delete the `time.Now()` fallbacks.
2. A store never reads the database clock for a domain time. Each store call
   that needs a time takes it as a parameter (Unix milliseconds, as the
   baselines already store). Each `now()` in the queries becomes a bound
   parameter. This also removes a difference between the dialects before
   MySQL.
3. A forbidigo rule bans `time.Now` outside `cmd/` and the clock package.
   It holds the change in place; it is not the fix.

### Cost

4 to 6 PRs. About 200 call sites and about 85 SQL lines. Blast radius:
every lease, expiry and audit time. No wire change: the times on the wire
keep their format.

### ADRs

| ADR | Decision | Owner-tagged |
| --- | --- | --- |
| 297 decision about deadlines | "computes each deadline and each expiry from its own clock at the issue" (`0297:102`). Relied on. | PM design accepted by the owner |
| 210 decision 10 | Times on the wire. Not changed. | No |
| 274 decision 8 | `sqlstore.Write` checks the state and the fence in the transaction. A time parameter fits it. | Yes, not changed |

No ADR decides the clock. Write an ADR after the owner or PM accepts the
rule "the application clock is the only clock".

Label: **Ready.**

---

## 5. Stored group references and one group-change guard

Swarm Lead and Foremen candidate 12. **Confirmed.**

### Evidence

- The group-change guard exists only for access limits:
  `pkg/gate/limitgroups/limitgroups.go:47` `LimitLiftedBy`, used by
  `pkg/dustpan/stateguard.go:80`.
- Each other kind of object that names a group gets its own guard, one pull
  request at a time: #2870 (limits, merged), #2978 (gates and approval
  rules, open), #2976 (the author of a limit, open), #2979 (approval
  defaults, open issue: not guarded).
- Groups are named by name in Cedar policy text (ADR 223 example:
  `principal in OpenDustpan::Group::"platform"`) and by ID in rules
  (`pkg/principal/principal.go:665` `GroupRef`). A rename or a delete of a
  group must find both forms.
- #1844 (closed): the approver check told a namespace role whether an
  installation group exists.

### Bug classes

A group change that lifts a limit or opens an approval because one kind of
referrer was not walked. A new referring kind that ships with no guard.

### Structural fix

1. One table of group references: (referrer kind, referrer ID, group ID,
   namespace). Each store that writes a referring object (access limit,
   gate, approval rule, approval default) writes its references in the same
   transaction. For Cedar text, the store parses the policy at write and
   stores the group IDs that it names.
2. One guard reads that table for each group change, rename and delete. It
   refuses through the route boundary of cut 1.
3. A conformance case: for each referring kind, a group change that would
   widen access is refused. A new kind fails the case until it writes its
   references.

The index needs no ADR change. Writing group IDs in place of names in the
Cedar text would change the form of a stored policy. That is a separate
decision.

### Cost

3 to 4 PRs, about 1,000 lines. Depends on cut 1 for the refusal path. It
can replace #2978 and #2976 before they merge.

### ADRs

| ADR | Decision | Owner-tagged |
| --- | --- | --- |
| 222 | "One model for access, gates, approvals and facts." Relied on. | Yes (2026-10-18), not changed |
| 223 decision 1 | "An access limit is one Cedar `forbid` policy." Not changed by the index. Group IDs in policy text would change the stored form. | Yes |
| 313 | Declared edges. Check its edge model before the first PR; the index can be a declared edge. | Yes (owner decisions of 18 and 19 October) |
| 52 | Groups, roles, grants. Not changed. | No owner tag in the status line; read its "Changed by" chain (67, 70, 203, 212, 215, 216, 222, 223, 354) in the PR |

Label: **Ready** for the index. **Needs RFC + owner** for IDs in Cedar text.
**One-way door** at the tag: the stored policy form ships with it.

---

## 6. The central table list, and costs derived from it

PM candidate 0, Swarm Lead candidate 10, Foremen evidence (d).
**Confirmed. The owner already decided the fix (ADR 274 decision 5). No
step of it has landed.**

### Evidence

- Each table is declared in four Go lists and two baselines:
  - `pkg/backup/backup.go:111` `var Tables` (foreign-key order),
    `ControlTables` (`coverage.go:36`), `EmptiedTables` (`coverage.go:74`),
    `AccessTables` (`access_tables.go:87`).
  - `portableSpec()` in `pkg/backup/portable_tables.go:73-804`, which joins
    25 `portable<Area>Tables()` functions. 30 `portable*.go` files, 3,942
    lines, about 881 column entries.
  - SQLite: `sqliteBackupTables` in `pkg/store/sqlite/backup_columns.go`
    (881 lines) and a `backup_<area>.go` for each area.
  - PostgreSQL: `pgCopyTables` in `pkg/store/postgres/backup.go:111` and a
    `backup_<area>.go` for each area.
  - Dev strip: `pkg/store/internal/devstrip` (921 lines).
  - RFC 104 counts about 1,900 column names written three times.
- `CheckCoverage` (`sqlite/backup.go:349-384`, and the PostgreSQL copy)
  compares names with the live schema. It does not check the kind of a
  column, the foreign-key order, the class of a table (data, control,
  emptied), membership of `AccessTables`, sealed columns, `OrderBy`, or a
  new principal column in a JSON body.
- 34 backup, restore and dev-strip files for each dialect: SQLite 8,147
  lines, PostgreSQL 7,595 lines, plus tests.
- 169 commits touch `pkg/backup`; 129 of them also touch a store.
  Drift-class PRs: #314, #341, #451, #652, #695, #820, #1939, #2018.
- `internal/servertest/roundtrips_test.go:1350` `restoreModel()` is one
  hand-summed line: `fixed: 342 + 1 + forgeStripClassTrips -
  breakGlassReviewTrips + stagedJobCheckTrips + removalAlertTrips`, with a
  paragraph of comment for each table that is a special case. Each new table
  edits that line. Conflicts and breaks: #2869, #2908, #2862, #2887, #2859.
- `internal/store/tables` does not exist. `internal/store/` holds only
  `storefault/`. ADR 274 decision 2 (owner) makes all its steps wait for
  RFC 103 step 8 (move `pkg/store` to `internal/store`). The pull requests
  that step 8 waits for (#1450, #1428, #1431, #1437, #1478) are all closed,
  but step 8 has not started.
- 114 issues have "backup" or "restore" in the title. Most open ones are
  about the restore protocol (#2092, #2199, #2342, #2306, #2127, #1944),
  not schema drift. The table list removes the drift class only.

### Bug classes

A table or column that one list misses (caught by name only). A wrong class
for a security-relevant table (claim tokens, reset codes). A wrong
foreign-key order. A new principal-linked column that the dev strip misses.
Merge conflicts and breaks in the hand-summed cost model.

### Structural fix

ADR 274 step 7, with the checks that RFC 104 already defines
(`docs/rfc/0104*.md`, "The table list"):

1. `internal/store/tables` declares each table once: name, class, access,
   columns with their kind, `OrderBy`, `After` (foreign keys), `Owner`
   (principal link), and a sealed flag on secret columns.
2. Both backup engines, the restore, the dev strip and the portable format
   read it. `sqliteBackupTables`, `pgCopyTables` and the column halves of
   each `backup_<area>.go` go away. The format types stay in `pkg/backup`.
3. Checks against the information schema: the kind against the PostgreSQL
   `data_type`, `After` against the foreign keys, `Owner` against columns
   that refer to a principal. Drift then fails the build, not a review.
4. The restore cost model computes its count from `tables.All()` (class,
   special flags). `restoreModel()` loses its hand-summed line.

Option (b), backup through the domain ports, is rejected: it breaks the
one-file-per-table format and byte-identical exports of ADR 42, needs a
port for each aggregate, and costs more than 10,000 lines.

### Cost

4 to 6 PRs. Deletes about 2,500 to 3,500 lines, moves about 3,900, adds
about 800 to 1,200. The portable format does not change, so a backup stays
restorable across drivers (ADR 42).

### ADRs

| ADR | Decision | Owner-tagged |
| --- | --- | --- |
| 274 decision 5 | "The table list is central. It lives in `internal/store/tables` ... The format types (`Column`, `Kind`, `PortableTable`) stay in `pkg/backup` ... Backup, restore, portable backup and dev data read the list." Relied on. | Yes |
| 274 decision 2 | "The work starts after step 8 of RFC 103." Blocks the cut. | Yes |
| 42 | "The project owner decided on 2026-10-12 that the portable format is the only format" (`0042:83`). Relied on; the format does not change. Changed by 248 and 253. | Yes |
| 38 | Restore through the server, in maintenance mode. Not changed. | Yes |
| 253 | Dev-strip layers. Relied on. | Yes (whole ADR) |
| 322 decisions 1, 2 | One baseline, no compatibility code. The cut needs none. | Yes |
| 30 | Backups and restore. Not changed. | Delegated by the owner, accepted |

Label: **Needs owner.** The owner must choose one: (a) do RFC 103 step 8
now, then step 7; or (b) let step 7 land in `pkg/store/internal/tables` and
move with step 8. Option (b) changes the order of ADR 274 decision 2, an
owner decision, but not its content.

---

## 7. One error registry and a committed-write answer

PM candidate 3. **Confirmed. The pieces are shared; the mapping is not.**

### Evidence

- `internal/api/problem/problem.go:306` `Writer.Problem` runs one order:
  credential, `ErrDenied`, `ErrUnavailable`, the adapter's `Classify`,
  `Shared`, then 500.
- The catalog `internal/errcode/catalog_*.go` (about 45 files) maps codes
  to specs. Nothing maps a sentinel to a code in one place.
- About 20 `writeError` functions and about 100 `classify*` functions: tfe
  10 (`tfe/errors.go:244-598`), pulumi 13 (`pulumi.go:1055-1356`), server
  about 30 in 12 files, `runnerapi/errors.go:130`, and own maps in
  inventory, jobs, triggers, settings, drift, registry, trustrules,
  selfservice, sourcefilter. `tofuhttp.go:358` returns `(int, string)` and
  does not use `problem`.
- 500 after a commit, fixed case by case: `server/principals.go:325-337`
  (#2960), `jobs/serve.go:192-205`, #2961 (open).

### Bug classes

A 500 after a committed write (#2960, #2961, #2238, #689). An unclassified
error that answers 500 (#2402, #2458, #2408). Refusal text that differs for
one sentinel between adapters.

### Structural fix

1. One registry: sentinel to (code for each protocol word, status). ADR 276
   decision 2 gives different object words for each protocol
   (`stack.not-found`, `workspace.not-found`, `unit.not-found`), so the
   registry keeps a column for each protocol.
2. One renderer for each protocol (problem JSON, TFE JSON:API, Pulumi,
   OpenTofu HTTP backend). The adapters delete their `classify*` functions.
3. A committed-write answer: after a write commits, the handler answers
   from what it wrote. A failed read after the commit gives the success
   answer with the fields it has, and a log line. It never answers an
   error.

### Cost

5 to 8 PRs, about 3,000 to 4,000 lines. Blast radius: every error answer.
The codes are a contract (ADR 276 decision 10), so a golden test must show
each answer byte for byte the same before and after.

### ADRs

| ADR | Decision | Owner-tagged |
| --- | --- | --- |
| 276 decision 7 | "One catalog in `internal/errcode`. ... an adapter does not declare its subset of codes next to its classify functions." Extended to sentinels. | No (the author's design) |
| 276 decisions 1, 2, 10 | "Every error answer has a code", "The names of the codes", "A code is a contract". Relied on; not changed. | Yes |
| 182 decision 7 | Classify functions for each adapter; superseded in part by 276. The rest changes with this cut. | No (decisions 1, 2 are owner) |
| 234 | Where the server localizes text. Not changed. | No owner tag in the status line |

Label: **Ready.** **One-way door** at the tag: the codes ship then.

---

## 8. One cursor codec and one page-size rule

PM candidate 4. **Confirmed.**

### Evidence

- `internal/pagetoken/pagetoken.go`: JSON, a query fingerprint compared in
  constant time, 4,096 bytes at most, unknown fields refused. 8 users.
- Hand-written base64 cursors with no fingerprint:
  `internal/api/server/namespaces.go:279,312` (the last name only),
  `pkg/principal/page.go:120,128`, `pkg/accesstoken/page.go:103,108`,
  `pkg/access/service.go:485,540` (checks `err` after `strings.Cut`),
  `pkg/access/caller_access.go:149`, `pkg/access/review_service.go:865,876`
  (has a binding hash), `pkg/store/internal/codec/objectcontext.go:142,155`.
- Four copies borrow `principal.MaxCursorLength` (512). `pagetoken` uses
  4,096.
- More than 15 default and maximum pairs for page size, for example
  `namespace.go:72` `maxPageLimit = 1001`.
- No cursor crosses a namespace; visibility is checked again on the server.

### Bug classes

A cursor not bound to its filter: a client changes the filter in the middle
of a scan and the server accepts it (live in `namespaces.go`). Limits that
differ for the same kind of list. Commits: #2838, #2780, #549.

### Structural fix

`pagetoken.Keyset{after, fingerprint}` and `pagetoken.Clamp(raw, default,
max)`. Move the hand-written cursors onto them. A route declares its default
and largest page size in the route table, as ADR 210 says.

### Cost

2 to 3 PRs, about 600 lines.

### ADRs

| ADR | Decision | Owner-tagged |
| --- | --- | --- |
| 210 | "Lists page with an opaque cursor ... Each list route declares its default and its largest page size." Relied on. | No (decision 6 only is owner) |

Label: **Ready.** **One-way door** at the tag only for the tool-compatible
APIs; the cursors are opaque, so their content can change before then.

---

## 9. One secret reader and one prefixed-token type

Swarm Lead candidate 11 (secret readers). **Confirmed.**

### Evidence

- Secret input from the environment or a file in `cmd/opendustpan`:
  `auditsinks.go:426` `webhookSecretFrom`, `auditsinks.go:433` `secretFrom`,
  `main.go:2033` `readKeyFile`, `auditcheckpoints.go:105`
  `readSigningKeyFile`, and more `os.ReadFile` calls in `devkey.go`,
  `devtls.go`. Issues: #2971 (`secretFrom` trims spaces that may be part of
  a password), #2959 (unset each sensitive variable after the read), #2956
  (the OIDC client secret may reach the client as a fingerprint token).
- Token types: `pkg/jobtoken/jobtoken.go:80-86,181` and
  `pkg/accesstoken/accesstoken.go:241-248,313` are near copies. SHA-256
  digest paths for secrets exist in `internal/token`,
  `pkg/runner/token.go:316,335`, `pkg/forgelink/forgelink.go:239`,
  `internal/chatops/link.go:44`, `pkg/localuser/localuser.go:606`,
  `pkg/identity/identity.go:1214,1262`, `internal/authgate/authgate.go:409`.
  20 files compare in constant time on their own.

### Structural fix

One `secretinput` reader in `cmd/opendustpan`: a value or a file, never
both, no trimming except one final line feed of a file, the variable unset
after the read, and a result type that has no `String` or `MarshalJSON`. One
generic `token.Prefixed` type with `Parse`, `Digest` and `Equal`.

### Cost

2 to 3 PRs, about 800 lines.

### ADRs

| ADR | Decision | Owner-tagged |
| --- | --- | --- |
| 238 | `--key-encryption-key-file` or `DUSTPAN_KEY_ENCRYPTION_KEY`. Relied on; names not changed. | No owner tag in the status line |
| 63 | The installation settings file. Not changed. | No owner tag in the status line |

Label: **Ready.**

---

## 10. One backoff and one limiter with a stored variant

PM candidate 8. **Confirmed.**

### Evidence

- `pkg/durable/backoff.go` (exponential with a cap and jitter) has 6 users.
  6 other loops do not use it: `pkg/audit/ship.go:476,508,551`,
  `pkg/notify/route.go:954` (no jitter), `pkg/blob/s3/store.go:869`,
  `pkg/blob/retry.go:12`,
  `pkg/store/postgres/maintenance_revocations.go:205-217`,
  `pkg/launcher/run/run.go:338`.
- About 8 limiters, none shared: `internal/authgate/authgate.go:211`,
  `internal/api/server/passkeys.go:547`, `internal/denylimit/denylimit.go:105`,
  `pkg/forge/checklimit.go:92`, `pkg/notify/sinks/limit.go`,
  `internal/api/jobs/logstreams.go:74`, `pkg/dustpan/warn_limiter.go:23`,
  `pkg/store/internal/gatestore/tx.go:95` (stored). Each has its own key cap
  and eviction rule.
- #2591 (runner exchange has no denial limiter), #1273 (rate-limit refusal
  audit entries), #2761, #533.

### Structural fix

Move the backoff to a neutral package and use it in all 12 places. One
limiter type `Window{limit, period, maxKeys, clock}` with two backends: in
memory, and stored (for the cases that must be shared across instances).

### Cost

3 to 4 PRs, about 1,000 lines.

### ADRs

| ADR | Decision | Owner-tagged |
| --- | --- | --- |
| 295 decision 4 | "A shared rate limit. The database holds the counter of the rate limit, so all instances share it. An IPv6 address counts as its /64 group." Kept: the stored backend serves it. | Yes |
| 298 | "Decisions 4 and 7 share one limiter." Kept. | PM design under owner delegation |
| 285 | Sign-in rate limits and lockout. Kept. | Yes, with PM decisions marked |

Label: **Ready**, if the stored backend keeps ADR 295 decision 4.

---

## 11. Constructors for wide domain structs

Swarm Lead candidate 9 and Foremen evidence (e). **Confirmed.**

### Evidence

- `principal.Principal` has 22 fields (`pkg/principal/principal.go:158`).
  Struct literals: `principal.Principal{` 74 in production, 73 in tests, 83
  files. `principal.Group{` 37 in 25 files. `ServiceConfig{` 34 in 26 files.
  `NamespaceID:` in 105 files, `LimitGroups` in 20 files.
- `exhaustruct` is on (`.golangci.yml:487`), so each new field must be named
  at each literal. #2918 (open) adds `Principal.JobUnit` to each literal and
  touched 103 files. Reviewers counted about 17 `exhaustruct` breaks after
  clean merges.

### Structural fix

Wide domain structs that other packages build get a constructor or a small
option struct in their own package (`principal.NewPerson`,
`principal.NewService`, `principal.NewJob`). Fields that only the domain
sets stay unexported, with accessors. Tests build them through the same
constructors or a builder in the `*test` package. `exhaustruct` stays on; it
then checks one literal for each type in place of 80.

### Cost

3 to 5 PRs, mostly mechanical. Do it before #2918 merges, or #2918 becomes
the first user.

### ADRs

| ADR | Decision | Owner-tagged |
| --- | --- | --- |
| 7 | "`exhaustruct` is on from day one." Kept. | No owner tag in the ADR |

Label: **Ready.**

---

## 12. Fakes replaced by the real SQLite store

PM candidate 7. **Partly confirmed.**

### Evidence

- 54 files define a fake, memory or stub store. About 18 run the
  conformance suite of their port, for example
  `inventorytest/mem_test.go:167`, `pkg/setting/memstore_test.go:211`,
  `durabletest/mem.go`.
- About 36 do not, though a suite exists for several:
  `pkg/savedview/service_test.go:28`, `pkg/forge/service_fake_test.go:23`,
  `pkg/tempadmin/tempadmin_test.go:32`, `pkg/trigger/fakes_test.go:165`,
  `pkg/jobtoken/service_test.go:28`, `pkg/backup/service_test.go:37`,
  `pkg/maintenance/maintenance_test.go:76`, and the fakes in
  `internal/api/*`.
- The real store already serves tests: 108 calls open
  `sqlite.Open(ctx, filepath.Join(t.TempDir(), "dustpan.db"))` outside
  `pkg/store`, with no shared helper.
- Drift: a16fb67d (#2782) changed `durabletest/mem.go` with the PostgreSQL
  query; b13e8b20 rewrote `jobtest/mem_test.go`.

### Structural fix

One `storetest.OpenSQLite(t)` helper. Fakes that only stand in for
persistence go away. Fakes that inject faults or record calls stay and must
run their suite.

### Cost

1 PR for the helper, about 8 PRs to move tests. Test run time not measured:
each test opens a store on a 1,729-line baseline.

### ADRs

CLAUDE.md: "Every port has a conformance suite ... A new adapter must run
it." A fake is an adapter. No ADR changes.

Label: **Ready.**

---

## 13. ADR 274 areas: store logic once, SQL per dialect

PM candidate 6. **Confirmed. The owner already decided the fix (ADR 274).
No step of it has landed.**

### Evidence

| | PostgreSQL | SQLite |
| --- | --- | --- |
| Go, non-test | 40,439 | 41,201 |
| Go, tests | 22,973 | 20,308 |
| Generated `internal/db` | 25,249 | 24,686 |
| `queries.sql` | 5,889 | 5,447 |
| Baseline | 2,438 | 1,729 |

- Over the 117 Go file names in both adapters, 78% of the lines are the same
  after the placeholders and driver names are normalized. `queries.sql`:
  about 83%. Examples: `access.go` 94%, `principals.go` 92%, `runner.go`
  93%, `workspaces.go` 89%, `audit.go` 84%. `backup.go` is 20%; the backup
  engine is really different for each dialect.
- RFC 104 measured 78% when each dialect had about 28,000 lines. Each now
  has about 41,000.
- 302 of the 375 commits that touch a store touch both dialects.
- `pkg/store/internal/*` (25 packages, 15,989 lines: `jobstore`,
  `gatestore`, `driftstore`, `codec`, `storeerr`, `migrate`) is the
  forerunner of the ADR 274 area packages, not a competing design. RFC 104
  moves them into the area packages.
- 18 fix commits touch one dialect only. Nearly all are specific to that
  dialect. No missed fix was found: the 47 shared suites catch drift. The
  cost is authoring and review, not correctness.

### Structural fix

ADR 274 as decided: a generated shared `Querier`, one area package for each
area, one SQLite writer behind a semaphore, the maintenance gate in the
`Runner`, one `Classifier` for each dialect. SQL, schema and migrations
stay for each dialect. MySQL then supplies its queries, migrations,
`Runner`, `Classifier` and backup engine.

### Cost

26 PRs. Deletes about 30,000 to 35,000 lines. Steps 3, 5 and 6 (fence, gate,
SQLite writer) carry the highest risk; each lands after its conformance case.

### Weighing the Swarm Lead view

The Swarm Lead says that this cut needs an RFC before MySQL and is not a
cut that stops the bleeding. The survey agrees on the second point: the
copies do not cause correctness bugs today. On the first point: the cut
itself needs no RFC, because ADR 274 decided it. A new RFC is needed only
to (a) start MySQL before ADR 274 finishes, or (b) share one query set
across dialects, which ADR 274 does not decide.

### ADRs

| ADR | Decision | Owner-tagged |
| --- | --- | --- |
| 274 decisions 1 to 7 | The design and the steps. Relied on. | Yes |
| 274 decision 2 | "The work starts after step 8 of RFC 103." | Yes |
| 274 decision 6 | "A third dialect lives in this repository only, for now." | Yes |
| 3 | "Hand-written per dialect"; superseded in part by 274. | No owner tag |
| 322, 324 | One baseline, a mismatch refuses. A schema change is cheap now. | Yes (322 decisions 1, 2, 4, 5; 324) |

Label: **Needs owner** (when to start step 8; whether MySQL waits for ADR
274). **Needs RFC + owner** only for a shared query set or an early MySQL.
**One-way door**: none before the tag; the schema is one baseline.

---

## 14. Hand-kept aggregates: baseline hash files

Swarm Lead candidate 10. **Confirmed for the hash files and the cost model
(the cost model is in cut 6).**

### Evidence

- `pkg/store/{sqlite,postgres}/migrations/0168_baseline.sql.sha256` hold one
  hash line each. Two pull requests that edit a baseline always conflict on
  that line. 11 commits changed the SQLite hash file.
- The only reader is `TestFrozenMigrations`
  (`pkg/store/sqlite/frozen_test.go:28`, and the PostgreSQL copy). That test
  is off until the release record lists a shipped migration. The baseline
  generator (`pkg/store/internal/baseline/generate_test.go:316`) writes the
  file. So before the first tag, no check reads the file.
- The runner records the SHA-256 of each migration in `schema_migrations`
  from the embedded SQL (ADR 322 decision 2). It does not need the file.
- The TypeScript clients are generated (`make clients-check`). CLAUDE.md
  already says to resolve a conflict by running the generator again.

### Structural fix

Until the first tag, do not commit the `.sha256` files. The tag script
(`scripts/copy-tag-migrations.sh`) writes them for the shipped migrations,
and the frozen check turns on from the release record as ADR 325 says.

### Cost

1 PR.

### ADRs

| ADR | Decision | Owner-tagged |
| --- | --- | --- |
| 322 direction 4 | "The owner wants the capability after the release (direction 3) and the checks disabled, not removed (direction 4)." The check stays; only the file before the tag goes. The owner must confirm this reading. | Yes |
| 325 | The release record turns the checks on. | No (PM decision) |

Label: **Needs owner** (reading of ADR 322 direction 4).

---

## 15. Audit: record the rule that the code follows

PM candidate 2. **The fan-out is rejected as a root cause. One gap and one
ADR mismatch are confirmed.**

### Evidence

- Mutations write their entry in the store transaction: about 103
  PostgreSQL and 104 SQLite store functions take an `audit.Entry`, and about
  253 port methods carry one. 85 of the 111 direct `Append` calls are in the
  two dialects, 9 more in `pkg/store/internal`. The entries are built in
  services and handlers (about 117 `audit.Entry{` literals) and committed
  by the store. This is the right shape: the entry and the change commit or
  fail together.
- About 50 appends outside a transaction record reads, reveals, refusals
  and failures. Reveal and export write fail closed before they answer.
- Change candidates outside the transaction:
  `pkg/backup/restore_steps.go:375,383`, `restore_service.go:767` (the
  restore replaces the store, then appends), `pkg/tempadmin/authenticator.go:355`.
- `internal/api/auditrequired/auditrequired_test.go` checks only that each
  adapter refuses a nil audit store. Nothing checks that each mutating route
  writes an entry.
- ADR 60 decision 3 says "one middleware writes the entry, not each
  handler". The code does not do this, and should not: a middleware entry
  cannot be in the transaction of the change.
- Open: #2590 (an audit rule with no writer), #1017 (no entry for a stack
  rename).

### Structural fix

1. Write an ADR that records the rule of the code: a change writes its entry
   in the transaction of the change; refusals, reads of secrets and failures
   write from the route boundary (cut 1).
2. A `Mutates` flag in the route table, and one test that each such route
   writes exactly one entry through a recording store.
3. Move the restore entries into the restore transaction, or record why
   they cannot be.

### Cost

2 PRs, about 800 lines, mostly tests.

### ADRs

| ADR | Decision | Owner-tagged |
| --- | --- | --- |
| 60 decision 3 | "one middleware writes the entry, not each handler". Changed by the new ADR. Also changed by ADR 126 for agent tool calls. | No (the owner answered RFC 22 on the port, the switch and the sinks, not on decision 3) |
| 60 decisions 1, 2 | The port, fully on or fully off. Not changed. | Yes (RFC 22 answers) |

Label: **Ready** (the ADR change is a PM decision under the owner's rule,
because decision 3 is not owner-tagged).

---

## Rejected

| Candidate | Why rejected |
| --- | --- |
| 2. Audit from 164 sites | The sites build entries; the stores commit them in the transaction of the change. That is one rule, applied. See cut 15 for the real gap. |
| 8b. Service layering half applied | 12 `internal/*svc` packages and 23 `pkg/` packages with a `*Service` is a split convention, but no handler bypasses a service for a domain write. It causes no bug found. The only bypass is audit entries built in handlers, which cut 1 and cut 15 cover. Record the convention when RFC 103 splits the packages. |
| 6c. Fixes missed in one dialect | No missed fix found. The shared conformance suites catch drift. The cost of two dialects is authoring time (cut 13). |

## Items that need an owner decision

1. **Cut 6 order (ADR 274 decision 2, owner).** Start RFC 103 step 8 now,
   or let ADR 274 step 7 (the table list) land first in `pkg/store/internal`
   and move with step 8.
2. **Cut 13 timing (ADR 274 decisions 2 and 6, owner).** Whether MySQL
   waits for ADR 274 to finish. An early MySQL, or one shared query set,
   needs an RFC.
3. **Cut 14 (ADR 322 direction 4, owner).** Whether "the checks disabled,
   not removed" allows the `.sha256` files to be absent until the first tag.
4. **Cut 5, only if group IDs replace group names in Cedar policy text (ADR
   223, owner).** The reference index needs no decision.
5. **Cut 10 (ADR 295 decision 4, owner).** No change asked; the shared
   limiter keeps the stored counter. Listed so that the owner sees it.

## One-way doors

All of these become one-way at the first tag, when the shapes ship:

- Cut 7: the error codes and their protocol words (ADR 276 decision 10).
- Cut 5: the stored form of a Cedar policy that names a group.
- Cut 8: page-size limits on the tool-compatible APIs.
- Cut 6: the portable backup manifest (ADR 42, ADR 253). The cut does not
  change it.

No cut in this survey is one-way today. Each is cheaper before the tag.

## Method

- Read `CLAUDE.md`, `docs/adr/INDEX.md`, and ADRs 3, 7, 17, 30, 38, 42, 60,
  182, 210, 222, 223, 253, 274, 276, 295, 297, 322, 324, 325, and RFCs 103
  and 104.
- Measured with `rg`, `wc` and `git log` on `dd563bf`. Spot-checked each
  live defect in the source: the three `readRefusal` copies, the nil webhook
  client, `route.DecodeBody` without `textbody`, the base64 namespace
  cursor, and `restoreModel()`.
- Issue numbers from the GitHub search API, read only.
