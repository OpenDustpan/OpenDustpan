# OpenDustpan: instructions for AI coding agents and contributors

OpenDustpan is an open-source, self-hostable backend for infrastructure-as-code
tools: state, history, locks and secrets for every tool a team uses, behind one
set of namespaces, identities and storage. Each tool connects through the
remote-backend protocol it already speaks: the Pulumi CLI through a Pulumi
Cloud-compatible API, OpenTofu through a TFE-compatible API (RFC 1), more later
(ADR 12).

OpenDustpan is not affiliated with any tool's vendor. Say "works with the
Pulumi CLI", "works with OpenTofu" and "TFE-compatible API". Never use another
project's name, logo or colors as ours. As a label for a tool, in the console
and in API values, write "Pulumi" or "OpenTofu" (see "Tool name" in
`GLOSSARY.md`).

Read `docs/adr/` and `docs/rfc/` before changing architecture. An ADR is a
decision. An RFC is an open question (ADR 15). For an open question, write an
RFC. Write an ADR only after the project owner accepts a decision. If a change
contradicts an ADR, write an RFC first. Use the terms in `GLOSSARY.md` in code, docs and
messages, and avoid the words it lists under _Avoid_. Add a term there when a
new domain concept gets a name.

## Definition of done

Consider a change done only when `make check` passes with zero findings:

- `golangci-lint run ./...` reports nothing. Fix code, do not weaken
  `.golangci.yml`. Allow a `//nolint:<linter> // <reason>` only with a
  specific linter and a real reason. Expect reviewers to question it.
- `make nilaway` reports no possible nil panic. Fix the code. Do not exclude
  a package or a file.
- `make vuln` (govulncheck) reports no known vulnerability.
- `make semgrep` reports nothing. Allow a `// nosemgrep: <rule id>` only with
  the full rule id and a comment that gives a real reason.
- `make trivy` reports nothing. Allow a line in `.trivyignore` only with a
  comment that gives a real reason. The target also fails on a dependency
  with a forbidden, restricted or unknown license (for example GPL or AGPL).
- `make trufflehog` finds no secret in the Git history.
- `make licenses` finds a license file for each module that the binary links.
  The release archives and the container image ship these files.
- `go test -race ./...` passes, including the conformance suites. Each test
  package has a `leak_test.go` file. Its `TestMain` calls
  `goleak.VerifyTestMain`, which fails the package if a goroutine continues
  to run after the tests. Add this file to each new test package.
- `sqlc diff` is clean (regenerate with `make generate`, never edit `internal/db`).

Do not report work as finished if any of these fail. Say what fails instead.

## Layout

```
.agents/skills/        agent skills, installed with `npx skills` (see skills-lock.json); .claude/skills/ links here
cmd/opendustpan/       the binary: flags, env, built-in adapters. Only place that may exit.
pkg/                   the public SDK (ADR 35)
  dustpan/             the SDK entry point: NewServer + options, routes, probes
  namespace/           domain: namespaces, home region, failover-version fencing
  inventory/           domain: cross-tool inventory projection of units
  stack/               domain (Pulumi CLI): projects, stacks; the update cycle (Updates: leases, checkpoints, events)
  secret/              domain: per-scope envelope encryption (first user: the Pulumi CLI's service secrets provider)
  workspace/           domain (OpenTofu): workspaces, locks, state versions (RFC 1)
  identity/            ports: Authenticator, Authorizer; bootstrap StaticToken, AdminOnly
  blob/                port: object storage; blob/fs and blob/s3 adapters; blob/blobtest suite
  backup/              domain: backup ports (export, import, registry) and the table list
  store/               SQL adapters: sqlite/, postgres/; storetest/ conformance suite
internal/              code that is not part of the SDK
  api/pulumi/          HTTP adapter for the Pulumi Cloud-compatible API (also used by the console)
  api/tfe/             HTTP adapter for the TFE-compatible API (OpenTofu)
  api/inventory/       HTTP adapter for the tool-neutral inventory API (also used by the console)
  api/server/          HTTP adapter for the tool-neutral installation API (backups, metrics)
  metrics/, token/...  helpers shared across packages
load/                  load harness that replays recorded CLI traffic (make load); not part of make check
e2e/                   end-to-end suite driving real CLI binaries of every supported tool (make e2e)
docs/adr/              architecture decision records: accepted decisions only
docs/rfc/              RFCs: open questions, options and proposals (ADR 15)
GLOSSARY.md            the project's domain language: one word per concept
```

The web console lives in its own repository, `OpenDustpan/console` (ADR 8).

## A shared core, one adapter per tool (ADR 12)

- Shared packages (`namespace`, `identity`, `blob`, `secret`, the fence) stay
  tool-neutral: no field, method or error in them may exist only because one
  tool needs it.
- Each tool's model of deployable units and state lives in its own domain
  package (`stack` for the Pulumi CLI, `workspace` for OpenTofu). Do not merge
  them or share types between them. Share concepts through the core instead.
- Each tool's wire protocol lives in its own HTTP adapter under `internal/api/<tool>/`,
  with its own route prefix. Adapters never call each other.
- Support a tool only once the e2e suite drives its real CLI. Every
  endpoint lands with its e2e step first.
- Cross-tool features never merge tool models (ADR 13). Shared code calls a
  stack or workspace a "unit", referenced as tool + namespace + the tool's own
  name. Authorization uses tool-neutral actions on those references. The
  console's cross-tool views read the inventory projection. Each tool package
  publishes to it in the same transaction as its own writes. User-facing text
  keeps each tool's own words.

## Backend rules (Go)

- Domain packages (`namespace`, `stack`, `secret`, `workspace`, `identity`, `blob`, `backup`) never import
  `net/http`, `database/sql`, drivers, `pkg/store/...` or `internal/api/...`. depguard enforces this.
- Define interfaces (ports) in the package that consumes them. Keep them
  small, and document them with the errors they return.
- Every port has a conformance suite (`*test` package). A new adapter must run
  it. A new port method must get conformance tests before any adapter implements it.
- Errors: sentinel `Err...` values in the domain package, wrapped with
  `fmt.Errorf("...: %w", err)`. Adapters translate driver errors (no rows,
  conflicts) into domain sentinels. Never return driver errors to callers.
- Writes inside a namespace carry a `namespace.Fence` and the store checks it in
  the same transaction. Never add a write path that skips the fence.
- SQL: hand-written per dialect. Put fixed queries in `pkg/store/<dialect>/queries.sql`
  and generate them with sqlc. Dynamic queries are hand-written in the adapter.
  Schema changes are new numbered files in `pkg/store/<dialect>/migrations/`. Never
  edit an applied migration. TestFrozenMigrations in each store adapter holds
  the SHA-256 of each file. Timestamps are Unix milliseconds in `BIGINT`.
- One exception to that rule occurred one time, before the first tag. The
  migrations of each dialect became one baseline migration,
  `0100_baseline.sql`. `scripts/regenerate-baseline.sh` writes it. A new
  migration gets a number above 0100. Do not make another baseline.
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
- Give every feature a REST API (ADR 17). The console, the CLI and all other
  clients use only that API. Do not add a feature that only the console, a CLI
  or a configuration file can use.
- Make every endpoint declare its authorization actions and the object of
  each action. Make every change pass the policy check. A route without an
  action fails the route test. Put routes that need no sign-in on the explicit
  public list in that test.

## Voice (UI text, errors, docs)

Exact, short, calm. Sentence case. Name the thing and the number. Errors say
what failed and the one command that helps. No exclamation marks, no emoji.

Write English in ASD-STE100 Simplified Technical English with the `asd-ste100`
skill (ADR 14):

- Strict mode: error messages, log messages, API error text, CLI output, and
  instructions for agents that we write (`CLAUDE.md`, our own skills, prompts).
- STE-flavored mode: README, ADRs, `GLOSSARY.md`, pull request descriptions,
  commit messages and code comments.
- Spell English in US spelling (ADR 16): "color", "license", "behavior",
  "organization", "canceled", "catalog". golangci-lint's `misspell` checks Go
  comments with the US locale. Wire values and other projects' names keep
  their own spelling, for example the Pulumi CLI's `"cancelled"`.
- Keep every hedge. "May", "might" and "could" carry the author's confidence.
  Never turn a hedge into a fact to make a sentence shorter.
- `make docs-lint` checks README, `CLAUDE.md`, `GLOSSARY.md` and ADRs from 14
  on. ADRs accepted before ADR 14 are exempt, because ADR 1 forbids edits.
- Do not use words that claim quality without proof. Give the measurement or
  the fact. Do not use emoji. `scripts/docs-banned-words.py` holds the list of
  banned words, and `make docs-lint` runs it on the same files.
- Do not put internal project references in code or in user documentation
  (ADR 66). An internal reference cites an ADR, an RFC, an issue or pull
  request number, a milestone or a research note. Code includes comments,
  tests, and error and log text. User documentation is `README.md`,
  `docs/guides/`, `docs/reference/` and `docs/explanation/`. Write the reason
  or the behavior instead. When you edit a line that has such a reference,
  remove the reference. Do not rewrite other lines only to remove references.

## Working in this repo

- Ask before adding a dependency. Prefer the standard library.
- Keep changes small and reviewable, with one concern per change.
- If you are unsure of the intent, ask rather than guess.
- Manage agent skills with `npx skills@1.7.0` and `DISABLE_TELEMETRY=1`,
  installed for Claude Code and OpenCode. Never edit files under
  `.agents/skills/` by hand. Update a skill only to an upstream commit at least
  two weeks old (ADR 7), and review its diff first.

## Agent skills

### Issue tracker

Issues for this repo live in GitHub Issues. The skills use the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

The five canonical triage roles map to same-named labels: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context layout: one `GLOSSARY.md` and `docs/adr/` at the repo root. See `docs/agents/domain.md`.
