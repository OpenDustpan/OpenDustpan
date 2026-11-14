## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing adapter tests for upload, plan start, argument allowlist, secret variables, signed log read, poll answers, gate refusal, missing pool, local mode, missing grant, cancel and apply refusal. Check: `go test ./internal/api/tfe/ -run TestRemotePlan -count=1` fails on main.
- [ ] 1.2 Add failing service tests for the run grant check and the change.propose gates. Check: `go test ./pkg/job/ -run TestRemotePlanGates -count=1` fails on main.

## 2. Build

- [ ] 2.1 Add the e2e step with the real OpenTofu CLI. It uses the existing route /api/v2/workspaces/{} to set execution-mode agent and the run routes of the TFE-compatible API (named as new routes in what_changes if the route table lacks them), with the access token of a Developer, and a test runner on /runner/v1. Check: `make e2e` passes on SQLite and PostgreSQL over TLS: tofu plan streams the runner log and tofu apply fails with the speculative-only error.
- [ ] 2.2 Wire the plan job, the authorization actions and the signed log read. Check: A GET of the signed log-read-url of the run, minted with the access token of a Developer, returns masked chunks and refuses expired and replayed URLs.
- [ ] 2.3 Write the docs page for remote speculative plans. Check: The command blocks of the page run as written in CI.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Run make check with zero findings. Check: `make check` passes with zero findings.
- [ ] 4.2 Confirm wiring, route actions, e2e step, docs page and usability check. The path uses the existing route /api/v2/workspaces/{} and the access token of a Developer. Check: The persona follows the docs page alone from setup to tofu plan with an access token, and each error names the one command that helps.
