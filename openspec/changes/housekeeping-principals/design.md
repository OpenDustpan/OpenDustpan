## Context

ADR 370 removes the namespace service principal and gives each housekeeping purpose its own built-in service principal with stored grants. Main has a rule in code for the namespace service principal (`pkg/access/authorizer.go:154`) and housekeeping that writes as `@server` with no grant. This is security code (ADR 250): it changes who may do what with no person in the loop.

## Decisions

1. **One closed list of purposes.** NEW `principal.HousekeepingPurpose` is a type with unexported fields. Its values come only from NEW `principal.HousekeepingPurposes()`. Each value has a stable subject (`housekeeping:<purpose>`), a role name and the actions of that role. A purpose that is not in the list cannot exist, so no code can act as a housekeeping principal of a made-up purpose.
2. **The install writes principals and grants.** In the transaction that makes the installation, the store writes one principal of kind `service` and source `builtin` for each purpose, and one stored grant of its role on its object, with the install as the creator and the install time. Code defines the role. The grant row decides who holds it.
3. **Each run authorizes.** Each housekeeping service takes its `identity.Principal` from NEW `principal.Housekeeping(purpose)` and calls the authorizer before it acts, as any caller does. A denial changes nothing and writes an audit entry with the outcome `denied`. The audit entry of each run names the principal.
4. **No path in from outside.** The authenticator refuses a credential that names a `builtin` principal. The token services refuse to make a token for one. A grant to a housekeeping principal of a role that its purpose does not hold is refused.
5. **The namespace service principal stays for now.** The dispatcher needs it until the run-as principal of ADR 369 decision 7 exists. The first trigger change (run-triggers) deletes it with that principal (ADR 370: this change lands before the trigger changes "or with them").

## Structure

- `HousekeepingPurpose` with unexported fields: a housekeeping principal of an unknown purpose cannot exist.
- The authorizer has no branch for a housekeeping principal: its grants are rows, like any grant. This makes the bug class "a principal that acts on a rule in code" impossible for housekeeping.
- No custom analyzer and no new tool (ADR 364).

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| Authenticator | A credential that names a `builtin` principal is refused. | `TestHousekeepingPrincipalCannotAuthenticate`: an access token, a session and a trust token that name `housekeeping:artifact-sweep`. Each is refused. |
| Authorizer | No code rule: only stored grants decide. | `TestNoHardCodedGrantLeft`: a principal with the old subject `system:namespace-service:acme` and with each housekeeping subject, and no grant row. Each action is denied. |
| Access service | A grant out of the purpose is refused. | `TestGrantOutOfPurposeRefused`: a grant of `namespace-admin` to a housekeeping principal is refused. |
| Each housekeeping service | It authorizes before it acts, and names its principal. | `TestRevokeStopsOnlyItsSweep`: revoke the grant of the artifact sweep; the artifact sweep changes nothing and writes a denial; the token sweep and the retention run as before. `TestEachSweepIsAttributed`: each audit entry of each sweep names its housekeeping principal, never `@server`. |

## Risks

- A sweep that a person revoked stops until a person grants it again. The docs page says so, and the denial entry names the principal and the action.

## Where the source filter runs

The store applies the `source` filter in the list query and in the count query. The service does not filter after the read, because that breaks the page totals and the cursors. The port `pkg/principal/store.go` takes the source, and both dialects and the conformance suite follow. The agent may put the built-in principal constants in `pkg/identity/identity.go` instead of a new file.
