## 1. Failing tests first

- [ ] 1.1 Add failing adapter tests for file set upload, speculative plan start, fixed argument allowlist, secret run variables, signed log read, poll answers, gate refusal, missing pool and apply refusal. Check: `go test ./internal/api/tfe/ -run TestRemotePlan -count=1` fails on main.
- [ ] 1.2 Add failing service tests for run-plan check and change propose gates on the remote plan. Check: `go test ./pkg/job/ -run TestRemotePlanGates -count=1` fails on main.

## 2. Build

- [ ] 2.1 Add the e2e step with the real OpenTofu CLI on a workspace with execution-mode agent. Check: `make e2e E2E=remote-execution` passes on SQLite and PostgreSQL over TLS with the real OpenTofu CLI: `tofu plan` streams the runner log and `tofu apply` fails with the speculative-only error.
- [ ] 2.2 Wire the plan job, the authorization actions and the signed log read. Check: `curl` read of `/api/dustpan/v1/namespaces/{ns}/jobs/{id}/logs/{phase}` through the signed URL returns chunks with masks and refuses expired and replayed URLs.
- [ ] 2.3 Write the docs page for remote speculative plans with execution-mode agent. Check: Docs page command block runs as written in CI.

## 3. Definition of done

- [ ] 3.1 Run make check with zero findings. Check: `make check` passes with zero findings.
- [ ] 3.2 Confirm wiring, route actions, e2e step, docs page and usability check. Check: Usability check: the persona follows the docs page alone from setup to `tofu plan`, and each error names the one command that helps.
