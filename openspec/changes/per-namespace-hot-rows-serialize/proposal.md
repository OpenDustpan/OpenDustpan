---
version: 1
---

Issue: #1424, #1599, #1801.

## Problem

An Operator who runs many writes in one namespace hits a ceiling of about one commit time per write: audit appends lock the namespace row and unit writes upsert one head row. The Operator needs the ceiling documented and the locked section shortened without breaking the audit chain.

## Evidence

- Each audit append locks the namespace row for update before it reads the last entry and inserts the next entry. Proof: pkg/store/postgres/audit.go:455:	version, err := q.GetFailoverVersionForUpdate(ctx, fence.Namespace.String())
- Each unit write takes the next inventory change number under the per-namespace head row. Proof: pkg/store/sqlite/inventory_changes.go:47:	seq, err := q.NextInventoryChangeSeq(ctx, nsID)
- The namespace-row lock query exists on main. Proof: pkg/store/postgres/queries.sql:100:-- name: GetFailoverVersionForUpdate :one
- No pack evidence proves pg_stat outputs as code fact. Proof: rg -n -F --glob '!*_test.go' 'pg_stat_statements' pkg internal cmd matches 0

## Fix boundary

- Run each audit append in a short transaction of its own after the read on the existing namespaces row
- Keep gapless per-namespace inventory change numbers with a shorter locked section
- Document the per-namespace write ceiling of #1424 in the scaling guide

Out of scope:

- Dedicated audit_chain_heads table and backfill, and gap-tolerant SEQUENCE numbers, wait for a later RFC
- The by-ID plan choice of #1599 stays open as a separate follow-up with its cause analysis linked
- The inventory lock_holder bimodal reads and writes of #1801 stay open as a separate follow-up with its cause analysis linked
- New audit chain semantics or new numbering semantics

## Check

- Add failing serial-chain test in NEW pkg/store/storetest/audit_hot_row_test.go for concurrent AppendAuditEntry calls. Check: go test ./pkg/store/storetest/ -run TestAuditAppendSerialChain -count=1 fails on main
- Add failing order test in NEW pkg/store/storetest/inventory_hot_row_test.go for poll readers. Check: go test ./pkg/store/storetest/ -run TestInventoryChangeOrder -count=1 fails on main
- Move the audit append in `pkg/store/postgres/audit.go` to a short transaction and keep the fence check atomic. Check: go test ./pkg/store/... -run TestAuditAppendSerialChain -count=1
- Shorten the inventory change path in `pkg/store/postgres/queries.sql` and `pkg/store/sqlite/queries.sql` without changing numbering. Check: go test ./pkg/store/... -run TestInventoryChangeOrder -count=1
- Document the ceiling in NEW docs/guides/scaling-per-namespace-ceiling.md with the per-namespace ceiling and the lock-wait command. Check: `test -f docs/guides/scaling-per-namespace-ceiling.md` passes
- Run the store suites for both dialects. Check: go test -race ./pkg/store/... -timeout 60m passes
- Run the done checks through route ANY /api/audit/entries with an access token, plus the load harness and the docs page. Check: `GET /api/audit/entries` returns 200 with an access token; `make load` records fields; `make check` with zero findings

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main locks the namespace row for update when it appends an audit entry. (`pkg/store/postgres/internal/db/queries.sql.go:6483 (func Queries.GetFailoverVersionForUpdate)`)
- Main reads the last audit entry of a scope before it inserts the next entry. (`pkg/store/postgres/internal/db/queries.sql.go:12844 (func Queries.LastAuditEntry)`)
- Main inserts one audit entry per append. (`pkg/store/postgres/internal/db/queries.sql.go:10490 (func Queries.InsertAuditEntry)`)
- Main numbers inventory changes per namespace with NextInventoryChangeSeq. (`pkg/store/sqlite/internal/db/queries.sql.go:18651 (func Queries.NextInventoryChangeSeq)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does an ADR already decide the audit chain order or the inventory change-log numbering? The pack names no ADR and no RFC. This change keeps hash-linked order and gapless numbering, so it changes no decided rule. The later dedicated head row and gap-tolerant numbers need an RFC first, per the issue text.
- What pack evidence proves the hot rows? pkg/store/postgres/audit.go:455 takes GetFailoverVersionForUpdate, and pkg/store/sqlite/inventory_changes.go:47 takes NextInventoryChangeSeq. Issue benchmark numbers stay in the issue text and pg_stat outputs are not code facts, proved by zero matches for pg_stat_statements.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
