---
version: 1
---

Issue: #924.

## Problem

A namespace administrator manages short-lived stacks and workspaces such as previews and sandboxes. They set an end at a date or after inactivity so unused units close through a safe path instead of running on.

## Evidence

- Issue #924 decides TTL as a unit setting with blueprint default and namespace maximum. Proof: 43.1 (owner, 2026-10-18): TTL is a unit setting, with a blueprint default and a namespace maximum.
- Issue #924 decides three expiry actions and blocks delete while resources exist. Proof: 43.3: at expiry: a destroy change order through approval rules when the unit has a runner pool; else a write-back delete for Catalog units in git; else a notice. Delete stays blocked while resources exist.
- Issue #924 decides TTL from a date and from inactivity. Proof: 43.4: TTL from a date AND from inactivity.
- Issue #924 keeps automatic delete of a unit with resources out of scope. Proof: Out: automatic delete of a unit that still has resources.
- The design status needs an ADR from the RFC 43 draft. Proof: Needs ADR (RFC 43 draft).

## Fix boundary

- Time-to-live setting with at date and inactive-for duration, blueprint default, and namespace maximum
- Durable schedule that finds expired units with notices 24 hours before expiry
- Three expiry actions with delete blocked while resources exist

Out of scope:

- Automatic delete of a unit that still has resources
- Schedules work from #720 and blueprint work from #901
- Registry, cost, and import behavior

## Check

- Write the failing over-limit save test in NEW pkg/unitttl/service_test.go. Check: TestSaveTimeToLiveAboveMaximumIsRefused fails on main. No route: the test calls SaveTimeToLive in memory only.
- Write the failing pool expiry test in NEW pkg/unitttl/expiry_test.go. Check: TestExpiredPoolUnitWaitsForApproval fails on main. No route: the test opens the order in memory only.
- Write the failing notice-only and inactivity tests in NEW pkg/unitttl/expiry_test.go. Check: TestExpiredUnitWithNoPoolGetsNoticeOnly fails on main. No route: in memory only.
- Wire the setting, the maximum check and the NEW action unit:set-time-to-live on the unit settings route. Check: PUT /api/dustpan/v1/namespaces/{}/units/{}/settings with an access token passing the NEW action unit:set-time-to-live shows the expiry and its source.
- Add the durable expiry claim and the three expiry actions with audit entries. Check: One destroy change order after two ticks and a restart; the test reads GET /api/dustpan/v1/namespaces/{}/units/{}/settings with an access token.
- Ship the docs page. Check: Docs page for time to live passes `make docs-lint`.
- Add an e2e step against the real CLI that sets a time to live through the unit settings route. Confirmed by: CI (make e2e). Check: E2e step sets a time to live through PUT /api/dustpan/v1/namespaces/{}/units/{}/settings with an access token.
- Run the full check. Confirmed by: the driver's gates. Check: `make check` reports zero findings.

Depends on:

- #720
- #901

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main has a unit settings store read. (`pkg/store/postgres/unitsettings.go:21 (func Store.UnitSettings)`)
- Main has unit settings error specs. (`internal/errcode/catalog_unitsettings.go:22 (func unitSettingsSpecs)`)
- Main has an approval rules service read. (`pkg/gate/config.go:206 (func ConfigService.ApprovalRules)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current), 2 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "Docs in the same pull request."
- Quote: "No page, not done."
- Effect: The setting, the refusal and the notice are seen by a person, so the pull request ships its docs page.
- Conflict: none.

## Open questions

1. How long before expiry does the notice go out and to whom? Decides: PM.
   - Options: Notice 24 hours before, repeat each 24 hours while blocked; Notices 12 and 24 hours before only.
   - Recommended: Notice 24 hours before, repeat each 24 hours while blocked Reason: One repeat notice keeps the path safe without mail load.
2. What happens when a lowered namespace maximum falls below stored values? Decides: PM.
   - Options: Mark over-limit and send a notice, keep stored value; Refuse new writes until the value fits.
   - Recommended: Mark over-limit and send a notice, keep stored value Reason: Marking avoids a mass rewrite and names the next step.

Not spec-ready: a code check fails: `internal/servertest/roundtrips_conflict_test.go` is in the touches of the stack plan, but the change names no such file: the plan is stale; run plan-refresh; a code check fails: `internal/servertest/roundtrips_model_test.go` is in the touches of the stack plan, but the change names no such file: the plan is stale; run plan-refresh; a code check fails: `internal/servertest/roundtrips_test.go` is in the touches of the stack plan, but the change names no such file: the plan is stale; run plan-refresh; question 1: the benchmark names none of Google, AWS, PCI DSS, SOC 2, ISO 27001; question 2: the benchmark names none of Google, AWS, PCI DSS, SOC 2, ISO 27001.

Questions considered and answered:

- Does any accepted ADR already define the time to live setting? No. RFC 43 is discussing, not an accepted ADR. Issue #924 records the shape as owner-fixed input: TTL is a unit setting with blueprint default and namespace maximum.
- Does the docs-page rule apply? Yes. ADR 214 decision 1 applies because a person sees the setting, the refusal and the notice.
- Does the change treat the owner-fixed 43.1 shape as open? No. Issue #924 records 43.3 order and 43.4 both forms as decided input. Open questions ask only notice timing and lowered-maximum handling.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
