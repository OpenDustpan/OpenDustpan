---
version: 1
---

Issue: #1349.

## Problem

A site administrator must approve an audit chain change during maintenance mode. The approval needs a fresh console session, but maintenance mode refuses the session write, so no fresh session is possible after maintenance starts.

## Evidence

- Maintenance routes exist on main. Proof: ANY/POST /api/dustpan/v1/maintenance/enter where route table or API description
- Session start route exists on main. Proof: ANY /api/server/session where route table or API description
- Audit chain route exists on main. Proof: ANY /api/server/audit/chain where route table or API description
- Maintenance refusal codes exist on main. Proof: maintenance.in-maintenance where internal/errcode/catalog_maintenance.go:14

## Fix boundary

- Allow site administrator POST to session start route during maintenance mode with its audit entry.
- Keep every other write refused with existing maintenance codes.
- Document sign-in path for audit chain approval during maintenance.

Out of scope:

- Change to fresh-session window
- Change to audit chain approval rule
- Backup or restore behavior

## Check

- Add failing server test for site administrator session start in maintenance mode in NEW pkg/session/maintenance_session_test.go via ANY /api/server/session with credential kind session from password sign-in through the maintenance gate. Check: NEW server test TestStartSessionInMaintenanceModeAsSiteAdministrator fails on main with maintenance.in-maintenance.
- Add failing atomicity test that a failed audit write creates no session in NEW pkg/session/maintenance_session_test.go via ANY /api/server/session with credential kind session. Check: NEW server test TestSessionAuditAtomicityInMaintenanceMode fails on main by creating a session without its audit entry.
- Add failing test that session policy writes, refresh, and sign-out stay refused for the same caller in NEW pkg/session/maintenance_session_test.go via ANY /api/server/session-policy with credential kind session through the maintenance gate. Check: NEW server test TestNonSessionWriteStayRefusedInMaintenanceMode fails on main by allowing the write.
- Open the single session write through the maintenance gate in pkg/maintenance with its audit entry and error mapping in internal/api/problem via ANY /api/server/session with credential kind session. Confirmed by: the driver's gates. Check: ANY /api/server/session in maintenance mode returns 200 for a site administrator with session cookies, and session policy writes still return maintenance.in-maintenance.
- Wire gate, route authorization in internal/api/audit/audit.go, and operator guide text for the fresh session approval via ANY /api/server/audit/chain with credential kind session and e2e step via e2e/signin_token.go:224 (func Env.FreshSession). Confirmed by: the driver's gates. Check: ANY /api/server/audit/chain with the fresh session succeeds, interim guide text says sign in before maintenance, and e2e step uses e2e/signin_token.go:224 (func Env.FreshSession).
- Confirm wiring and route authorization for the session-start exception and the audit chain approval. Confirmed by: the driver's gates. Check: ANY /api/server/session declares its authorization action and ANY /api/server/audit/chain accepts the fresh session in maintenance mode.
- Run e2e step against the real server with a site administrator session credential. Confirmed by: CI (e2e maintenance session path). Check: E2e step starts a session in maintenance mode via ANY /api/server/session then approves via ANY /api/server/audit/chain.
- Update the operator docs page in the same pull request. A person's step. Check: Operator guide page shows sign-in path during maintenance and each refusal names the one help command.

Depends on:

- move-http-route-list-out
- `move-http-route-list-out` (order of the milestone review)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main runs a fail-closed maintenance write check. (`pkg/store/storetest/maintenance.go:422 (func RunWritesFailClosed)`)
- Main has a fresh-session helper. (`e2e/signin_token.go:224 (func Env.FreshSession)`)
- Main checks site administrator in the access service. (`pkg/access/service.go:897 (func Service.isSiteAdministrator)`)
- Main guards the site administrator in the postgres principals store. (`pkg/store/postgres/principals.go:910 (func guardSiteAdministrator)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Does the session write interfere with a running backup or restore, and what test proves it?
   - Options: Allow the session write after a test with a running backup and restore proves non-interference; Keep the session write refused.
   - Provisional decision (owner review 14 Nov): Keep the session write refused until a test with a running backup and restore proves non-interference.
   - Reason: The pack gives no code read of the session write against backup or restore locks, so the confirmation the issue asks for is still missing. Benchmark: ISO 27001 privileged access during maintenance keeps break-glass access with a record of each use.
   - Persona impact: A site administrator waits for the proof; no persona gains a bypass. Escape hatch: None; until the proof passes the session write stays refused and the operator signs in before maintenance mode.
2. Which writes does a provider sign-in need in maintenance mode, such as callback, CSRF, or rate-limit counters, and which stay refused?
   - Options: Allow only password sign-in session start in maintenance mode; Allow provider sign-in after each callback and counter write is verified.
   - Provisional decision (owner review 14 Nov): Allow only password sign-in session start until each provider write is listed and verified.
   - Reason: The pack names the session route but no handler file or method, so the full write set of a provider sign-in is not proven. Benchmark: AWS practice for maintenance sign-in keeps a single local path and refuses federated callbacks until the handler allowlist is verified.
   - Persona impact: A site administrator who uses an external provider signs in with a password during maintenance until the callback path is verified. Escape hatch: Password sign-in works in maintenance mode; provider sign-in waits until its writes are verified.

Questions considered and answered:

- Does the fix keep the fresh-session check on audit chain change? The issue keeps the fail-closed fresh check on ANY /api/server/audit/chain. Only the session start write is in scope; the fresh window and the approval rule do not change.
- Can session start interfere with a running backup or restore? The pack gives no code read of the session write against backup or restore locks, so it stays the one blocking open question and the gate stays closed until the proof test passes.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
