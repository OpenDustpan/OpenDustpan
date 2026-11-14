---
version: 1
---

Issue: #2929.

## Problem

Today nothing calls PruneTickets and nothing outside tests calls NewStoreTickets. Once CAS sign-in is wired, used_tickets only grows, and backups carry the dead rows. The Operator needs a bounded table and a record of each cleanup.

## Evidence

- Nothing outside tests calls NewStoreTickets on main. Proof: rg -n -F --glob '!*_test.go' 'NewStoreTickets' pkg internal cmd matches 2 lines, both in pkg/session/usedtickets.go (111 and 114).
- PruneTickets has SQL adapters on main and no production caller. Proof: rg -n -F --glob '!*_test.go' 'PruneTickets' pkg internal cmd matches 11 lines: pkg/store/sqlite/tickets.go:65, pkg/store/postgres/tickets.go:62 and pkg/session/sessiontest/ticketstore.go:273, with no match in cmd/opendustpan.
- used_tickets exists in the schema, the backup table list and the generated queries. Proof: rg -n -F --glob '!*_test.go' 'used_tickets' pkg internal cmd matches 25 lines including pkg/backup/backup.go:62.

## Fix boundary

- Wire NewStoreTickets in cmd/opendustpan so that the prune has a store and a clock.
- Add a NEW durable operation that calls PruneTickets at start and then once per hour, with one log record of the deleted count.
- Add the NEW counter `cas_used_tickets_pruned_total` with no labels.
- Extend the restore case of the store suite in pkg/store/storetest/ticket_restore.go, so that both SQL adapters under pkg/store are covered.

Out of scope:

- Wiring internal/casauth to a sign-in route, and the CAS sign-in e2e step (the wiring change owns it).
- The rule that a CAS sign-in needs a subject attribute.
- SAML, LDAP, OAuth and trusted-header sign-in.
- The round-trip tests of internal/servertest: this change adds no request path, so internal/servertest/roundtrips_test.go, internal/servertest/roundtrips_model_test.go and internal/servertest/roundtrips_conflict_test.go stay unchanged.

## Check

- Write TestNewStoreTicketsRefusesNil and TestPruneKeepsLiveRows in pkg/session and pkg/session/sessiontest. Check: `go test -race ./pkg/session/...` fails on main for each named test.
- Write TestPruneRunLogsCount, TestPruneRunLogsFailure and TestPruneCounterSumsCounts in cmd/opendustpan. Check: `go test -race ./cmd/opendustpan/...` fails on main.
- Extend the restore case in pkg/store/storetest/ticket_restore.go for the SQLite and PostgreSQL adapters under pkg/store: the first prune deletes expired rows and live rows stay. Check: `go test -timeout 60m ./pkg/store/... -run TicketRestore` fails on main.
- Wire NewStoreTickets and the NEW prune operation in cmd/opendustpan, with the log record. Check: TestPruneRunLogsCount passes.
- Add the NEW counter in internal/metrics. Check: TestPruneCounterSumsCounts passes.
- The CAS replay e2e step is BLOCKED by subject-sources-can-change-cas, which wires internal/casauth to a sign-in route. No route and no credential exist to call today. The step stays unticked until the blocker merges. Check: The e2e step stays unticked until the blocker merges.
- Write docs/guides/cas-sign-in.md with the 1-hour record, the hourly prune and the metric. The prune adds no route and no authorization action. Check: `make docs-lint` reports nothing.
- Check that an Operator finds the prune record and the metric from the docs page alone. Check: The Operator finds the log record and the counter.

Depends on:

- subject-sources-can-change-cas (the CAS subject rule and the wiring of internal/casauth; it owns the sign-in route and the e2e step)
- `subject-sources-can-change-cas` (order of the milestone review)
- `architecture-review-7-nov-no` (order of the milestone review)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The UsedTickets type exists. (`pkg/session/usedtickets.go:24 (type UsedTickets)`)
- The constructor exists. (`pkg/session/usedtickets.go:114 (func NewStoreTickets)`)
- The ticket conformance suite exists. (`pkg/session/sessiontest/ticketstore.go:27 (func RunTicketStore)`)
- Restore keeps used_tickets. (`pkg/store/storetest/ticket_restore.go:17 (func testRestoreKeepsUsedTickets)`)

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P20)**, Item 18: #2915 subject sources that can change: "(i) refuse mutable subject sources (CAS needs an attribute, no plain header mode, LDAP entryUUID/objectGUID only, OAuth `sub` only). Reason: Entra, Okta and Kubernetes guidance; no users, so it costs nothing now and can be loosened later. ADR 321 (owner-accepted) text waits for the owner; ADR 70 d22 (owner) is kept."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 321: LDAP, SAML, CAS, OAuth 2.0 groups and trusted-header sign-in** (not owner-tagged).

- Decisions that apply: 26 (current).
- Changed by: ADR 332 changes decisions 19, 30, 33 and 38; ADR 332 extends decisions 13, 14, 15 and 22. Chain to the end: ADR 332.
- Current rule: this ADR, with ADR 332.
- Quote: "The server uses a ticket once, and refuses a second use with `ErrReplay`. A used ticket record lives 1 hour."
- Effect: The prune deletes a row only after its 1-hour record ends. The replay refusal stays in UsedTickets.
- Conflict: none.

## Open questions

Not spec-ready: a code check fails: task "The CAS replay e2e step is BLOCKED by subject-sources-can-ch" is not confirmed by the agent alone: end it with one fixed phrase: "Confirmed by: CI (...)", "Confirmed by: the driver's gates." or ; a code check fails: task "Check that an Operator finds the prune record and the metric" is not confirmed by the agent alone: end it with one fixed phrase: "Confirmed by: CI (...)", "Confirmed by: the driver's gates." or ; a code check fails: task "Run `make check` for the whole change." is not confirmed by the agent alone: end it with one fixed phrase: "Confirmed by: CI (...)", "Confirmed by: the driver's gates." or "A person's step."; a code check fails: and 3 more.

Questions considered and answered:

- Does the subject rule apply here? Owner decision, 13 Nov 2026 (queue P20) refuses mutable subject sources and needs a CAS attribute. This change adds no sign-in route, so the rule belongs to subject-sources-can-change-cas.
- How long does a used-ticket row live? ADR 321 decision 26: 1 hour. The prune deletes only rows with expiry at or before the cutoff.
- Must the prune leave a record? CLAUDE.md says an automatic action writes a record of what it did. The prune writes one log record with the count and adds to the metric.
- Which clock decides expiry? The clock passed to NewStoreTickets. A skew only delays a delete.
- Does the change add a request path? No. It adds a background operation only, so the round-trip tests of internal/servertest do not change.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
