---
version: 1
---

Issue: #1328, #1343, #1347, #1353.

## Problem

A developer applies review feedback and keeps the API description exact. Review rounds left small defects and one latent drift risk between authorizer calls and the API description. This change clears the nits and locks the mapping with a test.

## Evidence

- The chat loopback constructor exists at its path on main. Proof: pkg/notify/chat/loopback_e2e.go:27 (func NewLoopback)
- No non-test code names the gov-slack host test on main. Proof: rg -n -F --glob '!*_test.go' 'TestGovSlackHost' pkg internal cmd matches 0
- The description carries per-action object kinds. Proof: internal/api/apidesc/apidesc.go:54 ActionObjects map[string]string
- No non-test code defines the helper name suggested in issue 1353 on main. Proof: rg -n -F --glob '!*_test.go' 'newEveryModuleServer' pkg internal cmd matches 0
- A gate test already reads every described adapter through apidesc. Proof: pkg/dustpan/apidesc_gate_test.go:96 (func TestEveryDescribedAdapterReadsThroughApidesc)

## Fix boundary

- Keep and pass through IdempotencyKey in the chat sender as the retry deduplication key, keep NewLoopback as test-only with docs, rename the gov-slack host test.
- Fix RFC 102 text nits and typed-schema nits N1 to N5 as written in the linked reviews, plus N6 embedded-pointer required tag error and N7 kindsOf deduplication.
- Add one test in pkg/dustpan that asserts each described operation records at least one authorizer call unless exempt, and that recorded calls are a subset of op ActionObjects.

Out of scope:

- RFC 102 step-up behavior itself
- Typed schema feature work beyond the listed nits
- Chat sender behavior beyond the three listed nits

## Check

- Create NEW pkg/dustpan/authorizer_subset_test.go with NEW helper newEveryModuleServer that mounts all modules and records authorizer action and kind, sends one request per described operation with namespace acme through the existing route table with an access token, asserts non-empty records except the exempt allowlist, and asserts subset of op ActionObjects. Check: go test ./pkg/dustpan -run TestAuthorizerCallsAreSubsetOfActionObjects -count=1 fails on main then passes after fix
- Write NEW test for the dropped required tag on an embedded-pointer promotion in NEW pkg/dustpan/embedded_required_test.go. Check: go test ./pkg/dustpan -run TestEmbeddedRequiredTag -count=1 fails on main
- Keep IdempotencyKey in the chat Sender interface in pkg/notify/chat and pass it as the retry deduplication key; a repeated key returns the first result. Check: go test ./pkg/notify/chat/... -count=1 passes
- Keep NewLoopback exported in pkg/notify/chat/loopback_e2e.go:27 with test-only docs and rename the gov-slack host test in the pkg/notify/chat tests. Check: go test ./pkg/notify/chat/... -count=1 passes
- Fix the required tag on an embedded-pointer promotion so it raises a field error in internal/api/apidesc/read.go:209. Check: go test ./pkg/dustpan -run TestEmbeddedRequiredTag -count=1 passes
- Deduplicate kindsOf from pkg/store/storetest/inventory_changes.go:42 and its second test copy into one shared test helper keyed by action. Check: rg -n 'func kindsOf' pkg internal shows one definition
- Apply typed-schema nits N1 to N5 as written in the review comment of the typed schemas change in docs/rfc/0102-cli-step-up-for-writes-that-need-a-fresh-authentication.md and internal/api/apidesc/apidesc.go:54. Check: Each nit ticked in issue 1343
- Apply the 8 RFC 102 nits of the round 2 review in docs/rfc/0102-cli-step-up-for-writes-that-need-a-fresh-authentication.md. Check: Each nit ticked in issue 1328 and make docs-lint passes

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Loopback sender constructor exists in pkg/notify/chat. (`pkg/notify/chat/loopback_e2e.go:27 (func NewLoopback)`)
- A gate test already reads every described adapter through apidesc. (`pkg/dustpan/apidesc_gate_test.go:96 (func TestEveryDescribedAdapterReadsThroughApidesc)`)
- A helper named kindsOf exists in store conformance helpers. (`pkg/store/storetest/inventory_changes.go:42 (func kindsOf)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does this change contradict an accepted ADR? No. No accepted ADR is in the pack. RFC 102 is discussing and is not a decision. The work applies review nits as written in the linked reviews and adds one guard test.
- Does the authorizer guard need a new route or gate? No. The guard test reuses the wiring behind pkg/dustpan/apidesc_gate_test.go:96 (func TestEveryDescribedAdapterReadsThroughApidesc) and the ActionObjects map in internal/api/apidesc/apidesc.go:54. No route or gate changes.
- How does the guard avoid a false pass when an operation never reaches the authorizer? Each described operation asserts at least one recorded call. Public operations with no authorizer call are listed in an exempt allowlist with a reason.
- Which option wins for the IdempotencyKey and NewLoopback nits? Keep IdempotencyKey in the Sender interface and pass it as the retry deduplication key. Keep NewLoopback exported and document it as test-only. The options that remove the field or hide the constructor are rejected because they widen the interface change.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
