## Context

RFC 121 decision 7 says the model connect request creates Dusty. Main has no `ConnectModel` (NEW in this change), no connect route and no agent record. This change builds the record, the store port that owns the transaction, and the service method. It does not build the route or the connection write (see the proposal).

## Decisions

1. **The record.** `principal.Principal` with `Kind = KindAgent`, the subject `dusty` (reserved: `principal.ValidateSubject` refuses it for any other kind), `Source = SourceLocal` and no password, passkey, MFA factor or sign-in method. `Principal.Validate` refuses each of them for the kind `agent`.
2. **Sponsors and fallback group in their own tables.** `agent_record(principal_id, fallback_group_id, creator_id, created_at)` and `agent_sponsor(principal_id, sponsor_id, added_by, added_at)`, with stable IDs and foreign keys. 1 to 5 sponsors, each a person that is not disabled. The fallback group is set to the empty value at connect and is changed by a later change.
3. **One transaction, owned by one store port.** No port that main has takes a transaction from its caller: `principal.RecordStore.CreatePrincipal` and `access.Service.Grant` each commit their own transaction, and the grant check reads outside the transaction of the caller. So the service cannot compose them in one transaction. A NEW small port `ConnectStore` (NEW, in the NEW file `internal/agent/connectstore.go`) has one method, `ConnectModel(ctx, w ConnectWrite) error`. The adapter runs all writes in ONE transaction: the principal row, the sponsor row, the agent record (only when none exists), each ceiling grant with its fence check, the grant check (role and protected-role rule) read INSIDE the same transaction, and one audit entry for each write. A failure at any step rolls back all of them. A second connect makes no second record, no second sponsor and no grant. The model is the local user store of the SQL adapters, which already writes more than one row with its audit entry in one transaction; this port widens that pattern. `ConnectWrite` is a struct with unexported fields and a constructor `NewConnectWrite` that checks the invariants (decision 8). The port returns sentinel errors that the port file documents: `ErrConnectFenceStale`, `ErrConnectGrantRefused`, `ErrConnectSponsorInvalid` and `ErrConnectSubjectTaken` (all NEW). The service does not write the connection. The change `agent-connect-route` (a later change) writes the connection. When that route must write in the same transaction, it adds its write to `ConnectWrite` then. This change does not build that.
4. **Authorization.** `ConnectModel` needs `ActionServerManageAgent` on the installation, checked at the point of use inside the service, not only in the route. The creator and the sponsor come from the authenticated principal, never from the request body.
5. **The ceiling.** The role `agent-tools-read` holds the read actions of the Dusty tool set on the installation. The role `agent-tools` holds the propose and operate actions of the tool set on a namespace. Both are NEW built-in roles in `pkg/access/roles.go`, defined in code. Who holds them is a stored grant, made at connect with the administrator as creator. The request lists the namespaces for `agent-tools`; an empty list is valid.
6. **Refusals for the kind agent.** The grant service refuses a group membership, a grant of a protected role and the break-glass mark for a principal of the kind `agent` (decision 3 of RFC 121). The authenticator already has no sign-in method for it.
7. **Reads.** `GET /api/server/principals?kind=agent` adds `sponsors`, `creator` and `ceilingGrants` for the kind `agent`. A reader needs the same action as the principal list.
8. **The write is checked once, then again.** `NewConnectWrite` refuses a write that has no principal, 0 or more than 5 sponsors, a repeated sponsor, a subject other than `dusty` for the kind `agent`, or a ceiling grant that names a role other than `agent-tools-read` or `agent-tools`. The adapter checks the same limits again inside the transaction (see the layers table).
9. **One validation.** `CreateAgent` and `ConnectModel` call one unexported function of the service, `validateAgentRecord`, for the subject, the sponsors and the fallback group. No second copy of the rules exists.
10. **Agent tables follow `trust_rules` in a backup.** `agent_record` and `agent_sponsor` are access tables: a backup carries them and a restore puts them back only when the caller holds server:restore-access, in the same step that replaces the principals table. The principal, the record and the sponsors of an agent always come from one backup. A restore that would separate them is refused before the swap (task 2.9). No option lets a restore bring back the principal without its record.

## Structure

```go
// pkg/principal (NEW)
type AgentInput struct { Subject string; Sponsors []string; FallbackGroup string; Creator string }
func (s *Service) CreateAgent(ctx context.Context, e audit.Entry, in AgentInput) (Principal, error)
// internal/agent (NEW)
type ConnectInput struct { Namespaces []namespace.Name }
func (s *Service) ConnectModel(ctx context.Context, actor identity.Principal, in ConnectInput) error
// internal/agent/connectstore.go (NEW): the port. The adapter owns the transaction.
type ConnectStore interface {
	// ConnectModel writes all of w in one transaction, or none of it.
	ConnectModel(ctx context.Context, w ConnectWrite) error
}
type ConnectWrite struct { /* unexported fields */ }
func NewConnectWrite(principal principal.Principal, sponsors []string, grants []access.GrantInput, entries []audit.Entry, fence namespace.Fence) (ConnectWrite, error)
```

NEW error codes: `agent.sponsor-invalid` (a sponsor is not a person, is disabled, or the list is not 1 to 5) and `agent.subject-reserved` (the subject `dusty` for another kind). A failed step of the connect returns its own sentinel (decision 3).

## Contract

No new route. `GET /api/server/principals?kind=agent` answers the added fields. `POST /api/server/principals` keeps answering 400 `principal.invalid` for the kind `agent`.

## Risks

- A second record of Dusty from a race of two connects. The unique subject index and the transaction of the port stop it: the second connect finds the record.
- A grant of `agent-tools` on a namespace that a person deletes later. The grant follows the existing removal rules of grants.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| port adapter | The SQL checks refuse 0 or more than 5 sponsors, a duplicate sponsor, a non-person sponsor, and a second subject `dusty`. | `TestAgentRecordStoreRefusesHostileSponsors` (NEW, `pkg/principal/principaltest`): `[]`, six IDs, one ID twice, a service principal ID, a disabled person; a second `dusty`. Each stores no row. |
| port adapter | `ConnectStore.ConnectModel` writes in one transaction, checks the fence and the grant check inside it, and rolls back all on any failure. | `TestConnectStoreRefusesHostileWrites` (NEW, `internal/agent/agenttest`): a stale fence, a role that the grant check refuses (a protected role), a sponsor that is not a person, a failing audit append at each step. Each stores no row, not even the principal or the sponsor. Every SQL adapter runs it. |
| service | `CreateAgent` and `ConnectModel` check the same limits again through `validateAgentRecord`, and `ConnectModel` checks `ActionServerManageAgent` at the point of use. | `TestConnectModelRefusesACallerWithoutTheAction`, `TestConnectModelTakesCreatorFromTheCaller` (NEW, `internal/agent`): a body that names another creator is ignored; no action means no write. |
| service | `CreatePrincipal` keeps refusing the kind `agent`. | `TestCreatePrincipalRefusesAgent` (exists on main; keep). |
| transaction | A failed step leaves no record, sponsor or grant: the port adapter owns the transaction. | `TestConnectModelRollsBackEveryWrite` (NEW, `internal/agent`): a failing audit append at each step leaves no row. |
| grants | A group membership, a protected role and the break-glass mark are refused for the kind `agent`. | `TestAgentCannotJoinAGroupOrHoldAProtectedRole` (NEW). |
| HTTP handler | The create route refuses `agent`; the list route needs the read action. | `TestCreatePrincipalRefusesAgentAndUnknownKinds` (exists), `TestListAgentPrincipalsNeedsTheAction` (NEW). |
| audit | Each write has an entry with the actor, the object and the reason. | `TestConnectModelWritesOneEntryForEachWrite` (NEW). |

Each guard has a test that fails when the guard is removed.
