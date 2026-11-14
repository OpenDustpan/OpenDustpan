---
version: 1
---

Issue: #2305, #2338, #2400, #2406.

## Problem

Until the first tag the Operator needs the baseline to change in place, so the five checks skip. At the first tag the Operator needs those checks on from the record, or an edited shipped baseline ships and old databases refuse to open with no early failure.

## Evidence

- A frozen migration test exists in each dialect Proof: pkg/store/sqlite/frozen_test.go and pkg/store/postgres/frozen_test.go exist in the pack paths
- The baseline round-trip tests exist Proof: pkg/store/internal/baseline/roundtrip_test.go exists in the pack paths
- The version test exists Proof: internal/api/apidesc/version_test.go exists in the pack paths
- The release scripts exist Proof: scripts/check-release-record.sh and scripts/release-build.sh exist in the pack paths
- No workflow runs the release record check Proof: Issue 2400 states no workflow in .github/workflows calls release-build.sh or check-release-record.sh
- The record parser and release switch exist Proof: pack main lists internal/releaserecord/releaserecord.go:89 (func Parse) and :177 (func Record.Released)

## Fix boundary

- Change pkg/store/sqlite/frozen_test.go, pkg/store/postgres/frozen_test.go, pkg/store/internal/baseline/roundtrip_test.go and internal/api/apidesc/version_test.go to read internal/releaserecord
- Change internal/releaserecord/releaserecord.go to refuse #v lines and change scripts/check-release-record.sh to call that parser
- Add the NEW .github/workflows/tag-record.yml that runs scripts/check-release-record.sh on v tags with full history and an append-only diff
- At the tag commit only: add record lines in internal/releaserecord/shipped.txt, generate baseline hash files, set DocumentVersion in internal/api/apidesc/document.go, write openapi.pin, write tag copies, delete the one-baseline guard

Out of scope:

- Review 9 and 10 findings outside the five guards, the record parser, the record scripts and the tag workflow
- New numbered migrations before the tag
- Console changes

## Check

- Write a failing test that Parse refuses a #v line Check: `go test ./internal/releaserecord/ -run TestParseRefusesCommentedTag`
- Write a failing test that the PostgreSQL round trip fails with CI set and no database URL when the record says released Check: `CI=1 go test ./pkg/store/internal/baseline/ -run TestPostgresBaselineRoundTrip`
- Change the frozen guards in both dialects to read internal/releaserecord Check: `go test ./pkg/store/sqlite/ -run TestFrozenMigrations`
- Change the round-trip guards to read internal/releaserecord Check: `go test ./pkg/store/internal/baseline/ -run TestSQLiteBaselineRoundTrip`
- Make Parse refuse #v lines and make scripts/check-release-record.sh call Parse with go run Check: `go test ./internal/releaserecord/`
- Add the NEW .github/workflows/tag-record.yml with full-history checkout and an append-only diff Check: Run `scripts/check-release-record.sh v0.1.0` on a fixture repo with full history. Confirmed by: CI (tag-record workflow)
- At the tag commit only, after every other baseline edit has merged, set DocumentVersion, write openapi.pin, add record lines, generate hash files, write tag copies and delete the one-baseline guard Check: `go test ./internal/api/apidesc/ -run TestVersionFollowsTheDescription` with no skip. Confirmed by: CI (go test)
- Run the record check and make check in a fresh checkout Check: `make check` reports zero findings. Confirmed by: CI (make check)

Depends on:

- `runner-protocol-keep-release-reason` (order of the milestone review)
- `inventory-lastrelease-opentofu-only-field` (order of the milestone review)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- PostgreSQL keeps the frozen migration test (`pkg/store/postgres/frozen_test.go:29 (func TestFrozenMigrations)`)
- The SQLite round-trip test exists (`pkg/store/internal/baseline/roundtrip_test.go:54 (func TestSQLiteBaselineRoundTrip)`)
- The PostgreSQL round-trip test exists (`pkg/store/internal/baseline/roundtrip_test.go:86 (func TestPostgresBaselineRoundTrip)`)
- The description keeps one version (`internal/api/apidesc/document.go:20 (const DocumentVersion)`)
- The version test exists (`internal/api/apidesc/version_test.go:154 (func TestVersionFollowsTheDescription)`)
- The record reports if it is released (`internal/releaserecord/releaserecord.go:177 (func Record.Released)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 322: No migrations or compatibility code before the first tag** (owner-tagged: decision 1, 5).

- Decisions that apply: 1 (changed), 5 (changed), 6 (current).
- Changed by: ADR 325 changes decision 1; ADR 325 changes decision 5; ADR 325 changes decision 6; ADR 326 extends decision 1; ADR 362 changes decision 5. Chain to the end: ADR 325, ADR 326, ADR 362, ADR 363.
- Current rule: this ADR, with ADR 325, 326, 362.
- Quote: "One baseline for each dialect until the first tag (owner)."
- Quote: "A change after the tag is a new numbered file, and ADR 73 decision 6 applies to it again."
- Effect: Guards stay skipped until the tag. The release commit adds record lines and deletes the one-baseline guard.
- Conflict: none.

## Open questions

Not spec-ready: a code check fails: task "Write a failing test that Parse refuses a #v line" names no file: name the file on main (path:line) or the NEW path that it writes; a code check fails: task "Write a failing test that the PostgreSQL round trip fails wi" names no file: name the file on main (path:line) or the NEW path that it writes; a code check fails: `.github/workflows/tag-record.yml` is named by the design or a task but is not in the touches of the stack plan; a code check fails: and 1 more.

Questions considered and answered:

- Do the guards lose their skips outright? No. ADR 325 decision 3 keeps the skip while the record lists no file. Decision 4 runs each guard with no skip when the record lists a file.
- What lands before the tag and what lands at the tag commit? Before the tag: guards read the record, Parse refuses commented version lines, scripts call Parse, and the tag workflow lands. At the tag commit: the release adds record lines, hash files, DocumentVersion, openapi.pin, tag copies, and deletes the one-baseline guard.
- Is a baseline hash file written before the tag? No. ADR 362 decision 4 says the release generates the hash files at the tag.
- What does the PostgreSQL round trip do after release when the database is absent? It fails when CI is set. ADR 325 decision 4 says the checks run with no skip after release.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
