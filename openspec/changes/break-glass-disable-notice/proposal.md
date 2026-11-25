---
version: 1
---

Issue: #623.

## Why

**Persona: site administrator.** A site administrator who does not disable a break-glass account must learn at once that another administrator did, because the account is a recovery path. Today a disable writes an audit entry only. No one is told.

The user path this change must make work:

1. **API.** An administrator disables one of two enabled break-glass accounts. The change keep-two-working-break-glass accepts it and names the live count.
2. **Notification.** Every administrator of the installation gets one notice of type break-glass.disabled, with the account, the person who disabled it and the number of enabled break-glass accounts that stay.
3. **Audit.** The audit log holds one entry for the notice, so the person can ask why the notice exists and find the disable.
4. **Docs page.** The guide for break-glass accounts names the notice, its recipients and where to read it.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| The notice cannot be stored. | The disable is not stored and the answer is 500 with the code of the outbox failure. The audit log records the failed write. | `GET /api/server/principals/{id}` |

Evidence for the claims above:

- Main has the same pattern for another alert: the removal alert appends a notification to the outbox of the server scope for the administrators and one audit entry. Proof: `internal/removalalertsvc/removalalertsvc.go:246 (func alert, the Notification literal with Admins: true)`.
- The catalog of notification types holds no type for a disable. Proof: `pkg/notify/notify.go:133 (const MembershipMassRemoval)` is the last alert type before SinkTest.
- The disable route and its audit entry exist. Proof: `internal/api/server/principals.go:894 (func disablePrincipal)`.

## What Changes

- A new notification type `break-glass.disabled` in pkg/notify/notify.go (const BreakGlassDisabled), next to MembershipMassRemoval, with its line in the catalog list of Types().
- A new service type DisableNotice in NEW file internal/removalalertsvc/breakglass_disable.go. It reuses the Config ports Outbox, Audit, Logger and Now of internal/removalalertsvc, and its constructor NewDisableNotice returns ErrMissingDependency when a port is missing.
- The channel is the notification outbox of the server scope (@server), appended with the zero fence, as the removal alert does.
- The recipients are the administrators of the installation (`Recipient{Admins: true}`). The administrator who disabled the account and the holder of the disabled account are administrators too, so they get the notice as well. No recipient list is a code rule.
- The severity is warning. The summary names the account and the actor. The attributes are `principal.id` (stable ID), `actor.kind`, `actor.id`, `enabled.remaining` and `disabled.at`.
- The audit entry of the notice has the action `principal:break-glass-disable-notice`, kind change, outcome success, scope @server and the principal reference of the account as object.
- The disable of a break-glass account calls the service after the disable commits. A failed notice returns an error that wraps the cause and writes an audit entry of outcome failure, as the removal alert does. The disable stays stored: the account is already disabled and the audit entry of the disable exists.
- The guide for break-glass accounts names the notice.

Out of scope:

- The guard of disable, delete and restore (change keep-two-working-break-glass, which this change depends on).
- The guard of the other writes (change break-glass-floor-other-writes).
- A notice for a delete, an enable or a use of a break-glass account. The type break-glass.used exists on main.
- A new sink, a new channel kind or a new recipient kind.

Depends on:

- keep-two-working-break-glass

## Usability

Where the path still costs the person time or surprises them:

- One disable gives one notice for every administrator, so the count of notices does not grow with the number of administrators.
- The notice names the number of enabled break-glass accounts that stay, so the reader needs no extra call.
- The notice carries the stable ID of the account. The name is display only.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The removal alert service appends one notification to the outbox of the server scope and one audit entry. This change adds a second service in the same package and reuses its ports. (`internal/removalalertsvc/removalalertsvc.go:88 (func New)`, `internal/removalalertsvc/removalalertsvc.go:246 (func alert)`)
- The catalog of notification types has the alert type for the removal alert and three break-glass types. (`pkg/notify/notify.go:133 (const MembershipMassRemoval)`, `pkg/notify/notify.go:105 (const BreakGlassUsed)`)
- The route of the disable and its audit entry. (`internal/api/server/principals.go:894 (func disablePrincipal)`, `internal/api/server/principals.go:600 (route POST principalPath+"/disable")`)
- The port that disables a principal. (`pkg/principal/store.go:87 (func DisablePrincipal of Store)`)
- The wiring of the removal alert service. (`pkg/dustpan/localusers.go:126 (call of removalalertsvc.New)`)

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P19)**, Item 17: the break-glass floor: "the PM reading in build. Disable, delete and sweep stop at the last enabled account (ADR 306 d3); every other change keeps the two-working-paths floor (ADR 70 d24); first setup needs two. Reason: the stricter floor wherever the newer owner text is silent; owner ADRs 70 and 306 both kept."

## ADR check

Each quote is copied from the file in `docs/adr/`.

**ADR 306: Break-glass: the session limit, the mass-suspension alert and the disable rules** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current), 6 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Delete and sweep (owner, 2026-10-20, accepted as written for now). Delete and sweep follow the same "not the last one" rule."
- Effect: The disable is allowed while one enabled account stays. The notice tells the other administrators. ADR 306 gives no rule on the notice, so the notice adds a record and no restriction.
- Conflict: none.

**ADR 70: Act-as reach, identity provider groups and break-glass accounts** (not owner-tagged).

- Decisions that apply: 23 (changed), 24 (changed).
- Changed by: ADR 216 changes decisions 11, 23, 24 and 28; ADR 306 changes decisions 24, 25 and 26. Chain to the end: see the change keep-two-working-break-glass.
- Current rule: this ADR, with ADR 216 and ADR 306.
- Effect: No rule of these decisions changes. The notice reports a change that they allow.
- Conflict: none.

**ADR 74: No phone-home** (not owner-tagged).

- Effect: The notice goes to the outbox of the server and to the sinks that the operator configures. No destination that the operator did not name is added.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Which channel? The notification outbox of the server scope, as the removal alert does. The sinks that the operator configured deliver it.
- Which recipients? The administrators of the installation (`Recipient{Admins: true}`), as the removal alert does. No new recipient kind exists.
- Does the disabled account get the notice? Its holder is an administrator, so yes. No exclusion mechanism exists on main, and none is added.
- What happens when the notice fails? The disable stays stored, the failure is audited and the caller gets the error. A disable that cannot be told is still a disable that happened, and an undone disable would reopen the lockout risk.
- Does the notice change the floor? No. The floor guard of the change keep-two-working-break-glass runs before the disable commits.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs.
