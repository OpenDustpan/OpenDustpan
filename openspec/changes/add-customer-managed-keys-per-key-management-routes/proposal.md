---
version: 1
---

Issue: #954.

## Problem

A namespace administrator wraps the data of one namespace with a key in the own KMS of the tenant, so a tenant keeps control of its keys and reads fail with a clear key error when the KMS denies access.

## Evidence

- Issue 954 asks for a per-namespace KMS key, rotation by rewrap, and a docs page. Proof: #954 body Outcome and Scope and Acceptance criteria: wrap through a KMS port, rotation with rewrap, docs page.
- Main runs a store start check and denies unclassified actions. Proof: pkg/store/postgres/storechecks.go:24 (func Store.StartCheck) and pkg/access/access_test.go:466 (func TestUnclassifiedActionIsDenied) in the pack main list.

## Fix boundary

- NEW POST /api/dustpan/v1/namespaces/{}/key for key set and change with NEW action namespace:manage-key.
- NEW GET /api/dustpan/v1/namespaces/{}/key for key read with NEW action namespace:read-key.
- NEW DELETE /api/dustpan/v1/namespaces/{}/key for key clear with NEW action namespace:manage-key.
- NEW POST /api/dustpan/v1/namespaces/{}/key/probe and NEW POST /api/dustpan/v1/namespaces/{}/key/rotate-check with NEW action namespace:manage-key.
- Pre-change probe with namespace identity and confused deputy check, cache per keys.namespaceKeyCacheSeconds, start check, and 503 or 403 outage answers.
- Customer keys docs page and namespace scope audit entries for set, change, clear, probe failure, and rewrap probe.

Out of scope:

- Key record shape and Wrapper port of part one.
- Rewrap schedule and full re-encryption job of part three.
- Choice of KMS client libraries.

## Check

- Add failing service test for set with probe pass and probe fail in NEW file pkg/keysvc/key_test.go. Check: NEW file pkg/keysvc/key_test.go: TestKeySetStoresOnlyAfterProbePasses fails on main.
- Add failing route test for NEW actions namespace:manage-key and namespace:read-key in NEW file internal/api/key/key_test.go. Check: NEW file internal/api/key/key_test.go: TestKeyProbeDeniesWrongNamespace fails on main.
- Add failing start check test for wrapper ID mismatch in NEW file pkg/keysvc/start_test.go. Check: NEW file pkg/keysvc/start_test.go: TestKeyStartRefusesWrapperMismatch fails on main.
- Build NEW key set, change, clear, read, and probe routes with fence and audit entries in NEW file internal/api/key/routes.go. Check: NEW file internal/api/key/routes.go: NEW POST /api/dustpan/v1/namespaces/{}/key/probe returns pass or failed check with access token.
- Build key cache, start check, and outage answers with Retry-After in NEW file pkg/keysvc/cache.go. Check: NEW file pkg/keysvc/cache.go: cache drops entries per keys.namespaceKeyCacheSeconds and on revoke.
- Write customer keys docs page in NEW file docs/guides/customer-keys.md with set, rotate, probe, revoke, and outage steps. Check: NEW file docs/guides/customer-keys.md passes docs lint.
- Run e2e step for set, probe, read, and clear on a dev server with an access token against NEW key routes; BLOCKED by NEW key routes until they land. Check: BLOCKED by NEW key routes: GET and PUT against NEW /api/dustpan/v1/namespaces/{}/key from the docs page alone with an access token, plus Pulumi CLI read during KMS outage shows 503 shape.
- Run usability check, route action check with pkg/access/access_test.go:466, and full check. Check: `make check` reports zero findings.

Depends on:

- add-customer-managed-keys-per-key-record-wrapper
- `add-customer-managed-keys-per-key-record-wrapper` (the part before it in the split)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main runs a store start check at open. (`pkg/store/postgres/storechecks.go:24 (func Store.StartCheck)`)
- Main denies an unclassified action in the access check. (`pkg/access/access_test.go:466 (func TestUnclassifiedActionIsDenied)`)
- Main classifies each action with a route and refuses actions with no route. (`pkg/access/access_test.go:493 (func TestActionsWithoutRoutesAreUnclassified)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 217: Customer-managed keys for each namespace: the key hierarchy and rotation (owner)** (owner-tagged (all decisions)).

- Decisions that apply: 8 (current), 10 (current), 12 (current), 13 (current), 14 (current), 16 (current), 18 (changed), 25 (current), 4 (current).
- Changed by: ADR 220 changes decision 18. Chain to the end: ADR 220, ADR 273, ADR 286, ADR 363.
- Current rule: this ADR, with ADR 220.
- Quote: "A namespace administrator sets it."
- Quote: "A wrapper ID for each level."
- Quote: "Checks before the change."
- Quote: "Each instance keeps the open namespace key versions of a namespace in memory for `keys.namespaceKeyCacheSeconds`: 300 by default, from 60 to 3600."
- Quote: "Entries in the audit scope of the namespace."
- Quote: "Errors in the tool-compatible APIs."
- Quote: "The customer revokes the key."
- Quote: "The KMS cannot answer."
- Effect: This part builds the routes and checks of decisions 8, 10, 12, 13, 14 and 16 and writes the audit entries of decision 25. It uses the key record of part one and leaves rotation of decision 18 and re-encryption to part three.
- Conflict: none.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current), 2 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "Docs in the same pull request."
- Quote: "No page, not done."
- Effect: The customer keys docs page ships in the same pull request as the routes.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Which actions guard the key routes? ADR 217 decision 8 names namespace:manage-key and the read action, held by the namespace admin role. No open grant question stays.
- What checks, cache, and outage behavior apply? ADR 217 decisions 10, 12, 13, 14 and 16 fix the pre-change probe with confused deputy check, the cache setting, the 503 and 403 outage answers and the tool API shapes. No timing or code choice stays.
- What stays in part one and part three? ADR 217 decisions 4, 18 and 24 fix the start check, the rewrap schedule and the backup rule, which belong to part one and part three. This part covers only the start refusal on wrapper mismatch.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
