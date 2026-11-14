---
version: 1
---

Issue: #2197.

## Problem

A namespace administrator sets the code location and executor of a stack or workspace through the unit settings API, and the current PUT refuses the documented directory value while it accepts a null body and a repeated key.

## Evidence

- Docs page docs/reference/unit-settings-api.md exists. Proof: paths: docs/reference/unit-settings-api.md exists true
- PUT with directory "." answers 400 while the page gives "." as the effective value. Proof: issue #2197 section 1: Expected 200, Actual 400 unit-settings.invalid, page gives . as effective value
- A raw body of null answers 200 and stores an empty record, while [], "x", empty body and {} {} answer 400. Proof: issue #2197 section 2: null answers 200, [], "x", empty body and {} {} answer 400
- A repeated executor key answers 200 with last value wins, while PATCH /api/dustpan/v1/settings refuses it with 400. Proof: issue #2197 section 3: PUT stores write-back, PATCH refuses the same input with 400
- Route PUT /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/settings exists in the coverage matrix. Proof: coverage_gaps route PUT /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/settings op replaceUnitSettings

## Fix boundary

- Accept codeLocation.directory "." on PUT as unset, matching the documented effective value.
- Refuse a raw body of null with 400.
- Refuse a JSON object with a repeated key with 400, matching PATCH /api/dustpan/v1/settings.

Out of scope:

- Bulk PUT limits and idempotency replay, which the issue lists as checked and fine.
- If-Match, concurrency, and body limit behavior, which the issue lists as checked and fine.
- PATCH and bulk-put unit-settings paths keep current behavior; only the single-unit PUT changes, because the issue reports only that PUT.
- Directory values other than exactly "." keep current accept or refuse behavior.

## Check

- Add failing server tests in the NEW file `internal/api/unitsettings/put_strict_test.go` for dot directory, null body, repeated key, mixed invalid, and error shape on route ANY/DELETE/GET/PUT /api/dustpan/v1/namespaces/{}/units/{}/settings with access token. Check: `go test ./internal/api/unitsettings -run TestPutStrict -count=1` fails on main for dot, null, and repeated key in the NEW file `internal/api/unitsettings/put_strict_test.go`.
- Fix decode and normalization in the NEW file `internal/api/unitsettings/strict_decode.go` on the PUT path for route ANY/DELETE/GET/PUT /api/dustpan/v1/namespaces/{}/units/{}/settings. Check: `go test ./internal/api/unitsettings -run TestPutStrict -count=1` passes after the fix in the NEW file `internal/api/unitsettings/strict_decode.go`.
- Add e2e coverage in the NEW file `e2e/unitsettings_put_test.go` for the PUT and GET round trip on route ANY/DELETE/GET/PUT /api/dustpan/v1/namespaces/{}/units/{}/settings with access token. Confirmed by: CI (e2e/unitsettings_put_test.go). Check: E2E step PUTs directory "." then GETs the effective value in the NEW file `e2e/unitsettings_put_test.go` on route ANY/DELETE/GET/PUT /api/dustpan/v1/namespaces/{}/units/{}/settings with access token.
- Update `docs/reference/unit-settings-api.md` with the three rules: dot means unset, null refused, repeated key refused. Check: The page `docs/reference/unit-settings-api.md` states dot means unset, null refused, repeated key refused.
- Run the usability check from `docs/reference/unit-settings-api.md` and the full check with `make check`. A person's step. Check: `make check` passes with zero findings.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main has a unit-settings bulk-size error code in the unitsettings catalog. (`internal/errcode/catalog_unitsettings.go:7`)
- Main has a postgres store method for unit settings. (`pkg/store/postgres/unitsettings.go:21 (func Store.UnitSettings)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Should a body of null be accepted? The issue body proves null is stored as an empty record while [], "x", empty body and {} {} answer 400, so null is an inconsistent acceptance that the fix closes.
- Should PUT refuse a repeated key like PATCH does? The issue body proves PATCH /api/dustpan/v1/settings refuses a body that names a key two times with 400, so PUT matches that behavior for the single-unit PUT only.
- Should PUT accept "." for codeLocation.directory? The pack proves the docs page exists at docs/reference/unit-settings-api.md and the issue reports the page gives "." as the effective value, so no ADR decision is needed to treat exactly "." as unset.
- Should bulk-put change in this item too? The issue reports only the single-unit PUT and PATCH parity. The bulk-put route keeps current behavior to keep this change to one fix boundary; a bulk-put strict-decode change is a separate item.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
