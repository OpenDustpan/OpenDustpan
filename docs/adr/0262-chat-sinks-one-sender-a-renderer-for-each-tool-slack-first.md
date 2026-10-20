# 262. Chat sinks: one sender, a renderer for each tool, and Slack first

Status: proposed · 2026-10-20 · answers the open question on the first chat tools in ADR 145 and ADR 146. Waits for the acceptance of the project owner

## Context

On 18 October 2026 the project owner decided that OpenDustpan gets chat
adapters for Slack, Microsoft Teams, Mattermost and Matrix. The adapters
ship in that order, with one pull request for each tool. The product name of
the operator reaches chat messages, and chat text is localized (ADR 234).

[ADR 145](0145-one-event-model-and-sinks.md) decides the notification model.
A chat sink (`notify.SinkChat`) holds the incoming webhook URL of a chat tool
as a secret item. Its `Target` holds only the host of the URL. The sender
posts only to a URL whose host is that target (decisions 13 and 20). ADR 145
leaves the first chat tools open. [ADR 146](0146-chatops-actions-and-authorization.md)
decides the chat actions and leaves the same question open.

What exists on main today:

- the ports `notify.Sender`, `notify.Sink` and `notify.Message`, with the
  suite `notifytest.RunSender`,
- the sealing of sink secrets,
- one sender: the signed webhook (`pkg/notify/webhook`).

What does not exist on main: SQL stores for `Outbox`, `Subscriptions` and
`Deliveries`, the dispatcher and the delivery operation, the routes of
sinks and subscriptions, and a domain that appends a notification. So no
sink can receive a notification today. The webhook sender has no caller
outside its tests.

An incoming webhook of a chat tool is a URL that accepts a message. It has
no identity for the person who reads the message, and in Slack it cannot
call back to the server with a signed request. A button that calls back
needs an app of the chat tool (ADR 146 decision 7).

## Decision

### Tools, order and names

1. **The order.** Slack first, then Microsoft Teams, Mattermost and Matrix.
   One pull request for each tool. Each one ships its renderer, its
   conformance run, its user documentation page (ADR 214) and an end-to-end
   step. This answers the open question of ADR 145 and ADR 146.
2. **One sink kind and a tool.** The kind stays `chat`. A chat sink gets
   the field `Tool`: `slack`, `teams`, `mattermost` or `matrix`
   (`chat.Tool`). It is required for a chat sink, empty for any other kind,
   and set when the sink is created. A change of the tool is a new sink.
   The host does not name the tool, because Mattermost and Matrix run on
   hosts that the operator chooses.
3. **User-facing words.** Copy names the service first: "Slack
   connection", "Microsoft Teams connection", "Mattermost connection" and
   "Matrix connection". The API and the code say "sink" with the kind
   `chat` and the tool. `GLOSSARY.md` gets the term Chat sink.

### One sender and a renderer for each tool

4. **One sender for the kind.** `chat.Sender` is the one `notify.Sender`
   of the kind `chat`. It is the only code that opens a connection for a
   chat message. It does these steps, in this order, and none of them is
   optional for a tool:
   - it refuses a message of another kind or with an unknown tool
     (`ErrPermanent`),
   - it checks that `notify.ChatHost` of the URL is the target of the sink,
     and that the renderer of the tool accepts the host
     (`Renderer.CheckHost`),
   - it builds the `chat.Content` of the message (decision 6),
   - it posts the request of `Renderer.Render` with
     `egresshttp.Client(egress.NoRedirect, egress.HTTPSOnly)`, with a
     timeout of 10 seconds, with no user information and no redirect,
   - it reads at most `egress.MaxBodyBytes` of the answer,
   - it sorts the status:
     - 2xx goes to `Renderer.Check`,
     - 408, 429 and 5xx are `ErrTemporary`, with `notify.RetryAfterError` when
       the answer has a `Retry-After` header,
     - 3xx and any other 4xx are `ErrPermanent`,
     - a refused address is `egress.ErrRefused`.

   The sender makes one attempt. The delivery operation retries, up to 15
   attempts (ADR 145 decision 18).
5. **A renderer is pure.** `chat.Renderer` does no input or output. It
   gives the host rule of its tool (`CheckHost`), the request body for a
   `chat.Content` (`Render`), and the reading of a 2xx answer (`Check`).
   A tool that answers 200 with an error text in the body is `ErrPermanent`
   or `ErrTemporary` from `Check`. An error text never holds the URL, the
   network error, the body of the answer or a secret.
6. **One content for every tool.** `chat.Content` is the message before the
   format of a tool: the product name of the installation (ADR 235), a title
   and a summary in the language of the server (ADR 234), the severity, up
   to 10 fields and the links of the notification. A unit appears as fields
   with its tool, its namespace and the name that the tool uses. Copy keeps
   each tool's own words, "stack" or "workspace" (ADR 251). A `Content`
   never holds a plan, a log, a value or a secret (ADR 145 decision 4
   and ADR 146 decision 12).
7. **Text is untrusted.** A name of a unit or a namespace comes from a
   person. Every renderer encodes each string for its tool. A mention of the
   whole channel, a mention of a person or a group, a markup, a link
   markup or a control character in a name or a summary must do nothing.
   Each renderer carries its list of hostile strings in its conformance run.

### Slack, the first tool

8. **The webhook.** The adapter posts one JSON document to the incoming
   webhook URL of Slack. The host rule is `hooks.slack.com` and no other
   host. A text with a fallback line shows in a notification on a phone.
   The blocks show the product name and the title, the summary, the fields,
   the severity, and a link button for each link of the notification. The
   adapter PR checks the block limits, the host list (including the
   government host of Slack) and the error texts of Slack against the
   documentation of Slack.
9. **No action buttons from a webhook.** An incoming webhook shows link
   buttons only. An `open` button is a plain URL (ADR 146 decision 11). The
   `acknowledge` and `approve` buttons need the signed requests of a Slack
   app (ADR 146 decision 7). So a webhook sink offers no action button, and
   `Message.Actions` stays empty for it. Action buttons come with the inbound
   slice (decision 14).
10. **The answer.** Slack answers a message that it took with the status 200
    and the text `ok`. It answers a message that it refused with a 4xx and a
    short text. The sort of decision 4 applies, and `Check` reads the 2xx
    body.

### The secret

11. **The URL is a secret item.** A person gives the URL once, in the body
    of the create or of a change of the secret. The server seals it (ADR 23),
    and no route answers it again. A read of a sink shows the kind, the tool,
    the name and the host, and that a URL is set. A new URL is a new secret
    item under a new reference (ADR 145 decision 13). The URL is not in a
    log line, an audit entry, a metric, a delivery row, an error text or a
    test report. The conformance run of each adapter puts a canary in the URL
    and in the answer body, and fails when the canary shows in any of these.

### Routes, actions and audit

12. **The routes of ADR 145 decision 27 are the routes.** A chat sink is a
    sink: `POST`, `PUT` and `DELETE` need `namespace:manage-notifications`,
    reads need `namespace:read`, and the test route sends a fixed message
    through the same sender. The create body of a chat sink holds `tool`,
    `name` and `url`. Each write carries the fence of the scope and writes
    one audit entry without the URL (ADR 145 decision 28).

### Order of the work

13. **What blocks a message that a person sees.** A person sees a chat
    message only after these parts exist on main. The first is not specific
    to chat:
    - A. the stores of `Outbox`, `Subscriptions` and `Deliveries` in both
      dialects, the dispatcher, the delivery operation, the routes of sinks
      and subscriptions, the sealer for sink secrets, and at least one
      domain that appends a notification,
    - B. `chat.Sender` and the Slack renderer, with the user documentation
      page and an end-to-end step with a local receiver,
    - C. the renderers of Microsoft Teams, Mattermost and Matrix.

    B can land before A, with tests against a local server, but then no
    person can use it.
14. **Inbound is a later slice.** The Slack app, its `chatops.Verifier`, the
    link flow and the actions `acknowledge` and `approve` follow ADR 146.
    Chat never approves by itself (ADR 146 decision 8).

## Consequences

- A new chat tool needs one renderer and one conformance run. It needs no
  new network code and cannot skip an egress layer.
- `notify.Sink` gets the field `Tool`, and the stores of sinks get a column.
  That change belongs to part A or B, with the migration, and not to the
  design pull request.
- A Slack connection through an incoming webhook cannot hold a button that
  acts. A team that wants `acknowledge` from chat waits for the inbound
  slice.
- The renderer of a tool decides what its tool shows. The sender decides
  where a message goes and what the server logs.
- ADR 146 decision 11 says that an `approve` button shows in a chat sink of
  the namespace of the job. It holds for a sink that has a Slack app. A
  sink that is a webhook only shows link buttons. A follow-up note on ADR 146
  records this.

## Open questions

- The egress rules refuse a private address. A Mattermost or Matrix server
  that runs inside the network of the operator is then out of reach. Does
  the installation get an allowlist of private hosts for chat sinks, set by
  the operator and not by a namespace administrator?
- Microsoft has changed how Teams takes webhook messages, and a Teams
  webhook may need a different payload from the one for Slack. The Teams pull
  request decides the supported URL forms.
- Matrix has no incoming webhook in the protocol. The Matrix pull request
  decides whether the sink posts to the client-server API with an access
  token (a secret item) or to a bridge.
- Does a sink choose the language of its messages, or does the language of
  the installation always apply (ADR 234)?
