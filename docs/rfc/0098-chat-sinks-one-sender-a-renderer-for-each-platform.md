---
rfc: 98
title: Chat sinks, with one sender and a renderer for each chat platform
status: discussing
created: 2026-10-20
---

# RFC 98. Chat sinks, with one sender and a renderer for each chat platform

Status: discussing. The RFC decides nothing, except the order of the
platforms, which ADR 262 records. The only code is the port `chat.Renderer` and its
conformance suite in `pkg/notify/chat`. The decisions here become ADRs when
the project owner accepts them (ADR 15).

## Problem

On 18 October 2026 the project owner decided that OpenDustpan gets chat
adapters for Slack, Microsoft Teams, Mattermost and Matrix, one pull request
for each platform (ADR 262).

[ADR 145](../adr/0145-one-event-model-and-sinks.md) decides the notification
model. A chat sink holds the incoming webhook URL of a chat platform as a
secret item. Its `Target` holds only the host of the URL. The sender posts
only to a URL with that host (decisions 13 and 20).
[ADR 146](../adr/0146-chatops-actions-and-authorization.md) decides the chat
actions.

On main today:

- the ports `notify.Sender`, `notify.Sink` and `notify.Message`, with the
  suite `notifytest.RunSender`,
- the sealing of sink secrets,
- one sender: the signed webhook (`pkg/notify/webhook`).

Not on main: SQL stores for `Outbox`, `Subscriptions` and `Deliveries`, the
dispatcher, the delivery operation, the routes of sinks and subscriptions,
and a domain that appends a notification. So no sink can receive a
notification today, and the webhook sender has no caller outside its tests.

## Facts from primary sources

- An incoming webhook of Slack always belongs to a Slack app. It can carry
  interactive components. But the server cannot prove that a webhook URL
  belongs to the Slack app of the installation (ADR 146 decision 7). So a
  click on a button of a webhook message cannot be trusted as a request of
  the installation's app.
- A message that a webhook posted cannot be deleted or edited through the
  webhook.
- GovSlack uses the domain `slack-gov.com`, not `slack.com`.

The adapter pull requests check these facts again, and the block limits, the
host list and the error texts of each platform, against the documentation of
the platform.

## Proposal

### Platforms, order and names

1. **The order.** Slack, Microsoft Teams, Mattermost, Matrix. One pull
   request for each platform. ADR 262 records this. Each one ships its
   renderer, its conformance run, its user documentation page and an
   end-to-end step.
2. **One sink kind and a platform.** The kind stays `chat`. A chat sink gets
   the field `Platform`: `slack`, `teams`, `mattermost` or `matrix`
   (`chat.Platform`, the JSON field `platform`). A chat sink needs it. Any
   other kind leaves it empty. A change of the platform is a new
   sink. The host does not name the platform, because Mattermost and Matrix
   run on hosts that the operator chooses. `GLOSSARY.md` has the term Chat
   platform, and `chatops.Request.Platform` takes the type later. A chat
   service is a platform, never a "tool" (a tool is an infrastructure-as-code
   CLI).
3. **User-facing words.** A sink that posts to a webhook is a "Slack
   channel" or a "Slack webhook" in copy (the same pattern for the other
   platforms). "Slack connection" stays the word for the later connection to a
   Slack app, that is, credentials with a life cycle. The API and the code
   say "sink" with the kind `chat` and the platform.
4. **Two forms of one kind.** The chat sink stays one kind. Today the
   only form is the webhook: `SecretRefs` holds the URL. A later form adds
   a field `Connection`, a reference to an installation-level Slack app
   connection (bot token and signing secret, sealed as in ADR 213), and a
   channel ID. In the later form the sink posts as the app. `SecretRefs`
   holds the URL only in the webhook form.

### One sender and a renderer for each platform

5. **One sender for the kind.** `chat.Sender` is the one `notify.Sender` of
   the kind `chat`. It is the only code that opens a connection for a chat
   message. It does these steps, in this order, and none of them is optional
   for a platform:
   - it refuses a message of another kind or with an unknown platform
     (`ErrPermanent`),
   - it checks that `notify.ChatHost` of the URL is the target of the sink,
     and that the renderer accepts the host (`Renderer.CheckHost`),
   - it builds the `chat.Content` of the message (decision 7),
   - it builds the URL from the URL of the sink and the `PathSuffix` of the
     request, and it adds any authentication header from a named secret
     item. A renderer never sees a secret and never sets a header,
   - it posts the request of `Renderer.Render` with
     `egresshttp.Client(egress.NoRedirect, egress.HTTPSOnly)`, with no user
     information and no redirect. The limit of 10 seconds is a context
     deadline on the request, on top of the timeout of the client,
   - it reads at most `egress.MaxBodyBytes` of the answer,
   - it sorts the status:
     - 2xx goes to `Renderer.Check`,
     - 408, 429 and 5xx are `ErrTemporary`, with `notify.RetryAfterError` when
       the answer has a `Retry-After` header,
     - 3xx and any other 4xx are `ErrPermanent`,
     - a refused address is `egress.ErrRefused`.

   The sender makes one attempt. The delivery operation retries, up to 15
   attempts (ADR 145 decision 18).
6. **A renderer is pure.** `chat.Renderer` does no input or output. It
   gives the host rule of its platform (`CheckHost`), the request for a
   `chat.Content` (`Render`), and the reading of a 2xx answer (`Check`). A
   `chat.Request` has a `Method`, a relative `PathSuffix`, an
   `IdempotencyKey` (the notification ID) and the content type and body. The
   key is for a platform that deduplicates, for example the transaction ID
   of Matrix, so a retry does not post twice. A platform that answers 200
   with an error text in the body is `ErrPermanent` or `ErrTemporary` from
   `Check`. An error text never holds the URL, the network error, the body of
   the answer or a secret.
7. **One content for every platform.** `chat.Content` is the message before
   the format of a platform: the notification ID, the product name of the
   installation (ADR 235), a title and a summary, the severity, up to 10
   fields and the links of the notification. The text is in the language of
   the installation (decision 21). A unit appears as fields with its tool,
   its namespace and the name that the tool uses. Copy keeps each tool's own
   words, "stack" or "workspace" (ADR 251). A `Content` never holds a plan, a
   log, a value or a secret (ADR 145 decision 4 and ADR 146 decision 12).
8. **Text comes from outside, and the renderer encodes it.** A name of a unit or
   a namespace comes from a person. `Content.Validate` refuses a text that is
   not valid UTF-8 and a text with a control character other than a line
   feed or with a bidirectional control character (for example U+202E). A
   renderer then encodes every other string for its platform. A mention of
   the whole channel, a mention of a person or a group, a markup or a link
   markup in a name or a summary must do nothing. The renderer does not
   reject such a string: it encodes it ("encode where you use it", ADR 250).
   `Render` returns an error only for a `Content` that `Validate` refuses.
9. **Size limits are met by cutting.** A platform can have a limit that is
   lower than `chat.MaxRequestBytes` (about 28 KB for Teams). The renderer
   cuts a text on a character boundary, and never inside an escape
   sequence. `Render` returns nil for the largest valid `Content`.
10. **Links.** A renderer puts a URL only in a field that the platform takes
    as a URL, for example the `url` of a button. Where it must put a URL in
    markup text, it percent-encodes the characters `|`, `<` and `>`, so a
    URL cannot end a link label or start a mention.
11. **The conformance suite.** `chattest.RunRenderer` has a case for each of
    these decisions. A harness gives the decoder of its platform
    (`Harness.Decode`: the texts that the platform parses as markup, after
    the decoding of the wire format), the markup sequences, the hostile
    strings, the limit of the body (`Harness.MaxBody`) and whether the
    platform has one host (`Harness.SingleHost`). A check on the bytes of a
    JSON body is not enough: the encoder of JSON writes `<` as an escape, and
    a search for `<!channel>` in the bytes always fails to find it. So every
    hostile check runs on the decoded texts. A platform with one public host
    must refuse that host with a port, for example `:8443`.

### Slack, the first platform

12. **The webhook.** The adapter posts one JSON document to the incoming
    webhook URL of Slack. The host rule is `hooks.slack.com` and the hosts
    of GovSlack under `slack-gov.com`, and no other host. The text with a
    fallback line shows in a notification on a phone. The blocks show the
    product name and the title, the summary, the fields, the severity, and a
    link button for each link of the notification.
13. **No action button from a webhook.** A webhook sink offers no action
    button, because the server cannot prove that the webhook belongs to the
    installation's Slack app (ADR 146 decision 7). Action buttons need a sink
    that posts as that app (`chat.postMessage` with the token of the app),
    which the inbound slice adds. `Message.Actions` stays empty for a webhook
    sink. An `open` button is a plain URL and stays allowed (ADR 146
    decision 11). A follow-up note on ADR 146 records this.
14. **The answer.** Slack answers a message that it took with the status
    200 and the text `ok`. It answers a message that it refused with a 4xx
    and a short text. The sort of decision 5 applies, and `Check` reads the
    2xx body.
15. **No delete.** A webhook message cannot be deleted. The documentation
    page says so, and a test message from the test route says that it is a
    test.

### The secret

16. **The URL is a secret item.** A person gives the URL once, in the body
    of the create or of a change of the secret. The server seals it (ADR 23),
    and no route answers it again. A read of a sink shows the kind, the
    platform, the name and the host, and whether a URL exists. A new URL is a new
    secret item under a new reference (ADR 145 decision 13). The URL is not in
    a log line, an audit entry, a metric, a delivery row, an error text or a
    test report. The test route never echoes the URL or the body of the
    answer: it returns the class of the result and a fixed text. The
    conformance run of each adapter puts a canary in the URL and in the
    answer body, and fails when the canary shows in any of these.

### Routes, actions and audit

17. **The routes of ADR 145 decision 27 are the routes.** A chat sink is a
    sink: `POST`, `PUT` and `DELETE` need `namespace:manage-notifications`,
    reads need `namespace:read`, and the test route sends a fixed message
    through the same sender. The create body of a chat sink holds `platform`,
    `name` and `url`. Each write carries the fence of the scope and writes
    one audit entry without the URL (ADR 145 decision 28).

### Proposed answers to the open questions

18. **Private hosts: an installation-level egress allowlist.** The egress
    rules refuse a private address, so a Mattermost or Matrix server inside
    the network of the operator is out of reach. Proposed: an allowlist in
    `pkg/egress`. It is tool-neutral and applies to chat sinks and webhook
    sinks. Only the operator sets it (flag, environment variable, Helm
    value), never a namespace administrator, and it is empty by default.
    - An entry is an exact host and a required list of CIDRs.
    - The dial check passes a private address only when the host is on the
      list and the resolved IP address is in the CIDRs of the entry. The check
      runs at each dial, against the resolved address, so a DNS rebinding
      cannot move a host to another address.
    - Loopback, link-local (169.254.0.0/16), `fd00:ec2::254` and the
      unspecified addresses stay refused always.
    - A create or a test of a sink on an allowlisted private host writes the
      host, never the URL, to the audit entry. Startup logs the list.
    - Other projects do the same: Gitea `ALLOWED_HOST_LIST`, Mattermost
      `AllowedUntrustedInternalConnections`, the local-network setting of
      GitLab, and `data_source_proxy_whitelist` of Grafana. All are for
      administrators only, and refuse by default.
19. **Teams: Workflows webhooks.** The adapter supports the webhook URLs of
    Workflows (Power Automate) with an Adaptive Card payload. It does not
    support the Office 365 connectors.
20. **Matrix: the client-server API.** The adapter posts to the client-server
    API with an access token as a second secret item (the sender adds it as a
    header, decision 5). The transaction ID is the notification ID. There is
    no bridge.
21. **Language: the language of the installation.** A chat message uses the
    language of the installation (ADR 234). A locale for each sink can come
    later, as an additive field.

### Order of the work

22. **The parts.** A person sees a chat message only after these parts exist
    on main:
    - A1 (#1171): the store of sinks in both dialects, with the `Platform`
      column from the start, the sealer, the sink routes, the test route, and
      the webhook sender wired in,
    - A2 (#1172): the outbox, the deliveries, the dispatcher, the delivery
      operation, and one domain that appends a notification,
    - B (#1173): `chat.Sender`, the Slack renderer, the user documentation and
      an end-to-end step with a local receiver. B needs A1 and does not need
      A2,
    - the egress allowlist (#1174),
    - C1 (#1175) Teams, C2 (#1176) Mattermost and C3 (#1177) Matrix,
    - the inbound Slack app (#1178): the app connection, a sink that posts as
      the app, the `chatops.Verifier`, the link flow, and the actions
      `acknowledge` and `approve` (ADR 146). Chat never approves by itself
      (ADR 146 decision 8).

## Consequences

- A new chat platform needs one renderer and one conformance run. It needs no
  new network code and cannot skip an egress layer.
- `notify.Sink` gets the field `Platform`, and the stores of sinks get a
  column. That change belongs to A1, with the migration, and not to the
  design pull request.
- A Slack channel through an incoming webhook cannot hold a button that acts.
  A team that wants `acknowledge` from chat waits for the inbound slice.
- ADR 146 decision 11 says that an `approve` button shows in a chat sink of
  the namespace of the job. It holds for a sink that posts as the Slack app.
  A sink that is a webhook shows link buttons only.

## Open questions

- Does the owner accept decisions 18 to 21 as written? Each accepted
  decision becomes an ADR.
- Which hosts does GovSlack use for its webhooks? The Slack pull request
  checks this against the documentation of Slack.

## Outcome

Empty until the owner accepts or drops the RFC. ADR 262 records the order
of the platforms.
