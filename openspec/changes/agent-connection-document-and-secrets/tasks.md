## 0. Confirm against the code first

- [ ] 0.1 Confirm that main has no type for the connection document, no secret filter type, no check of a model URL and no route under the path prefix of the agent API. Check: `rg -n 'ConnectionSpec|SecretFilter|CheckModelURL|/api/agent/' internal pkg cmd` finds nothing outside tests of other changes.
- [ ] 0.2 Confirm that the older model connection type of the agent package and its write method have no caller outside `internal/agent/settings.go`, `internal/agent/agenttest/gatestores.go` and `internal/agent/agenttest/gatestores_fake_test.go`. Other types with a similar name belong to Git host connections and are out of scope. Check: `rg -n 'PutConnection|agent\.Connection\b' internal pkg cmd`.
- [ ] 0.3 Read ADR 129 decisions 1 to 4, 9 to 12, 25, 26, 32, 33 and note any change since the spec.

## 1. Failing tests first

- [ ] 1.1 In the NEW file `internal/agent/connection_test.go` (package `agent_test`): `TestParseConnectionSpec_Hostile` and the table of valid documents (all 18 fields, no default, `responses` fails), and `TestBaseURL_Hostile` and `TestHeaders_PlainSecretByName`.
- [ ] 1.2 The NEW conformance suites `agenttest.SecretItems` in the NEW file `internal/agent/agenttest/secretitems.go` and `agenttest.ConnectionStore` in the NEW file `internal/agent/agenttest/connectionstore.go` (create, replace, delete in use, sealed at rest, pending and promote, delete all in one transaction, missing reference). The NEW files `internal/store/sqlite/agentconnection_test.go` and `internal/store/postgres/agentconnection_test.go` run them.
- [ ] 1.3 `TestCheckModelURL_DeniedRanges` and `TestAllowlist_CannotAllowDeniedRange` in the NEW file `pkg/egress/modelurl_test.go`.
- [ ] 1.4 Route tests in the NEW file `internal/api/server/agent_connection_test.go`: `TestSecretRoutes_NeverAnswerValue`, `TestConnectionRoutes_*`, `TestConnectionRoutes_OversizeBody`, `TestAgentRoutes_Forbidden`, `TestEgressAllowlistRoutes_Forbidden`, `TestAuditEntries_NoValue`, `TestFailClosed_Table`. No route: the test table of these tests calls the handlers over `httptest` with the principals of the test only.
- [ ] 1.5 For each guard in the layers table of the design, the test that the table names, in the NEW files `internal/agent/connection_test.go`, `internal/agent/agenttest/secretitems.go`, `internal/agent/agenttest/connectionstore.go`, `pkg/egress/modelurl_test.go` and `internal/api/server/agent_connection_test.go`. Check: remove the guard, run the test, see it fail, put the guard back.

## 2. Build

- [ ] 2.1 `internal/agent`: in the NEW files `internal/agent/connection.go` and `internal/agent/secretitems.go`, the NEW types `ConnectionSpec` and `SecretRef`, the NEW ports and the NEW sentinel errors. In `internal/agent/settings.go`, remove the older connection type (line 15) and the three methods of `SettingsStore` that use it (lines 112 to 119). In `internal/agent/agenttest/gatestores_fake_test.go`, remove the three methods of `fakeSettings` that use it (lines 293 to 303). In `internal/agent/agenttest/gatestores.go`, remove the check of the older connection read in `caseSettingsDefaults` (line 450); the NEW suite `agenttest.ConnectionStore` covers the answer for no connection.
- [ ] 2.2 `internal/store/sqlite` and `internal/store/postgres`: tables for secret items, the current connection, the pending connection and the reference rows, with foreign keys, in `internal/store/sqlite/migrations/0168_baseline.sql` and `internal/store/postgres/migrations/0168_baseline.sql`, and the queries in `internal/store/sqlite/queries.sql` and `internal/store/postgres/queries.sql`. Run both adapters through the suites.
- [ ] 2.3 The NEW package `internal/agentsvc`: the service with the audit entries.
- [ ] 2.4 `pkg/egress`: in `pkg/egress/egress.go`, the NEW function `egress.CheckModelURL` and the NEW type `egress.Allowlist`. The storage of the setting, and the NEW flag `--egress-public-only` in `cmd/opendustpan/commands.go`.
- [ ] 2.5 `internal/api/server`: the nine routes of the contract table, each with its action and object, in the NEW files `internal/api/server/agent_secrets.go`, `internal/api/server/agent_connection.go` and `internal/api/server/egress_allowlist.go`. Add the new file `internal/errcode/testdata/catalog/agent-connection.txt` with the error codes.
- [ ] 2.6 The domain rule in `.golangci.yml` for `internal/agentsvc`.

## 3. Side effects

- [ ] 3.1 `make generate`, then `go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`, then `make clients-generate`. Never edit the generated files by hand.
- [ ] 3.2 Add the new file `docs/guides/agent-connection.md` with the generic worked examples: store a key, write the document, read it, delete it. Every command in the page is run by a test.

## 4. Definition of done

- [ ] 4.1 e2e step in the NEW file `e2e/agent_secrets_test.go`: with an administrator access token that holds `server:manage-agent`, call the NEW routes `POST /api/agent/secrets`, `GET /api/agent/secrets` and `DELETE /api/agent/secrets/{ref}`: store a secret, read the list, delete it, see 409 while a document refers to it (the test puts the document into the store through the store port, because the write route is a later change).
- [ ] 4.2 Usability check: an administrator with the guide only can store a key and read the effective connection. Every error names the one command that helps.
- [ ] 4.3 `make check` passes with zero findings. Paste the output into the pull request.
