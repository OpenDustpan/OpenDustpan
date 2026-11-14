## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing takeover test in NEW pkg/forge/takeover_refusal_test.go for forged trailer commit: one security entry, failed job, no delivered record. Check: TestTakeoverRefusalWritesOneSecurityEntry fails on main, passes after.
- [ ] 1.2 Add failing proof conformance cases in pkg/forge/forgetest/proof_cases.go for other content, other branch, parent mismatch, and old binding. Check: proof cases in pkg/forge/forgetest/proof_cases.go fail on main, pass after.
- [ ] 1.3 Move Prover from pkg/forge/github/proof.go to pkg/forge/forge.go with conformance cases in pkg/forge/forgetest/proof_cases.go. Check: pkg/forge/forge.go holds Prover and pkg/forge/github/proof.go holds no Prover type.

## 2. Build

- [ ] 2.1 Write NEW pkg/forge/takeover_refusal.go with the RefuseTakeover constructor, and wire the caller service step in pkg/forge/service.go (next to Service.refuse at line 556) to the audit log and the terminal failed job state. Check: TestTakeoverRefusalWritesOneSecurityEntry passes with audit append then job fail and dedup by job ID and commit ID.
- [ ] 2.2 Add the parent-equals-base check in pkg/forge/github/commit.go (func gitHub.deliveryIn, line 937). Check: parent mismatch proof case in pkg/forge/forgetest/proof_cases.go passes on the GitHub adapter.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Verify the existing jobs read route with its gate and existing credential kinds. Check: `GET /api/dustpan/v1/namespaces/{}/jobs/{}` shows failed state with the failed check reason for the existing jobs read grant.
- [ ] 4.2 Add user docs page at NEW docs/guides/write-back-refusals.md in the same pull request. Check: The page lists the refusal reason, the jobs read command, and the pre-tag rotation rule; make docs-lint passes.
- [ ] 4.3 Do usability check from the docs page alone — A person's step. Check: Persona completes path from docs page alone — A person's step.
- [ ] 4.4 Run make check with zero findings — Confirmed by: CI (make check) Check: `make check` passes with zero findings — Confirmed by: CI (make check)
