# Guard 02: every refusal writes an audit entry

Status: ready for implementation. Open questions: none.

Depends on spec 01 task 1 (the route exemplars and `internal/knownlist`).

## Problem

CLAUDE.md, security code step 5: "On an error or an unknown value, deny.
Write the denial to the audit log." ADR 60 decision 3: "A denied request and
a failed request also write an entry." The class is a refusal that the
client sees and that the audit log does not hold.

The route wrapper of each adapter records the denials of the Authorizer
through `internal/api/refusal` (`Recorder.Authorize`), and
`TestDeniedRequestsAreAudited` (`pkg/dustpan/denial_audit_test.go`) checks
four adapters with one route each. A refusal that a service decides after
the route check has no shared path and no check.

Real examples:

1. Issue #2927 (fixed by #2955). The registry refused a publish for an
   integrity failure (`ErrIntegrity`: the staged bytes changed between the
   check and the copy) and for a namespace with no signing key
   (`ErrNoKeys`). `internal/registry/service/refuse.go` (`refusalReason`)
   had no reason class for either, and `publish.go` and `module.go` did not
   pass the `move` error through `refusal()`. The audit log held no entry
   for the attempt that most needs one.
2. Issue #2925, second half. A create of a group that failed after the
   commit wrote a success entry and then a failure entry for the same group
   (`internal/api/server/principals.go`, `servePrincipalChange`). The audit
   log did not match the answer. Spec 03 covers the answer; this spec
   covers the rule that the audit record of a request matches its answer.
3. Issue #1685 (closed). The audit entry of a refused job start did not
   name the refused action.

## Definitions

- **Refusal code.** A code of the catalog (`internal/errcode`) whose class
  is `refusal`: the server decided not to do what an identified caller
  asked, for a reason of access, policy or trust. Examples: a missing
  permission, a gate or policy decision, an integrity, signature or trust
  failure, a missing fresh sign-in or step-up, an IP allowlist, an
  escalation, a lockout. These are not refusals: a request that is not
  valid, an object that does not exist, a conflict of state, a size or
  count limit, a store that does not answer.
- **Refusal answer.** An answer with the status 403, or an answer whose
  error code is a refusal code, whatever its status.
- **Recorded.** The audit log holds an entry with the request ID of the
  answer (`X-Request-Id`, field `requestID` of the audit API) and the
  outcome `denied`. A refusal that the denial limiter holds back counts as
  recorded only through the limiter (see "The check", part 2).

## The check

Three parts. All three run in `go test -race ./...`.

### Part 1: every code declares its class

`errcode.Spec` gets a field `Class` of a new type with three values:
`ClassUnset` (the zero value), `ClassRefusal`, `ClassError`. A test in
`internal/errcode` fails for every code of `Catalog()` with `ClassUnset`.
So each of the 331 codes is classified once, in task 1, and a new code
cannot be added without a class.

### Part 2: the route sweep

In `pkg/dustpan`, next to the probe of spec 01. For each non-public row of
`srv.RouteDeclarations()` that has an exemplar, it sends request A of spec
01 (every wildcard stored, the caller holds no grant). Each row gets its own
caller, made for the row, so the denial limiter, whose key holds the
principal, never holds back the first refusal of a row. The sweep fails a
row when the answer is a refusal answer and the audit log holds no entry
with its request ID and the outcome `denied`. Today that is 379 rows, of
which the exemplars of spec 01 cover the rows with no wildcard (79) and the
179 rows with wildcard defaults on day one.

### Part 3: the refusal drill

`pkg/dustpan/refusal_drill_test.go` holds one row for each refusal code: a
short request sequence on the seeded world of spec 01 that ends in an answer
with that code, for example "stage a release, change the staged bytes, call
publish" for an integrity refusal. The drill sends the sequence and fails
when the last answer does not carry the code, or when it is not recorded.

A test compares the drill with the catalog: it fails for each refusal code
that has no drill row and is not on the known list, and for a drill row of a
code that is not a refusal code. So a new refusal code needs a drill row in
the pull request that adds it.

Parts 2 and 3 read the audit entries through the audit API with the
administrator token, as `deniedEntries` does today, and match on
`requestID`.

## Allowed exceptions

`pkg/dustpan/testdata/guards/refusal-exceptions.txt`. One line for each
exception: the code or the pattern, a tab, the reason. The test refuses a
line with an empty reason, a code that is not a refusal code, and a line that
the check does not need. No inline marker. The owner reviews each change of
the file (`.github/CODEOWNERS`).

Expected entries: none. A refusal of a request with no credential (401) is
outside this guard (see "Decisions").

## Acceptance criteria

1. The class test fails for a planted code with `ClassUnset` (a mutation
   row that adds a spec to a copy of the catalog).
2. The route sweep fails on a planted offender: a small handler with one
   route that answers 403 with the code `access.denied` and writes no entry.
   It passes when the handler records the refusal through
   `refusal.Recorder.Record`.
3. The drill fails on a planted offender: a drill row run against a handler
   that answers the code with no entry. It passes when the entry exists.
4. The drill coverage test fails for a planted refusal code with no drill
   row, and for a drill row of an error-class code.
5. A refusal answer recorded with the wrong outcome (`failure` in place of
   `denied`) fails parts 2 and 3.
6. The known lists only shrink, through `internal/knownlist`.
7. `make check` passes.

## Existing offenders

- Part 1: all 331 codes have no class today. Task 1 classifies all of them
  in one change, so part 1 has no known list. A first pass by the meaning
  text finds 25 codes that read as refusals (for example `access.denied`,
  `access.ip-not-allowed`, `session.csrf`, `sign-in.refused`,
  `gate.denied`, `job.policy-denied`, `password.blocked`,
  `registry.signature-untrusted-key`, `registry.no-keys`,
  `state-version.upload-denied`, `trigger.saver-denied`,
  `workspace.override-refused`). The estimate is 35 to 45 refusal codes
  after the review of each code.
- Part 2: no confirmed offender. Every adapter checks its routes through
  the guard that the Recorder serves (each adapter under `internal/api`
  calls it). The first run gives the exact list.
- Part 3: every refusal code starts with no drill row. Task 1 adds the rows
  for the codes that the existing tests already drive through a full
  server (the route-level denials, the registry refusals of #2955, the
  sign-in refusals). The rest go on
  `pkg/dustpan/testdata/guards/refusal-no-drill.txt` with its limit. A
  drill row that finds a refusal with no entry puts the code on
  `pkg/dustpan/testdata/guards/refusal-unrecorded.txt`. Both lists only
  shrink.

## Tasks

1. **Classes, the sweep and the drill harness.** Add `Class` to
   `errcode.Spec` and classify all 331 codes. Add part 2, part 3 with its
   first rows, the planted offenders, and the two known lists. Acceptance:
   criteria 1 to 7. Size: about 900 lines (330 one-line class additions,
   the rest tests). Conflict area: every `internal/errcode/catalog_*.go`
   file (one field on each spec), so it lands as one change while no other
   change adds codes, and a rebase only adds the field to new specs.
2. **Drill rows, one area at a time.** Add the drill rows of one catalog
   area for each pull request. A code whose drill finds no entry goes on
   `refusal-unrecorded.txt`. Acceptance: the limit of
   `refusal-no-drill.txt` goes down by the rows added. Size: 100 to 300
   lines each. Conflict area: `refusal_drill_test.go` (split it into
   `refusal_drill_<area>_test.go` files in task 1, so that areas do not
   conflict).
3. **Fix each unrecorded refusal.** The service passes the refusal through
   the shared Recorder (`refusal.Recorder.Record`) or writes its own entry
   with the outcome `denied`, in the request. Acceptance: its line is gone
   from `refusal-unrecorded.txt` and the limit is lower. Size: 30 to 150
   lines each. Conflict area: the service of the code.

## Constraints

- ADR 60 decision 3 and CLAUDE.md security step 5: a denied request writes
  an entry. This spec checks the rule. It does not change it.
- ADR 182 and ADR 276: a code never changes its meaning or its arguments.
  The class is a new field on the spec. It changes neither.
- ADR 250: a fix that records a refusal at the service keeps the record at
  the route wrapper too, and each layer has its own test.
- The denial limiter stays (`internal/denylimit`). The sweep makes one
  caller for each row, so the limiter never hides a first refusal from the
  check.
- CLAUDE.md "No magic": each automatic refusal writes a record of who and
  why. The drill proves this for each refusal code.
- ADR 66: no issue numbers in the code or the drill names.
- ADR 14: the `Meaning` of a code and the reason of an entry stay in strict
  Simplified Technical English.

## Decisions taken in this spec (industry default)

- **A request with no credential (401) is out of scope.** It has no
  principal to record, and the sign-in failure windows of the
  authentication layer count these attempts. Adding them would change the
  scope of ADR 60, which this spec does not do.
- **A refusal is a class on the code, not a status.** A refusal such as an
  integrity failure can answer 409 or 422. The class covers it whatever its
  status. Every 403 counts too, so a new 403 path with a code of class
  error still fails part 2.
- **The check reads the audit API, not the store.** This is what an
  operator sees, and it proves the entry reached the namespace scope.

## Open questions

None.
