---
version: 1
---

Issue: #2011, #2478, #2504.

## Problem

A refusal or a change of a break-glass account leaves only an audit entry today. The other site administrators learn of it late or never. A site administrator who disables or deletes a break-glass account wants the others to know at once.

## Evidence

- The server refuses removal of the last break-glass account with a stable error code. Proof: internal/errcode/catalog_principal.go:13 lists principal.last-break-glass.
- The break-glass Store port exists on main. Proof: pkg/breakglass/store.go:40 defines breakglass.Store.
- The last-break-glass refusal has a test on main. Proof: pkg/principal/breakglass_test.go:33 holds TestLastBreakGlassIsKept.
- The notify package exists on main, and no outbox writer is listed. Proof: The pack lists the package pkg/notify under main. Issue 2011 says no production code writes to the outbox, so a task of this change adds the first writer.

## Fix boundary

- The principal service writes one critical notice to each other enabled site administrator in the same transaction as each disable or delete of a break-glass account.
- A refused disable, delete or sweep of the last enabled account writes its audit entry and one notice in a separate short transaction, then answers 409. At most one refusal notice goes out for each actor and target in 10 minutes.
- The notice holds the actor ID, the target ID, the action, the outcome and the time. It holds no reason text.
- The actor gets no notice. If no other enabled site administrator exists, the audit entry is the only record.

Out of scope:

- SQL adapters, review records and review routes for the review after use of a break-glass sign-in (issue 2478).
- Flood control of the passkey begin route (issue 2504, needs an RFC first).
- A REST create route for break-glass accounts, and the command in the error text that depends on it (part 2 of issue 2011).

## Check

- Write TestBreakGlassDisableWritesNotice in pkg/principal/breakglass_test.go: a disable with a second account writes one notice to each other enabled administrator, none to the actor. Check: TestBreakGlassDisableWritesNotice fails on main.
- Write TestBreakGlassRefusalWritesOneNotice in pkg/principal/breakglass_test.go: refusal of a disable, a delete and a sweep of the last account gives 409 and one notice. Check: TestBreakGlassRefusalWritesOneNotice fails on main.
- Write TestBreakGlassNoOtherAdministrator in pkg/principal/breakglass_test.go: the audit entry exists and no notice row exists. Check: TestBreakGlassNoOtherAdministrator fails on main.
- Write TestBreakGlassParallelDisable in pkg/principal/breakglass_test.go: two parallel disables of the last two accounts leave one enabled. Check: TestBreakGlassParallelDisable fails on main under go test -race.
- Write TestBreakGlassNoticeOutboxError in pkg/principal/breakglass_test.go: a forced outbox error leaves the account enabled and answers 500. Check: TestBreakGlassNoticeOutboxError fails on main.
- Wire the notice into the disable route /api/server/principals/{}/disable, the delete route /api/server/principals/{} and the sweep path of pkg/accesstoken. Use an access token of a site administrator. Keep each route action as declared. Add the docs page. Confirmed by: CI (route test and make check). Check: The route test and `make check` give zero findings.
- Add an e2e step that calls the existing route POST /api/server/principals/{}/disable, passes the site administrator action gate and uses an access token. The other administrator reads the notice. Confirmed by: CI (make e2e). Check: `make e2e` passes the new step.
- Usability check: a site administrator completes the path from the docs page alone and the 409 text names `GET /api/server/principals`. A person's step. Check: The docs page commands run in a test.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The server refuses removal of the last break-glass account. (`internal/errcode/catalog_principal.go:13 (error code principal.last-break-glass)`)
- The break-glass Store port exists. (`pkg/breakglass/store.go:40 (type breakglass.Store)`)
- The last-account refusal conformance test exists. (`pkg/principal/principaltest/breakglass.go:73 (func testLastBreakGlassRefused)`)

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P19)**, Item 17: the break-glass floor: "the PM reading in build. Disable, delete and sweep stop at the last enabled account (ADR 306 d3); every other change keeps the two-working-paths floor (ADR 70 d24); first setup needs two. Reason: the stricter floor wherever the newer owner text is silent; owner ADRs 70 and 306 both kept."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. How does a site administrator create a break-glass account through the REST API, so that the error text can name that command?
   - Options: Add a REST create route in a later change, then name it in the error text; Name the host recovery docs page in the error text.
   - Provisional decision (owner review 14 Nov): Add a REST create route in a later change, then name it in the error text.
   - Reason: Every feature has a REST API. The API exposes no break-glass flag and no create route today. This change names no create command and keeps `GET /api/server/principals` in the error text. This change does not depend on the answer. Benchmark: Google Workspace lets super admins manage other super admins through an API.
   - Persona impact: No persona is upset. The audit entry and the notice remain. Escape hatch: The host command for a temporary administrator stays.

Questions considered and answered:

- Which floor applies to the last break-glass account? Owner decision, 13 Nov 2026 (queue P19): disable, delete and sweep stop at the last enabled account. Other changes keep the two-working-paths floor. First setup needs two accounts. The change keeps these rules and adds only the notice.
- Does a failed notice block a change? Yes. The notice shares the transaction of a successful change, so an outbox error rolls back the change and answers 500. A refusal writes in its own transaction. If that transaction fails, the server answers 500 and not 409.
- Can an administrator flood the others with refusal notices? No. One refusal notice for each actor and target in 10 minutes. Every refusal still writes its audit entry.
- Do two parallel disables of the last two accounts both pass the floor check? No. The floor check and the change run in one transaction that locks the break-glass rows. The second transaction sees one enabled account and is refused.
- Does a disable of an already disabled account write a notice? No. The change does nothing and writes no notice. A delete of a disabled account writes a notice, because the account is removed.
- What does the error text name while no create route exists? The one command `GET /api/server/principals`. The create route is a later change.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
