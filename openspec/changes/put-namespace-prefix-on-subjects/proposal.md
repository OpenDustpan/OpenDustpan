---
version: 1
---

Issue: #575.

## Problem

A namespace administrator makes service principals for jobs in one namespace. The flat subject space lets that admin probe installation subjects through duplicate answers and reserve them before others use them, so new subjects need a namespace partition.

## Evidence

- ValidateSubject exists in pkg/principal. Proof: pkg/principal/principal.go:326 (func ValidateSubject)
- Namespace service helpers exist in pkg/identity. Proof: pkg/identity/namespace_service.go:61 (func NamespaceService)
- ValidateSubject has callers in tempadmin, host and accesstoken code. Proof: pkg/tempadmin/service.go:276, cmd/opendustpan/host.go:204, pkg/accesstoken/host.go:143

## Fix boundary

- New namespace principal creates take a short name and store the NEW subject namespace:<namespace ID>/<short-name>.
- ValidateSubject refuses the reserved prefix namespace: by ASCII case-insensitive match on installation routes.
- The stored subject is at most 256 characters.
- Identity provider subjects skip ValidateSubject.
- The baseline schema keeps its shape with no numbered migration.

Out of scope:

- Other backlog items such as cost gates, registry life cycle and import adapters.
- Rename of existing rows of a namespace.

## Check

- Write failing tests for the prefixed subject, the allowlist, the 256 character bound and the reserved prefix in NEW pkg/principal/namespaced_test.go. Check: go test ./pkg/principal -run TestNamespacedSubject fails on main
- Wire the create route POST /api/server/namespaces/{namespace}/principals to the NEW constructor NewNamespacedSubject in its handler with gate namespace:manage-principals and an access token. Confirmed by: CI (go test ./internal/servertest) Check: POST /api/server/namespaces/{namespace}/principals with an access token under gate namespace:manage-principals succeeds for ci-deploy in acme and beta and a duplicate answers 409
- Write the docs page NEW docs/guides/namespace-principals.md and add an e2e step for POST /api/server/namespaces/{namespace}/principals with an access token under gate namespace:manage-principals. A person's step. Check: POST /api/server/namespaces/{namespace}/principals with an access token under gate namespace:manage-principals shows the stored subject and NAMESPACE:acme/x answers 400 in make e2e

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- ValidateSubject checks a candidate subject. (`pkg/principal/principal.go:326 (func ValidateSubject)`)
- NamespaceService builds the service identity of a namespace. (`pkg/identity/namespace_service.go:61 (func NamespaceService)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 67: Who creates and administers a service principal** (not owner-tagged).

- Decisions that apply: 7 (changed).
- Changed by: ADR 70 changes decisions 2, 3, 4 and 5; ADR 70 extends decision 6; ADR 210 changes; ADR 215 changes decision 7; ADR 369 extends decisions 1 and 6. Chain to the end: ADR 70, ADR 210, ADR 215, ADR 369, ADR 142, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 354, ADR 247, ADR 286, ADR 248, ADR 370, ADR 149, ADR 297, ADR 273, ADR 335, ADR 351, ADR 301, ADR 339, ADR 295, ADR 333.
- Current rule: this ADR, with ADR 70, 210, 215, 369.
- Quote: "A namespace can have its own service principals. - A principal of a namespace belongs to one namespace. A holder of the new action `namespace:manage-principals` on the namespace makes it. The built-in namespace administrator role holds this action. - A grant to a principal of a namespace can only name that namespace, or an object inside it. The server refuses any other grant. - A principal of a namespace cannot be a member of a group, because a group belongs to the whole installation (ADR 52 decision 2). - A holder of `principal:manage-tokens` on the namespace can make the tokens of every principal of that namespace."
- Effect: Follows decision 7. The prefix records the one namespace of the principal in its stored subject.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Which separator marks the partition? No ADR sets it. The issue fixes namespace: and / and the design keeps them.
- What is the maximum length? No ADR sets it. The issue fixes 1 to 128 characters for the short name and the stored subject is at most 256 characters.
- Does the create body keep subject? Yes. The field keeps the name subject and holds the short name at the boundary. The storage keeps the prefixed subject.
- How does a namespace rename affect the stored subject? The stored subject embeds the stable namespace ID, so a rename of the display name keeps it valid. The change stores no display name in the subject.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
