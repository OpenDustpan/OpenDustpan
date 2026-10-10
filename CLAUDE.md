# OpenDustpan: instructions for AI coding agents and contributors

OpenDustpan is an open-source, self-hostable backend for infrastructure-as-code
tools: state, history, locks and secrets for every tool a team uses, behind one
set of namespaces, identities and storage. Each tool connects through the
remote-backend protocol it already speaks: the Pulumi CLI through a Pulumi
Cloud-compatible API, OpenTofu through a TFE-compatible API (ADR 9), more later
(ADR 12).

OpenDustpan is not affiliated with any tool's vendor. Say "works with the
Pulumi CLI", "works with OpenTofu" and "TFE-compatible API". Never use another
project's name, logo or colours as ours.

Read `docs/adr/` before changing architecture. If a change contradicts an ADR,
write a new ADR first.

## Definition of done

A change is done only when `make check` passes with zero findings:

- `golangci-lint run ./...` reports nothing. Fix code, do not weaken
  `.golangci.yml`. A `//nolint:<linter> // <reason>` is allowed only with a
  specific linter and a real reason. Expect reviewers to question it.
- `go test -race ./...` passes, including the conformance suites.
- `sqlc diff` is clean (regenerate with `make generate`, never edit `internal/db`).

Do not report work as finished if any of these fail. Say what fails instead.

## Layout

```
.agents/skills/      agent skills, installed with `npx skills` (see skills-lock.json); .claude/skills/ links here
dustpan.go, web.go   package dustpan: the SDK entry point (NewServer + options)
cmd/opendustpan/     the binary: flags, env, built-in adapters. Only place that may exit.
namespace/           domain: namespaces, home region, failover-version fencing
stack/               domain (Pulumi CLI): projects, stacks; the update cycle (Updates: leases, checkpoints, events)
secret/              domain: per-scope envelope encryption (first user: the Pulumi CLI's service secrets provider)
workspace/           domain (OpenTofu): workspaces, locks, state versions (ADR 9)
identity/            ports: Authenticator, Authorizer; bootstrap StaticToken, AdminOnly
blob/                port: object storage; blob/fs adapter; blob/blobtest suite
store/               SQL adapters: sqlite/, postgres/; storetest/ conformance suite
api/pulumi/          HTTP adapter for the Pulumi Cloud-compatible API (also used by the console)
e2e/                 end-to-end suite driving real CLI binaries of every supported tool (make e2e)
internal/            helpers shared across packages, not part of the SDK
docs/adr/            architecture decision records
```

The web console lives in its own repository, `OpenDustpan/console` (ADR 8).

## A shared core, one adapter per tool (ADR 12)

- Shared packages (`namespace`, `identity`, `blob`, `secret`, the fence) stay
  tool-neutral: no field, method or error in them may exist only because one
  tool needs it.
- Each tool's model of deployable units and state lives in its own domain
  package (`stack` for the Pulumi CLI, `workspace` for OpenTofu). Do not merge
  them or share types between them. Share concepts through the core instead.
- Each tool's wire protocol lives in its own HTTP adapter under `api/<tool>/`,
  with its own route prefix. Adapters never call each other.
- A tool is supported only once the e2e suite drives its real CLI. Every
  endpoint lands with its e2e step first.
- Cross-tool features never merge tool models (ADR 13). Shared code calls a
  stack or workspace a "unit", referenced as tool + namespace + the tool's own
  name. Authorization uses tool-neutral actions on those references. The
  console's cross-tool views read the inventory projection. Each tool package
  publishes to it in the same transaction as its own writes. User-facing text
  keeps each tool's own words.

## Backend rules (Go)

- Domain packages (`namespace`, `stack`, `secret`, `workspace`, `identity`, `blob`) never import
  `net/http`, `database/sql`, drivers, `store/...` or `api/...`. depguard enforces this.
- Interfaces (ports) are defined in the package that consumes them, kept small,
  and documented with the errors they return.
- Every port has a conformance suite (`*test` package). A new adapter must run
  it. A new port method must get conformance tests before any adapter implements it.
- Errors: sentinel `Err...` values in the domain package, wrapped with
  `fmt.Errorf("...: %w", err)`. Adapters translate driver errors (no rows,
  conflicts) into domain sentinels. Never return driver errors to callers.
- Writes inside a namespace carry a `namespace.Fence` and the store checks it in
  the same transaction. Never add a write path that skips the fence.
- SQL: hand-written per dialect. Fixed queries go in `store/<dialect>/queries.sql`
  and are generated with sqlc. Dynamic queries are hand-written in the adapter.
  Schema changes are new numbered files in `store/<dialect>/migrations/`. Never
  edit an applied migration. Timestamps are Unix milliseconds in `BIGINT`.
- HTTP: `net/http` and `ServeMux` only. No web frameworks, no ORMs, no
  testify (depguard bans them). Logging is `log/slog` with an injected logger.
- Tests: standard `testing` only, black-box `_test` packages, `t.Parallel()`,
  table-driven where it helps. Use `t.Context()`.
- The backend protocols implemented so far are not published, versioned specs
  we can implement from. Any endpoint change must be checked against the real
  CLI with `make e2e`.

## The APIs are contracts

Every tool's CLI depends on its API, and the console, which ships from another
repository on its own schedule, depends on them too.

- Never remove or rename a field, endpoint or status code that a released
  console or a supported CLI version uses. Add new fields. Deprecate old ones first.
- Each API keeps the shapes its tool expects. The Pulumi Cloud-compatible API:
  camelCase JSON, errors `{"code": <status>, "message": "..."}`. The
  TFE-compatible API: JSON:API documents and errors.
- An API change the console needs lands here first, released, then in the console.

## Voice (UI text, errors, docs)

Exact, short, calm. Sentence case. Name the thing and the number. Errors say
what failed and the one command that helps. No exclamation marks, no emoji.

Write English in ASD-STE100 Simplified Technical English with the `asd-ste100`
skill (ADR 14):

- Strict mode: error messages, log messages, API error text, CLI output, and
  instructions for agents that we write (`CLAUDE.md`, our own skills, prompts).
- STE-flavored mode: README, ADRs, `GLOSSARY.md`, pull request descriptions,
  commit messages and code comments.
- Keep every hedge. "May", "might" and "could" carry the author's confidence.
  Never turn a hedge into a fact to make a sentence shorter.
- `make docs-lint` checks README, `CLAUDE.md`, `GLOSSARY.md` and ADRs from 14
  on. ADRs accepted before ADR 14 are exempt, because ADR 1 forbids edits.

## Working in this repo

- Ask before adding a dependency. Prefer the standard library.
- Keep changes small and reviewable, with one concern per change.
- If you are unsure whether something is intended, ask rather than guess.
- Manage agent skills with `npx skills@1.7.0` and `DISABLE_TELEMETRY=1`,
  installed for Claude Code and OpenCode. Never edit files under
  `.agents/skills/` by hand. Update a skill only to an upstream commit at least
  two weeks old (ADR 7), and review its diff first.
