---
version: 1
---

Issue: #2135.

## Problem

As a Runner operator, a runner that cannot start a job leaves no record of why, because the server drops the release reason.

## Evidence

- The releaseClaim handler exists and decodes ReleaseRequest. Proof: internal/api/runnerapi/protocol.go:706 (func Handler.releaseClaim)
- ReleaseRequest carries a reason for people. Proof: pkg/runner/protocol/protocol.go:304: // ReleaseRequest gives back a claim. Reason is for people.
- No code names ImagePullBackOff. Proof: rg -n -F --glob '!*_test.go' 'ImagePullBackOff' pkg internal cmd returns 0 matches

## Fix boundary

- Store the ReleaseRequest reason on the claim record with a 500-rune cap, CR and LF replaced by spaces, other controls removed, and invalid UTF-8 replaced.
- Return the stored reason in the NEW field releaseReason of the existing runner management claim read.
- Refuse an oversize reason with the existing error code runner.invalid without repeating input.
- Edit the baseline of each dialect in place for the NEW reason column and extend the Store port.

Out of scope:

- Closed cause list and raw Kubernetes reason and message from the insights research
- Console display of the stored reason
- Retry policy for claims released before session open

## Check

- Write the failing server test TestReleaseClaimStoresReason in the NEW file internal/api/runnerapi/release_reason_test.go. It calls the existing route ANY /runner/v1 with a runner token handoff credential and passes the claim handoff check. Check: TestReleaseClaimStoresReason fails on main.
- Write the hostile test TestReleaseReasonHostileInputs in the NEW file internal/api/runnerapi/release_reason_hostile_test.go for 501 runes, CRLF, NUL, ESC, U+2028, and invalid UTF-8. Check: TestReleaseReasonHostileInputs fails on main.
- Add the NEW reason column to the claim table in the baseline of each dialect in place, in pkg/store/sqlite and pkg/store/postgres, and extend ClaimRecord in pkg/runner/store.go. Check: Store conformance tests pass for both dialects.
- Extend the Store port in pkg/runner with the release reason for third-party stores and add a conformance test in the NEW file pkg/runner/runnertest/release_reason.go covering adapters pkg/store/sqlite and pkg/store/postgres. Check: TestRunnerManagementReturnsReleaseReason passes.
- Pass the sanitized reason from releaseClaim in internal/api/runnerapi/protocol.go to the store and return it in the existing runner management claim read as the NEW field releaseReason. Check: TestReleaseClaimStoresReason passes against ANY /runner/v1 with the handoff token.
- Add the new file e2e/runner_release_reason_test.go (NEW file) for a runner release with a reason. It calls the existing route ANY /runner/v1 with a runner token handoff credential. Confirmed by: CI (e2e job). Check: The e2e step against ANY /runner/v1 with the runner handoff token shows the stored reason.
- Add the new file docs/guides/runner-release-reason.md (NEW file) with the field, the 500-rune cap, and the curl command. Confirmed by: the driver's gates. Check: The docs page builds with make docs-lint.
- Check that the Runner operator completes the read from the docs page alone and each error names one command. A person's step. Check: Usability check with the Runner operator from the docs page alone.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The releaseClaim handler decodes ReleaseRequest and drops the reason. (`internal/api/runnerapi/protocol.go:706 (func Handler.releaseClaim)`)
- ClaimRecord is the claim record type. (`pkg/runner/store.go:306`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 136: The Kubernetes coordinator** (not owner-tagged).

- Decisions that apply: 5 (current).
- Changed by: ADR 139 extends decision 9; ADR 305 extends. Chain to the end: ADR 139, ADR 305, ADR 282, ADR 304, ADR 343, ADR 310.
- Current rule: this ADR, with ADR 139, 305.
- Quote: "A Job that fails before its launcher opened a session, for example on an image pull error or an unschedulable pod, makes the coordinator release the claim. Another runner can take it."
- Effect: Decision 5 requires the release of a claim whose launcher never opened a session. This change keeps that release path and stores the existing ReleaseRequest reason field. It changes no ADR text.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does an ADR require the reason to reach the server or the console? No. ADR 136 decision 5 requires the release only. The pack text of decision 2 covers the pod template and names no reason field and no console display. The issue text that cites decision 2 for a console display is not supported by the pack text, so this change stores only the existing reason field and changes no ADR text.
- What does an empty reason do? An empty reason after trim is accepted and the stored reason does not change. This keeps existing runners that release with no reason working.
- What wins on a repeated release, an empty repeat, or a takeover? A repeated release with a non-empty reason replaces the stored reason. A release with an empty reason keeps the prior reason. A takeover is a new claim under ADR 136 decision 5 and holds no reason.
- How are the cap, the sanitizer, and body limits ordered? The 500-rune cap applies to the decoded reason first and refuses with runner.invalid. Then the sanitizer replaces CR and LF with spaces, removes other C0 and C1 controls, U+2028 and U+2029, replaces invalid UTF-8 with U+FFFD, and trims the ends. Unknown JSON fields are ignored and the 8 KiB body limit still applies.
- Does the management read need a new route or action? The NEW releaseReason field extends the existing runner management claim read under ANY /api/runners/{}/pools. It needs no new route and no new authorization action. The read returns the stored string verbatim.
- Does the release write audit or logs? No new audit entry is written for the release and the reason is not written to the log. The management read returns the stored value verbatim after the stored sanitizer already ran.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
