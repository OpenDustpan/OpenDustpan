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
- A feature that a user can see has its page in the user documentation in
  the same pull request (ADR 214).

Do not report work as finished if any of these fail. Say what fails instead.

## Layout

```
.agents/skills/        agent skills, installed with `npx skills` (see skills-lock.json); .claude/skills/ links here
cmd/opendustpan/       the binary: flags, env, built-in adapters. Only place that may exit.
cmd/clientgen/         the client generator: reads openapi.json and writes a client (make clients-generate)
pkg/                   the public SDK: dustpan, the port packages and the conformance suites (ADR 35, ADR 272)
  dustpan/             the SDK entry point: NewServer + options, routes, probes
  namespace/           domain: namespaces, home region, failover-version fencing
  inventory/           domain: cross-tool inventory projection of units
  stack/               domain (Pulumi CLI): projects, stacks; the update cycle (Updates: leases, checkpoints, events)
  secret/              domain: per-scope envelope encryption (first user: the Pulumi CLI's service secrets provider)
  workspace/           domain (OpenTofu): workspaces, locks, state versions (RFC 1)
  identity/            ports: Authenticator, Authorizer; bootstrap StaticToken, AdminOnly
  principal/           domain: the principals and the groups of an installation
  access/              domain: roles, grants and the decision of what a principal can do on an object
  accesstoken/         domain: the access tokens that the server issues to principals
  session/             domain: the console sessions that the server issues after a sign-in
  tempadmin/           domain: temporary administrators of an installation
  ipallow/             domain: per-namespace IP allowlists
  audit/               domain: the tool-neutral audit log, with a hash chain for each namespace
  collection/          domain: collections, named sets of units in one namespace
  maintenance/         domain: the installation state (normal operation or maintenance mode for a restore)
  installation/        domain: the installation kind, the marker of the command that made the data
  durable/             domain: durable operations, units of background work that survive the stop of an instance
  idempotency/         domain: the replay records of the Idempotency-Key header
  manuallock/          domain: manual locks that a person puts on a unit, a collection or a namespace
  profile/             domain: profiles, named sets of config, secrets and credentials with numbered revisions
  breakglass/          domain: the review after use of each break-glass
  notify/              domain: the notification model; notify/chat, notify/sinks and notify/webhook are the built-in senders
  logsink/             port: structured log records; logsink/file, objectstore, slogsink, syslog and webhook are the built-in sinks
  egress/              domain: the rules for outbound requests to an address that a principal gives
  blob/                port: object storage; blob/fs, blob/s3 and blob/cache adapters; blob/blobtest suite
  backup/              domain: backup ports (export, import, registry) and the table list
  history/             domain: unit history, redaction, item keys of history items, tombstones, retention
  job/                 domain: jobs, phases, gates, executors (write-back, runners) on durable operations
  runner/              domain: runner pools, runners, claims and the runner protocol (runner/protocol)
  objectcontext/       domain: context on every object and the context read API
  graph/               domain: the unit graph, blast radius and focus view
  drift/               domain: drift checks, the drift result and status, the drift schedule setting (RFC 25)
  trigger/             domain: run triggers that start a plan (review request push, schedule, follows unit), the cycle rule (RFC 24)
  outputshare/         domain: sharing rules, the output sharing setting of a namespace and the read check of outputs (RFC 15)
  savedview/           domain: saved views, a named query of the unit list (RFC 100)
  preferences/         domain: the preferences of a person (theme, language, formats, time zone), the merge patch and its checks
  registry/            domain: the private registry: the address, the release and the store port; registry/pgpverify checks signatures
  gate/                domain: gates, access limits, approval rules, facts and decision records
  gate/cedargo/        adapter: the linked Cedar engine (cedar-go) of the gate engine port; gate/gatetest holds its suite
  gate/jobgate/        adapter: the job.Decider that turns approval defaults into the approval gates of a job
  forge/               domain: the Forge port, forge connections and their errors; forge/forgetest holds the conformance suite
  forge/github/        adapter: the forge for GitHub
  store/               SQL adapters: sqlite/, postgres/; storetest/ conformance suite (the adapters move to internal/store/)
  <domain>/<domain>test/ the conformance suite of a port (for example stack/stacktest)
internal/              code that is not part of the SDK
  <domain>svc/         the service of each split domain package (for example outputsharesvc); the ports stay in pkg/<domain>
  agent/, dependency/, launcher/, source/, stepup/, unmanaged/, unitimport/, scopeaudit/, cron/
                       domain packages and helpers that the SDK port closure does not reach
  calendar/, catalog/, chatops/, cost/, freeze/, incident/, oncall/, policy/, quota/
                       features with their own package and suite (<name>test/)
  registry/            the registry service (service/), the release check (releasecheck/) and the signature fixture (pgpfixture/)
  egress/egresshttp/   the HTTP client that applies the egress rules
  breakglass/, graph/, history/, store/, blob/, notify/
                       test helpers (breakglasstest, graphtest, historytest, storefault, blobfault) and notify/chat
  authgate/, errcode/, oauth/, sessionauth/, token/, ident/, ...
                       helpers shared across packages (sign-in failure windows, error codes, OAuth, tokens, identifiers)
  textbody/            the read and the strict decode of a JSON request body, with the check of its text
  pulumijournal/       the decoder and the replayer of the Pulumi update journal
  cli/                 the command-line client
  clientgen/           the client generator: parser, writer, template sets (golang/, typescript/) and the conformance suite
  api/pulumi/          HTTP adapter for the Pulumi Cloud-compatible API (also used by the console)
  api/tfe/             HTTP adapter for the TFE-compatible API (OpenTofu)
  api/inventory/       HTTP adapter for the tool-neutral inventory API (also used by the console)
  api/objectcontext/   HTTP adapter for the tool-neutral context API (also used by the console)
  api/jobs/            HTTP adapter for the tool-neutral jobs API
  api/server/          HTTP adapter for the tool-neutral installation API (backups, metrics)
  api/runnerapi/       HTTP adapter for the runner protocol (/runner/v1) and the runner management API
  metrics/, pgtest/, servertest/, ...
                       metrics, the PostgreSQL test database and the test server
clients/typescript/    the generated TypeScript client package; make clients-check fails when it is not what a fresh run gives
load/                  load harness that replays recorded CLI traffic (make load); not part of make check
e2e/                   end-to-end suite driving real CLI binaries of every supported tool (make e2e)
deploy/helm/           the Helm chart in opendustpan/ (templates, values, schema, ci/ profiles); charttest/ holds its golden-file tests (make chart-lint)
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
  name. "Unit" stays in code and in the API. Copy says "stack", "workspace",
  or "stacks and workspaces" (ADR 251). Authorization uses tool-neutral
  actions on those references. The
  console's cross-tool views read the inventory projection. Each tool package
  publishes to it in the same transaction as its own writes. User-facing text
  keeps each tool's own words.

## Backend rules (Go)

- Domain packages (the `pkg/` packages that the `domain` rule of `.golangci.yml` lists, for example
  `namespace`, `stack`, `secret`, `workspace`, `identity`, `backup`, `history`, `job`, `runner`,
  `objectcontext`, `graph`, `drift`, `trigger`, `outputshare`, `savedview`, `registry`, `preferences`,
  `gate`, `forge`, and the ones in `internal/` that the rule lists, for example `agent`, `dependency`,
  `launcher`, `source`, `stepup`, `unmanaged`, `unitimport`) never import
  `net/http`, `database/sql`, drivers, `pkg/store/...` or `internal/api/...`. depguard enforces this.
- Define each port in the `pkg/` package of its domain. Put the service that
  uses the port in `internal/<domain>svc`. Keep ports small, and document them
  with the errors they return.
- Every port has a conformance suite (`*test` package). A new adapter must run
  it. A new port method must get conformance tests before any adapter implements it.
- Errors: sentinel `Err...` values in the domain package, wrapped with
  `fmt.Errorf("...: %w", err)`. Adapters translate driver errors (no rows,
  conflicts) into domain sentinels. Never return driver errors to callers.
- Writes inside a namespace carry a `namespace.Fence` and the store checks it in
  the same transaction. Never add a write path that skips the fence.
- SQL: hand-written per dialect. Put fixed queries in `pkg/store/<dialect>/queries.sql`
  and generate them with sqlc. Dynamic queries are hand-written in the adapter.
  Schema changes are new numbered files in `pkg/store/<dialect>/migrations/`.
  Never edit a migration file that a tag shipped. An applied file that no tag
  shipped stays frozen until the collapse before the next tag, which merges
  all such files into one (ADR 73). Give the collapsed file a version number
  above every file that it replaces, so that the migration check refuses a
  database from an untagged build with `ErrSchemaDiverged`. TestFrozenMigrations in each store adapter
  holds the SHA-256 of each file. Timestamps are Unix milliseconds in `BIGINT`.
- HTTP: `net/http` and `ServeMux` only. No web frameworks, no ORMs, no
  testify (depguard bans them). Logging is `log/slog` with an injected logger.
- Tests: standard `testing` only, black-box `_test` packages, `t.Parallel()`,
  table-driven where it helps. Use `t.Context()`.
- The backend protocols implemented so far are not published, versioned specs
  we can implement from. Any endpoint change must be checked against the real
  CLI with `make e2e`.

## The tool-compatible APIs are contracts

The real CLIs depend on the tool-compatible APIs, and we cannot change the
CLIs. Until 1.0, everything else can break (ADR 73).

- The Pulumi Cloud-compatible API, the TFE-compatible API and the OpenTofu
  HTTP backend must match what each supported version of the real CLI sends
  and expects. Never break them. Check each change with `make e2e`.
- Each API keeps the shapes its tool expects. The Pulumi Cloud-compatible API:
  camelCase JSON, errors `{"code": <status>, "message": "..."}`. The
  TFE-compatible API: JSON:API documents and errors.
- Before 1.0, the console and the tool-neutral APIs can change and break.
  Do not deprecate first. When a change breaks an API that the console uses,
  merge the console change before the next server tag. Between the two
  merges, `main` of either repository can be broken.
- Do not write compatibility code, aliases or data conversions for a shape
  that no tag shipped. A shape that a tag shipped keeps its upgrade path
  (ADR 72).
- Give every feature a REST API (ADR 17). The console, the CLI and all other
  clients use only that API. Do not add a feature that only the console, a CLI
  or a configuration file can use.
- Make every endpoint declare its authorization actions and the object of
  each action. Make every change pass the policy check. A route without an
  action fails the route test. Put routes that need no sign-in on the explicit
  public list in that test.

## Security code: defense in depth (ADR 250)

Security code is code that handles, for example, authentication, authorization, tokens, secrets, cryptography, file paths, processes, network connections, untrusted formats, webhooks, backups or restore. This list is not complete. If you are not sure, treat the code as security code.

Never use only one layer. Never use either/or. Each layer must stop the attack alone, without help from the other layers. "It is checked upstream" is never a reason to remove, skip or omit a check.

The steps below are the minimum. They are not complete. The examples in each step are examples, not a complete list. For each input that comes from outside the process, do all of these steps:

1. Check the input at the boundary. Use an allowlist. Reject an input that is not valid. Do not change it to make it valid.
2. Encode or contain the value where you use it. Examples:
   - SQL: placeholders only.
   - Paths: `os.Root`, or `filepath.Rel` with a containment check after cleaning.
   - Processes: `exec.Command` with an argument list. Never use a shell.
   - HTML: `html/template`.
   - Headers and logs: remove CR and LF.
   - Network: check the resolved IP address when the connection opens.
3. Limit the effect. Examples: the smallest permission, a size limit, a timeout, a sandbox for untrusted work.
4. Check authorization at the point of use, for the object.
5. Fail closed. On an error or an unknown value, deny. Write the denial to the audit log.

Treat everything from outside the process as untrusted. Examples: HTTP requests, tool state files, webhooks, Git host data, runner output, configuration, environment variables, restored backups, and database rows from another version.

Write one test for each step. The test sends hostile input directly to that step, as if every other step had failed. A step without its own test is missing.

If a check looks redundant, keep it, and give it a comment. The comment says that the check is a defense-in-depth layer and names the other layers that it does not rely on. Example: `// Defense in depth: the handler validates the name too; this check stays so that a new caller cannot skip it.` Do not put an ADR number in the comment. Do not remove a check that has such a comment. If a check blocks your work, stop and report to the PM.

A security change is not done without a test for each step and a comment on each redundant check.

Never print, echo, log or copy the environment or a secret. Do not use `env`, `printenv`, `set` or `cat /proc/*/environ`. Do not use a command that writes a secret to the output. Do not write a secret to a file in the repository, a commit, a pull request, an issue or a message. Read a variable by its name only, for example to check that it is set. To get a test database, start your own local PostgreSQL. If a secret is printed by mistake, stop and report it to the PM, so that the owner can rotate it.

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
- Never add telemetry code, also not code that is off by default (ADR 74).
- AI-assisted changes follow `CONTRIBUTING.md` (ADR 76): disclose the tool,
  sign off each commit, understand all of it, and pass `make check`. Until
  the push ends on 2026-10-22, an agent signs off with its own commit author
  identity (`git commit -s`).
- An agent pastes the output of its checks into the description of each pull
  request that it opens (ADR 76).
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

The five canonical triage roles map to same-named labels: `needs-triage`, `needs-info`, `ready-for-agent`,
`ready-for-human`, `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context layout: one `GLOSSARY.md` and `docs/adr/` at the repo root. See `docs/agents/domain.md`.

<!-- rtk-instructions v2 -->

# Command output

Command output here is condensed to save tokens, keeping every signal and
dropping costly noise. Treat it as the complete result: run commands
normally, and batch related commands into one call to avoid extra turns.
Truncated results state their recovery path in their own output. Re-run a
command as `rtk proxy <cmd>` only when its result is unusable: empty when
output was clearly expected, contradicting its exit code, or garbled.
<!-- /rtk-instructions -->

## Repowise

The Repowise MCP server gives indexed documentation, symbols, history, decisions and code
health for this workspace. The index has no checkout. It shows pushed commits only. The
checkout is the truth.

- Call `list_repos` first. Pass `repo` (`opendustpan-opendustpan` or `opendustpan-console`)
  to limit a call to one repository. `get_symbol` always needs `repo`.
- Call `get_answer` for a question about how, where or why. Call `search_codebase` to find
  a symbol, a file or a concept. Call `get_context` for a summary of files.
- `get_symbol` returns a signature, a location and graph edges. It does not return source.
  Read the file at the returned line range.
- Call `get_risk` before you edit a hotspot. Call `get_why` before you refactor.
- `get_change_risk` scores one pushed commit by its sha. It does not score a range or
  uncommitted work.
- Read the file again when `_meta.stale_warning` is present, when `confidence` is `low`, or
  when a hit has `search_method: bm25`.
- Use `rg` to find every call site for a rename.

The skills `codebase-exploration`, `pre-modification-check`, `change-review`, `code-health`,
`dead-code-cleanup` and `architectural-decisions` in `.agents/skills/` use these tools.
