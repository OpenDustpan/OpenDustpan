---
version: 1
---

Issue: #2573.

## Problem

Two webhook senders use the same header name and version for different signed data, so a receiver written for one fails on the other with no clear cause.

## Evidence

- The notify sender and the log-sink sender use the same header name with different covered data. Proof: Issue #2573 body cites pkg/notify/sign.go:21,37,52-56 and pkg/logsink/webhook/webhook.go:10-11,48,220.
- Main tests the old log-sink sign form over timestamp and body only. Proof: TestSignIsHMACOverTimestampAndBody at pkg/logsink/webhook/webhook_test.go:211 covers the old form.

## Fix boundary

- Add NEW leaf package pkg/webhooksig with shared Sign and Verify on the notify form.
- Make pkg/logsink/webhook sign with the shared call, send a generated batch delivery ID, and accept one or two secrets.
- Refuse the old log-sink form and unknown secrets, and update the receiver guide.

Out of scope:

- Forge commit adapter changes
- New forge webhook verification

## Check

- Add failing test TestLogSinkBatchVerifiesWithSharedVerify in NEW file `pkg/webhooksig/sign_test.go` for shared verify of a log-sink batch. Confirmed by: CI (`make lint`) Check: NEW file `pkg/webhooksig/sign_test.go` TestLogSinkBatchVerifiesWithSharedVerify fails on main, passes after.
- Add failing test TestOldLogSinkFormRefused in NEW file `pkg/webhooksig/sign_test.go` for refusal of the old timestamp-body form in `pkg/logsink/webhook/webhook.go`. Confirmed by: CI (`make lint`) Check: NEW file `pkg/webhooksig/sign_test.go` TestOldLogSinkFormRefused fails on main, passes after.
- Add failing test TestUnknownSinkSecretRefused in NEW file `pkg/webhooksig/sign_test.go` for refusal of unknown secrets during rotation in `pkg/notify/sign.go`. Confirmed by: CI (`make lint`) Check: NEW file `pkg/webhooksig/sign_test.go` TestUnknownSinkSecretRefused fails on main, passes after.
- Move Sign, Verify, header names, tolerance, and secret cap from `pkg/notify/sign.go` to NEW leaf package `pkg/webhooksig` with stdlib imports only and keep aliases in `pkg/notify/sign.go`. Confirmed by: CI (`make lint`) Check: rg webhooksig.Sign shows callers in `pkg/notify/sign.go` and `pkg/logsink/webhook/webhook.go`
- Make `pkg/logsink/webhook/webhook.go` sign each batch with the shared call, send a generated batch delivery ID in Dustpan-Notification-Id, and accept one or two secrets. Confirmed by: CI (`make lint`) Check: `make lint` with zero findings
- Add the NEW package `pkg/webhooksig` to the depguard domain list with no other import change. Confirmed by: CI (`make lint`) Check: depguard config lists the NEW package in the domain list
- Update the receiver guide in `pkg/logsink/webhook/webhook.go` docs comment, run the receiver verification path, run usability check, run `make check` with zero findings. A person's step. Check: Operator completes the receiver guide path from the docs page alone.

Depends on:

- `forge-github-commit-adapter-decides-hardening` (the part before it in the split)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main caps active signing secrets at two. (`pkg/notify/route.go:39 (func MaxSigningSecrets)`)
- Main defines the signature tolerance used by the notify form. (`pkg/notify/sign.go:34 (func SignatureTolerance)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Does the owner accept the notify form as the one shared webhook signature scheme before the first tag?
   - Options: Accept the notify form with delivery ID and rotation as the one shared scheme before the first tag; Keep two schemes with different headers and prefixes.
   - Provisional decision (owner review 14 Nov): Accept the notify form with delivery ID and rotation as the one shared scheme before the first tag.
   - Reason: The wire form is a one-way door at the first tag and the split scope says this part waits for owner confirmation. Benchmark: AWS practice: one versioned signature scheme per sender family with delivery ID, timestamp, rotation, and a dated removal of the old form.
   - Persona impact: Operator updates webhook receivers once; the receiver guide shows the exact verify call so no persona loses delivery without a stated step. Escape hatch: Before the first tag the old log-sink form stays refused only after the owner accepts the one-way wire choice; receivers pin the shared verify call.

Questions considered and answered:

- Which signature form wins for both senders? The shared form is the notify form because only it carries a delivery ID and accepts two secrets for rotation, per issue #2573. The change waits for owner confirmation before the first tag.
- Does any ADR decide the webhook signature scheme? No ADR in the pack touches webhook signing. The pack adrs list is empty, so no ADR check entry applies.
- How does the log sink use a form that needs a delivery ID? The log-sink batch gets a generated delivery ID sent as Dustpan-Notification-Id so the shared Verify path over delivery ID, timestamp, and body applies without a forge change.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
