---
version: 8
---

Roadmap: `docs/roadmap.md:171` (Send expiry warning email through SMTP queue.).

## Why

**Persona: developer.** A Developer uses a personal access token with the Pulumi CLI and OpenTofu. When the token ends, the next run stops with a 401. The Developer wants an email before that day, not after.

The earlier draft of this change specified its own email queue: a table of rows with the states queued, sending, retry, sent and failed, its own retry rule and its own audit kind. Main already has that model, in `pkg/notify`: an outbox of notifications, a delivery for each recipient with leases, retries and states, and a port for the personal contacts of a person. This change adds only what main lacks: the notification type, the job that finds the tokens, and the dependencies that the delivery to a person needs.

The user path this change must make work:

1. **API.** An administrator sets the SMTP relay and sender as installation settings (a dependency, see below).
2. **CLI.** Developer uses a personal access token that expires in less than 14 days.
3. **Email.** The server sends one email at 14 days and one at 3 days before the expiry to the verified address of the Developer.
4. **Docs page.** Developer and administrator read the access tokens guide for the warnings, the two times and the SMTP dependency.
5. **API.** The Developer rotates the token and gets no more warnings for the old one.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| The installation has no SMTP settings | The delivery of the warning is skipped with a reason. The audit entry and the delivery record name the reason. | `GET /api/dustpan/v1/settings` |
| The Developer has no verified email address | The delivery is skipped with the reason "no contact". | `GET /api/dustpan/v1/me/tokens?status=expiring` |

## What Changes

- Add the notification type `token.expiry-warning` to the catalog of `pkg/notify`, and its reference entry.
- Add a service `internal/tokenwarnsvc` (NEW). It lists the active personal tokens that expire within `accesstoken.ExpiryWarningSoon`, computes `accesstoken.WarningFor` for each, and appends one notification for each token and level through `notify.Outbox.Append`. The scope is `audit.ServerScope`. The recipient is `Recipient{Principal: <owner>}`. The dedupe key is the token ID, the level and the expiry time, so a repeat of the job, a second instance and a restart append nothing new, and a rotation that moves the expiry gives a new key.
- Add one durable schedule kind for the service in `pkg/dustpan/schedules.go`, next to `kindInactivity`, once for all instances (the same way as the inactivity sweep).
- The notification holds the token ID, the token name, the expiry time and the level in `Attributes`, and a fixed summary. It holds no secret and no prefix of one. Retry, lease, delivery state and the audit of a delivery come from `pkg/notify`, not from this change.
- Write the page in `docs/guides/access-tokens.md` and the notification type in the notification reference.

Depends on (gaps on main, see "What main already does"):

- `chat-a2-outbox-deliveries-dispatcher` for the dispatcher. It routes to subscriptions only and ignores `Recipients`.
- A change that routes a notification with `Recipients` to the personal contacts of a person, with an implementation of `notify.Contacts` (`ContactsOf`), and an email `Sender` that uses SMTP. No change in `openspec/changes` specifies these.
- The SMTP installation settings and the verified email address of a person. No setting name is declared on main.

The e2e step cannot run until these four exist. The tasks mark it BLOCKED.

Out of scope:

- Tokens of service principals, and exchange tokens (`jwt-exchange`, `mtls-exchange`). Who gets the warning for a service token is a question about who holds a permission. It is parked.
- Where the two warning times are stored as settings, and the names of the SMTP settings.
- The warning field, the CLI warning and the console banner (the display change).
- Enforcement of the expiry.
- A new queue, new retry rules or a new audit kind for deliveries.

## Usability

- The Developer gets two emails for one token, and none for a token that is already ended or rotated.
- An administrator sees a skipped delivery and its reason in the delivery records of `pkg/notify`, not in a second place.
- Quiet hours apply to a personal notice of severity `info` and `warning` (see the ADR check). The change uses `warning` for the first level and `critical` for the second, so the 3-day email goes at once. This is a use of the existing rule, and it is listed under the open points.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The notification model has `Notification` with `Recipients`, `DedupeKey`, `Scope` and `Attributes`, and the catalog of types. The last types are `sign-in.locked`, `membership.mass-removal` and `sink.test`. (`pkg/notify/notify.go:257-273`, `:95-136`)
- `Outbox.Append` stores in one transaction and skips a notification with a stored scope and dedupe key. (`pkg/notify/ports.go:36-46`)
- A notification for the server scope uses `audit.ServerScope` and the zero fence. One service already does this: it builds the notification with `Recipients: Admins` and calls `Outbox.Append`. (`internal/removalalertsvc/removalalertsvc.go:230-257`)
- `Recipient` names a principal, a group or the administrators. The text says that a recipient routes a notice also to the personal contacts of each person. (`pkg/notify/notify.go:222-233`)
- `Contacts.ContactsOf` is a port. The text says that the identity package implements it. (`pkg/notify/ports.go:520-533`)
- The sink kinds include email as a name in the text of the package, and the sender port exists. No email sender and no `ContactsOf` implementation exists outside `pkg/notify`. (`pkg/notify/notify.go:7`; the sibling change `chat-a2-outbox-deliveries-dispatcher` records the proof in its proposal, line 51)
- The dispatcher is the subject of `chat-a2-outbox-deliveries-dispatcher`: it makes deliveries for subscriptions only and ignores `Recipients`. (`openspec/changes/chat-a2-outbox-deliveries-dispatcher/proposal.md:51`, `:132`)
- `accesstoken.WarningFor` returns imminent up to 3 days and soon up to 14 days before the expiry, and none for an ended token. `ExpiringWithin` is 14 days. (`pkg/accesstoken/accesstoken.go:419-422`, `:447`, `:182`)
- `Service.List` pages tokens with a filter, and an empty `PrincipalID` lists all principals. `FilterExpiring` keeps the active tokens that expire soon. `Token.Kind` is personal, service, jwt-exchange or mtls-exchange. (`pkg/accesstoken/service.go:295`; `pkg/accesstoken/accesstoken.go:71-83`, `:196`)
- The inactivity sweep is a durable schedule kind with `jobKind(kindInactivity, s.sweepOnce)`, once for all instances. (`pkg/dustpan/schedules.go:57`, `:83`, `:153`; `pkg/dustpan/schedules_internal_test.go:345`)
- The token has no owner state (enabled or disabled) on the row. The owner is read through the principal service. (`pkg/accesstoken/accesstoken.go:148-160`; `pkg/accesstoken/service.go:283-289`)

Audit corrections against the earlier draft: it said that a bespoke queue and its audit kind are needed (`pkg/notify` has both), it cited tests as the proof of the warning computation (the code is at `accesstoken.go:447`), and it put the SMTP setting names and the plain-connection setting inside this change (the names are not declared on main and the plain-connection rule is a decision of the owner that the email sender change applies).

## ADR check

Each quote is copied from the file in `docs/adr/`. The chains come from `docs/adr/INDEX.md`.

**ADR 53: Access token life cycle, inactivity disable and expiry warnings** (not owner-tagged; its context records answers of the owner).

- Decision that applies: 11. Changed by: ADR 69 extends decisions 2 and 11; ADR 212 extends decision 11; ADR 354 extends. Current rule: this ADR, with ADR 69, 70, 212, 237, 354, 373.
- Quote: "an email to the owner of the token. This needs SMTP settings."
- Effect: this change is the email of decision 11. The owner of the token is the recipient.
- Conflict: none.

**ADR 69: Token lifetime and session defaults** (not owner-tagged).

- Decision that applies: 5. Changed by: ADR 297 changes decision 4 only.
- Quote: "The warnings of ADR 53 decision 11 start 14 days and again 3 days before the expiry. Both are settings."
- Effect: the change uses the constants of main (14 days and 3 days). It does not add a setting. The storage of the two times as settings is parked.
- Conflict: none.

**ADR 212: The planner role, SMTP settings, fresh MFA for weaker settings and approvals, trusted proxies, restore audit entries, the branding preset and one settings API** (not owner-tagged).

- Decision that applies: 2. Changed by: ADR 301 extends decision 2; ADR 365 changes decision 5. Current rule: this ADR, with ADR 227, 232, 235, 301, 365.
- Quote: "SMTP settings are installation settings."
- Effect: the email sender change reads the relay and sender from installation settings. This change declares none.
- Conflict: none.

**ADR 292: Plain LDAP and SMTP connections are refused unless an operator opts in** (owner-tagged, all decisions).

- Quote: "Refused by default (owner). The server refuses an LDAP connection and an SMTP connection that has no TLS, except to a loopback address. (owner)"
- Effect: the email sender change applies it. This change sends no email by itself.
- Conflict: none.

**ADR 145: One event model and sinks** (not owner-tagged).

- Decisions that apply: 24 and 25.
- Quote (24): "A notice names recipients: principals or groups. The `notify.Contacts` port gives the contacts of each person: the verified email address, and the chat identity that the person linked"
- Quote (25): "A personal notice of the severity `info` or `warning` waits for the business hours of the calendar of the person (ADR 140 decision 4). A `critical` notice goes at once."
- Effect: the notification is a personal notice with `Recipients`. The severity decides the quiet hours rule.
- Conflict: none.

## Open questions

none

## Parked (not questions of this change)

These points are outside the specified work. Each needs the owner, or a decision that no ADR makes. The tasks do not depend on them.

- Who gets the warning for a token of a service principal.
- Whether the first level (14 days) goes at once. This change gives it the severity `warning`, so quiet hours apply, and gives the second level (3 days) the severity `critical`, so it goes at once.
- The names and the home of the two warning time settings and of the SMTP settings.
