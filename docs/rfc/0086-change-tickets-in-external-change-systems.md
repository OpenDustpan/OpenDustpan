---
rfc: 86
title: Change tickets in external change systems, and what the core needs for them
status: discussing
created: 2026-10-19
---

# RFC 86. Change tickets in external change systems, and what the core needs for them

Status: discussing. The product manager wrote this RFC from a design
review. No code exists for it. The proposals have the label "(product
manager)". The owner answers the questions at the end.

## Problem

Many regulated teams record each production change as a ticket in an
external change system. Two common systems are Jira Service Management
and ServiceNow Change Management. A change advisory board, or one
approver, approves the ticket there. Today a team that uses OpenDustpan
must copy each change into the ticket by hand. A person must then check
the ticket before an OpenDustpan job applies. Nothing links the two
records.

[RFC 51](0051-change-management-integrations.md) records the gap as a
first idea: "change requests in an ITSM tool, opened and closed by
approval events". On 2026-10-17 the owner decided to spike Jira and
ServiceNow only to learn the shape of their interfaces, and not to build
them now ([ADR 247](../adr/0247-works-with-backstage-an-entity-tab-of-units.md),
Context).

This RFC answers one question: what does the tool-neutral core of
OpenDustpan need, so that Jira Service Management, ServiceNow Change
Management and later systems fit one port? The answer must keep these
accepted decisions:

- One model for gates, approval rules, facts and decision records (ADRs
  222 to 230).
- Tool models never merge (ADRs 12 and 13). A change ticket is about a
  change of units. It holds no field that only one tool needs.
- Every feature has a REST API, and no feature exists only in a
  configuration file (ADR 17).
- A machine principal is never a person (ADR 54 decision 6). Only a
  person in a session approves (ADR 225 decision 7).
- Notifications commit with their change and deliver at least once (ADR
  145).

### Words in this RFC

- **Change system**: an external product that holds change tickets, for
  example Jira Service Management or ServiceNow Change Management.
- **Change ticket**: one record in a change system for one change of
  OpenDustpan. In Jira Service Management it is a request of a change
  request type. In ServiceNow it is a record of the `change_request` table.
- **Ticket connection**: the stored object of OpenDustpan that names one
  change system, its address, its credential and its field map.

The product manager proposes these terms for the glossary. `GLOSSARY.md`
gets "Change ticket" when the owner accepts this RFC, and the _Avoid_
note of "Change order" then points at it. This pull request does not
edit `GLOSSARY.md`.

### What a change system offers

The facts below come from the public API documentation of each system.
The spikes must check each fact that has the mark "(spike)".

**Jira Service Management (Cloud).**

- All calls of a service account go to
  `https://api.atlassian.com/ex/jira/{cloudId}/...`. A call with an API
  token goes to the site URL.
- Create a request: `POST /rest/servicedeskapi/request`, with
  `serviceDeskId`, `requestTypeId`, `requestFieldValues` (a map of field
  IDs to values) and optional `raiseOnBehalfOf`. A dry run exists:
  `POST /rest/servicedeskapi/request/validate`.
  Source: <https://developer.atlassian.com/cloud/jira/service-desk/rest/api-group-request/>
- Read the approvals: `GET /rest/servicedeskapi/request/{issueIdOrKey}/approval`.
  Same source.
- Fields of a change request (change type, risk, planned start and end,
  affected services) are custom fields. Their IDs differ on each site
  (spike). So a ticket connection needs a field map.
- No idempotency key exists on the create call (spike). An issue entity
  property can hold our key. A JQL search of entity properties needs an
  app module that indexes the property, so a plain API user cannot use
  it (spike). Q8 uses a field that the create call sets.
  Source: <https://developer.atlassian.com/cloud/jira/platform/jira-entity-properties/>
- Webhooks: a webhook with a `secret` carries `X-Hub-Signature` as
  `sha256=<HMAC of the body>`. Each delivery has
  `X-Atlassian-Webhook-Identifier`, which stays the same across retries.
  Jira tries a failed delivery again up to five times, about 5 to 15
  minutes apart. Webhooks of OAuth 2.0 apps use bearer authentication
  instead of a secret. A webhook that the REST API registers expires
  after 30 days, and a `PUT` refreshes it. A webhook with basic
  authentication and a secret comes from the admin route of the site.
  That route needs the Jira administrator permission. Nobody has
  checked this fact yet (spike).
  Source: <https://developer.atlassian.com/cloud/jira/platform/webhooks/>
- Authentication: an API token of a user with HTTP basic authentication.
  Source: <https://developer.atlassian.com/cloud/jira/platform/basic-auth-for-rest-apis/>.
  Or a service account with an OAuth 2.0 client credential: the client
  ID and secret go to `https://auth.atlassian.com/oauth/token` with the
  `client_credentials` grant, and the access token lives 60 minutes.
  Source: <https://support.atlassian.com/user-management/docs/create-oauth-2-0-credential-for-service-accounts/>
- A second path exists for CI/CD tools: deployment gating. A tool sends
  deployment data to `POST /rest/deployments/0.1/bulk`. It then reads
  `GET /rest/deployments/0.1/pipelines/{pipelineId}/environments/{environmentId}/deployments/{deploymentSequenceNumber}/gating-status`.
  It needs OAuth 2.0 credentials for an on-premises integration, a
  Connect app or a Forge app.
  Source: <https://developer.atlassian.com/cloud/jira/software/rest/api-group-deployments/>

**ServiceNow Change Management.**

- The Change Management API has the base path `/api/sn_chg_rest/change`.
  It has routes for each change type: `.../change/normal`,
  `.../change/standard/{template}` and `.../change/emergency`. It has
  `GET .../change/{sys_id}`, `GET .../change/{sys_id}/nextstates`,
  `PATCH .../change/{sys_id}` with name-value pairs of the fields, and a
  conflict check. A `POST` or `PATCH` needs the role `itil`,
  `change_manager` or `admin`. ServiceNow gives one reference for each release.
  Source: <https://www.servicenow.com/docs/r/api-reference/rest-apis/change-management-api.html>
- Fields of a change request include `short_description`, `description`,
  `cmdb_ci`, `assignment_group`, `start_date`, `end_date`, `risk`,
  `impact`, `state`, `approval` and `correlation_id` (same source). The
  `approval` field has values such as `not requested`, `requested`,
  `approved` and `rejected` (spike). The numbers of `state` depend on the change model of
  the instance (same source).
- `GET .../change` takes name-value filters (same source). They are
  exclusive with `sysparm_query`: a call uses one of the two. So a search by
  `correlation_id` can find a ticket that an earlier try created (spike).
- Authentication: HTTP basic authentication of an integration user, or
  OAuth 2.0. The client credentials grant for inbound integrations exists
  from the Washington DC release, and an administrator must switch it on.
  Source: <https://www.servicenow.com/docs/r/platform-security/authentication/oauth-inbound.html>
- ServiceNow has no built-in signed webhook for a change of a record
  (spike). An administrator builds an outbound REST message in a business
  rule or a flow. The sign scheme of that message is what the
  administrator writes.

## Constraints from the accepted ADRs

1. **A fact never decides** (ADR 226 decision 1). Only a gate, an approval
   default, an ownership rule or a fixed control decides.
2. **An external check is a producer of facts** (ADR 226 decision 10). The
   server sends a signed request and waits for a signed callback. The
   answer is `passed` or `failed`. The timeout is 1 minute to 1 hour.
3. **Only a person in a session approves** (ADR 225 decision 7). A service
   principal, a job token and an access token never approve.
4. **A recheck at `change.apply`** reads the facts of that moment (ADR 224
   decision 3).
5. **An error fails closed** (ADR 224 decision 13). A producer that fails
   gives `available == false` (ADR 226 decision 12).
6. **Secrets.** A sink seals each secret value under the data key of its
   scope, and the API never answers a secret (ADR 145, `notify.SinkSealer`).
7. **Outbound calls** go through the dial guard of `pkg/egress` (ADR 145
   decision 20).
8. **Never claim compliance** (ADR 222 decision 9). A starter pack is
   "designed against" a control framework.

## Q1. How does an approval in a change system reach a gate? (blocks the rest)

| Option | For | Against |
| --- | --- | --- |
| A. A fact (proposed) | ADR 225 stays as it is. The ticket state is a fact, and a gate decides what it does. External checks (ADR 226 decision 10) already wait for an outside answer. A team can combine it with an OpenDustpan approval rule. | The timeout of an external check is at most 1 hour (Q2). The record shows who approved in the change system as evidence only, not as a signature. |
| B. A signature of a new kind | A ticket approval meets a requirement directly. The approval wait, the timeout and the rejection of ADR 225 decision 8 hold as they are. | It changes ADR 225 decision 7: a party that is not a person in a session signs. OpenDustpan cannot check who approved, or with which authentication. Fresh authentication (ADR 227) has no meaning for it. |
| C. A note only | No change to any ADR. The ticket shows the change, and a person approves in OpenDustpan as today. | Two approvals for one change. Nothing stops an apply when the ticket is still open. |
| D. Do nothing | No cost. | Teams copy each change by hand, as today. |

**Proposal (product manager): A.** The change system is a producer of
facts. A gate reads the fact, as for a cost estimate. OpenDustpan never
counts a ticket approval as a person's signature.

### The fact group

`change.planned` and `change.apply` get a group `changeTicket` in a new
minor version of their schemas (ADR 226 decision 2):

| Field | Type | Meaning |
| --- | --- | --- |
| `available` | boolean | `false` when no ticket connection holds for the change, or when the change system did not answer |
| `state` | string | `open`, `approved`, `refused`, `canceled` or `closed` |
| `inWindow` | boolean | `true` when the time of the evaluation is inside the planned start and end of the ticket. `false` when the ticket has no window. |
| `system` | string | the ID of the ticket connection |

Each adapter maps the states of its system to these five values. For
example, the `approval` value `rejected` of ServiceNow maps to `refused`.
The spikes give each mapping table.

The check name of the ticket also goes into `externalChecks`: `passed`
for `approved` when `inWindow` is `true` (`approved && inWindow`), `failed` for `refused`, `canceled` or `closed`, `pending`
for `open`, and for `approved` when the window has not started, and `timedOut` after the timeout. So a gate that reads only
`externalChecks` also works.

A starter gate, `change-ticket-approved`, ships in `evaluate` mode (ADR
229 decision 7):

```cedar
// change.apply, outcome deny
!context.changeTicket.available
  || context.changeTicket.state != "approved"
  || !context.changeTicket.inWindow
```

The starter gate fails closed. It reads `available` as ADR 226 decision 3
asks. A ticket with no window gets `inWindow == false`, so the gate
denies it.

`refused` is the state of a ticket in the change system. "Rejected" is
what an approver in OpenDustpan does to a change order (ADR 225 decision
8). The two words keep the two records apart.

## Q2. How long can a change wait for a ticket? (blocks Q1 A)

A change advisory board can need days. ADR 226 decision 10 limits an
external check to 1 hour.

| Option | For | Against |
| --- | --- | --- |
| A. A ticket check has a timeout of 1 hour to 30 days, default 7 days (proposed) | The same range as an approval rule (ADR 225 decision 1). The change waits in the approval wait, as for people. | A second timeout range for one concept. ADR 226 decision 10 needs an amendment. |
| B. Keep 1 hour, and start the change again later | No ADR change. | A person must start each change again after the board meets. The plan can be stale then. |

**Proposal (product manager): A.** While the ticket is `open`, or
`approved` with a window that has not started, the change stays in the
approval wait with the cause `change-ticket`. Each read that changes
`state` or `inWindow` evaluates `change.planned` again. A deny ends the
change, as a rejection does. The job state value stays `awaiting-approval`
(ADR 225 decision 8). The shortest timeout of the requirements and of the
ticket holds.

Option A amends ADR 226 decision 10 twice:

1. The timeout range of a ticket check is 1 hour to 30 days, not 1 hour.
2. The answer of a ticket check comes from a read of the server through the
   API of the change system, not from a signed callback (Q3).

The check `passed` needs `approved && inWindow`.

## Q3. What does OpenDustpan send, and what does it read?

### What OpenDustpan sends

| Moment in OpenDustpan | Effect on the change ticket |
| --- | --- |
| `change.planned` runs for a change that a ticket rule selects (Q5) | Open the ticket, with the plan summary, the units, the decision record ID and the console link. |
| The change waits for people in OpenDustpan | Add a note that names each open requirement. |
| OpenDustpan has each signature that the change needs | Add a note. |
| A gate denies, an approver rejects, or the change expires, or a person cancels it | Cancel the ticket, with the reason and the record ID. |
| The change applied | Close the ticket as successful, with the state version of each unit. |
| The apply failed | Close the ticket as not successful, with the record ID. |
| A new plan revision with other digest inputs (ADR 225 decision 12) | Cancel the old ticket and open a new one (Q6). |

The open is the request of the external check. The other effects read the
notification outbox of ADR 145. They need these notification types, in
addition to the `job.*` types of `notify.Types`:

- `changeorder.awaiting-approval`, `changeorder.approved`,
  `changeorder.rejected`, `changeorder.applied` and `changeorder.failed`.
- `job.approved`, which the catalog does not have today.
- `change-ticket.sync-failed`, for the owners of the scope, when a change
  system refuses the credential or each try of a write fails.

A ticket connection reads the outbox with its own cursor (ADR 145
decision 9). It is not a sink. A ticket connection records its creator. Before each
open and each write, the server applies the two checks of ADR 145
decision 15 to that creator and to the ticket rule. When a check fails,
the server suspends the connection and sends `change-ticket.sync-failed`.
A sink can lose a notification after a
day of failures (ADR 145 decision 19). A ticket must not lose its close.

When the cursor resumes after a stop, a reconcile pass writes each open
ticket from the current state of its change. Then the cursor reads the
notifications that follow. A note to a ticket that is already closed
answers `ErrConflict`. The adapter counts that as success.

The fields that leave the installation are: the change ID, the unit
references, the plan counts, the operation, the cost delta, the decision
record ID, the console link and the planned window. Each is optional in
the field map. The default sends the change ID, the unit references, the
counts and the console link. The approver names and email addresses that
a change system reports stay in the decision record for the life of the
record (ADR 229 decision 5), and in backups.

### What OpenDustpan reads

| Event in the change system | Effect in OpenDustpan |
| --- | --- |
| The approvers approve the ticket | `changeTicket.state` becomes `approved`. The check passes. |
| The approvers refuse the ticket | `state` becomes `refused`. The check fails. |
| A person cancels or closes the ticket before an approval | `state` becomes `canceled` or `closed`. The check fails. |
| The planned window of the ticket changes | `inWindow` changes at the next evaluation. |
| An approved ticket goes back to a state before the approval | `change.apply` reads the new state and the gate decides again (ADR 224 decision 3). |

**A callback is a hint only (product manager).** A callback from a change
system never sets a fact. It only tells the server to read the ticket.
The server reads the state through the API of the change system, with the
credential of the ticket connection, before each evaluation. So a forged
callback can cause one more read, and nothing else. The server also reads
each open ticket at an interval (proposed: 5 minutes), so a lost callback
delays a change by at most that interval.

Three limits keep a callback harmless:

- A callback causes a read only for a ticket that the server holds open
  for that connection, and at most one read for each ticket in each 30
  seconds.
- The route answers 204 to each request, also for an unknown connection
  and for a bad signature. The answer gives no information.
- The route is `POST /api/v1/namespaces/{namespace}/ticket-callbacks/{connection ID}`
  for a connection of a namespace. A connection of the installation uses
  `POST /api/v1/ticket-callbacks/{connection ID}`.

Benchmark: Stripe does not promise the order of its webhook events, and
tells a receiver that it can get the object through the API.
Source: <https://docs.stripe.com/webhooks>

## Q4. The port

`pkg/gate` keeps one producer port for each group of facts (ADR 226
decision 12). The ticket producer lives in a new tool-neutral domain
package, `pkg/changeticket`, which consumes the port below. The adapters
live in `pkg/changeticket/jsm` and `pkg/changeticket/servicenow`. The
conformance suite is `pkg/changeticket/changetickettest`.

```go
// System is one change system. An adapter calls the API of the system
// with the credential of the ticket connection.
type System interface {
	// Open creates the ticket of t, or returns the ticket that holds
	// t.Key. A second call with the same Key creates no second ticket.
	// It returns ErrInvalid when the system refuses a field, ErrAuth
	// when the system refuses the credential, ErrTemporary when the
	// system does not answer, and ErrPermanent for each other refusal.
	Open(ctx context.Context, t Ticket) (Ref, error)
	// Read returns the state and the planned window of the ticket. It
	// returns ErrNotFound for a ticket that the system no longer holds,
	// and ErrAuth, ErrTemporary and ErrPermanent as Open does.
	Read(ctx context.Context, ref Ref) (Status, error)
	// Write adds a note to the ticket, or closes or cancels it. A second
	// call with the same Update.Key changes nothing. It returns the
	// errors of Read, and ErrConflict when the ticket is in a state that
	// does not allow the change.
	Write(ctx context.Context, ref Ref, u Update) error
}

// Callbacks checks a request from a change system and returns the
// ticket that it names. It returns ErrSignature for a request whose
// signature or token does not match.
type Callbacks interface {
	Ticket(ctx context.Context, r CallbackRequest) (Ref, error)
}
```

The errors follow the classes of `notify` (ADR 145 decision 18): `ErrAuth`
and `ErrPermanent` stop the tries. `ErrTemporary` tries again.
`ErrNotFound` sets `changeTicket.available` to `false`.

`Ticket` holds only tool-neutral values: the change ID, the namespace,
the units as references (tool, namespace and the tool's own name), the
plan counts, the operation, the cost delta, the record ID, the console
link and the planned window that the person gives. Each value uses the
words of its tool when it names a unit, for example "stack" or
"workspace" (ADR 13). A ticket never holds a secret value, a state, a plan
body or an input value. The field map of the ticket connection maps these
values to the fields of the system.

**Alternatives for the port (product manager: the one above).**

| Option | For | Against |
| --- | --- | --- |
| A. Three methods and a callback check (proposed) | Small. A new system needs one adapter. The suite tests every adapter the same way. | The field map is data, so a field that needs logic (for example a lookup of a `cmdb_ci` by name) needs a new field kind. |
| B. A generic signed webhook only, and teams build the bridge | No adapter in OpenDustpan. | Each team writes and runs a bridge, with its own retries and its own credential store. |
| C. One port for each system | Each port fits its system. | Shared code then holds a field for one system only. ADR 12 refuses that. |

## Q5. Configuration

A **ticket connection** is an object of a scope, with the scope in the
route path as ADR 230 decision 3 gives:

- `GET`, `PUT`, `DELETE {scope}/ticket-connections/{id}`, with `system`
  (`jsm` or `servicenow`), the base URL, the auth kind, the field map and
  the secret refs.
- `PUT {scope}/ticket-connections/{id}/secrets/{ref}`, which takes a
  value and never answers it.
- `POST {scope}/ticket-connections/{id}/test`, which runs `Read` on a
  ticket key that the person gives, or the dry run of Jira Service
  Management.

A **ticket rule** says which changes open a ticket: a ticket connection, a
Cedar `when` over the facts of `change.planned`, and a timeout. It has a
`mode`, `enforce` or `evaluate`, as an ownership rule has (ADR 225
decision 9). A rule in `evaluate` mode opens no ticket and only writes to
the decision record. The check at save refuses a ticket rule that reads
`changeTicket`, with `gate.ErrInvalid`. The rule runs before a ticket
exists, so the fact has no meaning there.

New actions: `namespace:manage-ticket-connections` and
`namespace:read-ticket-connections`, and the same at the installation.
A change of a ticket connection or a rule writes an audit entry of the
kind `change` and needs a fresh authentication (ADR 227). The callback
route `POST .../ticket-callbacks/{connection ID}` needs no sign-in, so it
goes on the explicit public list of the route test.

## Q6. A new plan, and the record

**A new plan (product manager).** A new plan revision with other digest
inputs cancels the old ticket and opens a new one. An approval in the
change system is about the plan that the ticket shows. With identical
digest inputs, the ticket stays, under the same rule as the signature
carry-over (ADR 225 decision 12). The change stays in the approval wait
with the cause `change-ticket` until the new ticket is `approved` and
inside its window (Q2). A read of the canceled ticket does not change
the facts of the new plan.

**The record (product manager).** The decision record (ADR 229) holds,
for each change ticket:

- the ticket connection, the system, the ticket key and its link.
- the state and the window that the server read, and the time of the
  read.
- the approvers that the system reports, with their names and the time
  of each answer, as evidence. They are not principals. A person whose
  verified email address matches a principal shows with that principal,
  as `oncall.Directory` does (ADR 143 decision 6).
- the SHA-256 of the body of the read answer.

The audit entry of each step names the record ID (ADR 229 decision 5). A
change of `changeTicket.state` that the server reads writes a new record
for the evaluation that it causes.

## Q7. Authentication to each system

| System | Option | Proposal |
| --- | --- | --- |
| Jira Service Management | OAuth 2.0 client credentials of a service account | Proposed default. The token lives 60 minutes, and the server keeps it in memory only. |
| Jira Service Management | API token with basic authentication | Supported. Some sites do not have service accounts. |
| ServiceNow | OAuth 2.0 client credentials | Proposed default, from the Washington DC release. |
| ServiceNow | Basic authentication of an integration user | Supported for older releases. |

For each option:

- The client secret or the token is a secret value of the ticket
  connection. The server seals it with the data key of the scope, as
  `notify.SinkSealer` does. A new value gets a new ref. No configuration
  file and no environment variable holds it.
- The adapter asks for the smallest scope or role that the operations
  need. For ServiceNow that is the role `itil` or `change_manager`
  (spike).
- Calls go through `egresshttp` with no redirect, HTTPS only, and the
  dial guard.
- A change of the base URL or of the auth kind voids each secret of the
  ticket connection. A person must put each secret again before the next
  call. For Jira Service Management with a service account, the host is
  fixed (`api.atlassian.com/ex/jira/{cloudId}`), and the connection stores
  only the cloud ID.
- Trust rules (ADR 54) are about workloads that call OpenDustpan. The
  change systems do not send an OIDC token with a callback (spike). This
  is why a callback is a hint only (Q3).

Inbound callbacks:

- Jira Service Management: a webhook with a `secret`, checked with
  `X-Hub-Signature`. The server drops a duplicate by its
  `X-Atlassian-Webhook-Identifier`.
- ServiceNow: the business rule or flow sends a header with a token of
  the ticket connection. The server compares it in constant time. The
  inbound token is its own secret ref and has two active values, so a
  rotation has no gap (ADR 145 decision 22). An administrator can also
  sign the body with the scheme of `notify.Sign`.

## Q8. Idempotency and retries

- **The ticket key.** Each ticket has a key:
  `<installation ID>/<change ID>/<plan digest>`. ServiceNow stores it in
  `correlation_id`. Jira Service Management stores it in a field that the
  create call sets (a text custom field of the field map, or a label). The
  spike checks that JQL finds it right after the create. Before each
  create, the adapter searches for the key. A found ticket is the answer
  of `Open`.
- **Key length.** The digest is 64 hexadecimal characters, so the full key
  leaves 34 characters for two IDs, and two UUIDs do not fit in 100
  characters. The product manager proposes the first 16 characters of the
  digest. The key then has at most 100 characters when the two IDs have 82
  characters together. The spike checks the field limit of each system.
- **One writer.** Each open and each write is a durable operation with
  one owner and a lease (`pkg/durable`), so two instances never create
  two tickets at the same time. The call timeout of the adapter is shorter
  than the lease. The owner records "create started" in the fenced
  operation before the call.
- **A lost answer.** When a create succeeded and the server did not get its answer,
  the next try finds the ticket through the search. A search index can
  lag (spike). When the adapter finds two tickets with one key, it keeps
  the oldest and cancels the others with a note. ServiceNow has no unique
  `correlation_id`. When a duplicate is already approved, or is the ticket
  that the decision record cites, the adapter does not cancel it. It
  keeps that ticket, cancels the others, and sends
  `change-ticket.sync-failed` so that a person decides.
- **Retries.** The schedule of ADR 145 decision 18: 30 seconds, then
  double, up to 1 hour, at most 15 tries, and `Retry-After` up to 24
  hours. `ErrAuth` and `ErrPermanent` stop at once and send
  `change-ticket.sync-failed`.
- **Each write has a key.** `Update.Key` is the notification ID. A
  second write with the same key changes nothing. ServiceNow and Jira
  have no key on a note (spike), so the adapter writes the key in the note
  text and reads the notes before it writes.
- **Fail closed.** A change system that does not answer before the
  timeout gives `timedOut` and `available == false`. The starter gate
  denies.

## Q9. Jira: a request or deployment gating?

| Option | For | Against |
| --- | --- | --- |
| A. A customer request of a change request type (proposed for the spike) | Works with Jira Service Management alone. The approvals API exists. | The custom field IDs differ on each site, so the field map needs setup. |
| B. Deployment gating | The change system creates the change request itself, and Jira shows the gate in its own view. | It needs a Jira Software project and a link from a pipeline and an environment to the service. The model of a pipeline does not fit a change order of several units. |

**Proposal (product manager):** spike both. If the owner later decides to
build, build A first. Both fit the port of Q4.

## What stays out of scope

- Building the adapters now. The owner decided spikes only.
- An approval in OpenDustpan that answers an approval in the change
  system. OpenDustpan never approves on behalf of a person in another
  system.
- A sync of comments, attachments or the CMDB. A `cmdb_ci` is a fixed
  value or a value from the context of the unit (ADR 161) in the field
  map.
- Tickets for writes at `state.write` from a person's own CLI. A CLI
  cannot wait (ADR 224 decision 16). A later RFC can open a ticket after
  such a write.
- Change systems on a private network, for example Jira Data Center
  behind a firewall. The dial guard refuses private addresses (open
  question 4). Jira Data Center is a different adapter: it uses a personal
  access token and has no service accounts.
- A claim of compliance. A starter pack with the starter gate is
  "designed against" a control framework, for example SOC 2 CC8.1 or
  ISO/IEC 27001:2022 Annex A 8.32 (ADR 222 decision 9).
- Names and logos. The documentation says "works with Jira Service
  Management" and "works with ServiceNow Change Management". OpenDustpan
  shows no logo of either product as its own.

## What the spikes must answer

1. The fields of a change request on a test site of each system, and the
   minimum role or scope that can create, read and close it.
2. The map from the states and approval values of each system to the
   five states of Q1.
3. The values that close a ServiceNow change as successful or not
   successful, for example `close_code` (spike).
4. Whether a Jira webhook that a service account registers expires, and
   how to refresh it.
5. Whether a search by `correlation_id` or by an entity property finds a
   ticket right after its create.
6. One run of the port against each system: open, read, note, close.

## Open questions for the owner

1. **Q1.** Is a ticket approval a fact that a gate reads (A), or a
   signature of a new kind (B, which changes ADR 225 decision 7)?
2. **Q2.** Can a ticket check wait up to 30 days, as an approval rule
   can, and can its answer come from a server read instead of the
   signed callback? This amends ADR 226 decision 10 twice: the timeout
   and the source of the answer.
3. **First-party or community.** RFC 51 asks whether these adapters live
   in this repository. The product manager proposes the two adapters in
   this repository, with fakes of each API in the conformance suite. A
   test against a real instance needs a developer instance of each system
   and a credential for CI. Who owns those accounts?
4. **Private change systems.** Can a ticket connection name a private
   address, with an allowlist for each installation? An operator of the
   installation sets the allowlist, and a namespace never does, because a
   hosted installation has other tenants (ADR 215 and 220). Or does
   version 1 refuse them?
5. **Slice order.** Does the core part (the fact group, the ticket rule,
   the outbox cursor and the port with its suite) land before the first
   adapter, or with it?
6. **The other proposals.** Do you accept the other proposals of the
   product manager (Q3 to Q8) as written, or do you want any of them as a
   separate question?

## Outcome

None yet.
