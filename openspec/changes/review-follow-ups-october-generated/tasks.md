## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 In internal/clientgen/conformance/cases_state.go add the NEW test TestStaleIfMatchChecksCodeAndMessage that 412 carries the documented errorCode and a non-empty message. Check: The NEW test `TestStaleIfMatchChecksCodeAndMessage` in internal/clientgen/conformance/cases_state.go fails on main: it covers missing errorCode, wrong errorCode, and empty message.
- [ ] 1.2 In internal/clientgen/conformance/proxy.go add the NEW test TestProxyStripsConnectionNominated for Connection-nominated header filtering both directions. Check: The NEW test `TestProxyStripsConnectionNominated` in internal/clientgen/conformance/proxy.go fails on main: it covers mixed-case, multiple, and comma-separated Connection values both directions, close and keep-alive tokens, empty tokens, fixed exclusions, and survival of unrelated end-to-end headers.
- [ ] 1.3 In internal/clientgen/conformance/process.go add the NEW test TestDescendantCleanupOnClose that Close terminates a child with detached stdio while keeping reused process-group ID protection. Check: The NEW test `TestDescendantCleanupOnClose` in internal/clientgen/conformance/process.go fails on main: a child with detached stdio survives Close before the fix, plus SIGTERM-ignore escalation after 5 seconds, already-exited child, and PGID reuse cases.
- [ ] 1.4 In internal/clientgen/conformance/process_unix.go add the NEW Linux-only test TestHarnessDeathCleansAdapterAndChild for harness-death cleanup. Check: The NEW test `TestHarnessDeathCleansAdapterAndChild` in internal/clientgen/conformance/process_unix.go fails on main on Linux: it kills the harness while adapter and child ignore stdin EOF and checks neither survives, plus thread lifecycle does not kill a live-harness adapter.
- [ ] 1.5 In internal/clientgen/conformance/fixtures.go add the NEW test TestSeedSetupReusesValidProfile running the harness twice, plus cases for mismatched content that still fails and unrelated errors that still fail. Check: The NEW test `TestSeedSetupReusesValidProfile` in internal/clientgen/conformance/fixtures.go fails on main: the second run against the same server conflicts instead of reusing the profile.
- [ ] 1.6 In internal/clientgen/typescript/views.go add the NEW test TestDecodeNameCollision for TypeScript shared name registry decode identifiers. Check: The NEW test `TestDecodeNameCollision` in internal/clientgen/typescript/views.go fails on main: it covers Foo versus decodeFoo, enum Values collisions, cross-type decode-name collision, and invalid-name error before rendering.

## 2. Fix

- [ ] 2.1 Extend staleIfMatchGives412 in internal/clientgen/conformance/cases_state.go to check the documented errorCode and non-empty message, fix proxy forwarding in internal/clientgen/conformance/proxy.go, allow seed reuse in internal/clientgen/conformance/fixtures.go only on content match with ETag re-read, and claim decode identifiers in internal/clientgen/typescript/views.go with invalid-name error before rendering. Check: `go test ./internal/clientgen/... -count=1` passes.
- [ ] 2.2 In internal/clientgen/conformance/process.go make Close terminate descendants by process-group SIGTERM then SIGKILL after 5 seconds and keep process-group reuse protection; Linux harness-death cleanup in internal/clientgen/conformance/process_unix.go uses Pdeathsig with LockOSThread and is a no-op on macOS and Windows. Check: `go test ./internal/clientgen/conformance/ -run TestDescendantCleanupOnClose -count=1` passes on Linux; other platforms use the documented no-op path.
- [ ] 2.3 In internal/clientgen/golang/golang.go document the Go formatting toolchain condition for byte-identical output. Check: `grep -r byte-identical internal/clientgen/golang/golang.go` shows the package comment states byte-identical output needs the same Go formatting toolchain because generation uses go/format, and `go test ./internal/clientgen/golang/ -count=1` passes.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Run full checks, golden-file checks, and usability read of changed docs. Confirmed by: CI (make check, golden-file checks, and doc-text grep). Check: `make check` with zero findings. Confirmed by: CI (make check, golden-file checks, and doc-text grep).
