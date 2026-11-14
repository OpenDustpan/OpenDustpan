# 373. The job actions in the built-in roles, the answer to an ended token, and the break-glass pass

Status: accepted · 2026-11-14. Decided by the project owner

Changes:

- Extends ADR 212 decision 1: the job and runner actions in the built-in roles.
- Extends ADR 53: the answer to an ended access token.
- Extends ADR 227 decision 1: the proof of a break-glass pass.

## Context

Three OpenSpec changes stopped on a question that no ADR answered. The
project owner answered the three questions on 14 November 2026.

- `job-service-unit-settings-pool` gives the actions `unit:run-plan`,
  `unit:define-steps`, `job:read`, `job:read-plans`, `job:read-logs` and
  `namespace:manage-runners` to built-in roles. It named the roles `starter`
  and `lead`, which do not exist. The built-in roles are in
  `pkg/access/roles.go`. ADR 212 decision 1 gives `unit:run-plan` and
  `job:read-plans` to `planner`.
- `token-life-cycle-expiry-email-token-expiry-enforcement` asked for an
  answer that names an ended token. Such an answer tells a caller that the
  token existed. The server answers an expired, revoked or disabled token
  with the same 401 as an unknown token.
- `break-glass-pass-wiring` needs a store, the way a client sends the pass,
  and the proof that gets a pass. No ADR decides them.

## Decision

1. **The job actions go to the roles that exist (owner, 2026-11-14).** No
   new role `starter` or `lead` exists. `viewer` holds `job:read`. `planner`
   holds `job:read`, `job:read-plans`, `job:read-logs` and `unit:run-plan`.
   `writer` holds the actions of `planner`. `maintainer` holds the actions of
   `writer`, `unit:define-steps` and `namespace:manage-runners`. `admin`
   holds all of them.
2. **An ended token gets the plain 401 (owner, 2026-11-14).** An expired,
   revoked or disabled token gets the same 401 as an unknown token. The
   answer does not name the token. The audit entry holds the real reason and
   the ID of the token. A person sees the ended tokens with
   `GET /api/server/caller/tokens?status=ended`. The owner added: "Watch out
   for timing attacks". So an ended token and an unknown token take the same
   path and the same work before the 401: the lookup by the hash of the
   secret, the compare in constant time, then the check of the end. An
   unknown token also writes an audit entry, with the reason "unknown token"
   and no token ID. A test checks the structure, not the time: one refusal
   function for each 401, the same bytes and headers for each case, and the
   compare in constant time.
3. **The break-glass pass is a stored record (owner, 2026-11-14).** A pass is
   a row of a new table, written with its audit entry in one transaction. A
   request header carries the ID of the pass on the routes that take one. A
   person gets a pass only after a fresh authentication of ADR 227 decision
   1. A pass is for one use, and it ends after 15 minutes.

## Consequences

- The three changes are spec-ready.
- The action sets of decision 1 are code in `pkg/access/roles.go`. The
  catalog of roles in the API shows them.
- The guide of access tokens tells how to list the ended tokens.
