---
version: 1
---

Issue: #892, #893, #914, #918.

## Problem

Problem: a team wants to bring an existing cloud resource under a unit and to know why post-merge apply is the recommended flow, but there is no collected evidence on how review requests, imports, and approvals fit together.

## Evidence

- Issue #892 asks if and how a team can start a resource import from a pull request or merge request. Proof: ## Outcome A research note that says if and how a team can start a resource import (an OpenTofu `import` block or `pulumi import`) from a pull request or merge request.
- Issue #893 asks for an explanation page under docs/explanation for post-merge apply. Proof: In: a research note (sources: Atlantis, HCP Terraform, Spacelift, Pulumi Deployments, GitOps practice); an explanation page under `docs/explanation/`.
- Issue #914 runs state rm and state mv as reviewed runner jobs with preview and approval rules. Proof: ## Outcome A person removes or moves resources in state through a reviewed job with a preview, instead of a direct state edit.
- Issue #918 scopes resource import to a runner job with plan, change order and approval rules, and scopes state migration out. Proof: Out: state migration from other backends (the import tool, ADRs 241 to 244).
- docs/explanation exists on main. Proof: docs/explanation

## Fix boundary

- Research note with primary sources and a provisional recommendation for resource imports started from a review request.
- Explanation page under docs/explanation that documents post-merge apply as the recommended flow with trade-offs.

Out of scope:

- Job kinds for resource import and for state rm and state mv
- Review-request comment commands
- The post-merge apply setting itself
- State migration from other backends (ADRs 241 to 244)
- Kubernetes operators as import sources

## Check

- Write the research note to NEW docs/research/resource-imports-review-requests.md with primary sources, owner-decision quotes, approval-rules anchor, failure cases, and a provisional recommendation. No route: the task writes a file only. Check: NEW docs/research/resource-imports-review-requests.md exists with sources per issue (#892: Atlantis, HCP Terraform, Spacelift, env zero, Pulumi Deployments; #893: plus GitOps practice), quotes of owner decisions 27.4, 27.7, 42.1 and 6.4 with fit or conflict for each, anchor to ConfigService.ApprovalRules for what the change order shows, and named scenarios hostile-import-block, already-managed-resource, concurrent-apply, failed-import-after-merge, wrong-address, finding-conflict, closed-review-request.
- Write the explanation page to NEW docs/explanation/post-merge-apply.md for post-merge apply with trade-offs against pre-merge apply. No route: the task writes a file only. Check: NEW docs/explanation/post-merge-apply.md exists, is linked from the docs nav, covers drift between main and applied state, failed applies after merge, and approval rules for each flow, in STE voice.
- Add a section 'Follow-up issues' to NEW docs/research/resource-imports-review-requests.md. It names the RFC or ADR issues for #892, #914 and #918 and the page of #893, and records the owner decision there. A person's step. Check: The section 'Follow-up issues' in NEW docs/research/resource-imports-review-requests.md names each follow-up issue. A person's step.
- Run docs checks on NEW docs/research/resource-imports-review-requests.md and NEW docs/explanation/post-merge-apply.md and fix findings. Check: `make docs-lint` passes with zero findings on both NEW paths. Confirmed by: CI (make docs-lint).

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main validates review requests in the forge port. (`pkg/forge/forge.go:1158 (func ValidateReviewRequest)`)
- Main handles a review request on the server. (`internal/api/server/review.go:469 (func reviewRequest)`)
- Main reads approval rules from gate config. (`pkg/gate/config.go:206 (func ConfigService.ApprovalRules)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 165: Unmanaged-resource findings and the actions on them** (not owner-tagged).

- Decisions that apply: 8 (current).
- Changed by: ADR 166 changes decision 15; ADR 210 changes. Chain to the end: ADR 166, ADR 210, ADR 286, ADR 247.
- Current rule: this ADR, with ADR 166, 210.
- Quote: "Jobs. `import`, `write-code` and `choose-owner` start jobs through `job.Service.Start` (ADR 100 decision 17) with the person as the actor."
- Effect: Research keeps the actor rule for finding-started imports and records that a review-request start with no person actor is not covered by it.
- Conflict: none.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "Docs in the same pull request. A pull request that adds or changes a feature that a person or a client can see adds or changes its page in the user documentation."
- Effect: The explanation page for post-merge apply ships in the same pull request as the research note.
- Conflict: none.

**ADR 241: An import is an API resource that the server runs** (not owner-tagged).

- Decisions that apply: 7 (current), 8 (current), 10 (current), 12 (current).
- Changed by: ADR 248 changes decision 11; ADR 313 extends; ADR 343 changes decisions 2 and 3. Chain to the end: ADR 248, ADR 313, ADR 343, ADR 286.
- Current rule: this ADR, with ADR 248, 313, 343.
- Quote: "The objects. An **import** belongs to one namespace. It names one source (a source kind, an address and the selection of units), the credential way of decision 4, the options of ADR 242, and the target names. It holds on"
- Effect: Research does not change bulk state import; resource import with the tool CLI stays outside the import resource of ADR 241.
- Conflict: none.

**ADR 343: Runner work: tool imports, sweeps and the runner repository** (owner-tagged (all decisions)).

- Decisions that apply: 2 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "A bulk import runs in the server (owner, 2026-11-07). A bulk import reads another backend directly. It keeps the rule of ADR 241 decision 2: it runs in the server, in durable operations."
- Effect: Research keeps bulk import in the server in durable operations and studies resource import separately without changing this rule.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does an import started from a finding need a new finding action? No. ADR 165 decision 8 already lets an import action on a finding start a job through job.Service.Start with the person as actor. The research studies the runner job form and the review-request start, not the finding action itself.
- Does resource import reuse the bulk import resource of ADR 241? No. ADR 241 covers bulk state import from another backend in durable operations in the server, per ADR 343 decision 2. Resource import with the tool CLI stays out of ADR 241.
- Which start form for resource imports fits post-merge apply and what does the change order show? Provisional recommendation only in the note. The owner records the decision on the follow-up RFC or ADR, so this change has no open question.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
