## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Side effects (computed from main)

- [ ] 1.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 2. Research and docs

- [ ] 2.1 Write the research note to NEW docs/research/resource-imports-review-requests.md with primary sources, owner-decision quotes, approval-rules anchor, failure cases, and a provisional recommendation. No route: the task writes a file only. Check: NEW docs/research/resource-imports-review-requests.md exists with sources per issue (#892: Atlantis, HCP Terraform, Spacelift, env zero, Pulumi Deployments; #893: plus GitOps practice), quotes of owner decisions 27.4, 27.7, 42.1 and 6.4 with fit or conflict for each, anchor to ConfigService.ApprovalRules for what the change order shows, and named scenarios hostile-import-block, already-managed-resource, concurrent-apply, failed-import-after-merge, wrong-address, finding-conflict, closed-review-request.
- [ ] 2.2 Write the explanation page to NEW docs/explanation/post-merge-apply.md for post-merge apply with trade-offs against pre-merge apply. No route: the task writes a file only. Check: NEW docs/explanation/post-merge-apply.md exists, is linked from the docs nav, covers drift between main and applied state, failed applies after merge, and approval rules for each flow, in STE voice.
- [ ] 2.3 Add a section 'Follow-up issues' to NEW docs/research/resource-imports-review-requests.md. It names the RFC or ADR issues for #892, #914 and #918 and the page of #893, and records the owner decision there. A person's step. Check: The section 'Follow-up issues' in NEW docs/research/resource-imports-review-requests.md names each follow-up issue. A person's step.
- [ ] 2.4 Run docs checks on NEW docs/research/resource-imports-review-requests.md and NEW docs/explanation/post-merge-apply.md and fix findings. Check: `make docs-lint` passes with zero findings on both NEW paths. Confirmed by: CI (make docs-lint).
