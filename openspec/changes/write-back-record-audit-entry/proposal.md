---
version: 1
---

Issue: #2466, #2469, #2481.

## Problem

A developer runs a write-back job and a forged commit with the job trailer can claim delivery; the developer needs a failed job with a clear reason and a security record that names the failed check.

## Evidence

- FindCommit returns forge.ErrUntrustedCommit on a failed trust check. Proof: pkg/forge/github/commit.go:934:// forge.ErrUntrustedCommit, so that the caller never records it and never
- ErrUntrustedCommit exists on main. Proof: pkg/forge/forge.go:84 (var ErrUntrustedCommit)
- The security audit kind exists on main. Proof: pkg/audit/audit.go:62 (audit kind security)
- Prover lives in pkg/forge/github on main. Proof: pkg/forge/github/proof.go:21 (type Prover)
- The context pack lists zero ADRs and zero RFCs. Proof: adrs: [], rfcs: []

## Fix boundary

- Takeover refusal step writes one security audit entry with job, repository, commit ID, and failed check, with no host text.
- Takeover refusal step stops on forge.ErrUntrustedCommit, fails the job with no retry, and shows the reason in GET /api/dustpan/v1/namespaces/{}/jobs/{}.
- Proof binds the stable change digest and the branch name; other content or another branch is refused; parent must equal base.
- NEW Prover port in pkg/forge with conformance cases; GitHub adapter uses it.

Out of scope:

- MFA passkey findings of arch-round-12 items 1-4 and 10
- Expr limiter comment, e2e worker coverage, DeleteBranch head pin, and the full write-back executor M5-39 beyond the refusal record path

## Check

- Add failing takeover test in NEW pkg/forge/takeover_refusal_test.go for forged trailer commit: one security entry, failed job, no delivered record. Check: TestTakeoverRefusalWritesOneSecurityEntry fails on main, passes after.
- Add failing proof conformance cases in pkg/forge/forgetest/proof_cases.go for other content, other branch, parent mismatch, and old binding. Check: proof cases in pkg/forge/forgetest/proof_cases.go fail on main, pass after.
- Move Prover from pkg/forge/github/proof.go to pkg/forge/forge.go with conformance cases in pkg/forge/forgetest/proof_cases.go. Check: pkg/forge/forge.go holds Prover and pkg/forge/github/proof.go holds no Prover type.
- Write NEW pkg/forge/takeover_refusal.go with the RefuseTakeover constructor, and wire the caller service step in pkg/forge/service.go (next to Service.refuse at line 556) to the audit log and the terminal failed job state. Check: TestTakeoverRefusalWritesOneSecurityEntry passes with audit append then job fail and dedup by job ID and commit ID.
- Add the parent-equals-base check in pkg/forge/github/commit.go (func gitHub.deliveryIn, line 937). Check: parent mismatch proof case in pkg/forge/forgetest/proof_cases.go passes on the GitHub adapter.
- Verify the existing jobs read route with its gate and existing credential kinds. Check: `GET /api/dustpan/v1/namespaces/{}/jobs/{}` shows failed state with the failed check reason for the existing jobs read grant.
- Add user docs page at NEW docs/guides/write-back-refusals.md in the same pull request. Check: The page lists the refusal reason, the jobs read command, and the pre-tag rotation rule; make docs-lint passes.
- Do usability check from the docs page alone — A person's step. Check: Persona completes path from docs page alone — A person's step.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- FindCommit returns forge.ErrUntrustedCommit when a trust check fails. (`pkg/forge/github/commit.go:849 (func gitHub.FindCommit)`)
- ErrUntrustedCommit names the untrusted commit refusal. (`pkg/forge/forge.go:84 (var ErrUntrustedCommit)`)
- The security audit kind exists. (`pkg/audit/audit.go:62 (audit kind security)`)
- Prover lives in the GitHub adapter. (`pkg/forge/github/proof.go:21 (type Prover)`)
- deliveryIn selects the delivered commit and checks the proof for the base. (`pkg/forge/github/commit.go:937 (func gitHub.deliveryIn)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does any ADR decide the audit record or the proof binding? No. The context pack lists zero ADRs and zero RFCs (adrs: [], rfcs: []). The change follows the issue text only.
- Does main already refuse the untrusted commit? Yes. Main defines forge.ErrUntrustedCommit at pkg/forge/forge.go:84, gitHub.FindCommit at pkg/forge/github/commit.go:849 returns it, and the security audit kind exists at pkg/audit/audit.go:62. No caller writes the refusal entry yet. This change adds only the refusal record path, the terminal failed state, the digest and branch binding, the parent check, and the Prover move.
- What digest, branch encoding, rotation rule, and authorization apply? The digest is SHA-256 over sorted changed paths with bytes plus branch bytes, the branch uses the exact stored branch name with no normalization, and the rotation rule is pre-tag only. The jobs read route keeps its own authorization actions; this change adds no new action and no new route.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
