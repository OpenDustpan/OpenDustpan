## 0. Confirm against the code first

- [ ] 0.1 Confirm that main has no `agent.ConnectionSpec`, no `agent.SecretFilter`, no `egress.CheckModelURL` and no route under `/api/agent/`. Check: `rg -n 'ConnectionSpec|SecretFilter|CheckModelURL|/api/agent/' internal pkg cmd` finds nothing outside tests of other changes.
- [ ] 0.2 Confirm that `PutConnection` and the older `Connection` type have no caller. Check: `rg -n 'PutConnection|agent\.Connection\b' internal pkg cmd`.
- [ ] 0.3 Read ADR 129 decisions 1 to 4, 9 to 12, 25, 26, 32, 33 and note any change since the spec.

## 1. Failing tests first

- [ ] 1.1 `TestParseConnectionSpec_Hostile` and the table of valid documents (all 18 fields, no default, `responses` fails).
- [ ] 1.2 The conformance suites `agenttest.SecretItems` and `agenttest.ConnectionStore` (create, replace, delete in use, sealed at rest, pending and promote, delete all in one transaction).
- [ ] 1.3 `TestCheckModelURL_DeniedRanges` and `TestAllowlist_CannotAllowDeniedRange` in `pkg/egress`.
- [ ] 1.4 Route tests: `TestSecretRoutes_NeverAnswerValue`, `TestConnectionRoutes_*`, `TestAgentRoutes_Forbidden`, `TestEgressAllowlistRoutes_Forbidden`, `TestAuditEntries_NoValue`.
- [ ] 1.5 For each guard in the layers table, a test that fails when the guard is removed. Check: remove the guard, run the test, see it fail, put the guard back.

## 2. Build

- [ ] 2.1 `internal/agent`: `ConnectionSpec`, `SecretRef`, the ports and the sentinel errors. Remove the older `Connection` and `PutConnection`.
- [ ] 2.2 `internal/store/sqlite` and `internal/store/postgres`: tables for secret items, the current connection, the pending connection and the reference rows, with foreign keys, in the one schema file of each dialect. Run both adapters through the suites.
- [ ] 2.3 `internal/agentsvc`: the service with the audit entries.
- [ ] 2.4 `pkg/egress`: `CheckModelURL`, `Allowlist`. Setting storage and the flag `--egress-public-only` in `cmd/opendustpan`.
- [ ] 2.5 `internal/api/server`: the nine routes of the contract table, each with its action and object. Error codes in `internal/errcode/testdata/catalog/agent-connection.txt`.
- [ ] 2.6 The domain rule in `.golangci.yml` for `internal/agentsvc`.

## 3. Side effects

- [ ] 3.1 `make generate`, then `go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`, then `make clients-generate`. Never edit the generated files by hand.
- [ ] 3.2 The page `docs/guides/agent-connection.md` with the generic worked examples: store a key, write the document, read it, delete it. Every command in the page is run by a test.

## 4. Definition of done

- [ ] 4.1 e2e step: store a secret, read the list, delete it, see 409 while a document refers to it (the document is put into the store by the test through the store port, because the write route is a later change).
- [ ] 4.2 Usability check: an administrator with the guide only can store a key and read the effective connection. Every error names the one command that helps.
- [ ] 4.3 `make check` passes with zero findings. Paste the output into the pull request.
