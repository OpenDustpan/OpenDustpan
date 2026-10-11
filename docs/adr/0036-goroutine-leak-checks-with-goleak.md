# 36. Goroutine leak checks with goleak

Status: accepted · 2026-10-11

## Context

The server and its adapters start goroutines: HTTP servers, connection pools
and background work. A goroutine that continues to run after its owner stops is
a leak. A leak can hold a connection, a lock or memory, and the race detector
and the linters of ADR 7 do not report it.

The standard library has no check for goroutines that stay after a test.
`go.uber.org/goleak` is a small library that does this check. It reads the
stacks of all goroutines when the tests of a package end, and it fails the
package if a goroutine that the tests started continues to run.

`AGENTS.md` tells contributors to prefer the standard library and to ask before
they add a dependency. Before this decision, the module depended only on the
two database drivers and their indirect modules.

The project owner asked for goleak on 2026-10-11.

## Decision

1. The module depends on `go.uber.org/goleak`, pinned to v1.3.0 (2023-10-24),
   which is older than the two weeks that ADR 7 requires. Only test files
   import it, so the server binary does not contain it.
2. Each package that has tests has a `leak_test.go` file. Its `TestMain` calls
   `goleak.VerifyTestMain`. The check runs one time for each package, after all
   tests of the package end. Thus it works with `t.Parallel()`, which the
   per-test check `goleak.VerifyNone` does not.
3. A leak is a failure of `make test`, and thus of `make check` and of CI.
4. Fix a leak in the code that starts the goroutine. Use a goleak ignore option
   only for a goroutine that a dependency starts and that we cannot stop. Put a
   comment with the reason next to the option.
5. The e2e suite has no leak check. It starts CLI processes and servers that
   stay for the full run, and we did not run the suite with goleak.

## Consequences

- A new test package needs a `leak_test.go` file. No tool reports a package
  that does not have the file, so reviewers must look for it.
- A test that starts a server, a pool or a timer must stop it before the test
  ends, usually with `t.Cleanup`.
- goleak names the leaked goroutine but not the test that started it. To find
  the test, run the tests of the package one at a time with `-run`.
- A package that needs its own `TestMain` must call `goleak.VerifyTestMain`
  from that function.
- A later decision could add the leak check to the e2e suite.
