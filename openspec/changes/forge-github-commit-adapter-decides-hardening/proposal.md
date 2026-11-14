---
version: 1
---

Issue: #2450, #2452, #2455.

## Problem

A namespace administrator links a Git host and keeps write-back commits correct. Today the Commit adapter can retry a moved head without a clear bound, miss the write-access refusal, match trailers with two rules, and hide the find limit and clock, so reviews and branch rules surprise the team.

## Evidence

- The adapter follows a moved head up to three times Proof: pkg/forge/github/commit.go:142-145 cited in issue #2450
- requireAncestor calls compare with per_page=1 and can exceed the body cap Proof: pkg/forge/github/commit.go:167-170 and egress.MaxBodyBytes in pkg/egress/egress.go:45
- checkCommit and checkChanges repeat validation without direct hostile tests Proof: pkg/forge/github/commit.go:301 (func checkCommit) and pkg/forge/github/commit.go:364 (func checkChanges)
- hasTrailer and isTrailerBlock use two trailer-block rules Proof: pkg/forge/github/commit.go:1066 (func hasTrailer) and pkg/forge/github/commit.go:461 (func isTrailerBlock)
- No conformance case covers missing repository or missing write permission Proof: pkg/forge/forgetest/commit_cases.go has none of these cases per issue #2455 finding 6

## Fix boundary

- Bound moved-head retry at 3 with cached base blobs
- Refuse Commit without write access with ErrRefused
- Unify hasTrailer on isTrailerBlock with prose-plus-trailer case
- Document author, signature wait, head-follow bound, 500-commit ErrUnavailable limit, and since clock with skew margin
- Add direct hostile tests and forgetest conformance cases

Out of scope:

- Unify webhook signing in one shared package
- Change the log-sink or notify webhook wire form
- Document the one webhook form in user docs

## Check

- Add failing hostile test for `checkCommit` direct in `pkg/forge/github/commit.go:301` Check: `go test ./pkg/forge/github -run TestCheckCommitHostile`
- Add failing hostile test for `checkChanges` direct in `pkg/forge/github/commit.go:364` Check: `go test ./pkg/forge/github -run TestCheckChangesHostile`
- Add failing hostile tests for `validTrailer`, `hasTrailer`, and `isTrailerBlock` direct in `pkg/forge/github/commit.go:502` Check: `go test ./pkg/forge/github -run TestTrailerBlockHostile`
- Add conformance cases for missing repository on Commit and FindCommit plus ErrRefused without write permission in `pkg/forge/forgetest/commit_cases.go` Check: `go test ./pkg/forge/... -run TestForgetestCommitMissingRepo`
- Fix `hasTrailer` in `pkg/forge/github/commit.go:1066` to call `isTrailerBlock` with prose-plus-trailer case Check: `rg -n isTrailerBlock pkg/forge/github/commit.go` shows `hasTrailer` calls `isTrailerBlock`
- Bound moved-head retry at 3 and cache base blob IDs in `pkg/forge/github/commit.go:195` Check: `rg -n requireAncestor pkg/forge/github/commit.go` shows bound of 3 and cached base blobs with no file patches
- Update adapter doc in `pkg/forge/github/commit.go:51` and port doc in `pkg/forge/forge.go:251` for author, signature, head-follow, limit, and clock Check: Doc comment in `pkg/forge/github/commit.go:51` names app author, Co-authored-by trailer, signature wait, head-follow bound, 500-commit limit, and since clock with 5-minute skew margin
- Run forge unit and conformance suites in `pkg/forge/github` and `pkg/forge/forgetest` Check: `go test -race ./pkg/forge/...` passes

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- GitHub Commit adapter implements FindCommit (`pkg/forge/github/commit.go:849 (func gitHub.FindCommit)`)
- Port defines Commit and FindCommit with RetryAfter (`pkg/forge/forge.go:274 (func RetryAfter)`)
- Port validates a commit request (`pkg/forge/forge.go:650 (func ValidateCommit)`)
- Adapter validates trailer lines and trailer blocks (`pkg/forge/github/commit.go:502 (func validTrailer)`)
- Adapter checks file changes with limits and allowlist (`pkg/forge/github/commit.go:364 (func checkChanges)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 271: The Forge port, Git host connections and the installation-level GitHub App** (owner-tagged: decision 1, 2).

- Decisions that apply: 1 (current), 2 (current).
- Changed by: ADR 286 changes; ADR 299 changes decision 4; ADR 299 extends decision 2; ADR 299 extends decision 7; ADR 300 changes decision 7; ADR 303 extends decision 11; ADR 336 extends; ADR 374 extends decision 8. Chain to the end: ADR 286, ADR 299, ADR 300, ADR 303, ADR 336, ADR 328, ADR 359, ADR 368.
- Current rule: this ADR, with ADR 286, 299, 300, 303, 336, 374.
- Quote: "The port lives in `pkg/forge`, and that package owns the connections. `pkg/forge` is a tool-neutral domain package. It holds the `Forge` port, the store port of the connections, the sealed credential sets, the default flag, and the service that picks a connection and opens it. The package imports no `net/http`, no SQL and no adapter. The adapters live in `pkg/forge/github` and, lat"
- Quote: "One interface with capabilities. `Opener` makes a `Forge` for one connection and one credential set, and makes no call to the Git host. `Forge` has `Capabilities`, `Check`, and the groups `Files`, `Commits`, `Reviews`, `Tokens`, `Events` and `Rotation`. Every adapter implements every method. A method that the Git host or the auth kind cannot do returns `ErrUnsupported`, and `Capabi"
- Effect: Hardens the GitHub Commit adapter inside the tool-neutral Forge port with no new port method. The adapter doc states that authorship and head-follow follow a recommendation that waits for an owner decision.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does hardening the GitHub Commit adapter conflict with an ADR? ADR 271 decisions 1 and 2 place the port in pkg/forge and require one interface with capabilities; this change hardens the existing GitHub Commit adapter without new port methods. No accepted ADR decides the commit author, the signature form, or the moved-head rule: issue #2450 records RFC 12 questions 4 and 5 as draft with only a recommendation, so the adapter doc marks the rule as a recommendation that waits for an owner decision.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
