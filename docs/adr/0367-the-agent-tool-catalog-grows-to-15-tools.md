# 367. The agent tool catalog grows to 15 tools

Status: accepted · 2026-11-14. Decided by the project owner

Changes:

- Changes ADR 122 decision 14: the catalog can have more than 12 tools, for the three tools of the comment and propose change. The contract rules stay.
- Changes ADR 122 decision 1: the test keeps the catalog at 8 to 15 tools.
- Changes ADR 120: the range "about 8 to 12" tools of the owner's list in the context becomes 8 to 15 tools.

## Context

ADR 122 decision 14 says: "A change that makes the catalog larger than 12
tools needs a new ADR." ADR 122 decision 1 says: "A test keeps the catalog at
8 to 12 tools". The test is `TestCatalogSize` in
`internal/agent/tools_test.go`. The context of ADR 120 records the owner's
range: "About 8 to 12 task-shaped tools, not a passthrough of the raw API."
ADR 122 has the "Changed by" items of ADR 128 (decision 1, the row
`cancel_change`) and ADR 210 (the route paths and the `server:` action names).
Neither changes decision 14.

The catalog has 12 tools today. The OpenSpec change
`give-dusty-tools-comment-propose` adds three tools: `comment_job`,
`comment_change_order` and `propose_change_order`. ADR 128 decision 16 says
that a 13th tool needs an ADR. ADR 344 lets an agent draft a change order and
leaves the tool of ADR 122 to a later ADR.

The project manager gave the owner three options on 14 November 2026: a new
ADR that lets the catalog grow, merging tools so that the catalog stays at
12, or a different shape such as optional tool sets. The recommendation was to
let the catalog grow now and to measure before any optimization. The owner
said:

```text
"this isn't sprawl yet and I don't want to gatekeep it too early before
we've actually gotten there and had a chance to benchmark the thing. I would
much rather proceed as recommended and optimize it later."
```

## Decision

1. **The catalog can grow past 12 tools (owner, 2026-11-14).** The three
   tools of the change `give-dusty-tools-comment-propose` join the catalog
   without a further ADR. The catalog then has 15 tools.
2. **The test moves to the new count (owner, 2026-11-14).** The test of ADR
   122 decision 1 keeps the catalog at 8 to 15 tools. A change that makes the
   catalog larger than 15 tools needs a new ADR.
3. **The size is checked again after the benchmark (owner, 2026-11-14).**
   After the Dusty benchmark (the change
   `benchmark-dusty-runs-wait-approval`), the project checks the size of the
   catalog again. An optimization, for example a merge or a removal of tools,
   is a later decision.
4. **The contract rules stay (owner, 2026-11-14).** The other rules of ADR
   122 decision 14 do not change. A tool or a required parameter is never
   renamed or deleted without a deprecation. A new tool, an optional
   parameter or a new enum value can come at any time.

## Consequences

- `TestCatalogSize` changes its maximum from 12 to 15 in the pull request
  that adds the three tools.
- The pull request that adds the three tools also needs the decisions that
  ADR 344 leaves open: the state of a draft, the action name and the audit
  entry.
- A later merge or removal of a tool that a release shipped needs a
  deprecation (decision 4). Before the first release, no tool shipped, so a
  merge is not a deprecation.
