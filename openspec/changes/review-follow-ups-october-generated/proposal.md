---
version: 1
---

Issue: #2710.

## Problem

The October review left seven non-blocking gaps in generated clients and the conformance harness: weak 412 checks, undocumented toolchain condition, hop-by-hop header leaks, unsafe child cleanup, seed conflicts on rerun, and unclaimed decode names.

## Evidence

- The conformance state case file exists on main. Proof: internal/clientgen/conformance/cases_state.go
- The proxy, process, fixture, and generator files exist on main. Proof: internal/clientgen/conformance/proxy.go, internal/clientgen/conformance/process.go, internal/clientgen/conformance/fixtures.go, internal/clientgen/golang/golang.go
- The TypeScript generator files exist on main. Proof: internal/clientgen/typescript/views.go, internal/clientgen/typescript/typescript.go
- Issue 2710 lists seven review items from merged PRs 1367 and 1515 to check against main before change. Proof: Issue #2710 body items 1 through 7

## Fix boundary

- Validate error code and message in staleIfMatchGives412 alongside HTTP 412.
- Document toolchain condition for byte-identical Go output.
- Strip Connection-nominated headers when proxying requests and responses.
- Close terminates descendants on shutdown; Linux harness-death cleanup is a no-op elsewhere.
- Reuse existing valid seed profile on conflict only when content matches and ETag reads back; unrelated errors still fail.
- Claim decode identifiers in TypeScript shared name registry with invalid-name error before rendering.

Out of scope:

- Tool-neutral REST API behavior and route contracts.
- Console, CLI, and docs beyond the Go package note on formatting toolchain.
- Work of other October review changes.

## Check

- In internal/clientgen/conformance/cases_state.go add the NEW test TestStaleIfMatchChecksCodeAndMessage that 412 carries the documented errorCode and a non-empty message. Check: The NEW test `TestStaleIfMatchChecksCodeAndMessage` in internal/clientgen/conformance/cases_state.go fails on main: it covers missing errorCode, wrong errorCode, and empty message.
- In internal/clientgen/conformance/proxy.go add the NEW test TestProxyStripsConnectionNominated for Connection-nominated header filtering both directions. Check: The NEW test `TestProxyStripsConnectionNominated` in internal/clientgen/conformance/proxy.go fails on main: it covers mixed-case, multiple, and comma-separated Connection values both directions, close and keep-alive tokens, empty tokens, fixed exclusions, and survival of unrelated end-to-end headers.
- In internal/clientgen/conformance/process.go add the NEW test TestDescendantCleanupOnClose that Close terminates a child with detached stdio while keeping reused process-group ID protection. Check: The NEW test `TestDescendantCleanupOnClose` in internal/clientgen/conformance/process.go fails on main: a child with detached stdio survives Close before the fix, plus SIGTERM-ignore escalation after 5 seconds, already-exited child, and PGID reuse cases.
- In internal/clientgen/conformance/process_unix.go add the NEW Linux-only test TestHarnessDeathCleansAdapterAndChild for harness-death cleanup. Check: The NEW test `TestHarnessDeathCleansAdapterAndChild` in internal/clientgen/conformance/process_unix.go fails on main on Linux: it kills the harness while adapter and child ignore stdin EOF and checks neither survives, plus thread lifecycle does not kill a live-harness adapter.
- In internal/clientgen/conformance/fixtures.go add the NEW test TestSeedSetupReusesValidProfile running the harness twice, plus cases for mismatched content that still fails and unrelated errors that still fail. Check: The NEW test `TestSeedSetupReusesValidProfile` in internal/clientgen/conformance/fixtures.go fails on main: the second run against the same server conflicts instead of reusing the profile.
- In internal/clientgen/typescript/views.go add the NEW test TestDecodeNameCollision for TypeScript shared name registry decode identifiers. Check: The NEW test `TestDecodeNameCollision` in internal/clientgen/typescript/views.go fails on main: it covers Foo versus decodeFoo, enum Values collisions, cross-type decode-name collision, and invalid-name error before rendering.
- Extend staleIfMatchGives412 in internal/clientgen/conformance/cases_state.go to check the documented errorCode and non-empty message, fix proxy forwarding in internal/clientgen/conformance/proxy.go, allow seed reuse in internal/clientgen/conformance/fixtures.go only on content match with ETag re-read, and claim decode identifiers in internal/clientgen/typescript/views.go with invalid-name error before rendering. Check: `go test ./internal/clientgen/... -count=1` passes.
- In internal/clientgen/conformance/process.go make Close terminate descendants by process-group SIGTERM then SIGKILL after 5 seconds and keep process-group reuse protection; Linux harness-death cleanup in internal/clientgen/conformance/process_unix.go uses Pdeathsig with LockOSThread and is a no-op on macOS and Windows. Check: `go test ./internal/clientgen/conformance/ -run TestDescendantCleanupOnClose -count=1` passes on Linux; other platforms use the documented no-op path.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Process adapter group kill has a Unix test on main. (`internal/clientgen/conformance/process_unix_test.go:122 (func TestProcessAdapterKillsTheGroup)`)
- Client generator has a subset test on main. (`internal/clientgen/subset_test.go:261 (func TestRealDescriptionIsInSubset)`)
- Go generator has a generated-marker test on main. (`internal/clientgen/golang/golang_test.go:728 (func TestIsGeneratedReadsTheFirstLineOnly)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does any ADR govern generated clients or the conformance harness? No ADR governs generated clients. Ran `rg -l clientgen docs/adr` with zero hits, and the pack lists no ADR. Work stays inside internal/clientgen with no API contract change.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
