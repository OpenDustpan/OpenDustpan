---
version: 1
---

Issue: #1324.

## Problem

A Namespace administrator connects a git host and hits three small traps: an uncapped rate limit wait, two answers for no registration, and a range list that accepts public ranges it then ignores.

## Evidence

- The GitHub adapter has an uncapped waitTime helper Proof: pkg/forge/github/check.go:441 (func gitHub.waitTime)
- The allowed range parser is one shared constructor Proof: pkg/egress/egress.go:124 (func ParseAllowedRanges)
- The connection check route exists on main Proof: POST /api/dustpan/v1/namespaces/{}/git-host-connections/{}/check in route table or API description

## Fix boundary

- Cap pkg/forge/github waitTime with an unexported 60 second ceiling
- Return one coded 409 answer for no registration on check and create
- Refuse public ranges in ParseAllowedRanges with a why error

Out of scope:

- Fix round for #1311 bugs and security findings
- The 8 round 2 review nits whose text is not in the pack; tracked as a separate follow-up change BLOCKED by the review text
- New git host behavior beyond the cap, one answer, and parse refusal

## Check

- Add failing adapter test for the cap and the 5 second fallback in pkg/forge/github Check: `go test ./pkg/forge/github/ -run TestWaitTime -count=1` fails on main, passes after fix
- Add failing service test for one coded answer in pkg/forge Check: `go test ./pkg/forge/ -run TestNoRegistration -count=1` fails on main, passes after fix
- Add failing parse tests for public ranges in pkg/egress Check: `go test ./pkg/egress/ -run TestParseAllowedRanges -count=1` fails on main, passes after fix
- Cap waitTime in pkg/forge/github/check.go:441 with an unexported constant and a 5 second fallback Check: `rg -n maxWait pkg/forge/github/check.go` shows the 60 second ceiling
- Unify the no-registration answer of check and create in pkg/forge to the existing coded 409 body with no new error code Check: `go test ./pkg/forge/ -run TestNoRegistration -count=1` passes
- Refuse globally routable ranges in pkg/egress/egress.go:124 Check: `go test ./pkg/egress/ -run TestParseAllowedRanges -count=1` passes
- E2E step calls the existing route `POST /api/dustpan/v1/namespaces/{}/git-host-connections/{}/check`, passes its authorization gate with an access token credential, and shows the coded 409. Confirmed by: CI (make e2e). Check: e2e step passes in CI
- Write the git host connections docs page with the 60 second cap, the 409 answer and the parse refusal in docs/guides/ Check: `make docs-lint` passes

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main returns the GitHub requested wait from waitTime with no cap named in the pack (`pkg/forge/github/check.go:441 (func gitHub.waitTime)`)
- Main parses operator allowed ranges in one shared constructor (`pkg/egress/egress.go:124 (func ParseAllowedRanges)`)
- Main refuses hostile git host ranges at server build (`pkg/dustpan/githost_ranges_test.go:32 (func TestNewServerRefusesHostileGitHostRanges)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does waitTime sleep, what is the cap, and what is the fallback? No cap is present on main. The change caps the result at 60 seconds per call, does no sleep in waitTime, and uses a 5 second fallback for missing or hostile headers.
- Which answer wins for no registration on check and create? The coded 409 body that create answers on main wins. A connection ID that does not exist keeps 404. No new error code is added.
- Where should public ranges be refused? In ParseAllowedRanges at pkg/egress/egress.go:124, the one shared constructor.
- What are the 8 nits from the round 2 review? The pack carries no nit text. They are out of scope, BLOCKED by the review text.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
