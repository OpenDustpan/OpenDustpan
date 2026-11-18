# Architecture review, round 15

Range: `ce2265d3..a39ce447` on `main` (493 commits, 3472 files). The last round was 14 (`867814fa..ce2265d3`).

Method: the areas were ranked by risk (security code, new ports and services) and by the size of the change. Six areas were reviewed in depth against the "Current rule" column of `docs/adr/INDEX.md`, `GLOSSARY.md` and `CLAUDE.md`. Each finding was read again in the code at `a39ce447` before it was filed. A duplicate search was done for each.

## Checks run

- `go build ./...`: pass.
- depguard scope: the new packages `internal/jobunits`, `internal/jobproxysvc`, `internal/proxysessionsvc`, `internal/removalalertsvc`, `pkg/jobtoken`, `pkg/gate/limitgroups` and `pkg/egress/plainconn` import no `net/http`, `database/sql`, driver, `pkg/store` or `internal/api` package.
- Each test package added in the range has a `leak_test.go`.
- `scripts/adr-index.py --changes-from 296 --check`: 338 ADRs, the links and the index are correct.
- `make check` and the full test suites were not run (out of scope).

## Findings, ranked by severity

| Severity | Issue | Commit | Summary |
| --- | --- | --- | --- |
| high | #3540 | 231ce5bf, 09878f97 | The built-in stores do not set the job token store. The binary issues no job tokens; only e2e and servertest add the option. |
| high | #3543 | 09878f97 | A job token expires at the lease end of the issue time. A claim renewal does not extend it, so a step longer than the lease gets 401. |
| high | #3545 | 50e77a78 | A backup completes with registry files over the restore limit; its restore always refuses it. The backup-side limit has no production caller. |
| medium-high | #3546 | 71c1cae2 | A save of a new upstream proxy URL keeps the write-only credential, which then goes to the new host. |
| medium | #3547 | 3b39c78f, 09878f97 | The job token binds the starter by subject, not by stable ID. A re-created principal acts for an earlier job. |
| medium | #3548 | 4aae18d6 | The trust-rule service does not authorize `namespace:manage-trust-rules` for bundle changes and rule updates (route layer only). |
| medium | #3549 | 6e856cf5, 71c1cae2 | Two joins of proxy rule scopes disagree. Sets that save as valid fail each proxy session of the unit. |
| medium | #3550 | 93683580, 8a28201b | A failed read of the removal alert limits or the break-glass max length falls back to the default with no record. |
| medium | #3551 | 4aae18d6 | Trust bundle audit entries do not name the version; retired versions are deleted with no record. |
| medium | #3552 | b13e8b20 | A damaged job row can be counted but not listed or canceled through the API (follow-up to #2855). |
| medium | #3553 | 50e77a78 | Restore keeps a compatibility path for a backup with no `registryFiles` section, a shape that no tag shipped. |
| medium | #3554 | 5ba918ba, 5aa56d21 | Unmanaged sweeps refer to their target by name (ADR 362). |
| medium | #3555 | b9d81125 | IP allowlists have two switches and two gates (setting vs process flag), with two bootstrap rules (ADR 55 decision 8). |
| low | #3556 | several | Minor findings: proxy paths do not check runner isolation; a bad stored proxy rule set answers 422; unknown password provider is not audited; decision-record port in the HTTP package; a slot re-check comment names a missing layer; job token rows are never deleted. |

Covered by open issues, not filed again: #1434 (the job service does not refuse the `drift-` key prefix that `internal/api/triggers/serve.go:305` says it does), #2011 (break-glass disable notices). The fresh-auth refusals of destroy approval and job confirmation are covered by #2689, #903 and ADR 385.

## Areas covered in depth

1. Job proxy and proxy sessions: `internal/proxysessionsvc`, `internal/jobproxysvc`, `pkg/jobproxy`, `internal/api/server/proxyrules.go`, `internal/api/runnerapi` proxy and credential paths, `pkg/egress/plainconn`.
2. Job tokens: `pkg/jobtoken`, the store adapters, `pkg/accesstoken/scope.go`, `internal/api/server/tokens.go`, `internal/jobunits`, ADR 369, 370, 371, 386.
3. Break-glass, principal removal, temporary administrators, password sign-in.
4. Gates, trust rules and approvals: `pkg/trustrule`, `internal/trustrulesvc`, `internal/api/trustrules`, `pkg/gate/limitgroups`, `pkg/gate/cedargo`, `internal/api/decisionrecord`, `pkg/job/service_approve.go`.
5. Runner pools, trigger claims, quota counter, superseded checkpoint sweep. ADR 368 (apply claims) has no code in the range.
6. Backup and restore, registry service, unmanaged scans, IP allowlist wiring, `pkg/secret/itemkeyblob.go`.

## Areas not covered

- `openspec/changes/` (about 20% of the changed lines): spec text only, not reviewed against the code.
- `clients/typescript` and `internal/api/apidesc` (generated).
- `e2e/` (including `e2e/docs_commands.go`, `e2e/fetch.go`, `e2e/harness.go`) and `load/`.
- `internal/cli` (`collection.go`, `placement.go`) and `cmd/opendustpan/main.go`.
- `pkg/stack` and `pkg/store/*/updates.go` beyond the superseded checkpoint sweep; `pkg/store/*/unit_state.go`.
- `pkg/notify` and `notifytest`; `pkg/workspace` (state encrypted bit); `pkg/access/service.go` beyond `protected_batch.go`.
- `internal/api/server` routes outside proxy rules, tokens, principals, review and password sign-in.
- `internal/agent`, `internal/filescheck`, `scripts/` (`checkaiprspec`, `checkchangearchived`, `checkprbody`), `deploy/helm`, `docs/`.
