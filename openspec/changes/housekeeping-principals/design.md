## Context

ADR 370 removes the namespace service principal and gives each housekeeping purpose its own built-in service principal with stored grants. Main has a rule in code for the namespace service principal (`pkg/access/authorizer.go:154`) and housekeeping that writes as `@server` with no grant. This is security code (ADR 250): it changes who may do what with no person in the loop.

## Decisions

1. **One closed list of purposes.** NEW `principal.HousekeepingPurpose` is a type with unexported fields. Its values come only from NEW `principal.HousekeepingPurposes()`. Each value has a stable subject (`housekeeping:<purpose>`), a role name and the actions of that role. A purpose that is not in the list cannot exist, so no code can act as a housekeeping principal of a made-up purpose.
2. **The install writes principals and grants.** In the transaction that makes the installation, the store writes one principal of kind `service` and source `builtin` for each purpose, and one stored grant of its role on its object, with the install as the creator and the install time. Code defines the role. The grant row decides who holds it.
3. **Each run authorizes.** Each housekeeping service takes its `identity.Principal` from NEW `principal.Housekeeping(purpose)` and calls the authorizer before it acts, as any caller does. A denial changes nothing and writes an audit entry with the outcome `denied`. The audit entry of each run names the principal.
4. **No path in from outside.** Each layer that refuses a credential for the namespace service subject today refuses one for a housekeeping subject too, each on its own: the authenticator guard (`pkg/principal/namespace_service_guard.go`), the principal check (`pkg/principal/principal.go:352`), the session check (`internal/sessionauth/sessionauth.go:219`) and the token service (`pkg/accesstoken/service.go:680` and `:1011`). A grant to a housekeeping principal of a role that its purpose does not hold is refused (`pkg/access/service.go:1630`).
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

## Contract

Types and functions (`pkg/principal`):

- NEW type `principal.HousekeepingPurpose`, with unexported fields. A value comes only from the closed list of NEW `principal.HousekeepingPurposes()`. Each value has the subject `housekeeping:<purpose>`, a role name and the actions of that role.
- NEW `principal.Housekeeping(purpose)` returns the `identity.Principal` of one purpose.
- NEW source `builtin`, a constant next to `SourceLocal` (`pkg/principal/principal.go:116`). `validateSource` (`pkg/principal/principal.go:629`) accepts it. An unknown source returns `principal.ErrInvalid`.

Port (`pkg/principal/store.go:58`):

- `ListPrincipals` of `principal.Store` takes the source filter next to the kind. An empty source keeps every source. The total and the cursor count only the rows of the filter. It returns `principal.ErrInvalid` for an unknown source.

Endpoint (existing route, NEW query parameter):

- `GET /api/server/principals?kind=service&source=builtin` keeps the principals of that source. As for `kind`, a source that is not valid answers 400.

Tables:

- No NEW table. The install writes one row in `principals` (kind `service`, source `builtin`) for each purpose, and one stored grant of its role, with the install as the creator and the install time, in the baseline of each dialect.

Error codes and audit:

- 403 `principal.housekeeping` (NEW error code): a credential or a token for a housekeeping principal.
- 422 `access.grant-out-of-purpose` (NEW error code), with the purpose: a grant of a role that the purpose does not hold.
- A refused housekeeping run writes an audit entry with the outcome `denied`. The entry names the housekeeping principal and the missing action.

## Callers on main (task 0.1, checked on 17 November 2026)

The rule: a caller is a housekeeping purpose when the server starts the run itself (a schedule, a sweep, a reaper or a timer) and no person asked for it. A caller that writes `@server` only as the sender of a notification, or as an effect of the request of a person, is not a purpose: the person of the request, or the notification, already names who or what acted. This follows the practice of a service account for each background job with the least role that the job needs.

In scope, one principal for each purpose:

| Purpose | Caller on main |
| --- | --- |
| `artifact-sweep` | `pkg/job/artifactsweep.go:654` |
| `job-reconcile` | `pkg/job/reconcile.go:673` |
| `saved-view-cleanup` | `pkg/savedview/service.go:259` and `:293` |
| `token-sweep` | `pkg/accesstoken/policy.go:192` and `:201` |
| `temp-admin-expiry` | `pkg/tempadmin/service.go:157` and `:365`, and the expiry at the use of a record in `pkg/tempadmin/authenticator.go:296` |
| `history-retention` | `pkg/history/redaction.go:22` (actor `@server`, reason `RetentionReason`) |
| `name-lock-prune` | `internal/localusersvc/locks.go:415` |
| `agent-reaper` | `internal/agent/reaper.go:53` |
| `checkpoint-sweep` | `pkg/stack/superseded_sweep.go:72` |
| `blob-sweep` | `pkg/workspace/service.go:2527` |
| `issuer-rotation` | `internal/issuersvc/settings.go:194` (rotation and copy check, `internal/issuersvc/rotate.go:154` and `:227`) |

Not in scope, and why (each is read only here):

- The notifications that name `@server` as the sender of an alert or a notice: (read only) `internal/removalalertsvc/removalalertsvc.go`, (read only) `internal/mfasvc/breakglass.go`, (read only) `internal/signinsvc/passkey.go`, (read only) `internal/signinsvc/password.go`, (read only) `internal/issuersvc/notice.go`, and the alert at (read only) `internal/localusersvc/locks.go`. A notification is not a change, and a person's sign-in or event starts each one.
- The drop of runner credentials at a restore, (read only) `pkg/backup/restore_runner_credentials.go`: it is an effect of the restore that a person starts, and the restore entry names that person.
- The authorizer sites and the trigger sites of the namespace service principal: (read only) `pkg/access/authorizer.go`, (read only) `pkg/access/discovery.go`, `pkg/trigger/dispatcher.go`, `pkg/trigger/manager.go`, `pkg/trigger/trigger.go`, `pkg/job/trigger.go` and `pkg/dustpan/triggers.go`. The first trigger change removes them with the principal (decision 5). Only the credential and grant refusals in decision 4 get a housekeeping twin here.
- The test helpers that name `@server` (`pkg/job/jobtest/reconcile.go`, `internal/history/historytest/redactor.go`) follow the code that they test.

## Risks

- A sweep that a person revoked stops until a person grants it again. The docs page says so, and the denial entry names the principal and the action.

## Where the source filter runs

The store applies the `source` filter in the list query and in the count query. The service does not filter after the read, because that breaks the page totals and the cursors. The port `pkg/principal/store.go` takes the source, and both dialects and the conformance suite follow. The agent may put the built-in principal constants in `pkg/identity/identity.go` instead of a new file.
