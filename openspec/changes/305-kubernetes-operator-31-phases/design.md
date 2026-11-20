## Context

This change ships no code, no route, no schema change, and no operator binary change. It records that RFC 31 phases 3 and 4 stay open under ADR 305 decision 7 and wait on the Catalog and on runners scoped to namespaces. The operator work starts only after those two land, through OpenDustpan jobs and gates through the REST API.

## Decisions

1. **Record only, no behavior.** This change adds no API, no CLI, no docs page, and no operator binary change. Each open point of ADR 305 decision 7 stays open in RFC 31: resource names and API group, resource versions and conditions that fit kstatus, blueprint rollout relation, namespace-scoped install, Kubernetes namespace map, drift status, and deletion policy.
2. **Keep the decided order.** The operator comes after the Catalog ships and after runners scoped to namespaces, per ADR 305 decision 6. This change decides no milestone and starts no coordinator chart or binary work.
3. **Keep the decided shape.** When the refining RFC lands, it follows the decided points: tool-neutral resources with one tool field, work through jobs and gates with no operator state and no private endpoint, shared coordinator chart and binary, and kstatus status with `status.observedGeneration` and `Reconciling`, `Stalled`, `Ready` conditions from the first version.

## Structure

No structure change. No new stored type and no new unexported type. A record needs no constructor, so there is no bug class for a structure to remove.

## Risks

- A reader treats a provisional answer of the proposal as a decided rule. The proposal marks each one provisional for owner review, and ADR 305 decision 7 and decision 8 stay the source of what is open.
- A later change starts operator code before the Catalog or scoped runners land. The order decision above refuses it until those two ship.

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| none affected | none | none | This change adds no server code, so no latency, size or availability target changes. |

## Failure modes

| Step | Failure | Retry | State after |
| --- | --- | --- | --- |
| Record the open points | The proposal drifts from ADR 305 | Fix the record and run the factory checks again | No API, CLI, or docs change |
