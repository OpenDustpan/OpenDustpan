---
rfc: 127
title: Chat connections, channels per unit, and an operator route for chat traffic
status: discussing
created: 2026-11-07
---

# RFC 127. Chat connections, channels per unit, and an operator route for chat traffic

Status: discussing. Design only. Nothing in this RFC is decided, and no code
exists for it. The owner answers the questions at the end (ADR 15). Until the
owner decides, ADR 145, ADR 146, ADR 262 and ADR 334 stay as they are. This
RFC refines decision 4 of RFC 98 (the "later form" of a chat sink, which
posts as an app with a channel ID).

The text uses the glossary words: **notification**, **notification sink**
(copy says "notification destination"), **chat sink**, **chat platform**,
**subscription**, **delivery**, **label**, **collection**, **upstream
proxy**. It proposes three new terms: **chat connection**, **channel
binding** and **channel rule** (see "Glossary changes").

## Summary

A platform team wants to set the chat credentials once, and let each unit
owner pick a channel with no secret. Today one chat sink holds one incoming
webhook URL, and one webhook URL posts to one channel. So each channel needs
its own credential, and each change of a channel needs the person who holds
the credential.

This RFC proposes four additions. None of them changes the behavior of a chat
sink that exists today.

1. **A chat connection**: the credential of a chat platform, set once at the
   installation or in a namespace. For Slack, it is the bot token of a Slack
   app, which posts with `chat.postMessage` to a channel that the request
   names.
2. **A channel binding**: a channel (an ID, no secret) on a stack or
   workspace, on a collection or on a namespace, with a connection and a
   filter. A **channel rule** of a namespace binds a channel to the units
   whose labels match. A binding needs its own, smaller actions.
3. **Rich messages**: a notification of an operation carries the unit, the
   tool, the operation, the result, the actor and the links as named
   attributes, so a receiver needs no lookup.
4. **An operator route for chat traffic**: an upstream proxy for chat
   traffic and a gateway base URL for each chat platform, set as installation
   settings. The host checks of ADR 334 then accept the host that the
   operator configured, at save time and at send time, as two separate
   layers.

The recommendation is all four, in that order of delivery, with Slack first.

## Problem

### What the server does today

- A chat sink belongs to a namespace and holds one incoming webhook URL as a
  secret item (ADR 145 decision 13). A read shows only the host of the URL.
  See `docs/guides/notification-destinations.md` and
  `docs/reference/notification-destinations-api.md`.
- A Slack chat sink accepts only the hosts `hooks.slack.com` and
  `hooks.slack-gov.com`. Any other host answers 400 `sink.platform-host` at
  save time (`sinks.Service.checkPlatformHost`), and the chat sender checks
  the host again before each send (`slack.Renderer.CheckHost`). See
  `docs/guides/slack-notifications.md`.
- ADR 334 decision 2: the server checks the host against the strict host list
  when a person saves the sink, and the sender checks it again at send time.
  The two checks are separate layers. ADR 334 decision 1 removes the legacy
  host path.
- The outbound rules (`pkg/egress`, applied by
  `internal/egress/egresshttp`) refuse loopback, private, link-local and
  reserved addresses, at save time and on the resolved address of each
  connection. The port must be 443 unless the operator allows another with
  `--destination-ports`. No setting opens a private range for a sink.
  `--git-host-allowed-ranges` applies to Git host connections only.
- The HTTP client of the senders sets `Proxy: nil`. So the chat traffic
  never goes through a proxy, and the environment variables `HTTPS_PROXY` and
  `NO_PROXY` have no effect on it.
- A subscription routes the notifications of a namespace that match its
  filter to one sink (ADR 145 decision 14). A create or a change of a sink or
  a subscription needs `namespace:manage-notifications`.
- The test route sends one test message. The guides say that the server does
  not yet send notifications of events to a destination by itself.

### The need

A platform team described this need:

1. **One central connection.** The platform team sets the chat credentials
   once, at the installation or in a namespace. For Slack, that is an app with
   a bot token (`chat.postMessage` takes a channel), or an incoming webhook.
2. **A channel per unit, with no secret.** A unit owner sets only a channel
   (an ID or a name): on a stack or a workspace, on a collection, or by a rule
   on unit labels. A change of a channel needs no credential, and needs its
   own, smaller permission.
3. **Rich messages.** The notification carries the operation details (unit,
   tool, operation, result, actor, links), so a receiver needs no lookup in
   the API.
4. **An operator route for air-gapped installations.** An HTTP proxy, a
   Slack-API-compatible gateway base URL, or both, set as an installation
   setting. The host check then accepts the host that the operator
   configured, not only the built-in hosts of the vendor. The two-layer check
   of ADR 334 and the rules of ADR 250 stay intact. Only an administrator can
   set the configured host, with an audit entry and a fresh MFA step.

A constraint: the status quo stays. A chat sink with one webhook URL for one
channel keeps working with no change, for the teams that it suits. The new
model is additive.

### Why the webhook model does not scale for this need

With the model of today, a namespace with 40 units that post to 12 channels
needs 12 incoming webhooks, 12 sealed URLs and 12 sinks. Each webhook is a
credential that the person who made it must paste. A unit owner who wants
another channel asks an administrator, because a sink and a subscription need
`namespace:manage-notifications`. A Slack incoming webhook cannot post to
another channel (see "Facts from primary sources"), so no field of the sink
can fix this.

An air-gapped installation cannot reach `hooks.slack.com` directly, and it
cannot send through a proxy, because the client sets no proxy. It also
cannot reach a gateway on a private address, because the outbound rules
refuse private ranges for sinks.

## Facts from primary sources

Each fact names its source. The pages were read on 2026-11-07. Where a page
did not say a thing in words, the fact says so.

### Slack

- An incoming webhook posts to one channel. "You cannot override the default
  channel (chosen by the user who installed your app), username, or icon when
  you're using incoming webhooks to post messages." The URL is "specific to a
  single user and a single channel." The host is `hooks.slack.com`.
  Source: <https://docs.slack.dev/messaging/sending-messages-using-incoming-webhooks>
- `chat.postMessage` takes the argument `channel`: "An encoded ID or channel
  name that represents a channel, private group, or IM channel to send the
  message to." Bot tokens and user tokens need the scope `chat:write`. The
  method is `POST https://slack.com/api/chat.postMessage`. "New Slack apps do
  not begin life with the ability to post in all public channels. For your
  new Slack app to gain the ability to post in all public channels, request
  the `chat:write.public` scope." The error `not_in_channel` exists.
  Source: <https://docs.slack.dev/reference/methods/chat.postMessage>
- GovSlack: "use `slack-gov.com` when calling any API endpoint instead of
  `slack.com` when using Slack Web APIs." The page does not give the full
  base URL of the Web API in words.
  Source: <https://docs.slack.dev/govslack>

So for Slack the credential and the channel are separate only with a token.
With an incoming webhook, the credential is the channel.

### Mattermost

- An incoming webhook takes a `channel` field: "Overrides the channel the
  message posts in. Use the channel's name and not the display name." It
  "Defaults to the channel set during webhook creation." The webhook "can
  post to any Public channel and Private channel the webhook creator is in."
  Source: <https://docs.mattermost.com/developers/integrate/webhooks/incoming>
- The lock. "You can optionally limit bot posts to a specific channel by
  selecting **Lock to this channel**, or you can allow the webhook to post to
  any public channel or private channel the incoming webhook creator is a
  member of. Note that administrators can enforce channel locking for all
  incoming webhooks through **System Console > Integrations > Integration
  Management**."
  Source: <https://docs.mattermost.com/integrations-guide/incoming-webhooks.html>
- In the server code, the model `IncomingWebhook` has the field
  `ChannelLocked bool` with the JSON name `channel_locked`, and the request
  field `channel` is `ChannelName`.
  Source: <https://github.com/mattermost/mattermost/blob/master/server/public/model/incoming_webhook.go>
- What we could verify about the locked behavior, and what we could not:
  - Verified in the documentation: the creator can lock a webhook to its
    channel, and an administrator can enforce the lock for all incoming
    webhooks.
  - Read only through a summary of the server code, not word for word: a
    locked webhook refuses a payload that names another channel, with the
    error ID `web.incoming_webhook.channel_locked.app_error` and the status
    403 (`HandleIncomingWebhook` in
    <https://github.com/mattermost/mattermost/blob/master/server/channels/app/webhook.go>).
  - Not found: the name of the configuration key that enforces the lock. The
    part of `model/config.go` that we could read has
    `EnableIncomingWebhooks`, `EnablePostUsernameOverride` and
    `EnablePostIconOverride`, and no lock key.
  - The design below does not depend on the unverified parts. It treats any
    refusal of a channel override as a permanent failure of the delivery, and
    the test route shows it.
- The REST API has "Create a post" ("Create a new post in a channel"). The
  caller "Must have `create_post` permission for the channel". Bot accounts
  and personal access tokens exist. The page did not show the body schema in
  our read, so the name of the channel field is not verified here.
  Source: <https://docs.mattermost.com/api/reference/create-post>

### Microsoft Teams

- Office 365 Connectors, with the connector-based incoming webhooks, "were
  retired in May 2026. To receive webhook requests and post messages in
  Teams, use the Workflows app."
  Source: <https://learn.microsoft.com/en-us/microsoftteams/platform/webhooks-and-connectors/how-to/add-incoming-webhook>
- "A workflow webhook is configured for a specific channel or chat." The
  person who makes it chooses "the Team and Channel, or Chat that this
  webhook notification will post to." No channel parameter in the request is
  documented. Source: the same page, and
  <https://support.microsoft.com/en-us/office/create-incoming-webhooks-with-workflows-for-microsoft-teams-8ae491c7-0394-4861-ba59-055e33f75498>
- Microsoft Graph `POST /teams/{team-id}/channels/{channel-id}/messages`:
  the delegated permission is `ChannelMessage.Send`. "Application permissions
  are only supported for migration."
  Source: <https://learn.microsoft.com/en-us/graph/api/chatmessage-post>
- A bot can send a proactive message to a channel only after "the app that
  contains your agent" is installed in that team, and it needs a stored
  conversation ID for each channel.
  Source: <https://learn.microsoft.com/en-us/microsoftteams/platform/bots/how-to/conversations/send-proactive-messages>

So for Teams, no credential that a server holds alone can post to a channel
that the request names, except a bot app that a person installs in each team.

### Matrix

- `PUT /_matrix/client/v3/rooms/{roomId}/send/{eventType}/{txnId}`: the room
  is a path parameter, and the access token belongs to an account. An account
  that cannot send to the room gets 403 `M_FORBIDDEN`.
  Source: <https://spec.matrix.org/latest/client-server-api/#put_matrixclientv3roomsroomidsendeventtypetxnid>

A Matrix chat sink today holds the room URL and an access token. The token
already is a connection, and the room already is a channel.

### Other infrastructure tools

| Tool | Where the credential is | Where the channel is | Source |
| --- | --- | --- | --- |
| Pulumi Cloud | Each webhook holds its own URL. A webhook is on an organization or on a stack, and an ESC webhook on an environment. The Slack format takes a Slack incoming webhook URL. | The webhook URL | <https://www.pulumi.com/docs/pulumi-cloud/webhooks/>, <https://www.pulumi.com/docs/esc/concepts/webhooks/> |
| HCP Terraform | Each notification configuration holds its URL. "Each workspace has its own notification settings and can notify up to 20 destinations." Destination types: `generic`, `email`, `slack`, `microsoft-teams`. Team notifications exist too. | The webhook URL | <https://developer.hashicorp.com/terraform/cloud-docs/workspaces/settings/notifications>, <https://developer.hashicorp.com/terraform/cloud-docs/api-docs/notification-configurations> |
| Atlantis | One bot token for the server: `--slack-token` or `ATLANTIS_SLACK_TOKEN`. | Each webhook entry names `channel`, with `workspace-regex` or `branch-regex` filters. | <https://www.runatlantis.io/docs/sending-notifications-via-webhooks> |
| Spacelift | The account connects to the Slack workspace once, on the page _Integrations > Slack_, with OAuth2. | A notification policy chooses the channel (`channel_id`). The page says that the setup is "on a per-stack basis with notification policies". Our read of the policy page was partial. | <https://docs.spacelift.io/integrations/chatops/slack>, <https://docs.spacelift.io/concepts/policy/notification-policy> |
| env zero | Notification targets are made in the organization settings. The console flow uses its Slack app; the API takes an incoming webhook URL. | A project enables the targets that it uses. | <https://docs.envzero.com/docs/notifications> |
| Argo CD notifications | One token in `argocd-notifications-secret`, read by `service.slack` in `argocd-notifications-cm`. The Slack service has the option `apiURL`, "The server URL". No proxy option shows in its table. | Each Application names a channel in an annotation, for example `notifications.argoproj.io/subscribe.on-sync-succeeded.slack: my_channel`. | <https://argo-cd.readthedocs.io/en/stable/operator-manual/notifications/services/slack/>, <https://argo-cd.readthedocs.io/en/stable/operator-manual/notifications/> |

Two patterns exist. In the first, each destination holds a URL that is its
own credential (Pulumi Cloud, HCP Terraform, today's OpenDustpan). In the
second, one credential is set once, and each object or rule names only a
channel (Atlantis, Spacelift, Argo CD). The need above asks for the second
pattern next to the first. Argo CD also shows a base URL option for the
Slack API, which is the gateway half of the operator route.

## What is decided already

- One notification model with sinks and subscriptions (ADR 145). A
  notification holds no secret (decision 4). The creator of a subscription
  decides what flows out, and the dispatcher checks the creator before each
  delivery (decision 15).
- The order of the chat platforms: Slack, Microsoft Teams, Mattermost,
  Matrix (ADR 262).
- An action button shows only in a chat sink that posts as the installation's
  chat app (ADR 146 decision 11, as changed by RFC 98).
- The strict host list applies at save time and at send time, as separate
  layers. No legacy host path (ADR 334).
- Defense in depth for security code (ADR 250).
- A change of a setting through the API that makes the installation less
  secure needs a fresh MFA step. A value from a flag, an environment variable
  or the configuration file needs no step (ADR 212 decision 3). Each change
  of a setting writes an audit entry, and a sensitive setting shows only as
  `set` or `not set` (ADR 63 decision 6).
- The names "upstream proxy" and "outbound guard" (RFC 16, ADR 327).

## Proposal

### P1. The chat connection

A **chat connection** holds the credential of one chat platform, and no
channel. It belongs to a scope: the installation (`@server`) or one
namespace.

| Field | Content |
| --- | --- |
| `id` | `ccn-...` |
| `scope` | `@server` or the namespace |
| `platform` | `slack`, `mattermost` or `matrix` (see P8 for Teams) |
| `mode` | `token` (the platform's post API with a token) or `webhook` (an incoming webhook that takes a channel field: Mattermost only) |
| `name` | 1 to 100 bytes |
| `target` | The host that the connection posts to, for example `slack.com`. Never a path, a query or a token. |
| `tokenSet` | `true` when the sealed credential exists |
| `channels` | An optional allowlist of channel IDs. Empty means "the channels that the platform lets the credential post to". The API shows the effective value. |
| `namespaces` | Only for `@server`: the namespaces that can use it, or `*`. The default is no namespace. |
| `createdBy`, `version` | As for a sink |

- The credential is a secret item of the scope (ADR 23), as for a sink:
  write-only, sealed, and in no answer, audit entry or log. A Slack
  connection in the mode `token` holds a bot token. A Mattermost connection
  holds a bot token or an incoming webhook URL. A Matrix connection holds the
  homeserver host and an access token.
- A Slack incoming webhook is not a connection mode, because it cannot post
  to another channel. A Slack webhook stays a chat sink of today.
- The test route of a connection checks the credential without a post to a
  channel where the platform has such a call (Slack `auth.test`). It has the
  limits and the audit entries of the test route of a sink.
- An installation connection is offered to a namespace only when the
  namespace is in `namespaces`. So an administrator of the installation
  decides which namespaces can post as the installation's app. This is a
  stored setting, and a read shows it.

### P2. The channel binding

A **channel binding** sends the notifications of one object to one channel
through one connection. It holds no secret.

| Field | Content |
| --- | --- |
| `id` | `chb-...` |
| `object` | A unit reference with the stable unit ID set, a collection, or the namespace |
| `connection` | The ID of a chat connection that the namespace can use |
| `channel` | The channel, in the form of the platform (P4) |
| `filter.types`, `filter.minSeverity` | As in a subscription. No object filter: the object is the binding's own object. |
| `createdBy`, `version` | As in a subscription |

- **Which notifications.** A binding on a unit gets the notifications whose
  object is that unit. A binding on a collection gets the notifications of
  the units in the collection at the time of the route. A binding on the
  namespace gets the notifications of the namespace.
- **Most specific wins.** For one notification of a unit, the dispatcher
  uses the unit binding when one exists, else the collection binding, else
  the namespace binding. The read API shows the effective binding of a unit,
  with its origin (`unit`, `collection` or `namespace`), as the unit
  settings API shows an effective value. Owner question Q3 asks about this
  rule.
- **The creator rule of ADR 145 decision 15 applies.** Before each delivery,
  the dispatcher checks that the creator of the binding still holds the
  action that sets it (P5), can read the notification, and can still use the
  connection. When a check fails, the dispatcher suspends the binding and
  sends `subscription.suspended` to the administrators, as for a
  subscription.
- **A delivery records its origin.** Each delivery names the binding or the
  rule that made it, so a person can ask why a message went to a channel and
  get the name of the binding, its creator and its time.

In the code, a binding can be a `notify.Subscription` whose destination is a
connection and a channel, not a sink. That keeps one delivery path, one
history and one redeliver route. The API keeps the binding as its own
resource, because its object, its actions and its creator rule differ from
those of a subscription.

### P3. The channel rule

A **channel rule** of a namespace binds a channel to each unit whose labels
match a selector.

- A selector is a list of `key=value` pairs. A unit matches when it has
  every pair. A selector is never a regular expression.
- The rules of a namespace are a list. Each rule that matches makes a
  delivery. Two rules, or a rule and a binding, that name the same
  connection and the same channel make one delivery for one notification.
- A rule adds to the bindings of P2. It does not replace them. So a unit
  owner keeps the own channel, and the platform team adds a channel for all
  units with `team=payments`, for example.
- A rule needs `namespace:manage-notifications`, because it reaches units
  that the creator does not own. The creator rule applies to each delivery,
  so a rule never sends a notification of a unit that its creator cannot
  read.

The label of a unit is not a grant (GLOSSARY "Label"). A rule on labels
routes messages. It never gives access to the notification: the creator rule
decides that.

### P4. The channel

- **Slack.** A channel ID, for example `C0123ABCDEF`. The check at the
  boundary accepts only the ID form: an upper-case letter `C`, `G` or `D`,
  then 8 to 20 upper-case letters and digits. A name such as `#ops` is
  refused, because a rename in Slack would move the messages without a
  change in OpenDustpan. Owner question Q4.
- **Mattermost.** In the mode `token`, a channel ID. In the mode `webhook`,
  the channel name that the `channel` field of the webhook takes, with the
  characters that Mattermost allows in a channel name.
- **Matrix.** A room ID, `!<opaque>:<server>`. An alias is refused for the
  same reason as a Slack name.
- The channel is not a secret, and a read shows it.
- When the connection has a `channels` allowlist, a channel outside it
  answers 400 at save time, and the sender refuses it at send time with the
  class `refused`. The two checks are separate layers.

### P5. Actions

| Action | Object | Allows |
| --- | --- | --- |
| `server:manage-chat-connections` | the server | Create, change, delete and test an installation connection, and set its `namespaces` |
| `namespace:manage-chat-connections` | the namespace | The same for a connection of the namespace |
| `namespace:use-chat-connection` | the namespace | Name a connection in a binding of this namespace |
| `unit:set-chat-channel` | the unit | Create, change and delete the binding of the unit |
| `collection:set-chat-channel` | the collection | The same for a collection |
| `namespace:manage-notifications` | the namespace | The binding of the namespace, and the channel rules (no change) |

- A binding needs two actions: the action on its object, and
  `namespace:use-chat-connection`. This mirrors
  `namespace:use-git-host-connection`.
- A binding needs no action that reads or changes a credential. So a unit
  owner sets a channel without ever holding a token.
- Owner question Q5 asks which built-in roles hold the new actions.

### P6. Rich messages

A notification already has the object, its stable ID, the subject, the agent,
a summary, up to 10 links and up to 20 string attributes (ADR 145 decision
1). The proposal names the attributes, so that every receiver finds the same
key for the same fact.

| Attribute | Content | Example |
| --- | --- | --- |
| `tool` | The tool name (GLOSSARY "Tool name") | `OpenTofu` |
| `unit` | The name of the stack or the workspace | `payments-prod` |
| `collection` | The collection of the unit | `payments` |
| `operation` | The operation of the job, or the tool's own word for an operation that no job ran | `apply` |
| `result` | The terminal state | `failed` |
| `actor` | The display name of the subject | `ana` |
| `trigger` | What started the job | `review-request` |
| `durationSeconds` | The time from start to end | `412` |
| `resourceChanges` | `+<add> ~<change> -<delete>` | `+2 ~1 -0` |
| `reviewRequest` | The review request, when one started the job | `#418` |

- The links: the console page of the job, the console page of the unit, the
  runbook (ADR 145 decision 6), and the review request when one exists.
- No secret, no output value, no plan text and no log line (ADR 145 decision
  4). A resource count is a fact, not a value.
- The chat renderers show these attributes as fields, and the signed webhook
  JSON carries them in `attributes`. The JSON schema does not change: the
  attributes are strings, as today.
- Today the catalog has job types only. An operation that a person runs with
  the CLI, outside a job, has no notification type. The proposal adds
  `unit.operation-succeeded` and `unit.operation-failed`, with the tool's own
  word in `operation` (for example `up` for the Pulumi CLI). Owner question
  Q7.

### P7. The operator route for chat traffic

Two installation settings. Each one is optional, and each one is off by
default.

| Setting | Content | Example |
| --- | --- | --- |
| `chat-upstream-proxy` | The URL of an HTTP proxy that takes `CONNECT`, for all chat traffic | `http://proxy.corp.example:3128` |
| `chat-gateway-<platform>` | The base URL of a gateway that speaks the API of the platform, for example `chat-gateway-slack` | `https://slack-relay.corp.example/api/` |

**The upstream proxy.**

- The sender opens a `CONNECT` tunnel to the target host through the proxy.
  TLS stays end to end between the server and the target host, so the
  server checks the certificate of the target host, not of the proxy.
- The host check of ADR 334 applies to the target host of the tunnel, at
  save time and at send time, as today. The proxy does not change which
  hosts a sink or a connection can name.
- The outbound guard cannot check the resolved address of the target,
  because the proxy resolves it. The guard checks the address of the proxy
  on each connection instead, and the proxy applies the rules of the
  team's network. The documentation says so. This is the same split as for
  the upstream proxy of the job proxy (ADR 327).
- The proxy URL takes no user information. A proxy credential, when one is
  needed, is a separate sensitive setting, so a log line and an audit entry
  show it only as `set` or `not set`.
- The proxy is on a private address in most networks. The proxy address is
  an allowlist entry of its own: the outbound guard accepts that one address
  for the proxy connection, and still refuses loopback, link-local, cloud
  metadata, unspecified and multicast addresses, as `--git-host-allowed-ranges`
  does.

**The gateway base URL.**

- With `chat-gateway-slack` set, a Slack connection posts to
  `<base>/chat.postMessage`, and a Slack chat sink accepts a webhook URL on
  the host of the gateway. The base URL is `https`, with no user
  information, no query and no fragment.
- **The configured host joins the host list.** The platform host rule of
  Slack accepts `hooks.slack.com`, `hooks.slack-gov.com`, the Web API hosts
  `slack.com` and `slack-gov.com`, and the host of `chat-gateway-slack` when
  it is set. The match is exact, as today. The save check
  (`sinks.Service.checkPlatformHost`) and the send check
  (`Renderer.CheckHost`) each read the effective setting on their own. Neither
  check relies on the other (ADR 334 decision 2).
- A gateway on a private address needs its address in an allowed range for
  chat traffic, the same kind of entry as for the proxy.
- When the operator removes the gateway setting, a sink or a connection on
  the gateway host fails both checks: a delivery ends with the class
  `refused`, and a change needs a host that passes. This is the consequence
  of ADR 334 for a removed legacy host, applied to a removed gateway.

**Who can set them.**

- The static layer (a flag, an environment variable or the configuration
  file) needs no MFA step, as ADR 212 decision 3 says for every setting.
- Through the settings resource, a set or a widening of either setting is a
  change that reduces security. It needs `server:manage-settings` and a fresh
  MFA step (ADR 212 decision 3). An access token gets 403
  `mfa.fresh-step-required`.
- Each change writes an audit entry with the old and the new host. The proxy
  credential shows only as `set` or `not set`.
- A namespace cannot set either setting. A namespace administrator never
  widens the host list.

**Tests for each layer (ADR 250).** Each layer gets a test that sends
hostile input to that layer alone: a gateway URL with user information, a
query, a port or an `http` scheme at the setting boundary; a sink URL on a
host that is close to the gateway host (a sub-domain, a different case, a
trailing dot) at the save check and at the send check, each with the other
check removed; a proxy that answers a `CONNECT` with a redirect; a proxy
address in the link-local range; a channel ID with markup at the channel
check and at the renderer.

### P8. Platforms

| Platform | Connection modes | Reason |
| --- | --- | --- |
| Slack | `token` | A Slack incoming webhook cannot post to another channel. |
| Mattermost | `token`, `webhook` | A Mattermost incoming webhook takes `channel`, unless it is locked to its channel or an administrator enforces the lock. A refusal is a permanent failure that the test route shows. |
| Matrix | `token` | The access token posts to each room that its account can send to. |
| Microsoft Teams | none in this RFC | A Workflows webhook is bound to its channel. A Graph application permission posts only for migration. A bot needs a person to install it in each team. Teams keeps the chat sink of today, one Workflows webhook for each channel. |

The order follows ADR 262: Slack first, then Mattermost, then Matrix. Teams
gets a connection only when a later RFC designs the bot path.

### P9. What stays the same

- Every route, field, error code and audit entry of the sinks API stays. A
  chat sink with a webhook URL keeps its host rule, its sealed URL and its
  test route.
- A subscription keeps its filter and its creator rule.
- A Slack webhook sink still offers link buttons only. A message that a
  connection in the mode `token` posts can later carry the action buttons of
  ADR 146, because it posts as the installation's app. That is outside this
  RFC.
- The outbound rules, the port rule (`--destination-ports`) and the timeout
  stay. The upstream proxy and the gateway add entries. They remove no check.

## Options

### For the central connection

- **A. A connection object, with a channel per binding (proposed).** One
  credential, many channels. A unit owner never sees a secret. It is a new
  resource and a new sender path for each platform.
  Consequence: new code for the Web API of each platform, and a new
  test route.
- **B. A chat sink with a channel override field.** Works for Mattermost
  only. A Slack webhook refuses an override, so it does not meet the need
  for Slack.
- **C. Keep one webhook for each channel, and let a unit owner pick among
  sinks.** No new credential type. Each new channel still needs a person
  with the credential, so it does not meet need 2.

### For the channel of a unit

- **A. A binding resource with its own actions (proposed).** The smallest
  permission that meets need 2. One more resource to list and audit.
- **B. A field in the context of the object (ADR 160).** The context API and
  the reserved output already exist, and `unit:set-context` exists. But the
  reserved output comes from the code of a unit, so a program could move its
  messages to any channel that the connection reaches. Context is data for
  people and agents, not a route. Owner question Q6.
- **C. A field in the unit settings.** The unit settings hold what a job
  reads. A channel is not read by a job, and the namespace defaults hold the
  runner pool only.

### For the operator route

- **A. An upstream proxy and a gateway base URL, as installation settings
  (proposed).** Covers both kinds of air-gapped network. Two settings to
  document and test.
- **B. The proxy only.** A network with no route to Slack at all needs a
  gateway, so the proxy alone does not meet need 4 for that network.
- **C. The gateway only.** A network with a proxy and no gateway must deploy
  a gateway to post to Slack.
- **D. Honor `HTTPS_PROXY` from the environment.** No new setting. But it
  is an implicit route: a variable that a person set for another program
  would move the chat traffic with no record. That breaks "No magic".

## Changes to ADRs, if the owner accepts

- **ADR 145.**
  - Decision 13 changes: a chat sink keeps the webhook form. A chat
    connection is a new object, and its secret is a secret item of its
    scope. A channel binding and a channel rule hold no secret.
  - Decision 14 extends: a binding and a rule route notifications next to
    subscriptions.
  - Decision 15 extends: the creator rule applies to a binding and a rule,
    with `namespace:use-chat-connection`.
  - Decision 20 changes: a sender can post through the upstream proxy of
    chat traffic. The outbound guard then checks the proxy address on each
    connection, and the proxy resolves the target.
  - Decisions 5 and 6 extend: the named attributes of P6, and the types
    `unit.operation-succeeded` and `unit.operation-failed`.
- **ADR 262.** Extends decision 1: the connection modes of each platform, in
  the same order. Teams gets no connection mode in this RFC.
- **ADR 334.** Extends decision 2: the host list that both checks read is
  the built-in list of the platform plus the host of the gateway setting of
  that platform. The tunnel target of the upstream proxy goes through both
  checks too. Each check reads the setting on its own. Decision 1 stays: no
  second, weaker host path.
- **ADR 146.** No change now. Decision 11 later applies to a message that a
  Slack connection posts as the app.
- **ADR 212.** Extends decision 3: the list of changes that reduce security
  gets "set or widen `chat-upstream-proxy` or a `chat-gateway-<platform>`".

## Glossary changes

- **Chat connection**: The credential of one chat platform, at the
  installation or in a namespace, with no channel. A channel binding names
  it. _Avoid_: chat integration, app (alone). _Copy says_: "Slack
  connection", and the same pattern for the other platforms. This keeps the
  meaning that the entry "Chat sink" already reserves for "Slack
  connection".
- **Channel binding**: A channel on a unit, a collection or a namespace,
  with a connection and a filter. It holds no secret. _Avoid_: channel
  subscription, chat route.
- **Channel rule**: A rule of a namespace that binds a channel to each unit
  whose labels match a selector. _Avoid_: routing rule.
- **Upstream proxy**: extend the entry: it can also carry the chat traffic
  of the server, when the operator sets `chat-upstream-proxy`.

## Delivery plan

Each step is one pull request with its user documentation page (ADR 214)
and its end-to-end step against a local fake of the platform.

1. The named attributes of P6 and the two operation types. No new resource.
2. The chat connection for Slack in the mode `token`, at the namespace, with
   its test route.
3. The channel binding on a unit, a collection and the namespace, with the
   new actions.
4. Installation connections and `namespaces`.
5. The channel rule.
6. The operator route: the upstream proxy, then the gateway base URL.
7. Mattermost, then Matrix.

## Open questions for the owner

Each question stands alone.

### Q1. Add the chat connection next to the webhook sink?

Context: a Slack incoming webhook posts to one channel only, so one
credential means one channel. A platform team wants one credential and many
channels. Option A adds a connection object (a bot token) and a channel per
object. Option B adds a channel field to the chat sink, which Slack refuses.
Option C keeps today's model.

Recommendation: A. The webhook sink stays as it is.

Summary: one Slack app token for many channels, next to the webhooks of
today.

### Q2. Where can a connection live?

Context: a connection can belong to the installation, to a namespace, or to
both. An installation connection lets a platform team connect Slack once for
every namespace. A namespace connection lets one team use its own Slack
workspace.

Options: (a) both, with an installation list of the namespaces that can use
an installation connection; (b) the installation only; (c) the namespace
only.

Recommendation: (a). The installation list starts empty, so no namespace
posts as the installation's app until an administrator adds it.

Summary: both levels, and an administrator opts each namespace in.

### Q3. Which binding wins for a unit?

Context: a unit can have a binding, its collection can have one, and the
namespace can have one. Channel rules on labels can match too.

Options: (a) the most specific object binding wins (unit, then collection,
then namespace), and channel rules add to it; (b) every binding and rule
delivers, with one message for each distinct channel; (c) only the unit
binding counts.

Recommendation: (a). The read API shows the effective binding and its
origin, and each delivery names the binding or the rule that made it.

Summary: the unit's own channel replaces the collection's channel; label
rules always add.

### Q4. Channel IDs only, or names too?

Context: Slack `chat.postMessage` takes an ID or a name. A name follows a
rename in Slack, so a rename can move messages with no change in
OpenDustpan and no audit entry. An ID does not move.

Options: (a) IDs only for Slack and Matrix, names only where the platform
needs them (a Mattermost webhook); (b) IDs and names.

Recommendation: (a).

Summary: store the channel ID, so that a rename in Slack never moves the
messages.

### Q5. Which built-in roles hold the new actions?

Context: P5 adds `server:manage-chat-connections`,
`namespace:manage-chat-connections`, `namespace:use-chat-connection`,
`unit:set-chat-channel` and `collection:set-chat-channel`. A unit owner must
set a channel with no credential and no `namespace:manage-notifications`.

Recommendation: the admin roles hold all of them. `maintainer` holds
`namespace:use-chat-connection`, `unit:set-chat-channel` and
`collection:set-chat-channel`. `writer` holds `unit:set-chat-channel` and
`namespace:use-chat-connection`. No role below `writer` holds them.

Summary: the people who maintain a unit set its channel; only
administrators handle credentials.

### Q6. Can the code of a unit set its channel?

Context: the reserved output `opendustpan` sets the context of a unit from
its code (ADR 160). A channel from the output would let the program of a
unit move its messages to any channel that the connection reaches, with no
grant.

Options: (a) no, the channel comes from the API only; (b) yes, through a new
context field, limited to the `channels` allowlist of the connection.

Recommendation: (a) for now.

Summary: a person sets the channel through the API; the code of a unit does
not.

### Q7. Notifications for operations outside a job?

Context: the catalog has job types only. A `pulumi up` or a `tofu apply`
that a person runs with the CLI makes no notification today. Need 3 asks for
the operation details in each message.

Options: (a) add `unit.operation-succeeded` and `unit.operation-failed`,
with the tool's own word in the attribute `operation`; (b) one type for each
tool's operation; (c) jobs only.

Recommendation: (a).

Summary: a message for every finished operation on a unit, from a job or
from the CLI.

### Q8. The operator route: proxy, gateway, or both?

Context: an air-gapped installation cannot reach Slack today, because the
chat sender uses no proxy and the outbound rules refuse private addresses.
Some networks have an HTTP proxy. Others have no route to Slack, only a
gateway inside the network that speaks the Slack API.

Options: (a) both, as installation settings; (b) the proxy only; (c) the
gateway only; (d) the environment variable `HTTPS_PROXY`.

Recommendation: (a). A set or a widening through the API needs
`server:manage-settings` and a fresh MFA step, and writes an audit entry
with the old and the new host. Both host checks of ADR 334 read the setting,
each on its own.

Summary: the operator names one proxy and one gateway host, and both checks
then accept that one host and no other.

### Q9. Teams in this RFC or later?

Context: a Teams Workflows webhook is bound to its channel, a Graph
application permission posts only for migration, and a bot needs a person to
install it in each team.

Options: (a) Teams keeps the webhook sink of today, and a later RFC designs
a bot connection; (b) design the bot connection here.

Recommendation: (a).

Summary: Teams keeps one webhook for each channel for now.
