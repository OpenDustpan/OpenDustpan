## Context

RFC 121 decision 7 says the model connect request creates Dusty. Main has no connect route and no agent record. This change builds the record and the transaction. It does not build the route (see the proposal).

## Decisions

1. **The record.** `principal.Principal` with `Kind = KindAgent`, the subject `dusty` (reserved: `principal.ValidateSubject` refuses it for any other kind), `Source = SourceLocal` and no password, passkey, MFA factor or sign-in method. `Principal.Validate` refuses each of them for the kind `agent`.
2. **Sponsors and fallback group in their own tables.** `agent_record(principal_id, fallback_group_id, creator_id, created_at)` and `agent_sponsor(principal_id, sponsor_id, added_by, added_at)`, with stable IDs and foreign keys. 1 to 5 sponsors, each a person that is not disabled. The fallback group is set to the empty value at connect and is changed by a later change.
3. **One transaction.** `agent.ConnectModel(ctx, in)` writes in one transaction: the connection and its sealed credential (`SettingsStore.PutConnection`), the agent record of Dusty when none exists, the first sponsor (the connecting administrator), the ceiling grants, and one audit entry for each write. A failure at any step rolls back all of them. A second connect changes the connection only and makes no second record, no second sponsor and no grant.
4. **Authorization.** `ConnectModel` needs `ActionServerManageAgent` on the installation, checked at the point of use inside the service, not only in the route. The creator and the sponsor come from the authenticated principal, never from the request body.
5. **The ceiling.** The role `agent-tools-read` holds the read actions of the Dusty tool set on the installation. The role `agent-tools` holds the propose and operate actions of the tool set on a namespace. Both are NEW built-in roles in `pkg/access/roles.go`, defined in code. Who holds them is a stored grant, made at connect with the administrator as creator. The request lists the namespaces for `agent-tools`; an empty list is valid.
6. **Refusals for the kind agent.** The grant service refuses a group membership, a grant of a protected role and the break-glass mark for a principal of the kind `agent` (decision 3 of RFC 121). The authenticator already has no sign-in method for it.
7. **Reads.** `GET /api/server/principals?kind=agent` adds `sponsors`, `creator` and `ceilingGrants` for the kind `agent`. A reader needs the same action as the principal list.

## Structure

```go
// pkg/principal (NEW)
type AgentInput struct { Subject string; Sponsors []string; FallbackGroup string; Creator string }
func (s *Service) CreateAgent(ctx context.Context, e audit.Entry, in AgentInput) (Principal, error)
// internal/agent (NEW)
type ConnectInput struct { Connection Connection; SealedCredential []byte; Namespaces []namespace.Name }
func (s *Service) ConnectModel(ctx context.Context, actor identity.Principal, in ConnectInput) (Connection, error)
```

NEW error codes: `agent.sponsor-invalid` (a sponsor is not a person, is disabled, or the list is not 1 to 5) and `agent.subject-reserved` (the subject `dusty` for another kind). A failed step of the connect returns its own sentinel.

## Contract

No new route. `GET /api/server/principals?kind=agent` answers the added fields. `POST /api/server/principals` keeps answering 400 `principal.invalid` for the kind `agent`.

## Risks

- A second record of Dusty from a race of two connects. The unique subject index and the transaction stop it: the second connect finds the record.
- A grant of `agent-tools` on a namespace that a person deletes later. The grant follows the existing removal rules of grants.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| port adapter | The SQL checks refuse 0 or more than 5 sponsors, a duplicate sponsor, a non-person sponsor, and a second subject `dusty`. | `TestAgentRecordStoreRefusesHostileSponsors` (NEW, `pkg/principal/principaltest`): `[]`, six IDs, one ID twice, a service principal ID, a disabled person; a second `dusty`. Each stores no row. |
| service | `CreateAgent` and `ConnectModel` check the same limits again, and `ConnectModel` checks `ActionServerManageAgent` at the point of use. | `TestConnectModelRefusesACallerWithoutTheAction`, `TestConnectModelTakesCreatorFromTheCaller` (NEW, `internal/agent`): a body that names another creator is ignored; no action means no write. |
| service | `CreatePrincipal` keeps refusing the kind `agent`. | `TestCreatePrincipalRefusesAgent` (exists on main; keep). |
| transaction | A failed step rolls back the connection, the record and the grants. | `TestConnectModelRollsBackEveryWrite` (NEW): a failing audit append at each step leaves no row. |
| grants | A group membership, a protected role and the break-glass mark are refused for the kind `agent`. | `TestAgentCannotJoinAGroupOrHoldAProtectedRole` (NEW). |
| HTTP handler | The create route refuses `agent`; the list route needs the read action. | `TestCreatePrincipalRefusesAgentAndUnknownKinds` (exists), `TestListAgentPrincipalsNeedsTheAction` (NEW). |
| audit | Each write has an entry with the actor, the object and the reason. | `TestConnectModelWritesOneEntryForEachWrite` (NEW). |

Each guard has a test that fails when the guard is removed.
