---
version: 1
---

Issue: #1006, #1007, #1008, #1015.

## Problem

A namespace administrator manages shared profiles for many stacks and workspaces. A bad profile save reaches many units at once, so the administrator needs to name a good revision, find profiles by label, undo a bad save, close a bad revision and see what changed without reading secret values.

## Evidence

- Main has no SetTag or Retract implementation Proof: rg -n -F SetTag pkg internal cmd found 0 matches; rg -n -F Retract pkg internal cmd found 0 matches
- Main has a profile conformance suite and profile routes with a showSecrets query Proof: rg profiletest found pkg/profile/profiletest/cases.go; rg showSecrets found internal/api/profile/operations.go with showSecretsQuery
- Main stores profile revisions only and At takes a Number struct ready for tags Proof: Issue #1006 context and path pkg/profile/store.go
- A revision read on main answers entries read-only Proof: Issue #1007 context: A revision read on main answers entries (read only).

## Fix boundary

- SetTag and Retract on the store and At with a tag, with latest fixed and at most 32 other tags
- Labels with SetLabels and GET profile-labels, 0 to 32 per profile
- Rollback as a save, retract with reason and tag moves, diff with changed and unchanged per secret path
- Routes of the ADR 203 decision 12 table for tags, labels, rollback, retract and diff, with notifications profile.tag-changed, profile.label-changed and profile.revision-retracted

Out of scope:

- Entries API RFC (#1007) and any PUT/DELETE entry routes
- Profile and profile-folder authorization objects (#1008)
- Change rules, drafts, open rules and access requests (#1015)
- Taint over referrers and evaluated-host diff of blocks
- Redact with profile:redact

## Check

- Add failing profiletest cases for SetTag, Retract, At with tag, rollback, labels and diff first in pkg/profile/profiletest/cases.go:1. Check: profiletest in pkg/profile/profiletest/cases.go:1 fails on main: retract moves tags, retract of newest fails, routing tag move and rollback need profile:open, diff with showSecrets needs profile:open, tag and label limits, latest fixed, fence and audit entries
- Implement store methods, fences, backup rows and portable spec entries in pkg/profile/store.go and pkg/store/sqlite/profile.go. Check: go test ./pkg/profile/... ./pkg/store/... with sqlite and postgres suites green
- Wire the routes of the ADR 203 decision 12 table for tags, labels, rollback, retract and diff in internal/api/profile/routes.go:26 with notifications profile.tag-changed, profile.label-changed and profile.revision-retracted. Check: Route test in internal/api/profile/routes.go:26 passes including %2F profile and tag names, label list with labelKey and labelValue, retract and rollback routes, Idempotency-Key replay, rename by prf- ID keeping tags, audit content and showSecrets refusal
- Add e2e profiles step for tag, rollback and diff against GET /api/dustpan/v1/namespaces/{}/profiles with an access token. Check: e2e profiles step calls GET/POST /api/dustpan/v1/namespaces/{}/profiles with an access token to tag, roll back and diff against the real server
- Update the profiles API docs page in docs/reference/profiles-api.md. Check: docs/reference/profiles-api.md updated in the same pull request
- Run usability check from the docs page and make check with zero findings. Check: `make check` reports zero findings

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main stores profile revisions with insertProfileRevision in postgres (`pkg/store/postgres/profile.go:62 (func insertProfileRevision)`)
- Main decodes a profile revision row with ProfileRevisionFromRow (`pkg/store/internal/codec/profile.go:47 (func ProfileRevisionFromRow)`)
- Main parses authorization objects with ParseObject (`pkg/identity/identity.go:843 (func ParseObject)`)
- Main defines profile error codes profile.conflict and profile.routing-change-needs-open (`internal/errcode/catalog_profile.go:7 (profile.conflict)`)
- Main serves profile revision reads with a showSecrets query (`internal/api/profile/operations.go:62 (showSecretsQuery)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 200: Profiles: config, secrets and credentials for every tool** (not owner-tagged).

- Decisions that apply: 3 (changed).
- Changed by: ADR 210 changes decision 9; ADR 222 changes decision 3; ADR 224 changes decision 3; ADR 225 changes decision 3; ADR 286 changes; ADR 347 extends decision 10. Chain to the end: ADR 210, ADR 222, ADR 224, ADR 225, ADR 286, ADR 347, ADR 247, ADR 335, ADR 351, ADR 354, ADR 241, ADR 313, ADR 317, ADR 342, ADR 353, ADR 299, ADR 248, ADR 343, ADR 319, ADR 365, ADR 328, ADR 359, ADR 368.
- Current rule: this ADR, with ADR 210, 222, 224, 225, 286, 347.
- Quote: "The errors. `pkg/profile` holds these sentinels. Each port wraps them with `fmt.Errorf("...: %w", err)`, and an adapter translates each driver error into one of them."
- Effect: Uses the fixed sentinel list. New tag, label, rollback, retract and diff paths wrap the existing sentinels. Diff with showSecrets without profile:open is refused with the existing routing-change code.
- Conflict: none.

**ADR 202: Secrets, revisions, tags, change approvals and rotation of profiles** (not owner-tagged).

- Decisions that apply: 2 (changed), 3 (current), 4 (current), 5 (current).
- Changed by: ADR 222 changes decisions 6 and 7; ADR 224 changes decisions 6 and 7; ADR 225 changes decisions 6 and 7; ADR 341 changes decision 2. Chain to the end: ADR 222, ADR 224, ADR 225, ADR 341, ADR 286, ADR 335, ADR 351, ADR 354, ADR 241, ADR 313, ADR 317, ADR 342, ADR 353, ADR 299, ADR 248, ADR 343, ADR 319, ADR 365, ADR 328, ADR 359, ADR 368.
- Current rule: this ADR, with ADR 222, 224, 225, 341.
- Quote: "Revisions. Each save makes a new revision: the number (1, 2, 3 and so on), the document, the principal, the time, an optional reason of at most 500 characters, the Expr version and the SHA-256 of the document."
- Quote: "Tags. A **revision tag** names one revision of one profile."
- Quote: "`latest` always names the newest revision that is not retracted. Nobody moves or deletes it."
- Quote: "Labels. A profile has 0 to 32 labels, each a key and a value, for views, filters and search (the label of ADR 51 decision 7)."
- Effect: Implements tags, labels, rollback, retract and diff as decided. No change to decision words.
- Conflict: none.

**ADR 203: Opening a profile: sessions, attachments, delivery, actions and audit** (not owner-tagged).

- Decisions that apply: 10 (current), 11 (current), 12 (changed).
- Changed by: ADR 210 changes decision 12; ADR 222 changes decision 5; ADR 225 changes decision 5; ADR 241 extends decision 1; ADR 286 changes; ADR 300 changes decision 5; ADR 353 extends decision 1. Chain to the end: ADR 210, ADR 222, ADR 225, ADR 241, ADR 286, ADR 300, ADR 353, ADR 247, ADR 335, ADR 351, ADR 354, ADR 299, ADR 248, ADR 313, ADR 343, ADR 328, ADR 359, ADR 368.
- Current rule: this ADR, with ADR 210, 222, 225, 241, 286, 300, 353.
- Quote: "Notifications. The catalog of types of ADR 145 decision 5 gets:"
- Quote: "`profile.tag-changed`, `profile.label-changed` | A tag or a label changed."
- Effect: Wires the routes of the decision 12 table and emits the decision 10 notifications. Actions stay on the namespace until the decided object of #1008 ships.
- Conflict: none.

**ADR 210: The tool-neutral API: one prefix, resource paths, action names, versions and a style guide** (owner-tagged: decision 6).

- Decisions that apply: 6 (current).
- Changed by: ADR 247 extends decision 3; ADR 286 changes. Chain to the end: ADR 247, ADR 286.
- Current rule: this ADR, with ADR 247, 286.
- Quote: "**Every mutating route takes an `Idempotency-Key` (owner decision 2026-10-18).** Each route of the tool-neutral API that changes data (`POST`, `PUT`, `PATCH`, `DELETE`) accepts the header `Idempotency-Key`."
- Effect: Each mutating tag, label, rollback and retract route accepts Idempotency-Key and replays the stored answer.
- Conflict: none.

**ADR 341: A taint that reaches a secret source is a routing change** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The taint reaches an export key (owner, 2026-11-07). Any change whose taint reaches a secret source, in any export key, is a routing change. It needs `profile:open` as well as `profile:write`. This holds for a save, a rollback, a tag move and the apply of a draft."
- Effect: Tag moves and rollbacks use the same routing-change test as a save. Referrer taint stays out of scope as the issue states.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does this slice need an RFC before implementation? No. ADR 202 decisions 2 to 5 decide revisions, tags, labels, rollback, retract and diff, and ADR 203 decision 12 decides the routes, so the slice is decided and needs no RFC.
- Does rollback or diff need the referrer index or provider port? No. The change uses the own-document taint test only. Taint over referrers and evaluated-host diff of blocks stay out of scope per the issue.
- Should entries API, profile-folder objects, or drafts be built in this change? No. #1007 is a separate RFC-writing change, #1008 is a separate identity-object change, and #1015 is a separate drafts and access-requests change. This slice checks profile actions on the namespace.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
