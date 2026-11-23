# 396. Sweeps run on the team's own runners, and the CLI does not audit its own usage errors

Status: accepted · 2026-11-23. Records decisions of the project owner of 23 November 2026, in the owner's words

Changes:

- Changes ADR 164 decision 5: a sweep runs on a runner pool of the team that has the sweep purpose.
- Extends ADR 343 decision 3: sweeps run on a runner of the team.
- Extends ADR 250 decision 5: a refusal that the CLI makes before it sends a request has no audit entry.

## Context

The change `130-138-runner-pools-management` and the changes that copy its owner line stopped on two questions. The product manager put them to the project owner.

- On 13 November 2026 the owner answered queue item P14. The answer said that only an installation pool may have the sweep purpose, never a pool of a namespace. It kept an older line, "a sweep never runs on user runners". ADR 164 and ADR 343 put a sweep on the team's runner, with the read-only identity of its host. The two did not fit. The pool management API refused the sweep purpose on a pool of a namespace. No installation pool exists.
- ADR 250 step 5 says that a denial goes to the audit log. The CLI of runner pools refuses some input before it sends a request. An example is a pool name that is not valid. No server sees that refusal. The reviewer asked if the CLI must write an audit entry for it.

## Decision

1. **Sweeps run on the team's runners (owner, 2026-11-23).** A sweep runs on a runner pool of the team that has the sweep purpose. The host identity of the pool has no write access. ADR 164 decision 1 and ADR 343 decision 3 say so, and main implements it. The line "a sweep never runs on user runners" no longer holds. The answer of 13 November 2026 about an installation pool no longer holds. The server has no installation sweep pool. The pool management API accepts the sweep purpose on a pool of a namespace. It applies the same rules as for any other purpose. The owner answered: "Sweeps must run on the user's own runners as we most recently re-designed it."
2. **The CLI does not audit a client-side refusal (owner, 2026-11-23).** The owner answered: "Exempt client-side refusals, but document this". A refusal that the CLI makes before it sends any request is a usage error. It has no audit entry. The server audits each call that reaches it. ADR 250 step 5 applies to the audit log of the server. The user guide of runner pools says this. The security section of `CLAUDE.md` says this too.

## Consequences

- The changes that quote the answer of 13 November 2026 get a correction. The tasks that refuse the sweep purpose on a pool of a namespace go away. Task 2.3 of `add-job-session-paths-runner-2` adds no refusal. It tests that a claim on a pool with the sweep purpose gets no job session.
- A reviewer does not ask for an audit entry for a usage error of the CLI. The reviewer checks that the server audits each call that reaches it.
- A sweep pool of a team shows in the pool list with its purpose. Its host identity is a stored grant like any other.
- ADR 164 and ADR 343 list this ADR in their "Changed by" lists.
