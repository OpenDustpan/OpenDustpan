## Context

The mTLS exchange records a stored token in one fenced transaction. In it, the store checks again what the service checked before: the rule still names the SPIFFE ID alone, and the bundle and its version are still the active ones (change `mtls-mtls-exchange-token`, tasks 2.3 and 2.9). The check reads every rule of the namespace, with all columns and a sort that no index serves, while it holds the fence lock. The cost grows with the number of rules of the namespace (172.7 ms at 100,000 rules, 0.56 ms at 100).

The person is a runner operator whose jobs exchange a certificate for a token. The cost of one exchange must not depend on how many rules the namespace holds, and the answer must not change.

## Decisions

1. **The recheck reads the rows that it compares, and no more.** The check needs two facts: the rule of the exchange (its bundle ID), and how many rules name the same SPIFFE ID in that bundle. `RecordExchange` already holds the first one: it reads the rule row with `GetTrustRule` and checks it with `CheckExchangeRule` before it calls `recheckExchange`. The recheck takes that row as an argument and reads only the second fact, with one NEW query. The bundle row is read as before with `GetTrustBundleOfNamespace`, which is a read by key. So the recheck reads at most 2 narrow rows and one bundle row, at any size of the namespace, and the second read of the rule by ID that the issue proposes is not needed.
2. **The structure that makes the bug class impossible.** The bug class is "a check reads a list that grows with the namespace". The check functions take a narrow type, `MatchRow{ID, BundleID, SpiffeIDPattern}`, and the one rule row of the exchange. They have no parameter that can hold the list of the rules of a namespace, so a caller cannot pass the list by mistake, and the code does not need a lint rule or a new tool to keep it. `recheckExchange` no longer calls `ListTrustRulesOfNamespace`. The test of task 1.3 fails if a later change brings that call back (see "Layers"). No analyzer is added (decision of the project on added checks); the structure and the test take its place.
3. **One new query for each dialect, by equality, with `LIMIT 2`.** `ListTrustRulesNamingSpiffeID` reads `id`, `bundle_id` and `spiffe_id_pattern` where `namespace = $1 AND bundle_id = $2 AND spiffe_id_pattern = $3`. The check wants exactly one rule, and the one rule is the rule of the exchange. Two rows already refuse the exchange, so the read stops at 2. There is no `ORDER BY`: no row order changes the answer, and a sort would cost a sort. The query uses `=` on text and never `LIKE`, so a `%` or `_` in a stored pattern is a plain character. The `bundle_id` parameter is `CAST(... AS text)` (as `UpdateTrustRule` does), so sqlc generates a plain `string` and not a nullable type. A NULL `bundle_id` (a JWT rule) never equals a text value, so such a rule is never read. The query sits before `ListTrustRulesOfNamespace` in the name order of its area (the name starts with ListTrustRulesN and the other with ListTrustRulesO).
4. **One new index for each dialect.** `trust_rules_spiffe` on `(namespace, bundle_id, spiffe_id_pattern)`, a plain B-tree, not unique. It serves the query as a prefix lookup on all three columns. It is not a partial index and not unique:
   - Not partial: the query already excludes a NULL `bundle_id`, and a partial index adds a predicate that the planner must prove, in two dialects.
   - Not unique: a unique index would stop two rules with one SPIFFE ID in one bundle at the rule write. That is a change of the rule write (a new refusal that a person can hit), so it is a different capability. The exchange keeps its own count, so two such rules still refuse the exchange, as on main.
   The index goes into `internal/store/postgres/migrations/0168_baseline.sql` and `internal/store/sqlite/migrations/0168_baseline.sql`, in place, after `trust_rules_principal` (name order: `trust_rules_name`, `trust_rules_principal`, `trust_rules_spiffe`). No numbered migration. The SHA-256 of each baseline changes, so the store refuses an existing dev database with `ErrSchemaDiverged`; the operator runs `opendustpan dev reset`. No tag shipped the baseline, so no one else is affected.
5. **The check keeps its own comparison in Go.** SQL returns the rows with the equality, and `CheckExchangeMatch` compares the same two columns again on the narrow rows. The Go compare is a defense-in-depth layer: it does not rely on the `WHERE` clause, the index or the driver, and its comment says so. A hostile test sends the check rows that the query would never return (a row of another bundle, a row with a wildcard pattern, a row that is not the rule of the exchange) as if the SQL had failed.
6. **The rule row is checked too.** `CheckExchangeMatch` refuses unless the rule row has the ID of the rule of the exchange, a non-empty bundle ID and the pattern that is the SPIFFE ID of the exchange. On main the equivalent facts come out of the loop over the whole list (the row of the rule of the exchange must be in the list, and it must be the one match). The new refusals have the same sentinel (`ErrNotFound`) and the same cause in the data.
7. **The same locks, the same transaction.** The read runs in the fenced transaction on the queries of the transaction, as the full list did. A write of a rule takes the same fence, so a rule that another call added after the service read is still visible to the recheck (the conformance case of main for a rule that is added after the read, unchanged). The change shortens the time that the fence lock is held, and it adds no lock and no new transaction.
8. **The text of one refusal changes, nothing else.** When two or more rules match, the text says "at least 2 rules", because the read stops at 2. The sentinel `trustrule.ErrNotFound`, the service error `ErrNoMatchingRule` and the answer 401 mtls.no-matching-rule stay. `rg` on main finds no test that asserts that text (see "Evidence" in the proposal).
9. **Order.** The change starts after #3658 and task 2.11 of `mtls-mtls-exchange-token` are on `main` (see "Dependencies" in the proposal). The reason is the order of the work and not the safety of the read: the equality of the new query matches the equality that the Go check already makes (`CheckExchange` refuses a `*` in `x.SPIFFEID` before the transaction), so a stored wildcard never matches before or after this change. Starting after the wildcard refusal means the service and the store read one rule set, and no one has to reason about a stored wildcard in the new query. No code reads or converts a rule that was stored with a wildcard.
10. **The old text of tasks 2.3 and 2.9 of `mtls-mtls-exchange-token`.** Both tasks are done and ticked, and this change leaves their text as history. For the recheck read, this design supersedes "with the rows from the existing query ListTrustRulesOfNamespace (no new query, no sqlc change, no schema change)". The sentence of its design at line 114 is the intent that this change builds. The pull request of this change says so in its body.
11. **Call sites are edited by hand.** `CheckExchangeMatch` has 2 callers in code (the two adapters, through `recheckExchange`) and 1 test file. `CheckExchangeBundle` has the same 2 callers and 1 test file. The count is small, so no `go fix` or `gofmt -r` rewrite is needed.

## Layers

Each layer stops the attack alone. None relies on another. An attack here is a rule or a row that makes the store record a token for a rule that no longer names the SPIFFE ID alone.

| Layer | What it checks | Test (hostile table) |
| --- | --- | --- |
| Service (existing, in #3658) | The service verifies the chain and picks the one rule in memory. It sends the SPIFFE ID of the verified chain, never the pattern of the rule. A refusal of the store gives no token. | NEW test `TestMtlsExchangeStoreRefusal` in NEW file internal/trustrulesvc/mtls_exchange_recheck_test.go: a store that answers `ErrNotFound` gives no token, an audited refusal and `ErrNoMatchingRule`; the store sees the SPIFFE ID of the chain. Fails when the service sends another value or when it records a token after a refusal. |
| Store check, kind and SPIFFE ID | `CheckExchange` refuses a `*` in the SPIFFE ID, a missing one for the kind `mtls-exchange`, and one on a JWT token. | Existing `TestCheckExchangeKinds`. Unchanged. |
| Store check, match | `CheckExchangeMatch` refuses unless the rule row has the ID of the rule of the exchange, a bundle ID and the pattern that is the SPIFFE ID of the exchange, and exactly one narrow row has the same bundle and pattern and it is the rule. It compares in Go and does not trust the SQL. | NEW test `TestCheckExchangeMatchRows` in `internal/store/trustrulestore/exchange_check_test.go`: a table of hostile rows (see task 1.1). Each guard has a case that fails when the guard is taken out. |
| Store check, bundle | `CheckExchangeBundle` refuses unless the bundle row exists, is the bundle of the rule row and has the active version. | Existing `TestCheckExchangeBundle`, changed to the new signature (task 1.2). |
| Adapter (SQL) | The query binds the namespace, the bundle and the pattern as placeholders and compares with `=`. It reads at most 2 rows. | NEW internal tests of each adapter: `TestRecheckExchangeReadsOnlyTheMatchingRules`, `TestRecheckExchangeHostileRows`, `TestListTrustRulesNamingSpiffeIDPlan` (tasks 1.3 to 1.5). The hostile rows are written with SQL, as if the write guards had failed: a stored wildcard, `%` and `_` in a pattern, a pattern that differs by case or by a trailing slash, the same pattern and bundle ID in another namespace. |
| Handler (existing, in #3658) | The route answers the refusal with the code of the refusal, and never a token. | NEW test `TestExchangeMtlsStoreRefusalAnswer` in NEW file internal/api/trustrules/exchange_mtls_recheck_test.go: a store refusal answers 401 mtls.no-matching-rule, the body holds no token, and the audit log holds one denial entry. |

The redundant checks keep a comment that says they are a defense-in-depth layer and names what they do not rely on. The compare in `CheckExchangeMatch`: "Defense in depth: the query compares the same columns with `=`; this compare stays so that a wrong query, an index or a driver cannot make the store accept a row that does not name the SPIFFE ID of the exchange." The check of the rule row: "Defense in depth: RecordExchange reads this row by its ID and checks it with CheckExchangeRule; this check stays so that a new caller of the recheck cannot pass another row." No comment names an ADR.

## Contract

**Port.** `trustrule.ExchangeStore.RecordExchange` does not change: same signature, same sentinels.

**Store checks** (`internal/store/trustrulestore`, not public API):

```go
// MatchRow is the narrow row that the match query returns.
type MatchRow struct {
	ID              string
	BundleID        string
	SpiffeIDPattern string
}

func CheckExchangeMatch(rule Row, matches []MatchRow, x *trustrule.Exchange) error
func CheckExchangeBundle(row *BundleRow, rule Row, x *trustrule.Exchange) error
```

- `CheckExchangeMatch` returns nil for an exchange with no SPIFFE ID. For an exchange with a SPIFFE ID it returns `trustrule.ErrNotFound` unless `rule.ID` is `x.Rule`, `rule.BundleID` is not empty, `rule.SpiffeIDPattern` is `x.SPIFFEID`, and exactly one element of `matches` has the bundle `rule.BundleID` and the pattern `x.SPIFFEID`, and that element has the ID `x.Rule`. Same sentinel as main (`trustrulestore.go:345`).
- `CheckExchangeBundle` returns `trustrule.ErrNotFound` unless `row` is not nil, `row.ID` is `x.BundleID`, `rule.BundleID` is `x.BundleID` and `row.Active` is `x.BundleVersion`, for the kind `mtls-exchange`; nil for `jwt-exchange`; `trustrule.ErrInvalid` for any other kind. Same sentinels as main (`trustrulestore.go:293`). The parameter `rows []Row` becomes `rule Row`: the old code used `rows` only to find the bundle ID of the row of `x.Rule`.

**Queries** (each dialect, `pkg/store/<dialect>/queries.sql`):

```sql
-- name: ListTrustRulesNamingSpiffeID :many
SELECT id, CAST(COALESCE(bundle_id, '') AS text) AS bundle_id, spiffe_id_pattern
FROM trust_rules
WHERE namespace = sqlc.arg(namespace)
  AND bundle_id = CAST(sqlc.arg(bundle_id) AS text)
  AND spiffe_id_pattern = sqlc.arg(spiffe_id_pattern)
LIMIT 2;
```

The SQLite copy writes `AS TEXT`, as the other queries of that file do.

**Stored schema.**

| Dialect | Index | Columns | Kind |
| --- | --- | --- | --- |
| PostgreSQL | `trust_rules_spiffe` | `(namespace, bundle_id, spiffe_id_pattern)` | `CREATE INDEX ... USING btree`, not unique |
| SQLite | `trust_rules_spiffe` | `(namespace, bundle_id, spiffe_id_pattern)` | `CREATE INDEX`, not unique |

No column, table or constraint changes. No API route, JSON member, error code or CLI output changes.

## Read and write counts

The count of reads of the recheck, for an exchange in a namespace with N rules:

| | Main | After |
| --- | --- | --- |
| Statements | 2 (list, bundle) | 2 (match, bundle) |
| Rows returned by the match read | N (all columns, sorted) | at most 2 (3 columns, no sort) |
| Rows returned by the bundle read | 1 | 1 |
| Work in the database | scan of the namespace, sort | lookup in `trust_rules_spiffe` |

The number of round trips does not change (the rule read of `RecordExchange` is the same one as on main). The rows and the work no longer depend on N. The issue counts "2 round trips" as part of the cost. This change does not remove a round trip, because the bundle read is a separate fact; folding the two reads into one statement is not worth a join in two dialects when the cost is a keyed read.

## Failure modes

| Step | Failure | Retry | State after |
| --- | --- | --- | --- |
| Match read | The database is unavailable or the context ends | Retry the exchange as a new call. | The transaction rolls back. No token and no exchange audit entry. |
| Match read | The index is missing (an old dev database) | Run `opendustpan dev reset`. The store refuses to open with `ErrSchemaDiverged`. | The server does not start. |
| Match check | Two rules name the SPIFFE ID in the bundle | The administrator deletes one rule. | No token. 401 `mtls.no-matching-rule`. |

## Benchmark

Task 4.2 runs the benchmark of the issue before and after on PostgreSQL 18 (a local server that the implementer starts): 200,000 rows in `trust_rules`, one namespace with 100,000 rules (500 bytes of `conditions_json` each), one namespace with 100 rules, `EXPLAIN (ANALYZE, BUFFERS)` of the old list and of the new match read, with the namespace and the bundle of one real rule. The pull request body shows both outputs and the times. The change adds no CI gate and no benchmark file to the repository.
