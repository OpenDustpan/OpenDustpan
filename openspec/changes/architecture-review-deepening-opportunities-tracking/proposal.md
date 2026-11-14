---
version: 1
---

Issue: #1399, #1400, #1464, #3037.

## Problem

An embedder builds against pkg and needs a stable import surface. The reviews list copied error, route, audit and journal helpers, a hidden store contract and import edges against the shared-core rule. No single index tracks them.

## Evidence

- Issue #1399 F3 names near-copy override and audit helpers in the two OpenTofu adapters. Proof: Pack rg first_lines list internal/api/tofuhttp/tofuhttp.go:82 and internal/api/tfe routes; main has internal/api/tfe/overrides.go:74 (func Handler.allowance) and internal/api/tofuunit/tofuunit.go:100 (func Rules.RecordOverrideRefusal); paths internal/api/tfe/audit.go and internal/api/tofuhttp/audit.go exist.
- Issue #3037 has 4 numbered findings covering 6 import edges, including helpers importing apidesc. Proof: Main has internal/api/apidesc/request.go:313 (func HeaderValues); paths internal/clientip/clientip.go and internal/sessionauth/sessionauth.go exist.
- The session marker helper exists and adapters call it. Proof: Main has internal/sessionauth/sessionauth.go:262 (func WithSession); pack rg first_lines list internal/api/tfe/routes.go:599 with sessionauth.WithSession.
- No shared problem or route kit exists yet on main for the five tool-neutral adapters. Proof: Path internal/api/problem exists but pack rg for writeError lists only internal/api/runnerapi/errors.go:53 and no shared problem writer used by inventory, audit, profile, objectcontext and notifications.

## Fix boundary

- Index F1 to F4, F5 to F9 and F10 to F15 of #1399, each with its follow-up and gate label.
- Name the PM-gated decisions as separate follow-ups: F1b alignment, F2b token-scheme plus session-flag plus ScopePublic plus key-owner drifts, F3b empty TokenID in the workspace-delete audit entry, F4 journal decode that changes public ports, and #1400 finding 2 audit fail-open with nil store.
- Index findings 1 to 8 of #1400 and the nit groups of #1464. Link the 4 numbered findings of #3037 covering 6 import edges to the existing changes exported-pkg-signatures-name-internal, the four new-services-adapters-land-in changes, and errcode-catalog-imports-domain-packages instead of new follow-ups. This change decides none of them and lands last.

Out of scope:

- Implementation of any F1 to F15 refactor
- Implementation of any PM-gated wire, audit or security behavior change
- Implementation of the SDK surface freeze

## Check

- Write the failing test TestIndexDriftCheck in the NEW file internal/archindex/index_test.go for the scenario Index-drift-check. Check: `go test ./internal/archindex -run TestIndexDriftCheck` fails on main
- Re-run the evidence of every finding on current main, starting with internal/api/tfe/overrides.go:74, and save the drift log in the NEW file docs/guides/architecture-review.md. Check: `rg -n -F 'allowance' internal/api/tfe` runs and the log lists each result
- File follow-ups for F1 to F4, including F1b Cache-Control and errorCode alignment, F2b token-scheme and session-flag and ScopePublic and key-owner drifts, F3b empty TokenID, and F4 journal decode, and list them in docs/guides/architecture-review.md. Check: The index lists F1, F1b, F2, F2b, F3, F3b and F4 with gate labels
- File follow-ups for F5 to F9 and list them in docs/guides/architecture-review.md. Check: The index lists F5 to F9 with gate labels
- File follow-ups for F10 to F15 and list them in docs/guides/architecture-review.md. Check: The index lists F10 to F15 with gate labels
- File follow-ups or RFCs for findings 1 to 8 of #1400, including the finding 2 nil audit store fail-open, and list them in docs/guides/architecture-review.md. Check: The index lists findings 1 to 8 with the audit fail-open interim rule
- Link the #1464 nit groups plus the 4 numbered findings of #3037 to exported-pkg-signatures-name-internal, the four new-services-adapters-land-in changes, and errcode-catalog-imports-domain-packages, confirm the intent for edge 3 in internal/clientip/clientip.go and internal/sessionauth/sessionauth.go, and list them in docs/guides/architecture-review.md. Check: The index links the existing changes for #3037 edges and SDK surface instead of new follow-ups
- Refine this change when the SDK surface RFC is accepted, in docs/guides/architecture-review.md. Confirmed by: CI (make check). Check: Each finding links to its follow-up and `make check` reports zero findings

Depends on:

- exported-pkg-signatures-name-internal
- errcode-catalog-imports-domain-packages
- new-services-adapters-land-in-settings-collection-job
- new-services-adapters-land-in-trigger-internal
- new-services-adapters-land-in-gate-internal
- new-services-adapters-land-in-principal-guard
- `exported-pkg-signatures-name-internal` (order of the milestone review)
- `errcode-catalog-imports-domain-packages` (order of the milestone review)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The OpenTofu override allowance helper exists on the tfe handler. (`internal/api/tfe/overrides.go:74 (func Handler.allowance)`)
- A shared override refusal recorder exists on the tofuunit rules. (`internal/api/tofuunit/tofuunit.go:100 (func Rules.RecordOverrideRefusal)`)
- The header value reader exists in the apidesc request helper. (`internal/api/apidesc/request.go:313 (func HeaderValues)`)
- The session marker helper exists in sessionauth. (`internal/sessionauth/sessionauth.go:262 (func WithSession)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 12: A backend for many infrastructure-as-code tools** (not owner-tagged).

- Decisions that apply: the ADR as a whole.
- Changed by: ADR 13 extends; ADR 15 extends; ADR 35 changes; ADR 209 changes; ADR 248 changes; ADR 305 extends. Chain to the end: ADR 13, ADR 15, ADR 35, ADR 209, ADR 248, ADR 305, ADR 19, ADR 41, ADR 150, ADR 247, ADR 265, ADR 337, ADR 272, ADR 286, ADR 47, ADR 60, ADR 103, ADR 146, ADR 151, ADR 238, ADR 240, ADR 352, ADR 154, ADR 199, ADR 210, ADR 242, ADR 364, ADR 153, ADR 346, ADR 63, ADR 64, ADR 65, ADR 68, ADR 72, ADR 126, ADR 334, ADR 110, ADR 128, ADR 138, ADR 141, ADR 142, ADR 148, ADR 190, ADR 212, ADR 213, ADR 222, ADR 224, ADR 225, ADR 227, ADR 299.
- Current rule: this ADR, with ADR 13, 15, 35, 209, 248, 305.
- Quote: "Each tool has its own domain package for its model of deployable units and state (`stack` for the Pulumi CLI, `workspace` for OpenTofu) and its own HTTP adapter under `api/<tool>/` with its own route prefix."
- Effect: Index only. The shared OpenTofu helper later lands under internal/api so both adapters use it. The adapters never call each other.
- Conflict: none.

**ADR 35: The SDK lives in pkg/, internal code in internal/** (not owner-tagged).

- Decisions that apply: 2 (current).
- Changed by: ADR 272 changes decision 1. Chain to the end: ADR 272, ADR 364.
- Current rule: this ADR, with ADR 272.
- Quote: "Code that is not part of the SDK lives under `internal/`. The HTTP adapters move to `internal/api/<tool>`. The Go toolchain then stops other modules from importing them."
- Effect: Shared error and route helpers stay under internal/api. This index adds no SDK surface.
- Conflict: none.

**ADR 210: The tool-neutral API: one prefix, resource paths, action names, versions and a style guide** (owner-tagged: decision 6).

- Decisions that apply: 6 (current).
- Changed by: ADR 247 extends decision 3; ADR 286 changes. Chain to the end: ADR 247, ADR 286.
- Current rule: this ADR, with ADR 247, 286.
- Quote: "**Every mutating route takes an `Idempotency-Key` (owner decision 2026-10-18).** Each route of the tool-neutral API that changes data (`POST`, `PUT`, `PATCH`, `DELETE`) accepts the header `Idempotency-Key`."
- Effect: The F2b idempotency-key owner drift touches an owner-tagged rule. This index changes no key owner.
- Conflict: none.

**ADR 250: Defense in depth for security code** (not owner-tagged).

- Decisions that apply: 1 (current), 4 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Never only one layer. Every security-relevant path has at least two independent layers. Each layer must stop the attack alone, without help from the others."
- Quote: "Every layer has its own test. The test sends hostile input directly to that layer, as if every other layer had failed. A layer without its own test counts as missing."
- Effect: F2b drifts and finding 2 audit fail-open stay tracked as separate security follow-ups, each with one hostile test per layer. This index changes no guard.
- Conflict: none.

**ADR 272: The public SDK surface** (owner-tagged: decision 1, 2).

- Decisions that apply: 1 (current), 2 (current).
- Changed by: ADR 364 extends decision 4. Chain to the end: ADR 364.
- Current rule: this ADR, with ADR 364.
- Quote: "The ports and the conformance suites stay in the SDK, so a third party can write an adapter and prove it."
- Quote: "a port lives in the SDK package of its domain."
- Effect: The SDK freeze question extends the port and service split. No port moves in this index.
- Conflict: none.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Which names stay in the public SDK surface before the first tag?
   - Options: Freeze a minimal SDK surface and move the rest to internal before the first tag; Keep the pkg layout and document each exported name as a promise.
   - Provisional decision (owner review 14 Nov): Freeze a minimal SDK surface and move the rest to internal before the first tag.
   - Reason: Finding 1 counts 7491 exported names that freeze at 1.0. The owner confirms the freeze list in a new RFC before any move. This index is blocked on that answer only for the SDK items. Benchmark: Google: Google Cloud client libraries publish a small stable surface and keep internals private.
   - Persona impact: The embedder gains a stable import surface. No persona loses access before the first tag. Escape hatch: The RFC lists a name that stays public on request before the first tag.
   - Owner-tagged ADRs involved: ADR 272 (their words do not change).

Questions considered and answered:

- Does this change decide any PM-gated alignment? No. F1b, F2b, F3b, F4, finding 1 and finding 2 audit fail-open change wire values, audit semantics, public ports or frozen names. Each needs a PM or owner decision or an RFC first.
- Where do shared route, error and audit helpers belong? ADR 12 and ADR 35 decision 2: in a core helper under internal/api. Adapters never call each other.
- How does the SDK surface question relate to ADR 272 and RFC 99? ADR 272 decision 2 splits each domain into a port package and an internal service. It does not set the freeze list. RFC 99 covers only the API description under ADR 264, so a separate RFC sets the freeze list per finding 1 of #1400. The pkg signature and service-move work is already tracked by existing changes, which this index links.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
