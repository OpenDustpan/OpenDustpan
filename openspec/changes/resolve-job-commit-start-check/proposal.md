---
version: 1
---

Issue: #2922.

## Problem

The server never resolves the head commit at job start, so the protected-branch condition of the plan-only secrets rule fails closed.

## Evidence

- The job start handler exists in internal/api/jobs/start.go. Proof: paths: internal/api/jobs/start.go exists; main: internal/api/jobs/start.go:285 (func Handler.startRequest)
- The issue states the revision comes from the start request with no record of head resolution. Proof: Issue #2922 body: job.Location.Revision comes from the start request near line 320 of internal/api/jobs/start.go.

## Fix boundary

- Resolve the job commit at start on the server from the Git host when the opt-in trusted-source-check setting is on.
- Store the branch, the resolved commit, the repository ID, and the trusted-source verdict on the job.
- Apply the trusted-source checks of ADR 353 decision 4 and fail closed for protected-branch secrets only.

Out of scope:

- The plan-only secrets rule beyond accepting the protected-branch condition in #1026 PR 3b.
- New forge adapters.

## Check

- BLOCKED by the missing documented jobs start route in the pack: write the failing test of head resolution on the existing start path in internal/api/jobs/start.go with unit job-start authorization and an access-token credential. Confirmed by: the driver's gates. Check: NEW TestStartResolvesHeadCommit in internal/api/jobs fails on main.
- BLOCKED by the missing documented jobs start route in the pack: write the failing hostile tests of the verdict (force push, merge around gate, missing rules, timeout, rate limit) on the same start path with an access-token credential. Confirmed by: the driver's gates. Check: NEW TestStartTrustedVerdict in internal/api/jobs fails on main.
- BLOCKED by the missing documented jobs start route in the pack: wire the trusted-source-check setting, ResolveCommitAtStart, and the audit record into the existing start path in internal/api/jobs/start.go with unit job-start authorization and an access-token credential. Confirmed by: the driver's gates. Check: Existing start path in internal/api/jobs/start.go wired with unit job-start authorization and access-token credential.
- BLOCKED by the missing documented jobs start route in the pack: add the docs page for the setting and an e2e step that starts a job with an access-token credential and reads the resolved commit and verdict. Confirmed by: the driver's gates. Check: Docs page renders for the trusted-source-check setting.
- BLOCKED by the missing documented jobs start route in the pack: run the wiring, the audit record, the e2e step, the docs page, and a usability check of the start output with an access-token credential. Confirmed by: the driver's gates. Check: `make check` with zero findings.

Depends on:

- write-back-record-audit-entry
- `write-back-record-audit-entry` (order of the milestone review)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The job start request path exists. (`internal/api/jobs/start.go:285 (func Handler.startRequest)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 353: Gates at every step of a plan that runs untrusted code: every control is an opt-in setting** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current), 2 (current), 4 (current), 6 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Every control is a setting that a team turns on (owner, 2026-11-07)."
- Quote: "The default keeps today's behavior (owner, 2026-11-07)."
- Quote: "A trusted source has the repository ID and an exact name in `protectedBranches`."
- Quote: "Security rules stay (owner, 2026-11-07)."
- Effect: The check is an opt-in per-unit setting, default off, that keeps current behavior. The trusted-source definition and fail-closed rule follow decision 4 and the security rules of decision 6.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Must every job resolve the commit at start? No. ADR 353 decisions 1 and 2: the check is opt-in with default off and shows its effective value.
- What defines a trusted source? ADR 353 decision 4: repository ID, exact protected-branch name, no merge around the gate, no force push since last approved apply, host-read branch rules; host cannot report means untrusted.
- What happens when the forge times out or cannot report? Fail closed for the protected-branch condition only; the start still succeeds and the verdict is untrusted, per ADR 353 decisions 4 and 6.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
