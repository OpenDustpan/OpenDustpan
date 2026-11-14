---
version: 1
---

Issue: #2485.

## Problem

A runner operator tries a runner against a build from source and fixes header, proof, and body faults. The server answers every fault with the same bare invalid code and a dev build refuses its own release value, so the operator cannot tell the faults apart without the server log.

## Evidence

- Issue reports cases 1, 2, 3 return 400 bare code invalid with message runner request is not valid Proof: #2485 Actual: Cases 1, 2 and 3: 400 `{"code":"invalid","message":"runner request is not valid"}`
- Issue reports a dev build refuses Dustpan-Release dev and 0.0.1 gives 400 invalid not 426 Proof: #2485 Actual: A dev build refuses `Dustpan-Release: dev` ... `0.0.1` gives 400 `invalid`, not 426 `version-unsupported`, because the server has no version number
- Issue reports a 30 MB body gives 400 invalid not 413 Proof: #2485 Actual: A 30 MB body gives 400 `invalid`, not 413
- Main has the runner version check and the runner invalid code Proof: pkg/runner/version.go:128 (func CheckVersionAhead) and internal/errcode/catalog_runner.go:10

## Fix boundary

- Map missing Dustpan-Release, bad release, bad proof, bad body, and large body to distinct error codes and status codes, and replace the bare runner.invalid answer for these five classes
- Accept the server build release string including dev when peer equals server, with no 426 ordering on a dev server
- Return 426 NEW runner.version-unsupported for an older peer on a numbered server and 413 for a body over 1 MiB

Out of scope:

- Join, claims, and session exchange with a real runner key
- Changes to runner grants, claims, or audit rules

## Check

- Add failing server tests for missing header, bad release, malformed body, large body, bad proof on poll and session, check order, dev self-accept, and old release against route ANY /runner/v1 with a runner credential. Check: TestRunnerRefusalMissingRelease, TestRunnerRefusalBadRelease, TestRunnerRefusalBadBody, TestRunnerRefusalBodyTooLarge, TestRunnerRefusalBadProof, TestRunnerDevSelfAccept, TestRunnerOldReleaseUnsupported fail on main in internal/api/runnerapi
- Map each refusal class to its status and NEW error code in NEW file internal/api/runnerapi/refusal.go. Check: TestRunnerRefusalMissingRelease passes against route ANY /runner/v1 with a runner credential
- Accept the server release string as identical, including dev, before the semver parse in pkg/runner/version.go:128 (func CheckVersionAhead). Check: TestRunnerDevSelfAccept passes on a dev build string in pkg/runner/version.go:128 (func CheckVersionAhead)
- Enforce the 1 MiB body limit at the runnerapi boundary with a bounded reader in NEW file internal/api/runnerapi/refusal.go. Check: curl 30 MiB body against ANY /runner/v1 returns 413 runner.body-too-large
- Register each NEW error code in internal/errcode/catalog_runner.go and regenerate the API description. Confirmed by: CI (make check). Check: rg runner.missing-release internal/errcode/catalog_runner.go shows each NEW code. Confirmed by: CI (make check).
- Update the runner API reference and docs page in the same pull request. Check: Reference runner-api page shows each code, status, and the dev note
- Run a usability check of the full refusal path from the docs page alone. A person's step. Check: Runner operator completes join, poll, and session against a dev build from the docs page alone
- Run the definition of done: wiring, route ANY /runner/v1 with a runner credential, e2e step against the real directory, docs page, usability check, make check. A person's step. Check: `make check` with zero findings

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main checks if a runner version is ahead (`pkg/runner/version.go:128 (func CheckVersionAhead)`)
- Main warns once on a missing header (`pkg/dustpan/warn_limiter.go:98 (func warnLimiter.warnMissingHeader)`)
- Main has a JSON parse invalid helper in clientgen (`internal/clientgen/parse_json.go:27 (func invalid)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does the server hide the refusal cause on purpose? No. The proof answer stays generic with the NEW error code runner.bad-proof and no proof detail; other classes get distinct codes. The issue asks to tell missing header, bad body, and bad proof apart.
- Does main already return distinct runner refusal codes? No. The issue records only the bare code runner.invalid for these classes, and the pack lists func CheckVersionAhead and error code runner.invalid with no distinct refusal codes.
- Which ADR governs runner refusal codes? No ADR in the pack touches runner refusals, version checks, or body limits, so adr_check is empty and open questions stays none.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
