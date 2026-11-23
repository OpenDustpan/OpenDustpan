---
version: 1
---

Issue: #3685. This change supersedes the read of "every rule of the namespace" in tasks 2.3 and 2.9 of the change `mtls-mtls-exchange-token`, for the recheck inside the fenced transaction only.

## Why

**Persona: runner operator, and the site administrator who sizes the server.** A runner operator runs CI jobs that exchange a client certificate for a trust token. Each exchange takes the fence lock of the namespace for the length of its transaction. A site administrator of a large installation may have a namespace with many trust rules, because no cap on the rules of a namespace exists.

The path today (the mTLS exchange of change `mtls-mtls-exchange-token`):

1. **API.** The job calls `POST /api/dustpan/v1/namespaces/{namespace}/trust-rules/exchange` with its client certificate.
2. The service verifies the chain and the SPIFFE ID, and calls `RecordExchange` on the store.
3. The store opens a fenced transaction. In it, `recheckExchange` reads every rule of the namespace with all columns (`static_keys` and `conditions_json` too), sorted by `name COLLATE "C", id COLLATE "C"`. It then loops in Go to find the rules that name the SPIFFE ID. The sort cannot use the unique index `trust_rules_name (namespace, name)`, so each exchange sorts the whole set while it holds the fence lock.

The path after this change is the same for the caller. Only the read in step 3 changes: the transaction reads only the rules that name the same SPIFFE ID in the same bundle (at most 2 rows, by an index), and it reads the bundle row as before. The cost of the exchange no longer grows with the number of rules of the namespace.

Errors the person may hit, and the one command that helps. This change adds none and changes none. Each refusal of main stays, with the same error sentinel and the same HTTP answer:

| Error | Answer (unchanged) | The command that helps |
| --- | --- | --- |
| Two rules of one bundle name the SPIFFE ID | 401 mtls.no-matching-rule, an error code of #3658 (store sentinel `trustrule.ErrNotFound`) | `curl https://server/api/dustpan/v1/namespaces/ns-xxx/trust-rules` to list the rules, then `curl -X DELETE https://server/api/dustpan/v1/namespaces/ns-xxx/trust-rules/{rule_id}` for the rule that must go |
| The rule has no bundle, or the bundle is not the active bundle version | 401 mtls.no-matching-rule (the store refuses with `trustrule.ErrNotFound`, and the service answers every such refusal of the record with this code) | `curl https://server/api/dustpan/v1/namespaces/ns-xxx/trust-bundles` to read the bundle and its active version |

What the person gains: an exchange in a namespace with 100,000 rules costs about the same as in a namespace with 100 rules, and the fence lock is held for a shorter time. What the person loses: nothing that they can see. The text of one refusal changes (see "Usability").

## Evidence

- The issue measures the cost on PostgreSQL 18 with 200,000 rows in `trust_rules`: 172.7 ms for a namespace of 100,000 rules (500 bytes of `conditions_json` each; a parallel scan, then an external merge sort of 27.8 MB on disk, over 40 MB moved for each exchange), and 0.56 ms for a namespace of 100 rules. The issue found no cap on the rules of a namespace.
- The issue proposes the fix: a read of the rule by ID, then `SELECT id FROM trust_rules WHERE namespace=$1 AND bundle_id=$2 AND spiffe_id_pattern=$3 LIMIT 2`, with an index on `(namespace, bundle_id, spiffe_id_pattern)`. This change builds that fix, and the code shows that the first read is not needed (see "What main already does": `RecordExchange` reads the rule row before the recheck).
- The match is an equality test in Go today: the compare of the bundle ID and of the pattern with the SPIFFE ID of the exchange (`internal/store/trustrulestore/trustrulestore.go:345`, func CheckExchangeMatch, loop at line 364). An equality in SQL on the same two columns gives the same set of rows. The check also never compares a wildcard: `CheckExchange` refuses a SPIFFE ID with a `*` before the transaction (`internal/store/trustrulestore/trustrulestore.go:234`, func CheckExchange). The index lookup therefore changes no outcome, with or without a stored wildcard pattern. The dependency on the wildcard refusal (see "Dependencies") is a PM direction for one reason: the service and the store must read one rule set.
- The message of the refusal is not asserted on. Run on main (bebc30fa5, 19 Nov 2026): `rg -n "rules name the SPIFFE|want only its rule" --glob '*.go' .` prints only `internal/store/trustrulestore/trustrulestore.go:371`. The tests check the sentinel (`internal/store/trustrulestore/exchange_check_test.go:129`).
- A benchmark task (task 4.2) runs the benchmark of the issue before and after, so the pull request shows the number and not a claim.

## What main already does

Each fact names its file, line and function on main (bebc30fa5). The change specifies only the difference.

- The recheck of an mTLS exchange returns nil for a `jwt-exchange`, fails closed for another kind, and for `mtls-exchange` calls the query ListTrustRulesOfNamespace, then `CheckExchangeMatch(list, x)`, then the query GetTrustBundleOfNamespace, then `CheckExchangeBundle(row, list, x)` in Postgres (`internal/store/postgres/trustrules.go:346 (func recheckExchange)`)
- The SQLite twin of the recheck does the same four steps (`internal/store/sqlite/trustrules.go:363 (func recheckExchange)`)
- `RecordExchange` reads the rule row of the rule of the exchange with the existing query `GetTrustRule` (all columns, key namespace and ID), checks it with `CheckExchangeRule`, and only then calls the recheck, inside `inFencedAuditedTx`, in Postgres (`internal/store/postgres/trustrules.go:299 (func RecordExchange)`)
- The SQLite `RecordExchange` does the same, and calls the recheck at line 337 (`internal/store/sqlite/trustrules.go:313 (func RecordExchange)`)
- That row carries the bundle ID and the SPIFFE ID pattern (`internal/store/postgres/trustrules.go:277 (func trustRuleRow)`)
- `ListTrustRulesOfNamespace` selects 14 columns of every rule of the namespace and sorts by name and ID with the "C" collation in Postgres (`internal/store/postgres/queries.sql:6096`)
- The SQLite copy sorts by name and ID (`internal/store/sqlite/queries.sql:5563`)
- `ListTrustRulesOfNamespace` has another caller, which this change leaves as it is: the list of the rules of a namespace for the service and the list route (`internal/store/postgres/trustrules.go:101 (func List)`, call at line 106)
- The SQLite list of the rules is the same (`internal/store/sqlite/trustrules.go:106 (func List)`, call at line 114)
- `CheckExchangeMatch(rows []Row, x)` returns nil for an exchange with no SPIFFE ID, finds the bundle of the row of the rule of the exchange in `rows`, refuses with `ErrNotFound` when that bundle is empty, collects the IDs of the rows with that bundle and the pattern of the SPIFFE ID, and refuses with `ErrNotFound` unless there is exactly one and it is the rule of the exchange (`internal/store/trustrulestore/trustrulestore.go:345 (func CheckExchangeMatch)`)
- `CheckExchangeBundle(row *BundleRow, rows []Row, x)` uses `rows` for one value: the bundle ID of the row of the rule of the exchange (`internal/store/trustrulestore/trustrulestore.go:293 (func CheckExchangeBundle)`)
- The bundle check of the kind `mtls-exchange` finds that value in a loop (`internal/store/trustrulestore/trustrulestore.go:315 (func checkMtlsExchangeBundle)`)
- `Row` has the fields `ID` and `BundleID`, and the pattern field, among 14 (`internal/store/trustrulestore/trustrulestore.go:74 (type Row)`)
- `CheckExchange` refuses a `*` in the SPIFFE ID of the exchange before the transaction (`internal/store/trustrulestore/trustrulestore.go:234 (func CheckExchange)`)
- The table `trust_rules` has the columns `namespace`, `bundle_id` (nullable) and `spiffe_id_pattern` in Postgres. A JWT rule has `bundle_id` NULL and an empty `spiffe_id_pattern` (`internal/store/postgres/migrations/0168_baseline.sql:1459`)
- The SQLite table has the same three columns (`internal/store/sqlite/migrations/0168_baseline.sql:1197`)
- The indexes of `trust_rules` are the primary key on `id`, the unique `trust_rules_name (namespace, name)` and `trust_rules_principal (principal_id)`. None starts with the columns namespace, bundle ID and SPIFFE ID pattern (`internal/store/postgres/migrations/0168_baseline.sql:2323`)
- The SQLite file has the same two indexes (`internal/store/sqlite/migrations/0168_baseline.sql:1847`)
- The queries of the trust rules sit in one area of `queries.sql`, with the read of one rule before the list (`internal/store/postgres/queries.sql:6088`)
- The service-side read of the JWT path lists every rule of the namespace once, outside the transaction (`internal/trustrulesvc/exchange.go:269 (func match)`)
- The pattern check still allows a trailing `/*` (`pkg/trustrule/trustrule.go:489 (func checkSpiffeSegment)`)
- The conformance suite already has the mTLS cases of `RecordExchange` (`pkg/trustrule/trustruletest/mtls_exchange_cases.go:26 (func mtlsExchangeCases)`)
- The hostile unit table of the match check exists (`internal/store/trustrulestore/exchange_check_test.go:92 (func TestCheckExchangeMatch)`)
- The hostile unit table of the bundle check exists (`internal/store/trustrulestore/exchange_bundle_check_test.go:19 (func TestCheckExchangeBundle)`)

## The earlier change and the open pull request

These facts are about files that are not in the paths of the scope check, so they are not in the list above.

- On main the mTLS path of the service does not exist. It is in the open pull request #3658 (group B of `mtls-mtls-exchange-token`). The issue names `internal/trustrulesvc/exchange.go` near line 269 for the first read, and on main that line is the JWT path.
- Task 2.11 of `mtls-mtls-exchange-token` (refuse the wildcard at the rule write, issue #3585) is open and belongs to #3658 (`openspec/changes/mtls-mtls-exchange-token/tasks.md:33`).
- The ticked tasks 2.3 and 2.9 of `mtls-mtls-exchange-token` say "the existing query ListTrustRulesOfNamespace (no new query, no sqlc change, no schema change)" (`openspec/changes/mtls-mtls-exchange-token/tasks.md:16` and `:19`).
- The design of `mtls-mtls-exchange-token` has one sentence for this read and no task: "The store reads only the rules that the verified chain can match inside the fenced transaction" (`openspec/changes/mtls-mtls-exchange-token/design.md:114`).

Corrections to the issue and to the PM direction, found on main:

- The issue says the recheck needs a read of the rule by ID first. It does not: `RecordExchange` has the rule row in hand when it calls the recheck. The recheck takes that row as an argument. So the change adds one query for each dialect, not two.
- The issue says the first read is in `internal/trustrulesvc/exchange.go` near line 269. On main that is the JWT path. The mTLS path of the service is in #3658.
- The direction says an equality query matches a stored `/*` pattern only if wildcards cannot be stored. The Go check on main is an equality too, and `CheckExchange` never lets a `*` into the SPIFFE ID of the exchange, so a stored wildcard pattern never matches an exchange, before or after this change. The dependency on the wildcard refusal stays (see "Dependencies") as a rule of order, not as a condition of correctness.

## What Changes

- ONE NEW query for each dialect, `ListTrustRulesNamingSpiffeID`, reads `id`, `bundle_id` and `spiffe_id_pattern` of the rules of one namespace with one bundle ID and one SPIFFE ID pattern, by equality, with `LIMIT 2` and no `ORDER BY`.
- ONE NEW index for each dialect, `trust_rules_spiffe (namespace, bundle_id, spiffe_id_pattern)`, in the baseline file, in place.
- `recheckExchange` reads the match with the new query, takes the rule row that `RecordExchange` read, and reads the bundle row as before. It no longer calls `ListTrustRulesOfNamespace`.
- `trustrulestore.CheckExchangeMatch` and `CheckExchangeBundle` take the rule row of the exchange, and `CheckExchangeMatch` takes the narrow match rows. Both keep their sentinels.
- Tests: a read-count test of each adapter that fails on main, a plan test of each adapter, one conformance case, and hostile tables for the store check, the adapter, the service and the handler.
- A benchmark of the issue, before and after, in the pull request body.

Out of scope, behavior stays as today:

- The service-side read of the mTLS path in #3658 that lists the rules once outside the transaction. The issue proposes the fix for the transaction read only. A cap on the rules of a namespace is the other fix of the issue, and it is a new rule for the person, so it needs its own change.
- `ListTrustRulesOfNamespace` and the route that lists the rules.
- The wildcard refusal at the rule write (task 2.11 of `mtls-mtls-exchange-token`).
- A unique index that would refuse two rules with one SPIFFE ID in one bundle. It changes the answer of the rule write. The exchange still counts the rules, so two rules in one bundle refuse the exchange as on main.
- The JWT exchange.

## Dependencies

This change starts only after both of these are on `main`:

1. The pull request #3658 (group B of `mtls-mtls-exchange-token`): the service path of the mTLS exchange. Without it, the pull request would tune a read that no route calls.
2. Task 2.11 of `mtls-mtls-exchange-token` (the wildcard refusal at the rule write, issue #3585, in #3658). Without it, a rule with a `/*` pattern can be stored, and the service and the store would not read one rule set (see "Evidence"). No migration or compatibility code reads a rule that was stored with a wildcard: no tag shipped the shape, and the dev reset applies (ADR 322 decision 2).

Task 0.1 checks both before any code. The old text of tasks 2.3 and 2.9 of `mtls-mtls-exchange-token` stays as done history, and this change edits nothing in that change. The design of this change supersedes those two tasks for the recheck read: the pull request that completes this change says so in its body.

## Usability

Where the path still costs the person time or surprises them:

- An operator who upgrades a dev database gets `ErrSchemaDiverged` from the store, because the baseline file changes in place. The fix is `opendustpan dev reset`. No installation exists and no tag shipped the baseline, so no one else is affected.
- The text of one refusal changes. When two rules or more name the SPIFFE ID, main reports how many matched (any count). This change reads at most 2 rows, so the text reads "at least 2 rules name the SPIFFE ID of the exchange, want only its rule". It is true for any count of 2 or more. The sentinel, the HTTP answer and the error code stay.

## ADR check

Each quote is copied from the file in `docs/adr/`. The owner tags, the "Changed by" chain and the "Current rule" are from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the title of the ADR carries "(owner)".

**ADR 54: Workload identity: OIDC and JWT token exchange, and mTLS** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: ADR 69 extends; ADR 70 changes decisions 5 and 8; ADR 204 changes decision 3; ADR 222 extends decision 5; ADR 223 extends decision 5; ADR 284 changes decision 3; ADR 369 changes decision 6. Chain to the end: see the list in the proposal of `mtls-mtls-exchange-token`, which ends with ADR 363.
- Current rule: this ADR, with ADR 69, 70, 204, 222, 223, 284, 369.
- Quote: "Workload identity is in milestone 4. Two kinds are in scope: token exchange with OIDC and JWT issuers, and mTLS client certificates."
- Effect: The mTLS exchange keeps its scope and its answers.
- Conflict: none.

**ADR 284: The token exchange issues a stored access token** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current).
- Changed by: ADR 371 changes decision 1. Chain to the end: ADR 371.
- Current rule: this ADR, with ADR 371.
- Quote: "The exchange issues a stored access token. The exchange endpoint checks the JWT of a workload once, against the trust rules of the namespace."
- Effect: The exchange still checks against the trust rules of the namespace, and still issues a stored token. Only the read of the rules in the transaction is narrower.
- Conflict: none.

**ADR 371: Each way to prove identity in the token exchange has its own token kind** (owner-tagged (decisions 1, 2 and 3)).

- Decisions that apply: 2 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "An exchange that checks a TLS client certificate against the trust rules issues a stored access token of the kind `mtls-exchange` for the principal of the rule."
- Effect: The kind, the credential and the principal of the token do not change.
- Conflict: none.

**ADR 250: Defense in depth for security code** (owner-tagged (all decisions): "The project owner decided it.").

- Decisions that apply: 1 (current), 4 (current), 7 (current).
- Changed by: ADR 382 extends decision 3. Chain to the end: ADR 382.
- Current rule: this ADR, with ADR 382.
- Quote: "Never only one layer. Every security-relevant path has at least two independent layers. Each layer must stop the attack alone, without help from the others."
- Quote: "The test sends hostile input directly to that layer, as if every other layer had failed."
- Effect: The narrow read is a new way to give rows to a check. The check keeps its own comparison in Go (a layer that does not rely on the SQL `WHERE`), and each layer gets a hostile test that fails when the guard goes. See "Layers" in `design.md`.
- Conflict: none.

**ADR 322: No migrations or compatibility code before the first tag** (not owner-tagged as a title; decisions 1, 2, 4 and 5 are owner decisions).

- Decisions that apply: 1 (current), 2 (current).
- Changed by: ADR 325 changes decisions 1, 5 and 6; ADR 326 extends decision 1; ADR 362 changes decision 5. Chain to the end: ADR 325, ADR 326, ADR 362.
- Current rule: this ADR, with ADR 325, 326, 362.
- Quote: "One baseline for each dialect until the first tag (owner)."
- Quote: "No migration or compatibility code for untagged shapes (owner)."
- Effect: The index goes into both baseline files in place, in name order. No numbered migration. The SHA-256 of each baseline changes, so a dev database needs `opendustpan dev reset`.
- Conflict: none.

**ADR 362: Owner decisions of 8 November 2026: stable IDs, store order, MySQL, baseline hashes and portable backups** (owner-tagged (decision 1)).

- Decisions that apply: 1 (current), 5 (changed: the `.sha256` files of the baselines are absent until the first tag).
- Changed by: ADR 363 extends decision 2. Chain to the end: ADR 363.
- Current rule: this ADR, with ADR 363.
- Quote: "Every stored reference to an entity uses the stable ID of the entity when a user can rename the entity, or delete and recreate it."
- Effect: The new query binds the bundle by its stable ID (`bundle_id`) and never by a name. No `.sha256` file changes, because none exists before the first tag.
- Conflict: none.

**ADR 364: Unexported fields and golangci-lint before added checks** (owner-tagged: "(owner)").

- Decisions that apply: 1 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Effect: The design adds no custom check and no tool. The structure that removes the bug class is the narrow type of the match rows (see "Decisions" in `design.md`).
- Conflict: none.

No ADR or RFC speaks about the cost of a trust rule read, an index of `trust_rules` or a cap on the rules of a namespace. Proof: `rg -n -i "trust rule" docs/adr docs/rfc | rg -i "index|cap|limit|performance|latency"` (run on main bebc30fa5) names no decision about the read, the index or a cap. The cap is not decided here (see "What Changes").

## Open questions

none

Considered and answered:

- Does the narrow read change who may exchange? No: the SQL `WHERE` is the same equality as the Go loop, the Go check keeps its own comparison, and the conformance cases of main run again unchanged (task 1.6).
- Does the index need a different form for SQLite and PostgreSQL? No: both take a plain B-tree on three columns. A row with a NULL `bundle_id` (a JWT rule) never matches `bundle_id = $2`, and `CheckExchangeMatch` refuses an empty bundle anyway.
- Is a cap on the rules of a namespace needed too? The issue lists it as the other option. This change does not add it, because it adds a rule that a person can hit. The cost no longer depends on the count, so the reason for a cap is gone for this read. A cap for another reason is a new proposal.
- Order of this change. It starts after #3658 and task 2.11 are on main (see "Dependencies"), by direction of the PM.
