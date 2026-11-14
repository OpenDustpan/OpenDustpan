## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Work

- [ ] 1.1 Add query-count test for migrated open in NEW file pkg/store/postgres/open_roundtrip_test.go that asserts at most 2 schema_migrations metadata queries Check: `go test ./pkg/store/postgres/ -run TestMigratedOpenQueryCount` in NEW file pkg/store/postgres/open_roundtrip_test.go fails on main by counting more than 2 metadata queries
- [ ] 1.2 Batch column read and recorded-row read in pkg/store/postgres/postgres.go:227 (func Open) Check: Same test in NEW file pkg/store/postgres/open_roundtrip_test.go passes after the edit to pkg/store/postgres/postgres.go:227 (func Open)
- [ ] 1.3 Show tampered name, tampered sha256, unknown version, duplicate version, and NULL sha256 still refuse open in NEW file pkg/store/postgres/open_roundtrip_test.go Check: `go test ./pkg/store/postgres/ -run TestMigratedOpenRefusesTamperedRow` in NEW file pkg/store/postgres/open_roundtrip_test.go passes, covering scenarios tampered-name-refuses, tampered-sha256-refuses, unknown-version-refuses, duplicate-version-refuses, and null-sha256-refuses

## 2. Side effects (computed from main)

- [ ] 2.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Run make check with zero findings Check: `make check` with zero findings after the wiring in pkg/store/postgres/postgres.go:227 (func Open)
