---
rfc: 97
title: Test reports on stacks and workspaces, an extension of RFC 90 to test results
status: draft
created: 2026-10-20
---

# RFC 97. Test reports on stacks and workspaces, an extension of RFC 90 to test results

Status: draft. Nothing in this RFC is decided. No code exists for it. The
owner answers the questions at the end (ADR 15).

## Problem

Teams test infrastructure code: `tofu test` for OpenTofu modules, and unit
and integration tests of Pulumi programs in the test framework of each
language. The result ends as a red or green CI job. Nothing links a test
result to the stack or the workspace, to the commit or the plan that it
tested, or to the approval of that plan. A reviewer cannot see it, and a
gate cannot read it.

[RFC 90](0090-scanner-and-linter-reports.md) asks the same for scanner
reports, with SARIF. This RFC asks whether test results take the same path:
an upload bound to a unit and a subject, rows in a store, counts in the
inventory, and later a fact for a gate. It reuses the decisions that RFC 90
proposes for the API, the trust model, the limits and the defense in depth,
and asks only what is different for tests.

## Facts from primary sources

Checked on 2026-10-20.

| Tool | Native report | Source |
| --- | --- | --- |
| OpenTofu `tofu test` | `-json` (machine-readable output on stdout) and `-json-into=<file>`. No JUnit XML option. `-json-into` came with 1.12.0, from the change log. The version that added `tofu test -json` is UNCONFIRMED. | <https://opentofu.org/docs/cli/commands/test/>, the v1.12 change log |
| Terraform CLI `terraform test` | `-junit-xml=<file>`, generally available in 1.11.0 (experimental before). Not with `-cloud-run`. OpenDustpan does not support this CLI (RFC 53). | <https://developer.hashicorp.com/terraform/cli/commands/test>, the v1.11 change log |
| Pulumi CLI | No test runner and no report format of its own. Unit tests use the framework of each language (for example Mocha, `unittest`, `go test`, NUnit, JUnit 5) with the mocks of the SDK. A JUnit XML file comes from that framework, not from the CLI. | <https://www.pulumi.com/docs/iac/guides/testing/unit/> |

So JUnit XML is the common format of CI services and of most language test
frameworks. OpenTofu has only its JSON stream. The schema of that stream for
`tofu test` is not in the docs page that we read: UNCONFIRMED.

JUnit XML has no single published standard. CI services each read a
dialect: `testsuites`, `testsuite`, `testcase`, with `failure`, `error` and
`skipped` children, `time` attributes and `system-out` and `system-err`.

## What is different from scanner reports

- **The result is a pass or a fail of a test, not a finding with a
  severity.** A test report has suites, cases, durations and failure
  messages. RFC 90's severity map (its section C) and its fingerprints
  (section D) do not apply. A test case has a stable name in its suite.
- **A test runs code.** `tofu test` makes real resources unless each run
  block has `command = plan`, and a Pulumi integration test makes a stack.
  So a test run is not a scan of a plan. It can need cloud credentials, and
  it can leave resources when it fails.
- **The subject is a commit, rarely a plan.** A test of a module tests the
  code at a commit. A test does not bind to one stored plan of one unit.
  So a plan-bound test report (the strong case of RFC 90) is the exception.
- **A module is not a unit.** `tofu test` often tests a module that many
  workspaces use. The report binds to a unit only when the team says which
  unit, or to a source (a repository and a path) when the unit is not
  known.
- **Output holds secrets more often.** `system-out`, `system-err` and
  failure messages can hold values of the test, also secret ones.

## Options

### A. The format

- **A1. JUnit XML only.** The server parses one dialect, with an allowlist
  of elements and attributes. The runner or the CI converts the JSON of
  `tofu test` to JUnit XML.
- **A2. JUnit XML and the JSON of `tofu test`.** The server parses both.
  The JSON stream has no published schema, so each OpenTofu version needs
  an e2e check.
- **A3. One tool-neutral JSON document of OpenDustpan.** The CI or the
  runner converts each format. The server parses one small schema.

Recommendation: **A1**, with a converter for the JSON of `tofu test` in the
launcher, for runner jobs, and as a documented step for a team's own CI.
The server then has one parser for untrusted input, and the conversion runs
outside the server.

### B. Where tests run

- **B1. In the team's CI, with an upload.** As RFC 90 slice 1.
- **B2. In a step of a runner job.** The test step of the job. RFC 90's
  trust model says that a job token never gets the upload action, because
  the code under test can use it. The same holds here: the code under test
  can forge its own report.
- **B3. A separate test job of the runner**, with its own operation, its own
  credentials and its own cleanup of resources. This is a new operation of
  ADR 100 decision 3 and needs its own ADR.

Recommendation: **B1** now. B3 only after a spike with `tofu test` on a
runner, which proves the cleanup of resources after a failed test.

### C. The subject

- **C1. A commit of a unit** (as RFC 90).
- **C2. A commit of a source** (a repository and a path), with no unit. The
  report shows on each unit whose source matches.
- **C3. A stored plan** (as RFC 90), for a test with `command = plan` that
  the runner ran on the plan of the job.

Recommendation: **C1** in slice 1, and C2 as an open question.

### D. Trust

The trust model of RFC 90 applies with no change: a report from a team's CI
checks for mistakes. It does not protect against a hostile uploader. A gate
that must hold reads only a report from a producer that the code under
test cannot reach.

## Proposal (author)

### The API (ADR 17)

The routes follow RFC 90's API, with `test-reports` in place of
`scanner-reports` and `test-cases` in place of `scanner-findings`:

| Route | Action | Object |
| --- | --- | --- |
| `POST /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/test-reports` | `unit:upload-test-report` (new) | the unit |
| `GET .../units/{unit_id}/test-reports`, `GET .../test-reports/{id}` | `unit:read` | the unit |
| `GET .../test-reports/{id}/raw` | `unit:read-test-reports` (new) | the unit |
| `GET .../units/{unit_id}/test-cases` | `unit:read` | the unit |

The upload names `commit=<sha>`, and the tool and its version. It takes an
`Idempotency-Key` (ADR 210 decision 6).

### Limits and rules at the boundary (proposal)

- The body is at most 8 MiB, with at most 20,000 test cases.
- The parser reads XML with no DTD, no external entity and no entity
  expansion. It refuses a document with a `DOCTYPE`. Go's `encoding/xml`
  does not expand external entities, and the parser still refuses the
  declaration.
- It accepts the elements `testsuites`, `testsuite`, `testcase`,
  `failure`, `error`, `skipped`, `system-out`, `system-err` and
  `properties` only, and an allowlist of attributes. An unknown element
  refuses the report. It never repairs one.
- A name is at most 1 KiB, a message at most 16 KiB, of strict UTF-8.
- The server keeps no `system-out`, no `system-err` and no failure
  message in the rows. The raw report keeps them, sealed, behind
  `unit:read-test-reports`, because they can hold secrets.

### Defense in depth (ADR 250)

| Layer | What it does | Its test |
| --- | --- | --- |
| Check at the boundary | The rules above. A bad document refuses the whole report. | fuzz the parser. Send a `DOCTYPE`, an unknown element and each limit plus one |
| Encode at use | SQL placeholders only. The console escapes each string. CR and LF are removed from log values. Text given to Dusty or the MCP endpoint is marked as untrusted data. | a hostile name through the store, the log and the agent output, with the parser skipped |
| Limit the effect | Size, count and rate limits. The parser has a time limit. The raw blob is never parsed again at read. | an upload at each limit plus one byte or one item |
| Authorize at use | The store checks the unit of the upload and the action in the same transaction as the write. | a token of another unit, with the route check skipped |
| Fail closed and record | A parse error refuses the upload. Each refusal writes an audit entry. A producer error gives `available=false` for a later fact. | a refused upload gives an audit entry and no stored rows |

### Storage

- The raw report is a blob of a new kind `test-report`, sealed with its own
  item key (ADR 151), as RFC 90 proposes for a scanner report.
- Test cases are rows: report ID, unit ID, suite, name, outcome (`passed`,
  `failed`, `errored` or `skipped`) and duration in milliseconds. A row
  holds no failure message, because a message can hold a secret. The
  messages stay in the sealed raw report, behind `unit:read-test-reports`.
- Retention: the newest N reports for each unit and tool (question 6).

### The first slice

This slice only shows data. No gate reads it, and no fact exists.

1. `POST` and `GET` of test reports, and `GET` of test cases, for a unit
   and a commit, with the limits, the audit entries and the actions above.
2. The counts of the newest report in the inventory entry: passed, failed
   and skipped. The same port as RFC 90's inventory counts.
3. The store port with a conformance suite, and adapters for SQLite and
   PostgreSQL.
4. A converter from the JSON of `tofu test` to JUnit XML, as a command of
   the runner binary that a CI can also run.
5. **The e2e step:** the real OpenTofu CLI runs `tofu test -json` on a
   fixture with one passing and one failing run block, both with
   `command = plan`. The converter writes JUnit XML. The step uploads it
   for the workspace with a commit subject, and reads the list and the
   counts. A recorded JUnit XML file of a Pulumi program test (Go
   `go test` with a JUnit converter, and one of a JavaScript framework)
   goes into parser tests.
6. The user documentation page for the upload.

Later: a test job of the runner (B3), the source subject (C2), a fact
`tests` for a gate, and the console views.

## Open questions

The owner keeps these. Each recommendation is the author's.

1. Names: "test report" and "test case", and the package: a part of RFC
   90's `pkg/scanreport` (renamed to a word for both), or a package of its
   own? Recommendation: one package for both kinds of report, because the
   upload, the trust model, the storage and the inventory port are the
   same. The name waits for RFC 90 question 1.
2. The format: A1, A2 or A3? Recommendation: A1.
3. Does the converter of `tofu test -json` live in the runner binary, or in
   a separate small binary? Recommendation: the runner binary, with a
   subcommand that a CI can run without a server.
4. Does a report on a module bind to each unit that uses the module (C2)?
   This needs the map from a source path to units of RFC 24.
5. Is a test job of the runner (B3) a fifth operation of ADR 100, and who
   cleans up the resources of a failed test?
6. Retention of raw reports: the newest N for each unit and tool, or the
   retention of RFC 90?
7. Does a Pulumi preview or update with `--json` count as a test report?
   Recommendation: no. It is a plan or an update, and the job model already
   holds it.
8. Is the order of this RFC and RFC 90 one slice (one upload API for both),
   or RFC 90 first? Recommendation: RFC 90 first, then this RFC as a
   second kind on the same API.
9. Does a job token hold `unit:upload-test-report`? Recommendation: no,
   so that a job cannot report on its own result. The upload comes from the
   CI of the team with a principal of its own.

## Proposed glossary lines

For `GLOSSARY.md`, after acceptance. This PR does not edit the glossary.

- **Test report**: one uploaded report of one run of the tests of a unit,
  bound to a commit, with its test cases.
  _Avoid_: Test run (a run is a word of each tool), result, check (an
  external check, ADR 226)
- **Test case**: one test in a test report, with its suite, its outcome and
  its duration.
  _Avoid_: Test (alone), assertion, check

## Outcome

Empty until the RFC is accepted (link the ADR) or dropped (say why).
