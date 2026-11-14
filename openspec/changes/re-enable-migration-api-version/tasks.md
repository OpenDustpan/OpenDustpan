## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write a failing test that Parse refuses a #v line Check: `go test ./internal/releaserecord/ -run TestParseRefusesCommentedTag`
- [ ] 1.2 Write a failing test that the PostgreSQL round trip fails with CI set and no database URL when the record says released Check: `CI=1 go test ./pkg/store/internal/baseline/ -run TestPostgresBaselineRoundTrip`

## 2. Work before the tag

- [ ] 2.1 Change the frozen guards in both dialects to read internal/releaserecord Check: `go test ./pkg/store/sqlite/ -run TestFrozenMigrations`
- [ ] 2.2 Change the round-trip guards to read internal/releaserecord Check: `go test ./pkg/store/internal/baseline/ -run TestSQLiteBaselineRoundTrip`
- [ ] 2.3 Make Parse refuse #v lines and make scripts/check-release-record.sh call Parse with go run Check: `go test ./internal/releaserecord/`
- [ ] 2.4 Add the NEW .github/workflows/tag-record.yml with full-history checkout and an append-only diff Check: Run `scripts/check-release-record.sh v0.1.0` on a fixture repo with full history. Confirmed by: CI (tag-record workflow). The cloud agent cannot push .github/workflows. Write the full diff of the workflow file in the pull request description under the heading 'Workflow patch (apply locally)', and leave the PR as a draft. Confirmed by: A person's step.

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 At the tag commit only, after every other baseline edit has merged, set DocumentVersion, write openapi.pin, add record lines, generate hash files, write tag copies and delete the one-baseline guard Check: `go test ./internal/api/apidesc/ -run TestVersionFollowsTheDescription` with no skip. Confirmed by: CI (go test)
- [ ] 4.2 Run the record check and make check in a fresh checkout Check: `make check` reports zero findings. Confirmed by: CI (make check)
