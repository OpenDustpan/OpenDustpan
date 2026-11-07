# Review follow-ups (October): generated clients

## 1. Extend staleIfMatchGives412 to validate the documented errorCode and a
PR #1367

- Files: internal/clientgen/conformance/cases_state.go:147-161
- Change: Extend staleIfMatchGives412 to validate the documented errorCode and a non-empty message alongside HTTP 412.
- Acceptance: The case rejects 412 responses with missing or incorrect errorCode values or empty messages, and accepts a response with the expected code and a non-empty message.

## 2. Document that byte-identical generated output across machines requires the same
PR #1367

- Files: internal/clientgen/golang/golang.go:1-15
- Change: Document that byte-identical generated output across machines requires the same Go formatting toolchain because generation uses go/format.
- Acceptance: The package documentation explicitly states the toolchain condition for byte-identical output. Existing golden-file checks pass with the supported toolchain.

## 3. Exclude headers nominated by Connection when forwarding requests and responses
PR #1367

- Files: internal/clientgen/conformance/proxy.go:409-413; internal/clientgen/conformance/proxy.go:448-461; internal/clientgen/conformance/proxy.go:465-469
- Change: Exclude headers nominated by Connection when forwarding requests and responses. Parse all Connection values as comma-separated, case-insensitive header names while preserving the existing fixed exclusions.
- Acceptance: Proxy tests cover mixed-case names, multiple Connection fields and comma-separated values in both directions. Nominated and standard hop-by-hop headers are excluded, while unrelated end-to-end headers survive.

## 4. Provide lifecycle-safe descendant cleanup during graceful shutdown, or
PR #1367

- Files: internal/clientgen/conformance/process.go:172-187; internal/clientgen/conformance/process.go:190-209; internal/clientgen/conformance/process.go:259-268
- Change: Provide lifecycle-safe descendant cleanup during graceful shutdown, or explicitly require adapters to stop their children in the protocol documentation. Preserve protection against signaling potentially reused process-group IDs after reaping.
- Acceptance: A subprocess test starts a child with detached stdio and shuts the adapter down cleanly. Close terminates the child, or the documented adapter obligation and conforming test adapter demonstrate that the adapter terminates it before exiting.

## 5. Add Linux-specific cleanup for adapters and their children when the harness
PR #1367

- Files: internal/clientgen/conformance/process_unix.go:13-18
- Change: Add Linux-specific cleanup for adapters and their children when the harness dies. Account for spawning-thread semantics if using Pdeathsig rather than relying on an arbitrary runtime thread remaining alive.
- Acceptance: A Linux subprocess test terminates the harness while an adapter and its child ignore stdin EOF, then verifies neither survives. Tests also verify that thread lifecycle does not prematurely terminate an adapter whose harness remains alive.

## 6. Allow seed setup to reuse an existing valid seed profile rather than failing
PR #1367

- Files: internal/clientgen/conformance/fixtures.go:306-324
- Change: Allow seed setup to reuse an existing valid seed profile rather than failing solely because creation conflicts. Preserve failures for unrelated errors.
- Acceptance: A test runs the conformance harness twice against the same server. Both runs complete seed setup successfully and can read the expected profile with a usable ETag.

## 7. Claim emitted decode<Name> and <Name>Values identifiers in the shared name
PR #1515

- Files: internal/clientgen/typescript/views.go:82; internal/clientgen/typescript/typescript.go:272
- Change: Claim emitted decode<Name> and <Name>Values identifiers in the shared name registry. Reject collisions with ErrInvalidName before rendering.
- Acceptance: Add tests for Foo/decodeFoo and enum Foo/FooValues collisions in both schema orderings, requiring ErrInvalidName. Noncolliding descriptions must still generate compilable TypeScript.

## 8. Register generated decoder and enum-value identifiers alongside schema names in
PR #1515

- Files: internal/clientgen/typescript/views.go:82; internal/clientgen/typescript/typescript.go:272
- Change: Register generated decoder and enum-value identifiers alongside schema names in the shared registry. Return ErrInvalidName when those identifiers collide.
- Acceptance: Test schema collisions with decode<Name> and <Name>Values in both schema orderings. Each collision must return ErrInvalidName, while noncolliding generation succeeds.

## 9. Wait for the fake adapter's PID readiness before starting the timeout-sensitive
PR #1515

- Files: internal/clientgen/conformance/process_unix_test.go:62
- Change: Wait for the fake adapter's PID readiness before starting the timeout-sensitive call. Bound the readiness wait and ensure failure cleanup terminates the adapter and its children.
- Acceptance: Add a deterministic delayed-startup test showing that PID readiness, rather than a fixed startup window, controls progress. TestProcessAdapterKillsTheGroup must also pass alongside lint.

## 10. Validate every schema enum value with enumValuePattern during the description
PR #1314

- Files: internal/clientgen/subset_test.go:107; internal/clientgen/subset_test.go:132-140
- Change: Validate every schema enum value with enumValuePattern during the description walk instead of treating enum as an unchecked leaf.
- Acceptance: Add fixtures showing valid enum values pass and invalid values produce their JSON pointers, including in nested schemas. TestRealDescriptionIsInSubset must apply this validation to the published description.

---
_Generated by [Claude Code](https://claude.ai/code)_
