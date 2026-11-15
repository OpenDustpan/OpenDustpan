## Context

Main already stores trigger rows, serves the trigger routes, has the trigger error codes, the guide and the e2e file. This change adds declared actions, audit entries, plan-only enforcement, the save-time cycle check and the read fields.

## Decisions

1. **Service checks before write.** Validate shape, limits and same-namespace rule in the service before write. Serialize follows saves per namespace.
2. **Plan-only start port.** The start path calls only the plan-only port of the job service. It never passes an apply operation.

## Structure

An unexported trigger service type is built only by its constructor, which takes a plan-only Starter interface and a fenced store.

## Risks

- A caller bypasses the plan-only port.
- Two saves at the same time skip the cycle check.

## Diagrams

Today: `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers` on main (from the route table and the handler body, route row internal/api/triggers/routes.go:93, handler internal/api/triggers/serve.go:123).

```mermaid
sequenceDiagram
participant C as Client
participant R as GET route
participant H as listTriggers
participant AZ as Authorizer
participant Fmanager as manager
C->>R: GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers
R->>AZ: no action (ScopeAuthorized)
R->>H: listTriggers
H->>Fmanager: List
H-->>C: answer
```

After this change:

```mermaid
sequenceDiagram
Client->>Server: fire trigger
Server->>Server: start plan job
```

New imports of this change (each one allowed by the module rules):

- `internal/api/triggers` imports `pkg/trigger`

States and transitions after this change:

```mermaid
stateDiagram-v2
[*] --> saved
saved --> fired
fired --> saved
```

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| Trigger save latency including the cycle check | 200 | milliseconds at the 95th percentile | one namespace with up to 20 triggers on each unit |

## Threat model

Data flow and trust boundaries:

```mermaid
flowchart TB
C[Developer] -->|TLS with access token| H[Trigger handler]
H --> S[Trigger service]
S --> J[Job service]
```

| Boundary | STRIDE | Threat | Layer | Guard | Hostile test |
| --- | --- | --- | --- | --- | --- |
| TLS then server | E | A trigger start requests an apply. | limit | The starter allows the plan operation only. | Request an apply through the starter; it refuses. |

## Contracts

**trigger**

- Invariant: A trigger start requests the plan operation only.
- `Fire`: pre: The trigger exists and is enabled.. Post: A plan job starts through the job service..

## Failure modes

| Step | Failure | Retry | State after |
| --- | --- | --- | --- |
| Fire trigger | Job service refuses the plan start | Return the refusal to the caller. | trigger saved, no job started |
