# 359. Review requests: mirrored approvals, checks, comment commands and apply timing

Status: accepted · 2026-11-07. Records decisions 27.2, 27.3, 27.4 and 27.7 of the project owner of 18 October 2026.

Changes:

- Extends ADR 299 decision 1: the mirror of approvals between the Git host and the change order.
- Extends ADR 299 decision 15: the apply timing decides if an apply can run before the merge.
- Extends ADR 299 decision 25: the checks and the table in the plan comment.

## Context

[RFC 27](../rfc/0027-pull-request-automation.md) asks how a review request shows plans, takes approvals and starts work. ADR 299 records the review modes and the plan comment that the server edits in place (decision 25). ADR 271 decision 7 and ADR 300 decision 9 record the comment commands. On 18 October 2026 the owner answered four more questions of the RFC. No ADR records these answers. This ADR records them and nothing else.

## Decision

1. **Mirrored approvals (owner, 2026-10-18).** When the forge integration uses OAuth, the server mirrors approvals in both directions between the review request and the change order. An approval on the forge shows on the change order. An approval on the change order shows on the forge.
2. **No OAuth: read-only change order (owner, 2026-10-18).** Without OAuth, the change-order page is read-only. This holds for units that come from a VCS.
3. **Checks (owner, 2026-10-18).** A review request gets one required summary check with a fixed name. It also gets one check for each unit. The unit checks are not required.
4. **The table in the plan comment (owner, 2026-10-18).** The server edits one comment in place (ADR 299 decision 25). It has a table with one row for each unit. A row shows the status, the `+ ~ -` counts and a link to the change order.
5. **Comment commands follow Atlantis for apply before merge (owner, 2026-10-18).** For teams that apply before the merge, the comment commands follow the semantics of Atlantis.
6. **Merge first: commands that do not apply (owner, 2026-10-18).** Some repositories merge first and apply after. There, the server accepts only commands that do not apply. `plan` and similar commands are in this group.
7. **No state commands (owner, 2026-10-18).** A review request does not accept state commands.
8. **Imports need research first (owner, 2026-10-18).** An import from a comment needs deep research first.
9. **Apply timing at each level (owner, 2026-10-18).** The namespace and the collection each set a default for the apply timing. Each also allows or forbids an override at the levels below it, down to the unit. So an apply before the merge is possible.
10. **Apply after the merge is the recommended default (owner, 2026-10-18).** An apply after the merge is the strong recommendation and the recommended default. The project researches this flow and writes it in the docs.

## Not decided here

- The text of the summary check name, and which plan states make it fail or pass.
- The check state for a unit that waits for an approval.
- How the table of decision 4 shows resource addresses, which ADR 299 decision 25 puts in the summary by default.
- The names of the apply-timing values and of the override flag, and the order of resolution between the levels.
- The list of commands, the meaning of "similar commands" in decision 6, and which part of the semantics of Atlantis applies.
- How a commenter maps to a principal, and which authorization actions a command needs.
- How a review on the forge meets the fresh-authentication window, and how the identity link works.
- How decision 1 relates to the four review modes of ADR 299 decision 1.
- The message that a refused command or a refused write shows.

## Consequences

- The apply timing of a unit decides its command mode. Decisions 5 and 6 give the two modes, and decision 9 sets the timing at each level.
- An approval from the forge still passes the approval rules and the approval floor (ADR 299 decision 10) on the OpenDustpan side.
- A comment command runs as the principal that a person linked to the forge account (ADR 271 decision 7). A comment never passes a gate.
- The API shows the effective apply timing and its source, as ADR 299 decision 4 requires for the review mode.
- Each feature has a REST API and a page in the user documentation (ADR 214), and each endpoint has an e2e step.
- Each part comes in its own pull request. An ADR settles the details in "Not decided here" first.
