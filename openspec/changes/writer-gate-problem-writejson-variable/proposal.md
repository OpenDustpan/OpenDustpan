---
version: 1
---

Issue: #2403.

## Problem

A developer trusts a green writer gate that no adapter writes raw error text, but the gate still lets WriteJSON, variable status values, and map bodies through with no code and no log line.

## Evidence

- The generic JSON writer exists on main. Proof: internal/api/problem/problem.go:539 (func WriteJSON)
- WriteJSON has non-test callers under internal/. Proof: internal/api/objectcontext/objectcontext.go:241: problem.WriteJSON(ctx, h.logger, w, status, body)
- A variable WriteHeader site exists on main. Proof: internal/api/runnerapi/errors.go:285: w.WriteHeader(status)
- The body-type helper exists on main. Proof: internal/errcode/errcodetest/bodies.go:24 (func BodyTypes)
- No ADR text is in the pack for this item. Proof: pack adrs: [] and paths hold only internal/api/problem/problem.go, internal/api/problem/writer_gate_test.go, internal/errcode/errcodetest/bodies.go

## Fix boundary

- Narrow the generic JSON writer in internal/api/problem so error bodies pass only through the problem path with a fault-log line.
- Flag variable WriteHeader arguments and map or anonymous-struct encode calls in gated directories.
- Pin the fault-log exception list and compare by sorted keys with exact set match.

Out of scope:

- Changes to error-code catalog values.
- Changes to adapter behavior beyond routing writes through the problem path.
- Docs page: test-time gate only with no user-visible path.

## Check

- Add failing gate cases in internal/api/problem/writer_gate_test.go:156 for WriteJSON with 500 and map body, variable and converted WriteHeader, and map or anonymous-struct encode. Check: `go test ./internal/api/problem -count=1` fails on main for these cases
- Narrow the writer in internal/api/problem/problem.go:539 to the NEW WriteSuccess writer and the NEW WriteProblem writer and migrate each non-test caller such as internal/api/objectcontext/objectcontext.go:241 and internal/api/runnerapi/errors.go:285. Check: `go test ./internal/api/problem -run TestRefusedCodeLogsThroughFaultlog -count=1` passes
- Pin the exception list in internal/api/problem/writer_gate_test.go:803 by sorted keys with exact set match and add a failing swap test. Check: `go test ./internal/api/problem -count=1` fails on main for a remove-plus-add swap
- Extend BodyTypes in internal/errcode/errcodetest/bodies.go:24 for named map types and anonymous structs. Check: `go test ./internal/errcode/errcodetest -count=1` fails on main for map and anonymous-struct bodies
- Run the definition of done: narrow writers wired, no user route, no e2e step, no docs page, usability check of the gate text. Confirmed by: CI (make check with zero findings). Check: `make check` with zero findings

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main has a generic JSON writer that takes any status and any body. (`internal/api/problem/problem.go:539 (func WriteJSON)`)
- Main has a body-type helper that returns struct types with a body field. (`internal/errcode/errcodetest/bodies.go:24 (func BodyTypes)`)
- Main has a problem writer helper on the server. (`internal/api/server/server.go:552 (func problemWriter)`)
- Main has a test that refused codes log through the fault log. (`internal/api/problem/code_refused_log_test.go:18 (func TestRefusedCodeLogsThroughFaultlog)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does any ADR govern the writer gate or the error-code contract? No ADR text is in the pack (adrs: []). The error-code and 500-log contracts are code contracts on main enforced by internal/api/problem/code_refused_log_test.go:18 and internal/errcode/errcodetest/bodies.go:24.
- Does the fix need a new tool or analyzer? No. The fix stays inside internal/api/problem/problem.go, internal/api/problem/writer_gate_test.go, and internal/errcode/errcodetest/bodies.go with no new tool.
- Does narrowing WriteJSON break a tool-compatible API? No. The writer is internal to internal/api/problem. Narrowing it does not change a tool-compatible API shape.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
