---
rfc: 90
title: Reports of IaC scanners and linters on stacks and workspaces, SARIF first
status: draft
created: 2026-10-20
---

# RFC 90. Reports of IaC scanners and linters on stacks and workspaces, SARIF first

Status: draft. Nothing in this RFC is decided. No code exists for it. The
owner answers the questions at the end.

## Problem

Teams already run IaC scanners and linters: Checkov, Trivy, KICS, tfsec,
Semgrep, Snyk IaC and TFLint. Their output ends as a red CI job or as an
alert of a code scanning service. Nothing links a result to the stack or
the workspace, to the plan that was scanned, or to the approval of that
plan. A reviewer of a change order cannot see the results, and a gate
cannot read them.

The Pulumi CLI has its own form: the results of a policy pack in
`pulumi preview --policy-pack`. ADR 194 decision 8 already reads these
results into the resource policy report of a stored plan, and ADR 226
decision 7 makes them the `resourcePolicy` fact. That path covers runner
jobs only.

This RFC asks how OpenDustpan takes in these reports, stores them, shows
counts in the inventory, and lets a gate require "no unaccepted finding at
or above severity X on this plan".

**A name.** The glossary term _Finding_ is an unmanaged resource key (ADR
165). This RFC says **scan finding** for one result of a scanner or a
linter, and **scan report** for one uploaded report. Both are proposals
(question 1).

## Facts from primary sources

Checked on 2026-10-20.

| Tool | SARIF 2.1.0 output | Scans plan JSON | Source |
| --- | --- | --- | --- |
| Checkov | `-o sarif` (`--output`, can repeat), file with `--output-file-path` | yes, `--framework terraform_plan` | <https://www.checkov.io/2.Basics/CLI%20Command%20Reference.html> |
| Trivy (`trivy config`, or `--scanners misconfig`) | `--format sarif`, file with `-o` | yes, "Trivy can scan Terraform Plan files (snapshots) or their JSON representations" | <https://trivy.dev/latest/docs/configuration/reporting/>, <https://trivy.dev/latest/docs/coverage/iac/terraform/> |
| KICS | `--report-formats sarif`, directory with `--output-path` | yes, it scans the `planned_values` of a plan in JSON | <https://docs.kics.io/latest/commands/>, <https://docs.kics.io/latest/platforms/> |
| tfsec | `--format sarif` | not checked | <https://github.com/aquasecurity/tfsec>: "Tfsec is now part of Trivy". The README asks users to move to Trivy and says tfsec stays available "for the time being". |
| Semgrep | `--sarif` (stdout), or `--sarif-output=<file>` | no plan mode in its docs; it matches code patterns | <https://docs.semgrep.dev/cli-reference> |
| Snyk IaC (`snyk iac test`) | `--sarif`, or `--sarif-file-output=<file>` | yes; `--scan=resource-changes` (the default) or `--scan=planned-values` | <https://docs.snyk.io/developer-tools/snyk-cli/commands/iac-test> |
| TFLint | `--format sarif` | no plan mode in its docs; it reads HCL | <https://github.com/terraform-linters/tflint/blob/master/docs/user-guide/config.md> |

So tfsec needs no own work: a team that uses it can move to Trivy, and
its SARIF upload works the same.

The scanners above document plan JSON from Terraform. OpenTofu writes plan
JSON with `tofu show -json`. That the scanners read it the same is likely,
but not checked (question 9).

**The Pulumi CLI** (checked against the source of `pulumi/pulumi` on
`master`):

- `--policy-pack` and `--policy-pack-config` run packs in a preview
  (<https://www.pulumi.com/docs/iac/cli/commands/pulumi_preview/>).
- `pulumi preview --json` writes a summary that drops policy events. The
  source says: "At this point in time, we don't handle policy events in JSON
  serialization" (`pkg/backend/display/json.go`).
- With `PULUMI_ENABLE_STREAMING_JSON_PREVIEW` set, `--json` streams engine
  events instead. An engine event can hold a `policyEvent` with
  `resourceUrn`, `message`, `policyName`, `policyPackName`,
  `policyPackVersion`, `enforcementLevel` and `severity`
  (`sdk/go/common/apitype/events.go`).
- A hidden `--output json` flag exists on `master`. Its shape for policy
  results is not checked.
- No SARIF output exists for policy packs.

**SARIF itself** (<https://docs.oasis-open.org/sarif/sarif/v2.1.0/sarif-v2.1.0.html>):
a result has `ruleId`, `level` (`error`, `warning`, `note` or `none`),
`message`, `locations` (physical: file and region; logical: for example a
resource name) and `partialFingerprints`. A rule can carry `helpUri` and
properties. GitHub reads the rule property `security-severity` (0.1 to 10)
and maps it to `low`, `medium`, `high` and `critical`. Its upload limits
are 10 MB of gzip, 20 runs per file and 25,000 results per run
(<https://docs.github.com/en/code-security/code-scanning/integrating-with-code-scanning/sarif-support-for-code-scanning>).
These are a useful reference for our limits.

## Where the gate model already fits

- A gate at `change.planned` reads facts (ADR 224, ADR 226). A new fact
  group `scanFindings` fits there with no new decision point. A new group
  needs a new version of the fact schema (ADR 226 decision 2).
- A missing fact is a typed value with `available == false` (ADR 226
  decision 3). A missing or unreadable report then fails closed in the
  gate, as `resourcePolicy.unreadable` does.
- `change.apply` evaluates the gates again before each phase that changes
  (ADR 224 decision 3). An acceptance that expires between plan and apply is
  then caught.
- A stored plan has the SHA-256 of each part (ADR 102). A unit plan holds
  the binding of its stored plan, and the digest of a revision covers the
  part hashes (ADR 191 decisions 3 and 4). "The plan hash" in this RFC is
  the stored plan ID with the SHA-256 of its `plan-display` part.
- `state.write` (ADR 224 decision 16) has no plan: `plan.available` is
  always `false`. A write from a team's own CI has no stored plan to bind
  to (question 4).

## Options

### A. Where the scanners run

1. **The team's CI uploads** the reports. OpenDustpan runs no scanner. The
   CI shim of the secrets-out-of-state theme can do the upload later.
2. **A step of the plan job** on the runner. The owner's direction on RFC
   49 says jobs run only IaC code and its dependencies. A scanner may be
   outside that rule, or a dependency like a policy pack (question 3).
3. **An external check** (ADR 226 decision 10) reads the plan and returns
   a fact and a report. This keeps scanners out of jobs, but the service
   must read the plan.
4. **Do nothing.** Teams keep the results in CI and in code scanning
   alerts, with no link to the unit.

### B. What a report binds to

- **The plan hash**, for a scan of plan JSON. Only this binding can feed
  the approval condition.
- **The commit**, for a scan of source code. It shows in the views and in
  a commit status. It cannot prove that it covers the plan. ("Revision"
  is already a profile revision and a plan revision in the glossary, so
  this RFC says "commit".)

### C. Severity

1. **A fixed map.** Take the rule property `security-severity` with the
   GitHub bands. With none, map `level`: `error` to `high`, `warning` to
   `medium`, `note` to `low`, `none` to `info`. Cheap; it is wrong for
   tools that put all results at `error` or `warning`.
2. **The fixed map, plus overrides** by scanner and rule ID, stored on a
   namespace, a collection or a unit. More work; teams can correct a bad
   scanner.
3. **A map per scanner** in code. Exact for the 7 tools, but it breaks when
   a tool changes its output, and it does not cover a tool that we do not
   know.

Each scan finding keeps the scanner's own level and properties beside the
mapped severity, so that a map change can run again on stored findings.

### D. Deduplication

- **Across commits and plans of one unit, for one scanner:** a fingerprint.
  Take `partialFingerprints` when the report has them (sorted keys and
  values, SHA-256). Else compute SHA-256 over scanner name, rule ID, the
  resource address or URN if known, else the file path. Leave out the line,
  so that an edit above the result does not make a new scan finding. An
  acceptance matches by unit and fingerprint.
- **Across scanners:** options are none (each scanner has its own rows),
  or a stored map of equal rules (for example a Checkov ID to a Trivy ID).
  A map costs upkeep for each scanner version.

### E. File and line to a resource address or a URN

1. **Store only what the report gives.** File and line always. An address
   or a URN only when a logical location of the result gives one, or a
   scan of plan JSON names it. No parsing of code.
2. **The OpenTofu tool package maps HCL blocks to addresses** for a scan of
   source code. Limits: a block with `count` or `for_each`, or in a module
   that is called more than once, gives more than one address. The server
   would need the code, and it would parse untrusted HCL.
3. **For the Pulumi CLI**, a scan of the program source (for example
   Semgrep on TypeScript) cannot give a URN in a reliable way. A policy pack
   gives the URN directly.

The address and the URN stay opaque strings in shared code. Only the tool
package of the unit may read them (ADR 13).

### F. Exceptions

An acceptance (question 1 for the name; `unmanaged` uses "ignore rule"
for a similar idea) is a stored record:

- unit reference, scanner, rule ID, fingerprint (or all fingerprints of
  the rule on the unit, as an option);
- owner (a person, never an agent or a token, as for approvals);
- reason, 1 to 1,000 characters;
- expiry: required, 1 day to 365 days (proposal). An expired acceptance
  counts as none;
- one audit entry for the create, each change and the revoke.

An accepted scan finding stays visible with its acceptance. It does not
count in `unaccepted`.

### G. The approval condition

A new fact group `scanFindings` at `change.planned` and `change.apply`:

| Field | Meaning |
| --- | --- |
| `available` | `true` only when a report for this plan hash exists and was read |
| `unaccepted` | a record of counts by severity: `critical`, `high`, `medium`, `low`, `info` |
| `accepted` | the same counts for accepted scan findings |
| `scanners` | the set of scanner names with a report for this plan hash |
| `rejected` | the count of rejected uploads for this plan hash |

The gate for "no unaccepted high finding on this plan hash":

```cedar
when { !context.scanFindings.available ||
       context.scanFindings.unaccepted.critical > 0 ||
       context.scanFindings.unaccepted.high > 0 }
```

with outcome `deny` or `approval:<rule>`. Options for the built-in gates:
none (a starter pack in `evaluate` mode), or a built-in gate like
`builtin/mandatory-resource-policy` (question 6).

## Proposal (author)

### The API (ADR 17)

All routes are on the OpenDustpan API. ADR 210 says a unit path holds the
unit ID, never `{tool}/{name}`. A client that has only the unit reference
(`<tool>:<namespace>/<name>`) resolves it to the ID first (question 8).

| Route | Action | Object |
| --- | --- | --- |
| `POST /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/scan-reports` | `unit:upload-scan-report` (new) | the unit |
| `GET .../units/{unit_id}/scan-reports`, `GET .../scan-reports/{id}` | `unit:read` | the unit |
| `GET .../scan-reports/{id}/raw` | `unit:read-scan-reports` (new; the raw report can hold code snippets) | the unit |
| `GET .../units/{unit_id}/scan-findings` | `unit:read` | the unit |
| `POST`, `DELETE .../units/{unit_id}/scan-acceptances[/{id}]` | `unit:accept-scan-finding` (new) | the unit |

The upload names its subject in query parameters: `plan=<pln-ID>` with
`planSha256=<hex>`, or `commit=<sha>`. It also names `scanner` and
`scannerVersion` when the report does not. Each change passes the policy
check of ADR 17. A job token of a plan phase may hold
`unit:upload-scan-report` for its own unit only.

### Limits at the boundary (proposal)

- Body: at most 16 MiB after gzip decode, read with `http.MaxBytesReader`;
  a decode ratio above 50:1 is refused.
- `version` must be `2.1.0`. JSON depth at most 64. Strict UTF-8.
- At most 20 runs, 25,000 results in total and 25,000 rules per report.
- Per result: 10 locations kept; `ruleId` 1 to 256 bytes from an allowlist
  of characters; `message.text` at most 4 KiB; file path at most 1 KiB,
  relative, no `..`, no NUL. `message.markdown` is never stored.
- `helpUri`: `https` only, at most 2 KiB.
- At most 100 uploads per unit per hour, and 1,000 stored reports per unit
  (the oldest go first).

An input over a limit is refused. It is never cut down to fit.

### Defense in depth: one line per layer (ADR 250)

| Layer | What it does | Its test |
| --- | --- | --- |
| Check at the boundary | the limits above; unknown `version`, bad JSON or a bad subject refuse the whole report | fuzz the parser; one test for each limit |
| Encode at use | SQL placeholders only; strings stored as text; the console escapes each string and shows `helpUri` as text with its host; CR and LF removed from log values | a hostile string through the store and the log, with the parser skipped |
| Limit the effect | size, count and rate limits; the parser has a time limit; the raw blob is never parsed again at read | an upload at each limit plus one byte or one item |
| Authorize at use | the store checks the unit of the upload against the unit of the plan, and the action on that unit, in the same transaction as the write | a plan ID of another unit, and a token of another unit, with the route check skipped |
| Fail closed and record | a parse error, an unknown subject or a missing plan refuses the upload; each refusal writes an audit entry with the unit, the reason and the size; a refused upload for a plan hash makes `scanFindings.available` stay `false` | a refused upload gives an audit entry and no stored rows |

### Storage

- **The raw report** is one blob of a new kind `scan-report`, with its
  SHA-256 and size, under the history model like a stored plan: its own item
  key seals it (ADR 102, ADR 152).
- **Scan findings** are rows in the store: report ID, unit ID, scanner
  name and version, rule ID, mapped severity, scanner level and severity
  property, fingerprint, address or URN (opaque, optional), file and line,
  message, help URI. The write carries the namespace fence.
- **The inventory entry** gets the counts of open scan findings by severity
  for the newest report of each scanner. The same transaction writes them
  (ADR 13).
- **Retention options:** keep raw reports as long as the stored plan (400
  days by default, ADR 152), or shorter (90 days) and keep the rows; for a
  commit-bound report, keep the newest N per unit and scanner.

### Scans of plan JSON first

A plan-bound report can feed a gate; a commit-bound report cannot. Checkov,
Trivy, KICS and Snyk IaC scan plan JSON. So the author recommends plan-JSON
scans first, and commit-bound uploads only as display data.

### The first slice

1. `POST` and `GET` of scan reports, and `GET` of scan findings, for a unit,
   with the limits, the audit entries and the actions above.
2. The fixed severity map (option C1). Fingerprints by unit and scanner.
3. The counts in the inventory entry.
4. The store port with a conformance suite in `storetest`, and adapters for
   SQLite and PostgreSQL.
5. **The e2e step:** OpenTofu runs `tofu plan -out` and `tofu show -json`
   on a fixture with one known misconfiguration. A pinned Trivy runs
   `trivy config --format sarif` on the plan JSON. The step uploads the
   report for the workspace with a commit subject, then reads the list and
   the inventory count. Trivy is under the Apache License 2.0 and is one
   binary. A recorded SARIF file of each other scanner goes into parser
   tests, not into e2e.
6. The user documentation page for the upload.

Later: the plan-hash subject with stored plans, the `scanFindings` fact and
gates, acceptances, the console views (findings per stack and workspace,
the diff between two plans or commits, the cross-tool view, acceptances),
severity overrides, the upload from the CI shim, and Pulumi policy results
from a CI.

## Open questions

1. Names: "scan finding", "scan report" and "acceptance", or other words?
   The glossary needs them before code.
2. Is this a new tool-neutral domain package (for example `pkg/scan`), or
   a part of `job`, which owns stored plans?
3. Where do scanners run for runner jobs: a step of the plan job (is a
   scanner "IaC code and its dependencies"?), an external check, or only
   the team's CI?
4. A write from a team's own CI has no stored plan. Can a gate at
   `state.write` read a report at all? If so, what binds it: the SHA-256 of
   a plan JSON that the CI sends and the server cannot verify, or nothing?
5. Severity map: C1, C2 or C3? Who may set an override, and with which
   action?
6. Is there a built-in gate for scan findings in `enforce` mode, or only a
   starter pack in `evaluate` mode?
7. Acceptances: does an acceptance need a second person? May the author
   of the change accept a finding on it? Is 365 days the right maximum? Can
   an acceptance cover a collection or a namespace?
8. ADR 210 puts the unit ID in the path. Does a CI with only the unit
   reference need a resolve call first, or does the upload take the
   reference in its body as a second, checked input?
9. Do Checkov, Trivy, KICS and Snyk IaC read OpenTofu plan JSON the same as
   Terraform plan JSON, for each supported OpenTofu version? A spike must
   check it.
10. Pulumi policy packs: keep them only in the resource policy report of a
    stored plan, or also take the streaming engine events of a CI preview
    as a scan report? The hidden `--output json` of the Pulumi CLI may
    change.
11. Retention of raw reports: as long as the stored plan, or shorter?
12. Deduplication across scanners: none, or a map of equal rules?
13. Mapping of file and line to an address: only from the report (E1), or
    a map in the OpenTofu tool package (E2)?
14. Does a commit-bound report also set a commit status on the forge?

## Outcome

Empty until the RFC is accepted or dropped.
