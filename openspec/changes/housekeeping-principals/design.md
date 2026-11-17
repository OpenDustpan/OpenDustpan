## Context

ADR 370 removes the namespace service principal and gives each housekeeping purpose its own built-in service principal with stored grants. Part 1 (`housekeeping-principals-install`) made the principals and grants. Part 2 (`housekeeping-principals-sweeps`) made eight purposes run as their principals. This part, the third, covers the other three purposes and the refusal of a credential and of a grant. Main has a rule in code for the namespace service principal (`pkg/access/authorizer.go:154`) and housekeeping that writes as `@server` with no grant. This is security code (ADR 250): it changes who may do what with no person in the loop.

## Decisions

1. **Each run authorizes.** The sweep of access tokens, the expiry of temporary administrators and the rotation of issuer keys take their `identity.Principal` from `principal.Housekeeping(purpose)` (part 1) and call the authorizer before they act, as any caller does. A denial changes nothing and writes an audit entry with the outcome `denied`. The audit entry of each run names the principal.
2. **No path in from outside.** Each layer that refuses a credential for the namespace service subject today refuses one for a housekeeping subject too, each on its own: the authenticator guard (`pkg/principal/namespace_service_guard.go`), the principal check (`pkg/principal/principal.go:352`), the session check (`internal/sessionauth/sessionauth.go:219`) and the token service (`pkg/accesstoken/service.go:680` and `:1011`). A grant to a housekeeping principal of a role that its purpose does not hold is refused (`pkg/access/service.go:1630`).
3. **The namespace service principal stays for now.** The dispatcher needs it until the run-as principal of ADR 369 decision 7 exists. The first trigger change (run-triggers) deletes it with that principal (ADR 370: this change lands before the trigger changes "or with them").

## Structure

- NEW `HousekeepingPurpose` of part 1, with unexported fields: a housekeeping principal of an unknown purpose cannot exist.
- The authorizer has no branch for a housekeeping principal: its grants are rows, like any grant. This makes the bug class "a principal that acts on a rule in code" impossible for housekeeping.
- No custom analyzer and no new tool (ADR 364).

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| Authenticator | A credential that names a `builtin` principal is refused. | `TestHousekeepingPrincipalCannotAuthenticate`: an access token, a session and a trust token that name `housekeeping:artifact-sweep`. Each is refused. |
| Authorizer | No code rule: only stored grants decide. | `TestNoHardCodedGrantLeft`: a principal with the old subject `system:namespace-service:acme` and with each housekeeping subject, and no grant row. Each action is denied. |
| Access service | A grant out of the purpose is refused. | `TestGrantOutOfPurposeRefused`: a grant of `namespace-admin` to a housekeeping principal is refused. |
| Each housekeeping service | It authorizes before it acts, and names its principal. | `TestRevokeStopsOnlyItsSweep` of part 2, extended with the token sweep, the temporary administrator expiry and the issuer rotation: revoke the grant of one; it changes nothing and writes a denial; the others run as before. `TestEachSweepIsAttributed` of part 2, extended the same way. |

## Contract

Types and functions: none. This part uses `principal.Housekeeping` and the closed list of purposes of part 1.

Error codes and audit:

- 403 `principal.housekeeping` (NEW error code): a credential or a token for a housekeeping principal.
- 422 `access.grant-out-of-purpose` (NEW error code), with the purpose: a grant of a role that the purpose does not hold.
- A refused housekeeping run writes an audit entry with the outcome `denied`. The entry names the housekeeping principal and the missing action.

## Callers on main (task 0.1, checked on 17 November 2026)

The rule: a caller is a housekeeping purpose when the server starts the run itself (a schedule, a sweep, a reaper or a timer) and no person asked for it. A caller that writes `@server` only as the sender of a notification, or as an effect of the request of a person, is not a purpose: the person of the request, or the notification, already names who or what acted. This follows the practice of a service account for each background job with the least role that the job needs.

In scope of this part (the other eight purposes are in the design document of part 2):

| Purpose | Caller on main |
| --- | --- |
| `token-sweep` | `pkg/accesstoken/policy.go:192` and `:201` |
| `temp-admin-expiry` | `pkg/tempadmin/service.go:157` and `:365`, and the expiry at the use of a record in `pkg/tempadmin/authenticator.go:296` |
| `issuer-rotation` | `internal/issuersvc/settings.go:194` (rotation and copy check, `internal/issuersvc/rotate.go:154` and `:227`) |

Not in scope, and why (each is read only here):

- The notifications that name `@server` as the sender of an alert or a notice: (read only) `internal/removalalertsvc/removalalertsvc.go`, (read only) `internal/mfasvc/breakglass.go`, (read only) `internal/signinsvc/passkey.go`, (read only) `internal/signinsvc/password.go`, (read only) `internal/issuersvc/notice.go`, and the alert at (read only) `internal/localusersvc/locks.go`. A notification is not a change, and a person's sign-in or event starts each one.
- The drop of runner credentials at a restore, (read only) `pkg/backup/restore_runner_credentials.go`: it is an effect of the restore that a person starts, and the restore entry names that person.
- The authorizer sites and the trigger sites of the namespace service principal: (read only) `pkg/access/authorizer.go`, (read only) `pkg/access/discovery.go`, `pkg/trigger/dispatcher.go`, `pkg/trigger/manager.go`, `pkg/trigger/trigger.go`, `pkg/job/trigger.go` and `pkg/dustpan/triggers.go`. The first trigger change removes them with the principal (decision 5). Only the credential and grant refusals in decision 4 get a housekeeping twin here.
- The test helpers that name `@server` (in the test helpers of the job and history packages) follow the code that they test.

## Risks

- A sweep that a person revoked stops until a person grants it again. The docs page says so, and the denial entry names the principal and the action.
