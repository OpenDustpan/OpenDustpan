---
version: 1
---

Issue: #951, #1089, #1094, #1149.

## Problem

A namespace administrator publishes providers and modules for a namespace. When a version is old or bad, the administrator retires or revokes it, and developers see the effect on next install without a silent fallback to an unsafe version.

## Evidence

- The server has a registry read path for module versions. Proof: internal/api/tfe/registry_modules.go:108 (func Handler.moduleVersions)
- The server defines registry error codes including not-found and version-exists. Proof: internal/errcode/catalog_registry.go:21 (error code registry.not-found)
- Retired and revoked life cycle words already appear in server tests for runners and tokens. Proof: internal/api/runnerapi/refusal_test.go:261 (func TestRetiredRunnerIsRefused)

## Fix boundary

- One stored life cycle state per provider and module version: published, retired, revoked.
- Retired versions stay pinned-installable and hidden from new resolution; revoked versions are never served.
- One audit entry plus one version event per retire and revoke change in the same transaction.
- User docs page for the life cycle.

Out of scope:

- Automatic retire rules
- Vetted supply chain approvals and scanning from #1089
- OpenTelemetry run traces from #1094
- Module versions from git tags and OCI paths from #1149

## Check

- Add the NEW file `pkg/registry/registry_lifecycle_test.go` for retired pinned install, retired hidden from new resolution, and revoked refused Check: The NEW file `pkg/registry/registry_lifecycle_test.go` with TestRegistryRetiredPinnedInstalls fails on main; No route: the test reads the registry store only.
- Add the NEW file `internal/api/tfe/registry_lifecycle_test.go` for PUT provider and module version state, illegal move, unauthorized denied, and audit entry plus event per change Check: TestRegistryLifeCycleAudit in the NEW file `internal/api/tfe/registry_lifecycle_test.go` fails on main via PUT /api/dustpan/v1/namespaces/{namespace}/registry/providers/{name}/versions/{version} with an access token.
- Store state per version and enforce retired and revoked on read, list, mirror, and download paths Check: PUT /api/dustpan/v1/namespaces/{namespace}/registry/providers/{name}/versions/{version} and PUT /api/dustpan/v1/namespaces/{namespace}/registry/modules/{name}/{system}/versions/{version} with an access token change state with the NEW action registry:manage-versions on the namespace.
- Add e2e step against real OpenTofu through the registry. Confirmed by: CI (e2e job with real OpenTofu) Check: e2e run with real OpenTofu via GET /api/dustpan/v1/namespaces/{namespace}/registry/providers/{name}/versions with an access token: pinned retired version installs, revoked version fails with state in CLI message. Confirmed by: CI (e2e job with real OpenTofu)
- Add user docs page for retire and revoke Check: Docs page for registry life cycle renders from docs page alone.
- Run full check with zero findings. Confirmed by: CI (make check gate) Check: `make check` reports zero findings. Confirmed by: CI (make check gate)

Depends on:

- #949

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The server lists module versions for a namespace. (`internal/api/tfe/registry_modules.go:108 (func Handler.moduleVersions)`)
- The server converts audit rows to API entries. (`internal/api/audit/audit.go:173 (func toAuditEntry)`)
- A retired runner is refused by the runner API. (`internal/api/runnerapi/refusal_test.go:261 (func TestRetiredRunnerIsRefused)`)
- A revoked token ends lease requests in the Pulumi API. (`internal/api/pulumi/lease_revoke_test.go:123 (func TestRevokedTokenEndsLeaseRequests)`)
- The profile provider package has a registry test. (`pkg/profile/provider/registry_test.go:20 (func TestNewRegistry)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 250: Defense in depth for security code** (not owner-tagged).

- Decisions that apply: 1 (current), 2 (current), 3 (current), 4 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Never only one layer. Every security-relevant path has at least two independent layers. Each layer must stop the attack alone, without help from the others."
- Quote: "Every layer has its own test. The test sends hostile input directly to that layer, as if every other layer had failed. A layer without its own test counts as missing."
- Effect: The revoked refusal has two independent layers, store filter and download route, each with its own hostile test.
- Conflict: none.

**ADR 257: Providers and modules: the tools' own registry protocols first, and no OCI endpoint now** (not owner-tagged).

- Decisions that apply: 4 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The build tool can write OCI later. A command of `dustpan-provider` that pushes the archives of ADR 177 to an OCI registry of the team is a possible later change of ADR 177. It is not decided here."
- Effect: This change adds no OCI endpoint and changes only the existing registry version reads.
- Conflict: none.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current), 2 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "Docs in the same pull request. A pull request that adds or changes a feature that a person or a client can see adds or changes its page in the user documentation. A feature that a person or a client can see has a route, a setting, a command, a flag, a console page or an error that a user reads. A pull request that changes only internal code needs no page."
- Quote: "No page, not done. A reviewer refuses such a pull request when the page is not in it. A later pull request for the docs does not make the first one done."
- Effect: The change ships its user docs page in the same pull request.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does the life cycle need an OCI endpoint? No. ADR 257 decision 4 keeps an OCI push as a later change only.
- Does the change need a docs page? Yes. ADR 214 decisions 1 and 2 require the docs page in the same pull request.
- Does retired mean pinned installs still work while new resolution hides the version? Yes. Issue scope states retired versions stay installable for pinned users but are hidden from new resolution, and revoked versions are not served.
- Does the revoked refusal need defense in depth? Yes. ADR 250 decisions 1 through 4 apply. Each layer stops the bad read alone and has its own hostile test.
- How are the NEW action and NEW error code named and checked? The NEW names follow the existing registry.* error code family. The NEW action registry:manage-versions is checked on the namespace at the point of use. No existing role gains it by default.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
