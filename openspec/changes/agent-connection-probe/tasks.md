## 0. Confirm against the code first

- [ ] 0.1 Confirm the change `agent-connection-document-and-secrets` is on main: `rg -n 'ConnectionStore|PutPending' internal`. BLOCKED by that change.
- [ ] 0.2 Confirm the change `agent-model-adapters` is on main: `rg -n 'func NewModel|NewDialGuard' internal pkg`. BLOCKED by that change.
- [ ] 0.3 Confirm the durable operation runner and the token ledger on main: `rg -n 'durable|ledger' internal --type go -l`. Note the kind registration and the ledger key code.
- [ ] 0.4 Read ADR 129 decisions 29 to 31 and 33 and note any change since the spec.

## 1. Failing tests first

- [ ] 1.1 The conformance suite `agenttest.ProbesSuite`: start, get, cancel, stale version, rate limit, idempotent ledger row.
- [ ] 1.2 A table test of the eight checks against a fake server of each format: each check passes, fails or warns by the table of decision 30. Include the model that gives no tool call in 2 attempts.
- [ ] 1.3 Tests of the timeouts: 2 minutes, `reach` 10 seconds, the probe 10 minutes (with the fake clock).
- [ ] 1.4 Route tests: `TestProbeRoute_HostileDocument`, `TestProbeRoutes_Forbidden`, the 429 with `retry-after`.
- [ ] 1.5 For each guard in the layers table, a test that fails when the guard is removed. Check: remove the guard, see the test fail, put the guard back.

## 2. Build

- [ ] 2.1 `internal/agent`: the types, the port `Probes`, the sentinels.
- [ ] 2.2 `internal/store/{sqlite,postgres}`: probe rows, the start counter and the ledger origin `probe`, in the one schema file of each dialect.
- [ ] 2.3 `internal/agentsvc`: the kind `agent.probe`, one step for each check, the promote step and the cancel rules. The service writes the audit entry of a probe with no store. The check `reach` calls `egress.CheckModelURL` on the `baseURL` and writes an audit entry for a refusal.
- [ ] 2.4 `internal/api/server`: the two routes, with the action and the object. Error codes in the catalog file.
- [ ] 2.5 The domain rule for the new files in `.golangci.yml`.

## 3. Side effects

- [ ] 3.1 `make generate`, then `go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`, then `make clients-generate`. Never edit a generated file by hand.
- [ ] 3.2 The page `docs/guides/agent-connection-probe.md`: each check, its failure message and the field to change. A test runs each command of the page.

## 4. Definition of done

- [ ] 4.1 e2e step: start a probe of an inline document against a fake server, read each check, see a failure of the `tools` check name the field to change.
- [ ] 4.2 Usability check: an administrator with the page only fixes a document that the `tools` check refused.
- [ ] 4.3 `make check` passes with zero findings. Paste the output into the pull request.
