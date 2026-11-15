---
version: 1
---

Issue: #3223.

Short form: bug (a flaky test that removed two pull requests from the merge queue).

## Problem

**Persona: platform engineer (Priya).** Priya cannot merge a change because a queue run fails on a test that has nothing to do with it.

The case `AProviderThatDoesNotAnswerInTimeIsErrMethodUnavailable` of `TestMethodConformance` fails now and then (merge-group run 43722524281, job "go"): `the CAS answer is not valid: decode: EOF, want identity provider is not available`.

`AnswerSlow` in `internal/castest/castest.go` waits for `slowFor` or for the end of the request context. On the end of the context, the handler returns with no write. The `net/http` server then sends status 200 and an empty body. When the client reads that answer before its own timeout, `readAnswer` in `internal/casauth/casauth.go` returns an empty body, `ParseResponse` fails with EOF and the method returns `session.ErrMethodAnswer`. A real provider that does not answer in time sends no answer. So the fake, not the method, is wrong.

## Evidence

- `rg -n "AnswerSlow" internal pkg --glob "*.go"` finds the fake (`internal/castest/castest.go`), its mapping in `internal/casauth/method_test.go:201` and nothing else.
- `internal/casauth/casauth.go` `readAnswer` maps a failed read to `session.ErrMethodUnavailable`, and a status of 200 with an empty body to the parse step, which gives `session.ErrMethodAnswer`. This is the right class for an empty answer that a provider really sent, so the method stays as it is.

## Fix boundary

Work in `internal/castest/castest.go` and `internal/casauth/hostile_test.go` only. No change to `internal/casauth/casauth.go`.

- On the end of the request context, `AnswerSlow` ends the connection with `panic(http.ErrAbortHandler)` (the `net/http` way to abort a response) and never answers. The client then gets a failed read or its own timeout, and both give `session.ErrMethodUnavailable`.
- A new answer kind `AnswerEmpty` of the fake (status 200, empty body) and a row "an empty answer" in the table of `TestFinishRefusesAHostileAnswer` in `internal/casauth/hostile_test.go` pin the other side: a CAS server that answers status 200 with an empty body gives `session.ErrMethodAnswer`. This keeps the method honest, so that the fix of the fake does not hide a change of the method.

## Check

- `go test -race -count=200 ./internal/casauth/ -run TestMethodConformance/groups/AProviderThatDoesNotAnswerInTimeIsErrMethodUnavailable` passes.
- Mutation: restore the `return` on the end of the context in `AnswerSlow`. The 200-run loop fails at least once, or the new empty-answer row shows the class of the answer.
- `make check` exits 0. Confirmed by: CI.

## ADR check

- No ADR changes the CAS method or its fake. `rg -n -i "castest|AnswerSlow" docs/adr docs/rfc` finds no decision. No owner-tagged decision conflicts.
- The change touches sign-in test code only. It changes no sign-in behavior, so no security semantics change. A security review (rev2) still reads the pull request, because the package is sign-in code.

## Open questions

none. Considered: (1) change the method to map a timeout during the answer to unavailable (the lead proposed it): not needed, because the client already maps its own timeout and a failed read to unavailable, and an empty 200 from a provider is an answer that is not valid. (2) a wait loop instead of `ErrAbortHandler`: the abort is the standard way and ends the connection at once.
