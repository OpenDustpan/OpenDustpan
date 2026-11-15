## Context

The planner role is a data role in the existing access model. No new table or grant shape is needed.

## Decisions

1. **Planner as role-table entry.** Keep planner in the built-in role table with no reveal action.

## Structure

The built-in role table with an unexported action set that only its constructor makes; the constructor checks that planner holds the viewer actions plus unit:run-plan and job:read-plans. The action changeorder:author joins planner with the change order routes (ADR 197 decision 3).

## Risks

- A handler declares a wrong action and lets a planner write.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| service | The access service refuses reveal, writes and token creation for another principal. | Call each action as planner on the service; each fails. |
| HTTP handler | Each route declares its action. | Send up, destroy, apply and reveal with a planner token; each gets 403. |

## Diagrams

After this change:

```mermaid
sequenceDiagram
  Developer->>Server: read state
  Server-->>Developer: masked state
  Developer->>Server: up
  Server-->>Developer: 403
```

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| audit completeness | 1 audit entry for each refusal | entries per refusal | planner refusals |

## Threat model

Data flow and trust boundaries:

```mermaid
flowchart LR
  subgraph Client[Client boundary]
    CLI[CLI]
  end
  subgraph Server[Server boundary]
    H[HTTP handler] --> S[Access service]
  end
  subgraph Data[Data boundary]
    D[(Store)]
  end
  CLI --> H
  S --> D
```

| Boundary | STRIDE | Threat | Layer | Guard | Hostile test |
| --- | --- | --- | --- | --- | --- |
| API routes for state, secrets and tokens | E | A planner escalates a read grant to a write or a secret reveal. | boundary check | Each route declares its action. | Call each route as planner; each gets 403. |
| API routes for state, secrets and tokens | E | A planner escalates a read grant to a write or a secret reveal. | encode or contain | The handler decodes the body strictly and rejects unknown fields, so no input names another action or principal. | Send a body with an extra principal field as planner; the server rejects it. |
| API routes for state, secrets and tokens | E | A planner escalates a read grant to a write or a secret reveal. | limit | The handler caps the body size and the role holds no write action. | Send an oversized body as planner; the server rejects it. |
| API routes for state, secrets and tokens | E | A planner escalates a read grant to a write or a secret reveal. | authorize at point of use | The service checks the action. | Call the service directly; it fails. |
| API routes for state, secrets and tokens | E | A planner escalates a read grant to a write or a secret reveal. | fail closed | An unknown action is denied and audited. | Send an unknown action; denied and audited. |

## Contracts

**access**

- Invariant: Planner holds the viewer actions plus unit:run-plan and job:read-plans. The action changeorder:author joins planner with the change order routes (ADR 197 decision 3).
- `Authorizer.Authorize` (pkg/access/authorizer.go:111), tested by TestPlannerAccessDecision in pkg/access/planner_role_test.go: pre: Caller presents a session or an access token. Post: Allows planner state reads; refuses writes and reveal with access.denied.

## Failure modes

| Step | Failure | Retry | State after |
| --- | --- | --- | --- |
| planner runs up | The write is refused. | Ask an administrator for a write role. | No state version changes; the refusal is audited. |
