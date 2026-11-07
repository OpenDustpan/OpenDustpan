# 354. Agent principals, delegation and the kind agent

Status: accepted · 2026-11-07. Decided by the project owner on 7 November 2026

Changes:

- Changes ADR 240 decisions 1, 3 and 4: `principal.kind` gets the value `agent`, and an unknown kind fails closed.
- Extends ADR 52 decisions 1, 3 and 4: a fourth record kind. An agent holds ceiling grants only and is never a member of a group.
- Extends ADR 53: delegated tokens, their lifetime and rotation.
- Changes ADR 70 decision 13: a delegation is a new edge kind with intersection semantics, and the word "delegation" there becomes "act-as right".
- Changes ADR 120 decisions 13, 14, 15 and 16: Dusty becomes an agent principal, and a run names a delegation.
- Changes ADR 125 decisions 3, 5, 9 and 15: agent rules key on the delegation.
- Changes ADR 128 decisions 1, 5 and 17: `job.AgentRef` holds the agent, the delegation and the token family.
- Extends ADR 205 decisions 3 and 6: the content of `act`, the `sub` key `agent` and the claim `agent`.
- Changes ADR 222 decision 5: an access decision of a delegated request has two evaluations.
- Extends ADR 224: gates and approvals name the subject of a delegated request.
- Extends ADR 226 decisions 3 and 4: new facts, and approver exclusions name the subject.
- Extends ADR 227: an agent never has a fresh authentication.
- Extends ADR 263: triggers and drift schedules carry the delegation.

## Context

[RFC 121](../rfc/0121-agent-principals.md) designs the agent principal, the delegation of a person to an agent, held requests, confirmations and the agent policy of an installation. It has 52 decisions in its "Proposal". This ADR does not repeat them. Read the RFC for the record, the threat model, the one-way doors and the implementation slices. The accepted design of the RFC is options 1D, 2C, 3B and 4A with the decisions of its "Proposal".

On 7 November 2026 the project owner accepted the five open questions of the RFC, with the changes below.

## Decision

Decided by the project owner on 7 November 2026.

1. **The person who delegated may confirm (owner, 2026-11-07).** Question 1. The person who delegated (Alice) may confirm a change that her own agent proposed, by default. This is the confirmation step of ADR 125 and ADR 128. Four-eyes comes from an approval rule that counts Alice as the author: with `authorMayApprove: false` on the namespace, a second person must approve. A one-person installation keeps working.
2. **Revocation during an apply (owner, 2026-11-07).** Question 2. A phase that the person confirmed finishes, because the person saw and allowed that exact plan. No new phase starts. Every phase that was not confirmed stops at its next call. This is configurable: an installation setting holds the behavior, with the recommended value as the default. The API shows the effective value.
3. **The lifetimes of a delegation (owner, 2026-11-07).** Question 3. The default lifetime is 7 days, and 30 days at most, for a public client. It is 30 days and 90 days for Dusty and for confidential clients. This is configurable: an installation setting holds the lifetimes, with these values as the default. The API shows the effective values.
4. **Typed confirmation (owner, 2026-11-07).** Question 4. An agent can propose a destroy or a replacement of a stateful resource. For these, the person types the unit name or the count of destroys that the page shows. Every other confirmation stays one click. This is configurable. An installation setting turns the typed confirmation on or off. The default is "on". The API shows the effective value.
5. **No task scope for each run in version 1 (owner, 2026-11-07).** Question 5. A run does not narrow itself to the namespaces of the objects that the person named. The mark of untrusted input, the held requests and the page fact "a unit you did not ask about" cover the risk. This is configurable: an installation setting holds the choice, with "no narrowing" as the default and the value shown in the API. A later RFC can design the narrowing when the run data of Dusty shows how often a run crosses namespaces.
6. **The rest of the proposal (owner, 2026-11-07).** Decisions 1 to 52 of the "Proposal" of the RFC apply. Decisions 1 to 5 of this ADR replace the matching recommendations. The one-way doors of the RFC apply before the first tag.
7. **The glossary entry (owner, 2026-11-07).** The first slice fixes the GLOSSARY entry "Principal kind". A token from a trust rule gives `workload`, as ADR 240 decision 3 says.

## Consequences

- RFC 121 has the status accepted, with a pointer to this ADR.
- Step 3 of the exit test of milestone 10 in `docs/roadmap.md` changes: the change moves on only after a person confirms it. With a two-person approval rule on the namespace, the person who delegated cannot be the second approver.
- The work is the slices of the RFC, each with its own pull request and e2e step. The installation settings of decisions 2 to 5 show their effective value in the API. A change of a setting writes a record of who changed it.
