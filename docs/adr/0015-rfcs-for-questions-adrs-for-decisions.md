# 15. RFCs for open questions, ADRs for decisions

Status: accepted · 2026-10-10

## Context

ADR 1 records decisions in `docs/adr/`. ADRs 9, 10 and 11 had the status
"proposed" for days, while code on `main` already followed parts of them.
Readers could not tell an open proposal from a decision. There was also no
place to write down early thinking, such as how to structure what sits under
a namespace, without it looking like a decision.

## Decision

- Open questions go in `docs/rfc/` as RFCs. An RFC can be rough. Its front
  matter and its first lines give its state: `draft`, `discussing`,
  `accepted` or `dropped`. `docs/rfc/README.md` gives the process and a
  template.
- `docs/adr/` holds only accepted decisions. An ADR has no "proposed" status.
- When the project owner accepts an RFC, a short ADR with the next ADR number
  records the decision and links the RFC. The RFC then gets `status:
  accepted` and the ADR number, and does not change after that.
- A dropped RFC stays in `docs/rfc/` with its reason, so that the idea does
  not come back without its history.
- Proposed ADRs 9, 10 and 11 become RFCs 1, 2 and 3. ADR numbers 9 to 11
  stay unused. ADRs 12 and 13 cite the old numbers. ADR 1 forbids edits to
  them, so `docs/rfc/README.md` maps the old numbers to the RFCs.
- `make docs-lint` does not check RFCs, so that writing one costs little.

## Consequences

- An agent writes an RFC for an open question and an ADR only after the
  project owner accepts a decision. `CLAUDE.md` says so.
- ADR 12 asks for an ADR for each new tool. Under this ADR, a new tool starts
  as an RFC, and its ADR comes when the RFC is accepted.
- Code can still run ahead of a decision, as the `workspace` package does for
  RFC 1. Such code cites the RFC, not an ADR, so readers see that it is not
  decided.
