# 179. Conformance suites for provider wrappers

Status: accepted · 2026-10-18 · part of ADR 170

## Context

AGENTS.md says that every port has a conformance suite, and that a new
adapter runs it. The project owner decided on 17 October 2026 that a team
writes and declares providers of its own (owner). A team then needs a way to
test a provider without OpenTofu or the Pulumi CLI, and before its users
find a problem.

`providerkit` has three ports: the core port (ADR 172), the HTTP execution
port (ADR 173) and the CLI execution port (ADR 174).

## Decision

1. **`lifecycletest` for providers.** A provider author runs
   `lifecycletest.Run` with a fixture: the provider, its configuration, and
   for each type the configuration of one object. The suite drives each
   resource type through the core port and the shared planner:
   - The schemas are valid, and each type has a case in the fixture.
   - Each sensitive configuration attribute has its file form (ADR 175
     decision 2). The provider configures with the file form of each secret
     of the fixture, and refuses a secret together with its file form.
   - Create returns an ID, every attribute, no unknown value, and the known
     planned values unchanged. `lifecycle.ForTool` then makes each
     write-only value null.
   - When the fixture gives `Received`, each write-only value of the
     configuration reached the fake of the remote system, in create and in
     update.
   - Read after create finds no drift, and a plan of the same configuration
     is a no-op.
   - Import by ID gives the same values.
   - An update changes the object in place and keeps its ID, and a plan of
     the same configuration after it is a no-op.
   - When the fixture gives `Replace`, the plan is a replace, and the suite
     deletes the object and creates the new one, in the default order of
     OpenTofu.
   - Read after delete is `ErrNotFound`, an import of the deleted ID is
     `ErrNotFound`, and a second delete returns nil.
   - A second object of the type goes through create, read and delete at
     the same time as the first, through one session (ADR 172 decision 11).
   - A data source returns known values, and two reads agree. When the
     fixture gives `NotFound`, a read of that configuration is
     `ErrNotFound`.
2. **`httpexectest` for callers.** An HTTP caller adapter runs the suite
   against local servers (ADR 173). It also checks that the caller refuses
   a request to another origin and gives it no credential (ADR 173
   decision 13).
   The suite tests itself against a small reference caller on `net/http`.
3. **`cliexectest` for runners.** A runner adapter runs the suite with a
   helper program (ADR 174). The test binary of the adapter is the helper:
   its `TestMain` calls `cliexectest.RunHelper` first. In the mode `spawn`,
   the helper starts a grandchild that sleeps and writes its process ID to
   a file. After `ErrTimeout`, the suite checks that the grandchild no
   longer runs, so a runner that stops only its child fails. The suite also checks a
   canceled context, and standard error over the limit. The suite tests
   itself against a small reference runner on `os/exec`.
4. **Declared providers run the provider suite too.** The HTTP engine runs
   `lifecycletest` against each example declaration with a fake API on a
   local server. The CLI engine does the same with a fake command. A team
   can do the same for its own declaration.
5. **The protocol adapters have e2e steps.** `lifecycletest` covers the core
   port. Each protocol adapter also lands with an e2e step that drives the
   real CLI (ADR 176 decision 7).
6. **Table tests where the behavior is a rule.** The planner, the
   declaration rules, the template rules, the retry rules, the command rules
   and the struct tags each have a table test.
7. **The same gates as the root module.** Each test package has a
   `leak_test.go` with `goleak`. `golangci-lint`, `go test -race` and
   `nilaway` run on the module with the configuration of the repository.

## Consequences

- A team that changes its declaration runs one Go test against a fake of its
  API, and sees a broken mapping before a user does.
- The suites need no network and no tool CLI.
- `make check` must run the suites of `providerkit`. The pull request of this
  ADR asks for that change to the shared `Makefile` and CI workflow.

## Open questions

- A replay fixture. A team could record replies from a test instance of its
  API in a file, and run `lifecycletest` against a server that replays
  them. This needs a file format of its own.
- A suite for the protocol adapters below e2e, with recorded gRPC calls of
  each tool. It depends on the gRPC dependency question of ADR 176.
