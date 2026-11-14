## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. reproduce

- [ ] 1.1 Add failing serial-chain test in NEW pkg/store/storetest/audit_hot_row_test.go for concurrent AppendAuditEntry calls. Check: go test ./pkg/store/storetest/ -run TestAuditAppendSerialChain -count=1 fails on main
- [ ] 1.2 Add failing order test in NEW pkg/store/storetest/inventory_hot_row_test.go for poll readers. Check: go test ./pkg/store/storetest/ -run TestInventoryChangeOrder -count=1 fails on main

## 2. fix

- [ ] 2.1 Move the audit append in `pkg/store/postgres/audit.go` to a short transaction and keep the fence check atomic. Check: go test ./pkg/store/... -run TestAuditAppendSerialChain -count=1
- [ ] 2.2 Shorten the inventory change path in `pkg/store/postgres/queries.sql` and `pkg/store/sqlite/queries.sql` without changing numbering. Check: go test ./pkg/store/... -run TestInventoryChangeOrder -count=1
- [ ] 2.3 Document the ceiling in NEW docs/guides/scaling-per-namespace-ceiling.md with the per-namespace ceiling and the lock-wait command. Check: `test -f docs/guides/scaling-per-namespace-ceiling.md` passes

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/db` (`make generate`, sqlc); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. definition of done

- [ ] 4.1 Run the store suites for both dialects. Check: go test -race ./pkg/store/... -timeout 60m passes
- [ ] 4.2 Run the done checks through route ANY /api/audit/entries with an access token, plus the load harness and the docs page. Check: `GET /api/audit/entries` returns 200 with an access token; `make load` records fields; `make check` with zero findings
