---
version: 3
---

## Why

RFC 121 decision 7: "Dusty is an agent principal with the fixed name `dusty`. The model connect request creates it, with the connecting administrator as its creator and first sponsor." ADR 354 accepted this on 2026-11-07 (owner). Main has the kind `agent` (`pkg/principal/principal.go:112-120`) and refuses to make one: `pkg/principal/service.go:150` (`CreatePrincipal`) and `internal/api/server/principals.go:765` say the record "comes from the model connect request, with its sponsors". No code makes it. The delegation record (person-delegates-dusty) stores the stable ID of an agent principal, and a Dusty run (dusty-run-names-delegation) names a delegation to it. Both wait for this change.

## What Changes

- An agent record: the principal row of the kind `agent`, 1 to 5 sponsors (persons), a fallback group, a creator and a time (RFC 121 decisions 1 and 4). A NEW method `principal.Service.CreateAgent` makes it. `CreatePrincipal` keeps refusing the kind `agent`.
- Dusty: one agent record with the subject `dusty`, made inside the transaction that stores the model connection (decision 7). The connecting administrator is the creator and the first sponsor.
- The ceiling grants of Dusty (decision 8): the built-in role `agent-tools-read` on the installation, and the built-in role `agent-tools` on the namespaces that the administrator picks, none by default. They are stored grants with the administrator as creator, in the same transaction.
- `principal.Validate` refuses a password, a passkey, an MFA factor, a console session and a sign-in method for the kind `agent` (decision 1), and the grant service refuses a group membership, a protected role and a break-glass mark for it (decision 3).
- Audit entries for each write: the record, each sponsor, each ceiling grant, in the same transaction.
- Reads: `GET /api/server/principals?kind=agent` lists the record with its sponsors and its ceiling grants (the route that the create route names in its 400 answer).

## Out of scope

- The route that connects a model, with the connection document and the probe (ADR 129 decision 29: `PUT /api/agent/connection` answers 202 with a probe ID). The change `agent-connect-route` writes it. Its last step calls the NEW function `agent.ConnectModel` of this change in the transaction of its own write, and writes the connection with `ConnectionStore.Promote`. This change does not write the connection.
- The delegation record (person-delegates-dusty), the run that names a delegation (dusty-run-names-delegation), the notification, the disable and the delete of an agent, the sponsor grace period (RFC 121 decisions 4 to 6 after the record), and the installation agent policy (decision 19).
- A client registration of an agent. Dusty has none (decision 10).
- A second agent. The kind allows it. Only Dusty has a maker in this change.

## Depends on

- Main: the kind `agent` (archive 2026-11-15-agent-shapes-before-first-tag) and `ActionServerManageAgent` (`pkg/identity/identity.go:541`).
- Used by: `agent-connect-route`, which calls `ConnectModel` in its transaction. Until it is on main, the e2e step of this change calls the connect function through the test server.

## Usability

The persona is the administrator who connects a model. The connect request shows before the connect the ceiling that it will grant, and answers the stored record. The administrator reads the record with `GET /api/server/principals?kind=agent` and sees the creator, the sponsors and each grant. Nothing about Dusty is hidden: the name, the sponsor and the grants are rows that a person can list, audit and revoke. An error says what failed and the one command that helps, for example: "The sponsor prn-x is not a person. Send a person ID with GET /api/server/principals?kind=person."

## What main already does

- `pkg/principal/principal.go:112-120`: `KindAgent` and `ParseKind` accept it.
- `pkg/principal/service.go:150`: `CreatePrincipal` refuses `KindAgent`.
- `internal/api/server/principals.go:765`: the create route refuses `agent` and names the list route.
- `pkg/identity/identity.go:541`: `ActionServerManageAgent` "allows connecting and disconnecting a model".
- `internal/agent/settings.go:15,115`: `Connection` and `PutConnection` exist. No route calls them. The change `agent-connection-document-and-secrets` removes them.
- `pkg/access/roles.go:218`: `BuiltInRoles`. It has no role `agent-tools-read` or `agent-tools`.
- No table holds a sponsor or a fallback group. `rg -i sponsor pkg internal` finds only the two comments above.

## ADR check

- ADR 354 (accepted 2026-11-07, owner), decision 6: "Decisions 1 to 52 of the "Proposal" of the RFC apply." This change builds decisions 1, 3, 4 (the record and the sponsors), 7, 8 and 10. It changes no decision.
- ADR 120 decision 15 ("Dusty is not a principal") no longer holds. ADR 354 changes it (Changed-by chain: ADR 354 changes ADR 120 decisions 13 to 16). This change follows ADR 354, not ADR 120 decision 15.
- ADR 120 decision 3 (one model connection for the installation) holds: Dusty is one record, made at the first connect, and a later connect makes no second record.
- ADR 215 (principal home): the record has a principal home like every principal.
- ADR 129 decision 29 (owner, 2026-10-18): the route answers 202 with a probe. This change adds no route that connects a model and so does not touch those words.
- ADR 250 and ADR 364: the layers table in design.md. No new lint rule.
- ADR 362: every reference is a stable ID (sponsor, fallback group, creator, role grant).

## Open questions

none
