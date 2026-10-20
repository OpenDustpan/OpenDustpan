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
counts in the inventory, and lets a gate require "no unexcepted finding at
or above severity X on this plan".

**Names (proposal, question 1).** The glossary term _Finding_ is an
unmanaged resource key (ADR 165), and ADR 224 decision 6 uses "exemption"
for a carve-out of a gate. This RFC says **scanner finding** for one result
of a scanner or a linter, **scanner report** for one uploaded report, and
**finding exception** for the owner's record that a scanner finding is
known and allowed for a time. The package is `pkg/scanreport`, not
`pkg/scan`, because `unmanaged.Scan` and the _Sweep_ Avoid list already use
that word. "Acceptance" is taken by another concept. The glossary lines are
at the end of this RFC.

## Facts from primary sources

Checked on 2026-10-20.

| Tool | SARIF 2.1.0 output | Scans plan JSON | Source |
| --- | --- | --- | --- |
| Checkov | `-o sarif` (`--output`, can repeat), folder with `--output-file-path` | yes, `--framework terraform_plan` | <https://www.checkov.io/2.Basics/CLI%20Command%20Reference.html> |
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
GitHub also keeps at most 1,000 locations for each result. These are a
useful reference for our limits.

## Where the gate model already fits

- A gate at `change.planned` reads facts (ADR 224, ADR 226). A new fact
  group `scannerFindings` fits there with no new decision point. A new group
  needs a new version of the fact schema (ADR 226 decision 2).
- A missing fact is a typed value with `available == false` (ADR 226
  decision 3). A missing or unreadable report then fails closed in the
  gate, as `resourcePolicy.unreadable` does.
- `change.apply` evaluates the gates again before each phase that changes
  (ADR 224 decision 3). An exception that expires between plan and apply is
  then caught.
- A stored plan has the SHA-256 of each part (ADR 102). A unit plan holds
  the binding of its stored plan, and the digest of a revision covers the
  part hashes (ADR 191 decisions 3 and 4). "The plan hash" in this RFC is
  the stored plan ID with the SHA-256 of its `plan-display` part.
- `state.write` (ADR 224 decision 16) has no plan: `plan.available` is
  always `false`. A write from a team's own CI has no stored plan to bind
  to (question 4). The recommendation is no `scannerFindings` fact there
  in v1.

## Options

### A. Where the scanners run

1. **The team's CI uploads** the reports. OpenDustpan runs no scanner. The
   CI shim of the secrets-out-of-state theme can do the upload later.
2. **A step of the plan job** on the runner. Not recommended. The owner's
   direction on RFC 49 says jobs run only IaC code and its dependencies, and
   ADR 226 decision 10 keeps producers of facts out of the job. The scanned
   code could also forge its own report in that step.
3. **An external check** (ADR 226 decision 10) that reads `plan-display`
   after the plan and returns a fact and a report. This keeps scanners out
   of jobs.
4. **A separate server-side post-plan phase** that has no access to the plan
   sandbox, and runs the scanner on the stored `plan-display`.
5. **The scanner report as a part of the stored plan** (precedent: the
   policy report of ADR 194 decision 8, which is read into the plan).
   The report is a part with its own SHA-256, so the plan digest covers it
   and no later upload can change what the approval saw.
   - For: the binding to the plan is by construction, with no second path to
     keep consistent; retention and redaction follow the plan.
   - Against: only the plan phase can write it, so it needs option 2, 3 or
     4 to produce the report, and CI uploads cannot use it. A late scan
     cannot be added after the plan is sealed. `pkg/job` must know the part
     kind, which ties a tool-neutral plan to one report format.
6. **Do nothing.** Teams keep the results in CI and in code scanning
   alerts, with no link to the unit.

**Recommendation.** CI upload now (A1). For runner jobs, an external check
that reads `plan-display` after the plan (A3), or a separate server-side
post-plan phase without access to the plan sandbox (A4). Not a step of the
plan job (A2). A5 is the one to weigh against A1 and A3 together: A5 gives
the strongest binding, A1 and A3 give the widest reach.

### B. What a report binds to

- **The plan hash**, for a scan of plan JSON. Only this binding can feed
  the approval condition.
- **The commit**, for a scan of source code. It shows in the views. It
  cannot prove that it covers the plan. ("Revision" is already a profile
  revision and a plan revision in the glossary, so this RFC says "commit".)

### C. Severity

1. **A fixed map.** Take the rule property `security-severity`
   (a number). The bands, with no gap: below 0.1 or above 10, or not a
   number, is no value and the `level` map applies; from 0.1 up to but not
   including 4.0 is `low`; from 4.0 up to but not including 7.0 is
   `medium`; from 7.0 up to but not including 9.0 is `high`; from 9.0 to
   10.0 is `critical`. With no value, map `level`: `error` to `high`,
   `warning` to `medium`, `note` to `low`, `none` to `info`. A value of 0
   is `info`. Cheap; it is wrong for tools that put all results at `error`
   or `warning`.
2. **The fixed map, plus overrides** by scanner and rule ID, stored on a
   namespace, a collection or a unit. More work; teams can correct a bad
   scanner.
3. **A map per scanner** in code. Exact for the 7 tools, but it breaks when
   a tool changes its output, and it does not cover a tool that we do not
   know.

Each scanner finding keeps the scanner's own level and properties beside the
mapped severity, so that a map change can run again on stored findings.

### D. Deduplication and fingerprints

- **Across commits and plans of one unit, for one scanner:** a fingerprint.
  The fingerprint is always SHA-256 over length-prefixed fields: scanner
  name, rule ID, then the fingerprint input. The input is the sorted keys
  and values of `partialFingerprints` when the report has them. Else it is
  the resource address or URN if known, else the file path. A scanner name
  or a rule ID can then never merge with the next field, and a value from
  the report is never used as a bare key (the same length-prefix rule as
  the cho-digest). The line is left out, so that an edit above the result
  does not make a new scanner finding. An exception matches by unit and
  fingerprint, and also by the severity that was mapped at accept time:
  the exception records that severity, and a finding with a higher severity
  does not match it.
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

A finding exception (`unmanaged` uses "ignore rule" for a similar idea) is a
stored record:

- unit reference, scanner, rule ID, fingerprint (or all fingerprints of
  the rule on the unit, as an option);
- the mapped severity at accept time (see D);
- owner (a person, never an agent or a token, as for approvals);
- reason, 1 to 1,000 characters;
- expiry: required, 1 day to 365 days (proposal). An expired exception
  counts as none;
- one audit entry for the create, each change and the revoke.

An excepted scanner finding stays visible with its exception. It does not
count in `unexcepted`.

### G. The approval condition

A new fact group `scannerFindings` at `change.planned` and `change.apply`.
The counts are per plan, and they are monotonic:

- A fact counts the union of all accepted reports that are bound to the
  plan (same plan ID and SHA-256 of `plan-display`). A later upload adds to
  the union and never lowers a count. A finding that is in one accepted
  report stays in the count even when a newer report of the same scanner
  does not have it.
- Eviction (the 1,000 reports per unit limit, retention) never removes a
  report that is bound to a stored plan still in retention.
- A report that was rejected is not accepted, and it adds nothing.

| Field | Meaning |
| --- | --- |
| `available` | `true` only when at least one accepted report is bound to this plan and the producer read it; `false` when the producer fails (see "The producer port") |
| `unexcepted` | a record of counts by severity: `critical`, `high`, `medium`, `low`, `info` |
| `excepted` | the same counts for excepted scanner findings |
| `scanners` | the set of scanner names with an accepted report for this plan |
| `uploaders` | for each scanner, the set of uploader credential kinds: `job-token`, `trust-token`, `access-token` |
| `rejected` | the count of rejected uploads for this plan |

"Rejected" means a refused upload (parse error, limit, bad binding). It is
never "available". "Available" means that a report was accepted and read.
They are two facts, so a gate can tell "the scan failed" from "no scan".

When a gate needs one scanner and it has no report, `available` stays
`true` if another scanner has one, because `available` is about the plan,
not about a scanner. The gate then asks for the scanner, as here, and a
missing one denies:

```cedar
when { !context.scannerFindings.available ||
       !context.scannerFindings.scanners.contains("trivy") ||
       context.scannerFindings.unexcepted.critical > 0 ||
       context.scannerFindings.unexcepted.high > 0 }
```

with outcome `deny` or `approval:<rule>`. A gate that must trust the report
more can also read `uploaders` and require a runner-produced report (see
"Trust model"). Options for the built-in gates: none (a starter pack in
`evaluate` mode), or a built-in gate like
`builtin/mandatory-resource-policy` (question 6).

## Proposal (author)

### The API (ADR 17)

All routes are on the OpenDustpan API. ADR 210 says a unit path holds the
unit ID, never `{tool}/{name}`. A client that has only the unit reference
(`<tool>:<namespace>/<name>`) resolves it to the ID first (question 8).
The upload and the exception routes take an `Idempotency-Key` (ADR 210
decision 6), so that a retry in CI does not store a second report.

| Route | Action | Object |
| --- | --- | --- |
| `POST /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/scanner-reports` | `unit:upload-scanner-report` (new) | the unit |
| `GET .../units/{unit_id}/scanner-reports`, `GET .../scanner-reports/{id}` | `unit:read` | the unit |
| `GET .../scanner-reports/{id}/raw` | `unit:read-scanner-reports` (new) and, for a plan-bound report, `job:read-plans` and `unit:read-secrets` | the unit |
| `GET .../units/{unit_id}/scanner-findings` | `unit:read` | the unit |
| `POST`, `DELETE .../units/{unit_id}/finding-exceptions[/{id}]` | `unit:except-scanner-finding` (new) | the unit |

The upload names its subject in query parameters: `plan=<pln-ID>` with
`planSha256=<hex>`, or `commit=<sha>`. It also names `scanner` and
`scannerVersion` when the report does not. Each change passes the policy
check of ADR 17. A job token of a plan phase may hold
`unit:upload-scanner-report` for its own unit only.

### Secrets from plan JSON

The `plan-display` part is stored unmasked in the server (ADR 102: the
route answers masked, and `reveal=true` needs `unit:read-secrets`). A
scanner that reads plan JSON can therefore copy a secret value into its
SARIF. Rules:

- Rows never store `snippet`, `contextRegion`, `codeFlows` or `fixes` of a
  SARIF result, and the parser drops them. The message text is stored; it
  can still hold a value, so it goes through the same redaction as other
  history items (ADR 151) before it is stored or shown.
- The raw report of a plan-bound scan needs `job:read-plans` and
  `unit:read-secrets` on the unit, or the same masking as the readable plan.
  The route answers masked by default, and unmasked only with those actions.
- Rows and the raw report fall under the redaction path and the item key of
  history items (ADR 151) and the history policy (ADR 152). A secret that is
  later registered is masked in the stored rows by the same job as for plans.

### Limits and rules at the boundary (proposal)

An input over a limit or against a rule is refused. It is never cut down
to fit. Each rule below has one test, named after the rule.

| Rule | Test name |
| --- | --- |
| Gzip only (`Content-Encoding: gzip` or none). Absolute caps: 8 MiB compressed and 32 MiB decoded, both read with `http.MaxBytesReader` or a counting reader. No ratio check: a fixed cap is simpler and stops the same attack. | `TestUploadRefusesOverCompressedCap`, `TestUploadRefusesOverDecodedCap`, `TestUploadRefusesNonGzipEncoding` |
| `version` must be `2.1.0`. Strict UTF-8. | `TestParseRefusesUnknownVersion`, `TestParseRefusesInvalidUTF8` |
| JSON depth at most 64, found by a token-stream pre-pass before the main decode. | `TestParseRefusesDeepJSON` |
| A duplicate JSON key in one object is refused, found by the same pre-pass. | `TestParseRefusesDuplicateKeys` |
| At most 20 runs, 25,000 results in total and 25,000 rules per report. | `TestParseRefusesOverRunResultRuleCount` |
| Per result: 10 locations kept (GitHub keeps 1,000; 10 is enough for display); `ruleId` 1 to 256 bytes from an allowlist of characters; `message.text` at most 4 KiB. | `TestParseRefusesLongRuleID`, `TestParseRefusesBadRuleIDChars`, `TestParseRefusesLongMessage` |
| Every field named `*.markdown` is dropped, never stored. | `TestParseDropsMarkdownFields` |
| The `properties` bag is dropped, except the keys the severity map reads (`security-severity`), each value at most 64 bytes; at most 32 keys and 4 KiB per bag. | `TestParseDropsUnknownProperties`, `TestParseRefusesOverBigProperties` |
| Message templates (`message.id`, `{0}` placeholders) are not expanded. Only `message.text` is read. | `TestParseDoesNotExpandMessageTemplates` |
| `artifactLocation.uri` is percent-decoded once, then refused if it has a scheme, an absolute path, a backslash, a NUL, a `..` segment, or a `uriBaseId` outside an allowlist (`%SRCROOT%`, none). The server never opens a file from it. | `TestParseRefusesBadArtifactURI` (one case each), `TestParseRefusesUnknownURIBaseID` |
| `helpUri`: `https` only, no userinfo, at most 2 KiB. The server never fetches it, and the console never links it automatically; it shows it as text with its host. | `TestParseRefusesBadHelpURI`, `TestConsoleShowsHelpURIAsText` |
| At most 100 uploads per unit per hour, and 1,000 stored reports per unit. The oldest go first, except a report bound to a stored plan in retention (see G). | `TestUploadRateLimit`, `TestEvictionKeepsPlanBoundReports` |

### Trust model

A report from a team's CI checks for mistakes. It does not protect against
a hostile uploader. A hostile uploader can send an empty SARIF, a report
that omits findings, or a scanner name of their choice, and the server
cannot tell. The RFC does not claim more.

To let a gate ask for more, the fact carries the credential kind of the
uploader for each report: `job-token` (a runner job), `trust-token` (a
workload identity, ADR 54) or `access-token` (a person or a token in CI).
A gate can then require `uploaders["trivy"].contains("job-token")`, which is
a report that a runner produced. An enforced scan gate needs this, and so it
needs runner jobs with stored plans. Self-declared scanner names in the
report are stored as given and are shown as "declared by the uploader"; the
fact `scanners` is made from them and has the same limit.

### Defense in depth: one line per layer (ADR 250)

| Layer | What it does | Its test |
| --- | --- | --- |
| Check at the boundary | the limits and rules above; unknown `version`, bad JSON or a bad subject refuse the whole report | fuzz the parser; one test for each rule |
| Encode at use | SQL placeholders only; strings stored as text; the console escapes each string and shows `helpUri` as text with its host; CR and LF removed from log values; finding text given to the assistant or an MCP tool is marked as untrusted data in a delimited field, never as instruction | a hostile string through the store, the log and the assistant output, with the parser skipped |
| Limit the effect | size, count and rate limits; the parser has a time limit; the raw blob is never parsed again at read; the assistant never gets a tool that a finding text can call | an upload at each limit plus one byte or one item |
| Authorize at use | the store checks the unit of the upload against the unit of the plan, and the action on that unit, in the same transaction as the write | a plan ID of another unit, and a token of another unit, with the route check skipped |
| Fail closed and record | a parse error, an unknown subject or a missing plan refuses the upload; each refusal writes an audit entry with the unit, the reason and the size; a producer error gives `available=false` | a refused upload gives an audit entry and no stored rows |

Finding text (message, rule ID, help URI, file path) comes from a scanner
that read untrusted code, so it is untrusted data wherever it goes: the
console, the logs, and the output of Dusty and the MCP endpoint. It is added
to the ADR 250 list of untrusted inputs in the ADR that accepts this RFC.

### Storage

- **The raw report** is one blob of a new kind `scanner-report`, with its
  SHA-256 and size, under the history model like a stored plan: its own item
  key seals it (ADR 102, ADR 152).
- **Scanner findings** are rows in the store: report ID, unit ID, scanner
  name and version, rule ID, mapped severity, scanner level and severity
  property, fingerprint, address or URN (opaque, optional), file and line,
  message, help URI, and the uploader credential kind of the report. No
  snippet, region, code flow or fix (see "Secrets from plan JSON"). The
  write carries the namespace fence.
- **Retention options:** keep raw reports as long as the stored plan (400
  days by default, ADR 152), or shorter (90 days) and keep the rows; for a
  commit-bound report, keep the newest N per unit and scanner. A
  plan-bound report is never evicted before its plan.

### The producer port and the inventory counts

- **One producer port in `pkg/gate`** for the `scannerFindings` group
  (ADR 226 decision 12), with a conformance suite in `gate/gatetest`. A
  producer error gives `available=false` and the gate fails closed.
- **The inventory counts.** `scanreport` is tool-neutral, and the inventory
  is its own domain package. ADR 13 has each tool package publish to the
  inventory in the same transaction as its own writes. A tool-neutral
  package that writes inventory counts is a new pattern. This RFC proposes a
  port in `scanreport`, `InventoryCounts`, that the store adapter calls in
  the same transaction as the report write. The inventory entry gets the
  counts of open scanner findings by severity for the plan or the commit of
  the newest report. This needs a decision in the ADR that accepts the RFC.

### Scans of plan JSON first

A plan-bound report can feed a gate; a commit-bound report cannot. Checkov,
Trivy, KICS and Snyk IaC scan plan JSON. So the author recommends plan-JSON
scans first, and commit-bound uploads only as display data.

### The first slice

Slice 1 is display data only. A report binds to a commit. No gate reads it,
and no fact exists yet.

1. `POST` and `GET` of scanner reports, and `GET` of scanner findings, for a
   unit, with the limits, the audit entries and the actions above.
2. The fixed severity map (option C1). Fingerprints by unit and scanner.
3. The counts in the inventory entry, through the port above.
4. The store port with a conformance suite in `storetest`, and adapters for
   SQLite and PostgreSQL.
5. **The e2e step:** OpenTofu runs `tofu plan -out` and `tofu show -json`
   on a fixture with one known misconfiguration. A pinned Trivy runs
   `trivy config --format sarif` on the plan JSON. The step uploads the
   report for the workspace with a commit subject, then reads the list and
   the inventory count. Trivy is under the Apache License 2.0 and is one
   binary. The e2e answers question 9 for Trivy and OpenTofu only; the other
   scanners stay open. A recorded SARIF file of each other scanner goes into
   parser tests, not into e2e. Those tests include a recorded Checkov SARIF
   from `--framework terraform_plan` on plan JSON.
6. The user documentation page for the upload.

**How the persona sees results in slice 1.** Through the API only (list of
reports and findings). Option: the inventory count column in the console,
as a console change in the other repository. Question 15 asks.

Later: the plan-hash subject with stored plans, the `scannerFindings` fact
and gates, exceptions, the console views (findings per stack and workspace,
the diff between two plans or commits, the cross-tool view, exceptions),
severity overrides, the upload from the CI shim, and Pulumi policy results
from a CI.

## Open questions

The owner keeps these. Each has the recommendation of the reviewer.

1. Names: "scanner finding", "scanner report", "finding exception" and the
   package `pkg/scanreport`, or other words? The glossary needs them before
   code. Recommendation: these.
2. Is this a new tool-neutral domain package, or a part of `job`, which
   owns stored plans? Recommendation: `pkg/scanreport`.
3. Where do scanners run for runner jobs? Recommendation: not a step of the
   plan job (RFC 49, ADR 226 decision 10, and the scanned code could forge
   its own report). CI upload now. For runner jobs, an external check that
   reads `plan-display` after the plan, or a separate server-side post-plan
   phase without access to the plan sandbox.
4. A write from a team's own CI has no stored plan. Can a gate at
   `state.write` read a report at all? Recommendation: no
   `scannerFindings` fact at `state.write` in v1. Later, optionally, bind
   to the commit claim in a trust-token (ADR 54), labeled as a check against
   mistakes. Enforced scan gates need runner jobs with stored plans.
5. Severity map: C1, C2 or C3? Who may set an override, and with which
   action?
6. Is there a built-in gate for scanner findings in `enforce` mode, or only
   a starter pack in `evaluate` mode?
7. Exceptions: does an exception need a second person? May the author of
   the change accept a finding on it? Is 365 days the right maximum? Can an
   exception cover a collection or a namespace?
8. ADR 210 puts the unit ID in the path. Does a CI with only the unit
   reference need a resolve call first, or does the upload take the
   reference in its body as a second, checked input?
9. Do Checkov, Trivy, KICS and Snyk IaC read OpenTofu plan JSON the same as
   Terraform plan JSON, for each supported OpenTofu version? The e2e step
   checks Trivy only; a spike must check the rest.
10. Pulumi policy packs: keep them only in the resource policy report of a
    stored plan, or also take the streaming engine events of a CI preview
    as a scanner report? The hidden `--output json` of the Pulumi CLI may
    change.
11. Retention of raw reports: as long as the stored plan, or shorter?
12. Deduplication across scanners: none, or a map of equal rules?
13. Mapping of file and line to an address: only from the report (E1), or
    a map in the OpenTofu tool package (E2)?
14. Does a commit-bound report also set a commit status on the forge?
15. Does slice 1 add the inventory count column to the console, or is the
    API enough?
16. Option A5 (the report as a part of the stored plan) against A1 and A3:
    which one first for runner jobs?

## Proposed glossary lines

For `GLOSSARY.md`, after acceptance. This PR does not edit the glossary.

- **Scanner finding**: one result of an IaC scanner or linter in a scanner
  report, kept with its mapped severity and fingerprint.
  _Avoid_: Finding (taken by unmanaged resources), alert, issue, violation
  (a policy result), vulnerability
- **Scanner report**: one uploaded report of one scanner, bound to a plan or
  to a commit.
  _Avoid_: Scan (taken by `unmanaged.Scan`), scan result, SARIF file as a
  label
- **Finding exception**: the record by a person that a scanner finding is
  known and allowed until an expiry date.
  _Avoid_: Acceptance, exemption (a carve-out in a gate), ignore rule
  (unmanaged), waiver, suppression

## Outcome

Empty until the RFC is accepted or dropped.
