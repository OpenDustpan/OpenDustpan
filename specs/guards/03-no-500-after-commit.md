# Guard 03: no failure answer after the change is committed

Status: ready for implementation. Open questions: none.

Depends on spec 01 task 1 (the route exemplars and `internal/knownlist`).

## Problem

When a request has committed its change, the answer must say that the
change is stored. The class is a handler that commits the change and then
does more work that can fail (a read for the response body, a flag, a
second lookup) and turns that failure into a 5xx. The client sees a failure
for a stored change. A retry then gets 409, and the audit log can hold a
success entry and a failure entry for one change.

Real examples:

1. Issue #2925. `createGroup` and `changeGroup`
   (`internal/api/server/principals.go:905-935` and `:980-1010`) call
   `protectedGroupOf` after `svc.CreateGroup` or the rename has committed. A
   failed read of the protected flag answers 500 through `failed(...)`, and
   `servePrincipalChange` writes a second audit entry with the outcome
   `failure`. Open PR #2960 fixes these two handlers.
2. Issue #2961. `writePrincipalResult`
   (`internal/api/server/principals.go:450-465`) calls `markTempAdmin` after
   the write of each principal change, and a failed read answers 500 for a
   stored change. It was found in the review of the fix of example 1, which
   shows that a fix of one handler does not find the next one.

Each fix so far was one handler. No check finds the next one.

## The check

A post-commit fault sweep in `pkg/dustpan`. It runs in
`go test -race ./...`.

### The commit observer

Both SQL stores get one option, `WithCommitObserver(func(ctx
context.Context, kind CommitKind))`, in `pkg/store/sqlite` and
`pkg/store/postgres`. The store calls it after each successful commit of a
write transaction, with the context of the call. `CommitKind` is `audit`
for the transaction of an audit append and `change` for every other write.
Production code does not set it. It is an observation hook, like the
existing `postgres.WithQueryTracer`.

Every commit in a store goes through one helper of that store, which calls
the observer. A test in each store package reads the Go files of the
package (`go/parser`, not test files, not `internal/db`) and fails for a
call of `Commit` or of `pgx.BeginTxFunc` / `pgx.BeginFunc` outside that
helper. Today there are 14 commit sites in `pkg/store/sqlite` and 10 in
`pkg/store/postgres` (3 `Commit` calls and 7 `BeginTxFunc` calls), so a new
transaction cannot skip the observer.

### What it enumerates

Every row of `srv.RouteDeclarations()` with the method POST, PUT, PATCH or
DELETE that has an exemplar of spec 01 with a body and a success status.
Today there are 209 such rows. 183 have the signed-in scope.

### What it asserts

For each row, on a fresh seeded world for each run:

1. **Run 1 (count).** Send the exemplar as an administrator. Count the
   commits of kind `change` whose context is the request context. Call the
   count K. If K is 0, the row commits nothing, and the sweep goes to the
   next row.
2. **Run 2 (fault).** Send the exemplar again on a fresh world. A test
   middleware gives the request a context with `context.WithCancelCause`.
   The observer cancels that context, with the cause
   `errPostCommitFault`, right after commit K of kind `change`. Every later
   store call of the request then fails, as a database that stops answering
   after the commit does.
3. **Assert.** The status of run 2 equals the success status of run 1. The
   audit log holds no entry with the request ID of run 2 and the outcome
   `failure`. The body may differ: a handler can answer a degraded body,
   for example a flag set in the safe direction.

The failure names the pattern, the status, and the store call that failed
first after the commit (the observer records it).

### Audit appends during the fault

The audit append of a committed change must not fail with the request. The
refusal Recorder already writes on a context that the client cannot end
(`writeTimeout` in `internal/api/refusal/refusal.go`). Task 1 adds one
helper for the success entry of each adapter that does the same:
`context.WithoutCancel` plus the same time bound. A success append that
still uses the request context fails the sweep, and its row goes on the
known list.

## Allowed exceptions

`pkg/dustpan/testdata/guards/post-commit-exceptions.txt`. One line for each
exception: the pattern, a tab, the reason. The reason says why the client
must see a failure for a stored change. The test refuses a line with an
empty reason, a pattern that no server serves, and a line that the sweep
does not need. No inline marker. The owner reviews each change of the file.

Expected entries: none. A route whose answer is the content that it read
after the commit (for example a stream) reads that content in the
transaction or answers from the data in hand.

## Acceptance criteria

1. The store test fails for a planted `tx.Commit` outside the helper, and
   for a planted `pgx.BeginTxFunc`, in a copy of a source file in
   `testdata`.
2. The observer test in each store package proves that the observer runs
   once for each committed write, with the kind `change` or `audit`, and
   never for a rolled-back transaction.
3. A planted offender fails the sweep: a small handler that commits a row,
   then reads it back with the request context and answers 500 on a failed
   read. The same handler passes when it answers from the data in hand.
4. A planted offender that writes a failure audit entry after the commit
   fails the sweep on the audit assertion, even when its status is 2xx.
5. A row whose request commits nothing is skipped and named in the test
   log, not failed.
6. The known lists only shrink, through `internal/knownlist`.
7. `make check` passes. The sweep runs its rows in parallel subtests, and
   its run time is in the pull request description.

## Existing offenders

Found by reading the code (the sweep gives the exact list on its first
run):

- Confirmed: 2 rows. `POST /api/server/groups` and the group change route
  (issue #2925). If PR #2960 merges first, they pass.
- Candidates: 15 rows of the installation API whose handler is
  `servePrincipalChange`, `serveTokenChange` or `serveFreshAccessChange`,
  and the 2 rows of `serveNamespacePrincipalChange`. All of them answer
  through `writePrincipalResult`, which reads the temporary-administrator
  mark after the commit (issue #2961).
- Rows with no exemplar yet: every mutating row that spec 01 lists on
  `no-exemplar.txt`. The sweep covers them as spec 01 task 2 adds rows.

Plan: the first run writes its findings to
`pkg/dustpan/testdata/guards/post-commit-known.txt` with its limit. The
list only shrinks. Each fix removes its line and lowers the limit.

## Tasks

1. **The observer and its static check.** Add `WithCommitObserver` and
   `CommitKind` to both stores, the one commit helper of each store, the
   static test, and the observer conformance rows. Acceptance: criteria 1
   and 2. Size: about 400 lines. Conflict area: the transaction helpers of
   `pkg/store/sqlite/sqlite.go` and `pkg/store/postgres/postgres.go`, and
   each file with a commit site (24 sites).
2. **The sweep and the detached success append.** Add the middleware, the
   sweep, the planted offenders, the shared success-append helper, and the
   known list from the first run. Acceptance: criteria 3 to 7. Size: about
   600 lines. Conflict area: `pkg/dustpan` tests (new files) and the audit
   write of each adapter wrapper (one call each).
3. **Fix each known row.** One pull request for each handler family: read
   in the transaction, or answer from the data in hand, and fail closed for
   a flag (for example `protected: true`), with a warning log line that
   names the object ID and the error type only. Acceptance: the line is
   gone and the limit is lower; a handler test with a failing read after
   the commit answers success and the audit log holds one entry. Size: 50
   to 200 lines each. Conflict area: the adapter of the handler.

## Constraints

- CLAUDE.md "No magic": each action writes a true record, and the answer
  matches the stored state.
- ADR 60 decision 3: a failed request writes an entry. A request whose
  change committed is not a failed request, so it writes a success entry
  only.
- ADR 41 decision 5 (a plain secret read fails when its audit write fails)
  stays as it is. The sweep covers mutating routes, and the detached append
  does not change what happens when the audit store itself fails.
- ADR 40: fault injection is a test tool. The observer is an option that
  production code does not set, and the sweep lives in test files only.
- ADR 272: `WithCommitObserver` is a new option of two public SDK
  packages. It is additive.
- ADR 250: a flag that the server cannot read after a commit is set in the
  safe direction (fail closed) in the answer.
- CLAUDE.md "Tests": `t.Context()`, `t.Parallel()`, no wall-clock timing
  (the fault is a cancel after a commit, not a timer).

## Decisions taken in this spec (industry default)

- **The fault fires after the last change commit, not after each one.**
  After the last commit the change is fully stored, so any failure answer is
  the class. A fault after an earlier commit tests atomicity, which is
  another class.
- **The fault is a cancel of the request context.** It needs no driver
  wrapper, and it reaches every store call of the request on both
  dialects. The audit append runs on its own bounded context, as the
  refusal Recorder does today.
- **The body is not compared.** A degraded body in the safe direction is
  the fix that issue #2925 asks for.
- **The sweep runs on SQLite.** The class is in the handler, not in the
  dialect. The observer lands on both stores so that the PostgreSQL suites
  can use it later.

## Open questions

None.
