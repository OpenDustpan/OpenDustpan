# Architecture review 10

Range: `842af92..519b3bd` on `main`, 65 commits, trains 46 to 54. Reviewed
on 2026-11-04. All file and line references are at `519b3bd`.

Issues filed: 7 major (#2399 to #2405) and one issue with 22 minor and info
findings (#2406). No critical finding.

## Summary

| Severity | Issue | Finding |
| --- | --- | --- |
| Major | #2405 | The `pkg/dustpan` area files take the whole `*settings` and write build outputs into it. This differs from ADR 326 decision 5. |
| Major | #2400 | No CI job or make target runs the release-record check, so a tag can ship with the release guards off (ADR 325). |
| Major | #2401 | Four paths of a restore that ends before the swap write no end entry. The log lines drop the cause. |
| Major | #2404 | 13 hidden-character checks refuse different sets after the shared set landed. |
| Major | #2403 | The writer gate still lets `problem.WriteJSON`, a variable status and map bodies through. |
| Major | #2402 | A TFE registry Authorizer error answers 500 with no faultlog line. |
| Major | #2399 | The sshsig ECDSA point encoder does not refuse a key on another curve. Its comment says that it does. |
| Minor | #2406 | 22 findings: release record, dev reset, faultlog, routes and e2e, area files, swarm wave 4, tooling. |

The finding that matters most is #2405. ADR 326 asks for four more moves
of `dustpan.go` areas. Each move copies the pattern of the first four. If
the pattern stays, the hidden order that ADR 326 refused spreads into about
25 files.

## The area-file pattern in pkg/dustpan (ADR 326 decision 5)

Four areas moved: `area_backup.go` (8ee419ee), `area_installation.go`
(a8c9f635), `area_signin.go` (6ca046d6) and `area_profile.go` (d2b7fc97,
partly reverted by 519b3bd). About 700 lines left `dustpan.go`, which now
has 3820 lines.

Does it reduce conflicts? Yes, a small amount: a change to one of these
areas no longer edits `dustpan.go`. The measurement of ADR 326 (4 of 200
pull requests) is too small to show a change after one train. Measure again
with the `queries.sql` count of decision 4.

Does it hide coupling? Yes. Facts:

- Each area constructor takes `cfg *settings`, so it can read any field of
  any other area. `area_installation.go` reads `cfg.authenticator`,
  `cfg.authorizer`, `cfg.auditShipper` and `cfg.denialLimiter`.
  `area_backup.go` reads `cfg.secretWrapper` and `cfg.operationLease`.
  The signature does not show this.
- `gatedAuthenticator` (`area_signin.go:65,76,87`) writes three gates into
  `cfg.signInGates`. `setSignInLimiters` (`signin.go:420-428`) reads them
  later. A nil limiter means "no failure limit"
  (`internal/api/server/signin.go:127-128`). This is the "shared struct
  that the loop fills" that ADR 326 refused.
- NewServer rewrites `cfg.authenticator` four times and `cfg.authorizer`
  in place (`dustpan.go:885-954`). No test checks this order in NewServer.
  `TestSignInRoutesUseTheirOwnGate` runs `wireTokens` in its own fixture.
- The profile area holds three APIs (profiles, issuer, sinks). 519b3bd moved
  `newProfileAPIs` back two hours after d2b7fc97 moved it out.

Recommendation: before the next move, area constructors take explicit
arguments, and build outputs are return values, not settings fields.
`mountAPI` stayed as it is, which is correct: the security wrappers are
still in one function.

## Topics

### sshsig ECDSA layers (189cca26, dd56bccf, 1c856806)

There is now one point encoder, so the duplicated encoders can no longer
differ. The parser refuses compressed points, the point at infinity, points
off the curve and wrong lengths. Each refusal has a layer test. `checkECDSA`
and `verifyECDSA` check each field and each range.

Finding #2399: `ecdsaPoint` calls `pub.Bytes()`, which also encodes P-224,
P-384 and P-521 points. `Marshal` and `verifyECDSA` rely on it alone. A
review probe confirmed that a P-384 key passes. The gap cannot be reached
today, because `check()` runs first.

### Pulumi state.write gates (76ad8cb6, f8c232b4, bed01bdf, 064f23aa, 0aca605e, c37502ae)

No defect found. Import, restore, cancel, delete and rename are gated
before the store call. `TestEveryWriteRouteMeetsTheGates` fails on a new
write route that is not gated or listed. The update and checkpoint routes
are listed as `pulumi.update` in `UncheckedPaths`, and the API shows them.
All adapters call the one `stategate.Admitter`, which fails closed on each
unknown value and audits each refusal. The Pulumi adapter does not import
the TFE adapter. `pkg/gate` did not change. Minor item 20 of #2406 is about
cancel and `unit:write-forced`.

### The release record (ADR 325: a70ab56f, 923f991a, cf78a38d, 7b1505b5, 939bbc8c)

The Go layer matches ADR 325 decisions 3 to 7. A bad record fails each
guard, and an empty tag directory fails the frozen check. Finding #2400: no
automation runs the record check at a tag. Minor items 1 to 3 of #2406:
a commented line, skips of the PostgreSQL round trip, and the parsers in
shell and in Go.

### Restore audit entries (2942ef7a, abe406cc, 56482972, 41577452)

The tests cover the main ends (finished, canceled before and in the swap
window, failed before and after the swap, takeover). The entries stay
tool-neutral. Finding #2401 lists the paths that write no end entry. It is
separate from #2351, which is about a crash.

### faultlog and the 500 log line (16452f10, 3d1801ee, b3b9e0e1, c5c5d51e, 1339afbc, 5c757596, e9528a86)

Good: the cause class is a closed list and never holds error text. The
server mints the request ID and does not take it from the client. faultlog
checks the request ID and the route again. faultlog imports no tool code.
The ungated row of c5c5d51e is justified, because faultlog writes no answer.
Findings: #2402 (one 500 path with no log line), #2403 (gate bypasses),
minor items 6 to 9 of #2406.

### ADR 326 and RFC 120 steps 2, 3 and 5

- Step 2 (1d80c4b6): a pure move. `ServedRoutes` and `CoverageAllowlist`
  stay as plain call lists, with no `init()` registration. Each area file
  has `//go:build e2e`. Pre-existing gaps were kept (minor items 10 and 11).
- Step 3 (db5a94d8, 526eccf0, 036d9908): only test code composes the
  adapters. The per-adapter route tests stay, as decision 3 says. Minor
  items 12 and 13.
- Step 5: see the area-file section above.

### Swarm wave 4

- dev reset and devdir: the paths are fixed constants. The old copies stay
  until the remake succeeds, and the owner check fails closed. Minor items
  4 and 5 (the `Lstat` branch, the half-done Windows removal).
- principaltest: a design-only suite refuses a fixture. The guard has
  direct tests. Minor item 18 (comment wording).
- Hidden-character checks: #2404.
- Registry publish: `refuseOtherContent` runs before promote, and the store
  check stays as a second layer. Minor item 17.
- Artifact sweep (7ef9293e): reading one row past the page makes `Capped`
  correct. #2220 stays open for jobs that one run does not reach.
- Stack constants (cbb1100f): in `pkg/stack` only. Minor item 16.
- stategate and writergate tests: see the topics above.
- CauseEOF (1339afbc): correct order (unexpected EOF before EOF), tested.
- Final version reader (5d520f65): minor item 15.
- cfead332: the order of the checks and the lock behavior stay the same.

### internal/testchild (c6a42eed)

It runs the test binary again with an argument list and no shell. It checks
the name and the value of each extra variable and never prints the
environment. It has `leak_test.go`. blobtest, durabletest and principaltest
use it. Its doc says it returns `*exec.ExitError`, but it returns that
error wrapped. `errors.As` still works, and a test checks it.

### scripts/declhash (2fad9c29) and the TruffleHog exclude (2389b7df)

Minor items 21 and 22. The exclude is anchored to one file. That file is
conformance-suite source, not a `_test.go` file.

## Rule checks

- Shared core: no tool-specific field was added to a shared package in this
  range. No adapter calls another adapter.
- Layering: no domain package gained a `net/http`, driver or store import.
  `pkg/job` now imports `pkg/audit` only for a character set (#2404).
- Conformance suites: the restore audit appender is an interface assertion
  with no suite (#2401). The job store final version reader has jobtest
  cases.
- No magic: #2401, #2405 and minor item 15 name a silent path or a hidden
  order.
- Error codes: the catalog is used at each new site. #2403 lists the ways
  around the gate.

## Method

Four parallel read-only review passes (sshsig and Pulumi gates; release
record, restore, dev reset and tooling; faultlog and the writer gate;
RFC 120 and swarm wave 4) and one direct pass on `pkg/dustpan`. Each major
finding was checked again against the code before it was filed. One probe
ran on a copy of `internal/sshsig` in a scratch directory. `GOOS=windows go
build ./cmd/opendustpan` was run to confirm minor item 5. No code was
changed.
