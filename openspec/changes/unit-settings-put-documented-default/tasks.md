## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Fix

- [ ] 1.1 Add failing server tests in the NEW file `internal/api/unitsettings/put_strict_test.go` for dot directory, null body, repeated key, mixed invalid, and error shape on route ANY/DELETE/GET/PUT /api/dustpan/v1/namespaces/{}/units/{}/settings with access token. Check: `go test ./internal/api/unitsettings -run TestPutStrict -count=1` fails on main for dot, null, and repeated key in the NEW file `internal/api/unitsettings/put_strict_test.go`.
- [ ] 1.2 Fix decode and normalization in the NEW file `internal/api/unitsettings/strict_decode.go` on the PUT path for route ANY/DELETE/GET/PUT /api/dustpan/v1/namespaces/{}/units/{}/settings. Check: `go test ./internal/api/unitsettings -run TestPutStrict -count=1` passes after the fix in the NEW file `internal/api/unitsettings/strict_decode.go`.

## 2. Side effects (computed from main)

- [ ] 2.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Add e2e coverage in the NEW file `e2e/unitsettings_put_test.go` for the PUT and GET round trip on route ANY/DELETE/GET/PUT /api/dustpan/v1/namespaces/{}/units/{}/settings with access token. Confirmed by: CI (e2e/unitsettings_put_test.go). Check: E2E step PUTs directory "." then GETs the effective value in the NEW file `e2e/unitsettings_put_test.go` on route ANY/DELETE/GET/PUT /api/dustpan/v1/namespaces/{}/units/{}/settings with access token.
- [ ] 3.2 Update `docs/reference/unit-settings-api.md` with the three rules: dot means unset, null refused, repeated key refused. Check: The page `docs/reference/unit-settings-api.md` states dot means unset, null refused, repeated key refused.
- [ ] 3.3 Run the usability check from `docs/reference/unit-settings-api.md` and the full check with `make check`. A person's step. Check: `make check` passes with zero findings.
