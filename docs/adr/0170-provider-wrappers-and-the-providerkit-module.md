# 170. Provider wrappers, and the providerkit module

Status: accepted · 2026-10-18 · ADRs 171 to 179 give the parts

## Context

Units often need facts from systems that no IaC tool manages: the person on
call, a cost center, a free address from an IPAM API. RFC 47 asks if the
backend finds or scans such things itself, and its first idea is "the backend
never scans clouds itself". RFC 15 shares outputs between units, but a fact
from outside comes from no unit.

The project owner decided on 17 October 2026 (owner):

- Facts are an IaC concern. OpenDustpan ships no server-side fact adapters.
- OpenDustpan offers provider wrappers instead. A team turns any HTTP API
  (and later a gRPC API) or any command-line tool into resources and data
  sources. OpenTofu and the Pulumi CLI use them like any other provider.
  Facts then come in through ordinary data sources in ordinary units.
- An HTTP provider declares resources and data sources from a description,
  for example an OpenAPI document plus a small mapping file. It maps create,
  read, update and delete to HTTP calls, with authentication, pagination,
  retries, and drift detection through read.
- A CLI provider runs a declared command as an argument array, with no
  shell, by the same rule as the launcher. It parses the output as JSON.
- A Go SDK lets a team write a small provider for its own tool in a few
  hundred lines, with the same conventions.
- One description produces a provider for OpenTofu and a provider for the
  Pulumi CLI.
- The code is pure Go, with no cgo.

The owner left open where the code lives and how one description serves
both tools. The owner later decided where the code lives (decision 3). ADR
176 decides how one description serves both tools.

The label "(owner)" names a decision that the text of the owner of 17
October 2026 states. The label "(PM, for the project owner)" names a decision
that the PM made for the project owner. The project owner did not approve the
other decisions of ADRs 170 to 179 one by one. They are the design of these
ADRs, and the project owner accepts or refuses them with the pull request.

## Decision

1. **No server-side fact adapter (owner).** The server never calls an IPAM,
   an on-call service or any other source of facts for a unit. A fact
   enters a unit only through a data source in the code of the unit, while
   the tool runs a plan or an apply.
2. **Three ways to make a provider (owner).** A team declares an HTTP
   provider (ADR 173), declares a CLI provider (ADR 174), or writes a
   provider in Go with the SDK (ADR 178). All three implement one core
   port (ADR 172), and each gives one provider that works with OpenTofu
   and with the Pulumi CLI (ADR 176).
3. **A separate repository (owner).** The project owner decided on 18
   October 2026 that the code lives in its own repository,
   `opendustpan/providerkit`, with the module path
   `opendustpan.org/providerkit`. Its release tags are `vX.Y.Z` of that
   repository. The reasons:
   - A provider is a client program. OpenTofu or the Pulumi CLI starts it on
     a workstation or on a runner. It must not link the SQL drivers, the
     key-encryption key code or any other part of the server. A separate
     module makes this a fact of the build, not only a rule of the linter.
   - Provider authors import the SDK. ADR 73 lets the server break its APIs
     before 1.0. The SDK needs a version line of its own, so that an author
     can pin it apart from the server.
   - The `go.mod` of the module lists only what providers need. An author
     who adds the SDK does not get the server's dependencies in the module
     graph of the provider.
4. **The gates of this repository, copied (owner).** The new repository has
   its own CI, its own release line, and the lint and security gates of this
   repository: the same `.golangci.yml`, `make check` with lint, `go mod
   tidy -diff`, `go test -race`, `nilaway`, `govulncheck`, `semgrep`,
   `trivy` with the license gate, `trufflehog` and `make licenses`, a
   `leak_test.go` with `goleak` in each test package, and Dependabot. The
   ADRs of the provider kit stay in this repository. `pkg/` is not the
   place, because it is the SDK of the server (ADR 35) and has the version
   of the server.
5. **No import across the boundary.** `providerkit` imports no package of
   OpenDustpan. OpenDustpan imports no package of `providerkit`. The CLI
   engine and the launcher share the rule "an argument list, no shell" as a
   rule, not as code. A later need to share code between them is a new
   decision.
6. **The layout.** This ADR adds the first packages. The engines, the
   protocol adapters and the build tool come with their implementation.
   The review of this design reads the packages in `providerkit/` of this
   repository. After the review, the code moves to the new repository, and
   this repository keeps no copy.

   ```
   opendustpan/providerkit
     schema/                  types, attributes, values
     lifecycle/               the core port and the shared planner (ADR 172)
       lifecycletest/         conformance suite for a provider (ADR 179)
     declaration/             the declaration format (ADR 171)
     httpexec/                the HTTP execution port (ADR 173)
       httpexectest/          conformance suite for a caller
     cliexec/                 the CLI execution port (ADR 174)
       cliexectest/           conformance suite for a runner
     sdk/                     the SDK for authors (ADR 178)
     engine/http/, engine/cli/    later: the two declared engines
     protocol/tofu/, protocol/pulumi/  later: the two adapters (ADR 176)
     cmd/dustpan-provider/    later: the build tool (ADR 177)
   ```

7. **Pure Go (owner).** Every binary of the module builds with
   `CGO_ENABLED=0`.
8. **Names.** The documentation says that a built provider "works with
   OpenTofu" and "works with the Pulumi CLI". The file names of a built
   provider follow what the installer of each tool expects (ADR 177). Such
   a file name is a protocol name. It does not make another project's name
   ours.

## Consequences

- `make check` of this repository does not cover the provider kit, and does
  not need to. Until the code moves, the checks run by hand in
  `providerkit/`. After the move, the CI of `opendustpan/providerkit` runs
  them.
- A change to the rule "an argument list, no shell" needs a pull request in
  each repository.
- RFC 47 keeps its question about scanning. This ADR answers only how facts
  come in: through providers that a team owns.
- A team that wants a fact from OpenDustpan itself, for example the outputs
  of another unit, still uses RFC 15. A provider wrapper is for systems
  outside OpenDustpan.
- The new repository needs its own copy of the gates of decision 4. The
  pull request of this ADR lists them.

## Open questions

- gRPC APIs (owner: "later gRPC"). A gRPC engine needs the descriptors of
  the API at build time. It is not designed here.
- Does OpenDustpan serve the provider registry protocol of OpenTofu for
  providers that a team builds? ADR 177 lists it.
