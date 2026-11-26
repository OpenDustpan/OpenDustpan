## Context

On main, the setting of a namespace has its port, its column, its service and its route. The SQL adapters of `outputshare.Store`, the table `output_sharing_rules`, the first rule of a new unit and the removal of the rules of a purged object are on main (part 1 and the change `add-output-sharing-setting-new`). This change adds the service of the rules and the routes. It does not change the setting route and it does not change the checker (the change `output-sharing` reads the stored rules).

## Decisions

1. **The setting stays on the output-sharing route.** ADR 273 decision 10 names `GET` and `PUT` on `/api/dustpan/v1/namespaces/{namespace}/output-sharing`, and main serves them (`internal/api/inventory/outputsharing.go:49`). The generic settings API has no key for it, and #897 leaves per-feature keys to each feature. A second path for one value would give two writers for one column. No change to the setting route, its body or its service.
2. **The rules use separate paths.** ADR 273 decision 10 gives the routes of a unit, and says that a collection and a namespace have the same shape. The rows are in the route table below. Each list route mirrors `unit:read-access` for its own object kind: `collection:read-access` for a collection and `namespace:read-access` for a namespace (owner, 2026-11-20; ADR 273 decision 10, "the same shape"). A list route resolves its object from the path, as the save and the removal do, and declares the action on that object, so a grant on a unit never lists the rules of its collection or namespace. The routes are in the inventory adapter beside the setting routes, and the rule service is a second type in `internal/outputsharesvc`, built over `outputshare.Store`.
3. **Rules by stable ID, always.** The producer and the consumer of a rule are stored by stable ID, never by name (ADR 391, owner, 2026-11-20: "Stable ID always. No entities should be keyed by name."). A consumer in the producer namespace is resolved to its ID by the save, which checks that the unit or collection exists. A consumer in another namespace is stored as the ID of that namespace plus the unit or collection ID that the caller gives. The save checks the format of that ID with an allowlist (`Rule.Validate`) and does not look it up, so the answer does not tell whether the object exists (ADR 273 decision 3 and ADR 220 decision 11 stay). A rename, or a delete and make again, in any namespace never matches a rule by name. The table keeps no name of a consumer. How a person learns the ID of an object of another namespace, and how the body of the route names it, are decided in this change.
4. **The server makes the rule ID** with `ident.NewID` (like the function `NewID` in `internal/ident/ident.go`), so a caller cannot choose it.
5. **Each write has its audit entry in its transaction**, as the setting has (`internal/outputsharesvc/outputsharesvc.go:96`). The entry names the actor, the producer, the consumer, the action `unit:manage-sharing`, and the rule ID.
6. **No job-token read here.** The checker keeps its denial of a read by a job (`internal/outputsharesvc/checker.go:226`) until the change `output-sharing` lands. A rule saved by this change gives no read yet. The guide says so.

## Routes

| Route | Action and object | Answer |
| --- | --- | --- |
| `GET /namespaces/{namespace}/units/{unit_id}/sharing-rules` | `unit:read-access` on the unit | 200, rules ordered by ID |
| `POST /namespaces/{namespace}/units/{unit_id}/sharing-rules` | `unit:manage-sharing` on the unit | 201 with the rule |
| `DELETE /namespaces/{namespace}/units/{unit_id}/sharing-rules/{rule_id}` | `unit:manage-sharing` on the unit | 204 |
| `GET /namespaces/{namespace}/collections/{collection}/sharing-rules` | `collection:read-access` on the collection (owner, 2026-11-20) | 200, rules ordered by ID |
| `POST /namespaces/{namespace}/collections/{collection}/sharing-rules` | `unit:manage-sharing` on the collection | 201 |
| `DELETE /namespaces/{namespace}/collections/{collection}/sharing-rules/{rule_id}` | `unit:manage-sharing` on the collection | 204 |
| `GET /namespaces/{namespace}/sharing-rules` | `namespace:read-access` on the namespace (owner, 2026-11-20) | 200, rules ordered by ID |
| `POST /namespaces/{namespace}/sharing-rules` | `unit:manage-sharing` on the namespace | 201 |
| `DELETE /namespaces/{namespace}/sharing-rules/{rule_id}` | `unit:manage-sharing` on the namespace | 204 |

The body of a POST is `{"consumer": "<reference>"}`: a unit, a collection or a namespace reference. The handler resolves a consumer of the producer namespace to its stable ID. It does not resolve a consumer of another namespace. The error map: `ErrInvalid` 400, `ErrUnknownObject` 404, `ErrNotFound` 404, `ErrAlreadyExists` 409, `ErrLimit` 409, `namespace.ErrFenced` 409 with its existing code.


## Structure

The service keeps `outputshare.Store` as its only port, and takes `Authorizer` and `namespace.Store` as the setting service does. A rule that the store reads and that fails `Rule.Validate` never matches `Find`, so a bad row denies.

## Risks

- A route that lists the rules of a collection or of a namespace with the read action of a unit leaks the rules of its siblings. Each list row declares the read action of its own object kind (see the threat model).
- The setting and the rules have two writers if a path serves both. The rule routes stay separate from the setting route (decision 1).

## Diagrams

After this change:

```mermaid
sequenceDiagram
participant A as Administrator
participant H as Rule handler
participant S as Rule service
participant T as Store
A->>H: POST sharing-rules
H->>S: Save with consumer reference
S->>S: check unit:manage-sharing on the object
S->>T: Put rule, fence and audit entry
T->>T: Validate, resolve stable IDs, count, insert, append audit
T-->>A: 201 or the error
```

Modules that the change touches, and their imports on main:

```mermaid
flowchart LR
H[internal/api/inventory] --> V[internal/outputsharesvc]
V --> P[pkg/outputshare]
SQ[internal/store/sqlite] --> P
PG[internal/store/postgres] --> P
D[pkg/dustpan] --> V
```

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| rule list size | 100 | rules at most for each producer | `outputshare.MaxRulesPerProducer` (`pkg/outputshare/outputshare.go:81`) |

## Threat model

Data flow and trust boundaries:

```mermaid
flowchart TB
 R[rule request] --> H[handler allowlist] --> V[service authorize] --> S[store Validate and fence] --> T[(rule table with CHECK)]
```

| Boundary | STRIDE | Threat | Layer | Guard | Hostile test |
| --- | --- | --- | --- | --- | --- |
| HTTP adapter | T | A request names a principal, a whole other namespace, an unknown field, a second JSON value or an oversize body as the consumer. | boundary check | Decode with an allowlist of object kinds and a body limit. | A table of hostile bodies sent to the handler gives 400 and stores no rule. |
| HTTP adapter | I | A caller with `unit:read-access` on a unit lists the rules of its collection or namespace, or a caller with `collection:read-access` lists the rules of the namespace, or the reverse. | authorize at point of use | Each list route row declares the read action of its own object kind and resolves that object from the path. | A caller with one read action on the wrong object kind gets 403 on each list route, and a caller with no grant gets 403. The test fails when the action of a row is changed to another kind. |
| HTTP adapter | E | A caller with `unit:read-access` saves a rule. | authorize at point of use | The route row declares `unit:manage-sharing` on the object. | A caller with only `unit:read-access` gets 403, and the store stays empty. |
| Service | I | A caller with a read action on collection A, or on a namespace, lists the rules of another collection or namespace. | authorize at point of use | `List` checks the read action of the kind of the producer on the producer of the call. | The service called with `collection:read-access` on a sibling collection, or with `namespace:read-access` on another namespace, denies and lists nothing. The test fails when the check is removed. |
| Service | E | A caller with `unit:manage-sharing` on unit A saves or removes a rule of unit B. | authorize at point of use | The service checks the action on the producer of the call. | The service called with a grant on a sibling unit denies. |
| Service | E | A removal by rule ID reaches a rule of another producer. | contain | The store binds `Get` and `Delete` to the producer of the route. | `Delete` with the ID of a rule of another producer returns `ErrNotFound` and changes nothing. |
| Service | R | A rule change leaves no record. | fail closed | A failed audit append fails the write. | A store fault on the audit append leaves no rule. |

## Contract

**outputShareRule**

- Invariant: A rule names a unit, a collection or a namespace as producer and as consumer. It never names a principal or a whole other namespace.
- Invariant: A rule keeps its producer and its consumer by stable ID and never by name. A consumer of another namespace is that namespace plus an ID with a valid format.
- `Put`: pre: the fence is current and the rule is valid. Post: the rule and its audit entry are stored in one transaction, or nothing is stored.

## Failure modes

| Step | Failure | Retry | State after |
| --- | --- | --- | --- |
| Save a rule | The audit append fails. | The caller sends the request again. | No rule. |
| Remove a rule | The fence is stale. | Read the namespace and send again. | The rule stays. |
