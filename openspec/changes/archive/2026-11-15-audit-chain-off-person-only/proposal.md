---
version: 1
---

Issue: #3169.

Short form: bug (SECURITY). Owner lane: M10 (it holds agent-shapes-before-first-tag, which changes the same function).

Waits for: #3166

## Problem

**Persona: site administrator.** Turning the audit chain off removes the
proof that the audit log was not changed, so the project decided that a
person approves it, and that a principal that one family can control
cannot. Today the check refuses only a service principal. A workload
principal passes, a subject with no principal row passes, and every caller
passes when the principal reader is missing. A refusal writes no audit
entry.

## Evidence

- `pkg/dustpan/auditchain.go:552` (func auditChain.refuseServiceCaller)
  returns nil when `c.principals` is nil (:553), returns nil for
  `principal.ErrNotFound` (:558, the comment says "A subject with no principal
  row is a person and passes"), and refuses only `principal.KindService`
  (:566). It runs from `checkApprovalGate` (:482) for a change to off only.
- `internal/api/server/server.go:852` maps `audit.ErrChainNeedsPerson` to 403.
  `internal/api/server/audit_chain.go:134` (func approveAuditChain) writes the
  error and no audit entry for it.
- The route is wrapped by `h.fresh` (`internal/api/server/audit_chain.go:89`),
  and `requireFresh` (`internal/api/server/freshness.go:82`) refuses a
  request with no console session, so an access token never reaches the
  check through HTTP. The check itself still lets a subject with no row pass.
- #3166 (agent-shapes-before-first-tag) changes the same function to an
  allowlist of person and workload.

## Fix boundary

- The check becomes an allowlist of one kind: only a principal of kind
  person passes. Workload, service, agent and an unknown kind are refused
  with `audit.ErrChainNeedsPerson`.
- It fails closed: a subject with no principal row, a read error and a nil
  principal reader each refuse the approval.
- Each refusal writes one denied entry in the `@server` scope, with the
  action `server:set-audit-chain` and the reason, through the existing
  server audit path.
- Out of scope: the change to on, which stays an operator action.

## Check

The hostile tables pass at the check, the service and the handler, each
fails with its guard removed, and `make check` reports zero findings
(Confirmed by: CI).

## ADR check

**ADR 64: The audit chain is a start setting, and a change is a migration**
(not owner-tagged). Changed by: ADR 72 changes decisions 6, 8 and 10 (decision
6: the migration claim follows the claim rule of ADR 72 decision 2); ADR 210
changes the route paths and the action names. Current rule: this ADR, with
ADR 72 and ADR 210. Quotes, decision 6: "**A person approves the change.**"
and "Turning the chain off needs `server:set-audit-chain` (operator family)
and `server:disable-audit-chain` (administration family), so "both families"
is literal, and a fresh MFA step. A service principal cannot approve it: an
account administrator can make tokens for any service principal, so one
family could act through such a principal." Effect: the title rule is
person only; the reason given for services applies to each kind that is not
a person. Conflict: none.

**ADR 67: Who creates and administers a service principal** and **ADR 215**
(it changes ADR 67 decision 7 to persons, workloads and groups of a
namespace). Effect: workload principals are made and administered by
administrators like service principals, so the reason of ADR 64 decision 6
applies to them. Conflict: none.

**ADR 227: One fresh-authentication window** (decision 6). Quote: "An access
token never gives a fresh authentication." Effect: the bootstrap static
token is an access token, so it cannot give the fresh step that ADR 64
decision 6 asks for, and it cannot approve the change to off. Conflict:
none.

**ADR 339** (owner, 2026-11-07), Context, applies the same rule to the
bootstrap token: "The bootstrap admin token cannot make a grant. Each grant
needs a fresh authentication, and an access token never gives one (ADR 227
decision 6)." Conflict: none.

## Open questions

none. Considered: (1) can the bootstrap static token (no principal row)
approve the change to off? No: ADR 64 decision 6 asks for "a fresh MFA step",
and ADR 227 decision 6 says "An access token never gives a fresh
authentication"; ADR 339 applies this to the bootstrap token. The route
already refuses a request with no console session. A subject with no row is
refused by the check too. (2) keep workloads allowed, as #3166 does (refused:
the reason of ADR 64 decision 6 applies to them).

Depends on:

- agent-shapes-before-first-tag
