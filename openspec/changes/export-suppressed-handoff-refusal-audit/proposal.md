---
version: 1
---

Issue: #2599.

## Problem

An Operator monitors runner handoff refusals and cannot see how many refusal audit entries the limiter suppressed, because the count has no metric. The Operator opens the metrics reference docs page, then scrapes ANY /api/server/metrics for the counter; when the counter is missing the scrape shows no series and the one command that helps is `curl $OPENDUSTPAN_URL/api/server/metrics`.

## Evidence

- The production wiring sets OnHandoffRefusalSuppressed to nil. Proof: pkg/dustpan/runners.go:167:		Plans: deps.plans, Artifacts: deps.artifacts, OnHandoffRefusalSuppressed: nil,
- The runner API defines an OnHandoffRefusalSuppressed callback for each refused handoff whose audit entry was suppressed. Proof: internal/api/runnerapi/runnerapi.go:83:	// OnHandoffRefusalSuppressed, when set, runs once for each refused handoff

## Fix boundary

- Wire OnHandoffRefusalSuppressed in pkg/dustpan/runners.go to a Prometheus counter with zero labels.
- List the counter in the metrics reference docs.
- Add a test that the wiring is not nil.

Out of scope:

- Change of the per-window audit limiter or the refusal decision.
- New per-runner or per-cause label dimensions.

## Check

- Add failing wiring test in pkg/dustpan/runners_test.go that the production runner API config sets OnHandoffRefusalSuppressed. Check: TestSuppressedHandoffRefusalWiringIsNotNil in pkg/dustpan/runners_test.go fails on main.
- Wire the callback in the runner area file of pkg/dustpan to the NEW Prometheus counter runner_handoff_audit_suppressed_total with zero labels and already-registered reuse. Check: TestSuppressedHandoffRefusalCounterIncrements in pkg/dustpan/runners_test.go passes with exactly-once increment, including the carried-count and written-entry cases.
- Add route scrape and second-server reuse tests in pkg/dustpan/runners_test.go. They call the existing route ANY /api/server/metrics, served by internal/api/server (the HTTP adapter of the installation API), with an operator access token, and check the NEW counter name and help text. Check: TestSuppressedHandoffRefusalCounterVisibleAtMetrics shows the NEW counter at zero then after suppression, and TestSecondServerReusesCounterRegistration passes.
- Document the NEW counter in the NEW file docs/reference/metrics.md and run the usability walk from the docs page alone. Confirmed by: CI (make check). Check: docs/reference/metrics.md lists runner_handoff_audit_suppressed_total with zero labels, reset-on-restart and rate() note.

Depends on:

- architecture-review-7-nov-composition-dustpan-wiring-split
- `runner-exchange-no-denial-limiter` (order of the milestone review)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main builds handoff refusal decisions with a constructor. (`pkg/runner/handoff.go:63 (func NewHandoffRefusal)`)
- Main appends the handoff refusal audit entry in the runner API handler. (`internal/api/runnerapi/handoffaudit.go:205 (func Handler.appendHandoffRefusal)`)
- Main covers the suppressed-count carry to the next audit window with a test. (`internal/api/runnerapi/handoffaudit_test.go:402 (func TestHandoffRefusalNextWindowCarriesSuppressedCount)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does the metric need per-runner or per-reason labels? No. The issue asks for labels kept to a fixed set, and zero labels keeps one series and avoids cardinality growth.
- Does the change alter when an audit entry is suppressed? No. The change only wires the existing OnHandoffRefusalSuppressed callback to a counter and leaves the limiter decision unchanged.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
