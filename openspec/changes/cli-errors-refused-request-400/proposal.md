---
version: 1
---

Issue: #1591, #1593, #2408, #2458.

## Problem

The problem: the client commands add `check the server log and run <command> again` to every HTTP failure. For a 4xx answer the same command fails again, and the error does not name the command that helps. A full disk also logs as a network cause.

## Evidence

- faultlog notes that syscall.Errno implements net.Error Proof: internal/api/faultlog/faultlog.go:130
- faultlog puts local storage errors before the network test Proof: internal/api/faultlog/faultlog.go:149
- No no_space cause exists on main Proof: code fact: rg -n -F 'no_space' pkg internal cmd gave 0 matches
- job.unsupported code exists on main Proof: internal/errcode/catalog_job.go:22

## Fix boundary

- The CLI adds the retry hint only for a connection error and a 5xx answer; for a 4xx answer it prints the server message once
- The runner management routes answer a JSON 404 with the NEW error code `runner.off` when no public URL is set
- faultlog tests syscall.Errno before net.Error and returns the NEW cause no_space for ENOSPC

Out of scope:

- Readiness or metric signal for repeated blob write failures
- Restore active-work hint text
- The host create-temp-admin reason wording
- The job.unsupported next step text
- The missing state blob cause and the backup hint

## Check

- Write failing tests for 4xx answers with no hint, and 500, 503 and connection failure with the hint, in NEW internal/cli/hint_test.go. Check: TestCliNoRetryHint
- Write failing tests for the runner-off JSON 404 with an access token, in NEW internal/api/runnerapi/off_test.go. Check: TestRunnerApiOff
- Write a failing faultlog test for ENOSPC to no_space in NEW internal/api/faultlog/cause_test.go. Check: TestFaultCauseNoSpace
- Wire the CLI hint helper, the runner-off handler and the faultlog order. Confirmed by: CI (make check). Check: TestCliNoRetryHint
- Call GET /api/runners/{namespace}/pools with an access token on a server with no public URL. Confirmed by: the driver's gates. Check: TestRunnerApiOff
- Run the real CLI namespace create for an existing name with an access token. BLOCKED by the namespace create route. A person's step. Check: `opendustpan namespace create`
- Update the docs page for the CLI hint rule and the no_space cause, and run `make check` with zero findings. Confirmed by: CI (make check). Check: `make check`

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The DUSTPAN_PUBLIC_URL setting exists (`internal/cli/settings.go:209 (func envSetting)`)
- The namespace create command exists (`internal/cli/namespace_create.go:5 (func NamespaceCreateCommand)`)
- The job.unsupported error code exists (`internal/errcode/catalog_job.go:22`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does any ADR decide CLI retry hints, runner API gating, or fault causes? The pack lists no ADRs or RFCs, so there is no quoted decision to follow and no ADR is touched.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
