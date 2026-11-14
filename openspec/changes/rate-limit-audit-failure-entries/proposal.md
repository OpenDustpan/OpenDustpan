---
version: 1
---

Issue: #1273.

## Problem

The site administrator reviews the audit log, but a caller with a valid credential can send refused preference and token requests at any rate and each one appends a failure entry to the @server scope, which fills the log and hides real entries.

## Evidence

- The issue reports refused requests write one failure or denied entry on the preferences route and the token routes. Proof: Issue #1273 body: A refused request writes one audit entry with the outcome `failure` or `denied`. The preferences route (`PATCH /api/dustpan/v1/me/preferences`) and the token routes (`internal/api/server/tokens.go`) both do this for each refusal.
- The token routes live in internal/api/server/tokens.go and the preferences routes live next to them. Proof: Pack path internal/api/server/tokens.go exists; main lists internal/api/server/tokens.go:293 (func Handler.tokenRoutes) and internal/api/server/preferences.go:296 (func Handler.preferencesRoutes).

## Fix boundary

- Add a per-principal token-bucket limit on failure and denied entries for refused requests, applied the same way on every route that writes a refusal entry.
- When over limit, keep the same refusal answer, write no entry, and count dropped entries in a metric.
- Keep policy-check denials and real-change entries outside the limit.

Out of scope:

- Changes to token or preference checks themselves
- Retention or redaction of existing audit entries

## Check

- Add failing limiter test for AllowRefusal: per-principal bucket, global caps, and summary entry Check: TestRefusalLimiterDropsOverLimit in internal/api/server fails on main
- Add failing server test for PATCH /api/dustpan/v1/me/preferences over-limit refusal with access token Check: TestPreferencesRefusalOverLimitKeepsAnswer using PATCH /api/dustpan/v1/me/preferences with access token in internal/api/server fails on main
- Add failing server test for over-limit refusal on the existing token route handler Handler.tokenRoutes, called with an access token, passing the existing route authorizer Check: TestTokenRefusalOverLimitKeepsAnswer driven by Handler.tokenRoutes in internal/api/server/tokens.go with access token fails on main
- Implement NEW internal/api/server/refusallimit.go refusalLimiter with fixed allowlist, per-process LRU table, global caps, summary entry, and metric hook, and wire it into the refusal audit path Check: go test -race ./internal/api/server/... for PATCH /api/dustpan/v1/me/preferences with access token
- Wire route with existing authorization actions for PATCH /api/dustpan/v1/me/preferences with access token, add e2e refusal burst step with access token, update docs page, run usability check from docs page alone Check: PATCH /api/dustpan/v1/me/preferences refusal burst with access token stores at most the limit plus one summary entry; docs page states the limits; `make check` with zero findings

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main has preferences routes handler. (`internal/api/server/preferences.go:296 (func Handler.preferencesRoutes)`)
- Main has token routes handler. (`internal/api/server/tokens.go:293 (func Handler.tokenRoutes)`)
- Main converts stored rows to audit entries in the audit HTTP adapter. (`internal/api/audit/audit.go:173 (func toAuditEntry)`)
- Main has server metrics with audit chain hooks. (`internal/metrics/server.go:262 (func ServerMetrics.SetAuditChainInvalid)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

Not spec-ready: a code check fails: task "Add failing server test for over-limit refusal on the existing token r" runs an acceptance path but names no existing route and credential kind; name them, or write "BLOCKED by <the missing part.

Questions considered and answered:

- Which ADRs does this audit limiter touch? No ADR text is in the pack (adrs: []). The change keeps stored grants, audit kinds and refusal answers unchanged, so the ADR check is empty.
- Should each route enforce its own refusal-entry limit? One limiter at the refusal audit-write path applies the same rule to every route. Per-route rules would drift.
- Should an over-limit refusal change the response? The refused answer stays byte-identical; only the audit write is dropped and counters record drops. Issue #1273 requires this.
- What are the per-principal limit and window? Closed decision in this change: 20 per 60 seconds per verified subject with burst 20, global 200 per 60 seconds for the @server scope, empty-subject 100 per 60 seconds. Fixed code constants documented on the docs page; no setting and no API field in this change.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
