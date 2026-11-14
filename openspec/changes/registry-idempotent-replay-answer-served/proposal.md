---
version: 1
---

Issue: #1313.

## Problem

A repeat publish with a stored Idempotency-Key answers the publisher and time of the repeat, not of the first publish. The version list does not say whether the registry serves a release, so a person cannot see why `tofu init` skips a version.

## Evidence

- The registry module routes exist on main. Proof: internal/api/tfe/registry_modules.go:99 (func Handler.moduleRoutes)
- The management module version list route exists on main. Proof: GET /api/dustpan/v1/namespaces/{}/registry/modules/{}/{}/versions
- The management provider version list route exists on main. Proof: GET /api/dustpan/v1/namespaces/{}/registry/providers/{}/versions
- The registry upload route exists on main. Proof: POST /api/dustpan/v1/namespaces/{}/registry/uploads
- Registry error codes exist on main. Proof: internal/errcode/catalog_registry.go:7

## Fix boundary

- Idempotent replay of registry publish returns the stored first publisher and time.
- Registry management version list gains a served flag per release.

Out of scope:

- Registry serve path changes
- Signing key lifecycle changes
- Reason text for unserved releases

## Check

- Add failing replay test for a repeat publish with the same Idempotency-Key in NEW internal/api/idempotent/registry_replay_test.go Check: TestRegistryReplayReturnsFirstPublishAnswer fails on main, passes after fix
- Add failing concurrent replay test for the same Idempotency-Key in NEW internal/api/idempotent/registry_replay_test.go Check: TestRegistryConcurrentReplayMakesOneRelease fails on main, passes after fix
- Add failing served flag test for module and provider version lists in NEW pkg/registry/served_test.go Check: TestRegistryVersionListHasServedFlag fails on main, passes after fix
- Return the stored first answer on replay and refuse a fingerprint mismatch with 409 in internal/api/idempotent and internal/api/tfe/registry_modules.go:99 (func Handler.moduleRoutes) Check: POST /api/dustpan/v1/namespaces/{}/registry/uploads with a reused key returns the first publisher subject and time, using an access token
- Add the served flag to module and provider version lists from the read-only helper in pkg/registry and internal/api/tfe/registry_modules.go:99 (func Handler.moduleRoutes) Check: GET /api/dustpan/v1/namespaces/{}/registry/modules/{}/{}/versions and GET /api/dustpan/v1/namespaces/{}/registry/providers/{}/versions entries carry served, using an access token
- Run an e2e `tofu init` step against the real OpenTofu CLI. It calls GET /api/dustpan/v1/namespaces/{}/registry/modules/{}/{}/versions and POST /api/dustpan/v1/namespaces/{}/registry/uploads with an access token, through the registry read and write authorization gates. Add the docs page and check the usability from the docs page alone. Check: The e2e step shows served flags, `tofu init` picks only served releases, the docs page shows the flag, `make check` reports zero findings

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main wires the registry module routes through the TFE handler. (`internal/api/tfe/registry_modules.go:99 (func Handler.moduleRoutes)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

Not spec-ready: a code check fails: task "Run an e2e `tofu init` step against the real OpenTofu CLI. I" is not confirmed by the agent alone: end it with one fixed phrase: "Confirmed by: CI (...)", "Confirmed by: the driver's gates." or .

Questions considered and answered:

- Does any ADR fix the idempotent replay answer or the version list fields? The pack lists no ADR, so no ADR text fixes them. The change follows the project rules on stable IDs and stored references.
- What happens when the same Idempotency-Key repeats with another body or caller? The key is scoped to caller, namespace and body fingerprint. A mismatch is refused with 409 and creates no release.
- How is the stored answer kept? The first answer is kept byte for byte with the publisher subject string. A later rename or removal does not change the replayed bytes.
- What happens on a concurrent repeat while the first publish still runs? An atomic claim of the key makes a concurrent repeat wait for the first answer. Only one release is created.
- Is a failed first publish stored? No answer is stored for a failed first publish. The same key stays retryable.
- Where is the replay record kept? No new table is added. The existing Idempotency-Key record keeps its retention, cleanup and backup behavior.
- How do releases published before this change replay? They have no stored answer. A repeat under an old key runs a new publish.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
