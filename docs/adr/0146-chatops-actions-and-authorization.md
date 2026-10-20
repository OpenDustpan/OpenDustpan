# 146. ChatOps: three safe actions, linked identities and approval with a fresh authentication

Status: accepted · 2026-10-18. Decisions 9 and 13 superseded in part by ADR 149. Route paths and `server:` action names superseded in part by ADR 210. Decision 11: an action button shows only in a chat sink that posts as the installation's chat app (RFC 98)

## Context

On 17 October 2026 the project owner decided that chat offers only safe
actions: acknowledge, open, and approve. An approval from chat follows
the fresh-authentication rule that the console uses for a confirmation.

[ADR 17](0017-api-authorization-and-policy-for-every-feature.md) says that
every feature has a REST API, that every endpoint declares its actions and
the object of each action, and that every change passes the policy check.
[ADR 103](0103-job-gates-actions-and-audit.md) decision 2 makes a
confirmation need a fresh authentication of at most 15 minutes
(`job.ConfirmFreshness`): a new MFA step, or a new authentication at the
SSO identity provider with a maximum age (`job.ConfirmAuth`). A verdict on
an approval gate (`job.GateApproval`) in the console needs no fresh
authentication, and `job.Service.Approve` records none. ADR 123 decision 7
and ADR 125 decision 5 accept such a fresh-authentication step only from a
console session, never from an access token, so that an agent that holds
the token of its person cannot pass it.

A chat tool tells the server which of its users clicked a button. It cannot
tell the server which principal that user is, and it cannot give a fresh
MFA step. A message in a channel is visible to every member of the
channel. A person who gets a link from somebody else can be tricked into
a click on it.

## Decision

### The actions

1. **Three actions, and no more.** `chatops.Decide` holds the list, and a
   table-driven test covers it:

   | Action | Target | Outcome | Needs |
   | --- | --- | --- | --- |
   | `open` | any | an answer with the console link of the target | nothing: the console checks access |
   | `acknowledge` | an incident | the person takes the incident, and its escalation stops | a link, and `namespace:respond-incidents` |
   | `approve` | a job that waits on an approval gate (`job.GateApproval`), with the plan binding that the message showed | a link, visible to the person only, to a fresh authentication | a link, and `job:approve` |

   Any other action, or an action on a target that it does not fit, is
   `ErrUnsupported`. A confirmation gate (`job.GateConfirmation`) is not
   offered from chat: the person opens the console. Chat never starts,
   cancels, applies or breaks a freeze. It never records a break-glass
   verdict.
2. **No rejection from chat.** A rejection is safe, because it only stops a
   change. But the owner listed three actions, and this ADR keeps to them.
   An open question asks for the owner's answer.

### Linked identities

3. **A chat user maps to a principal through a link request.**
   A link holds the chat tool, the workspace of the tool, the user ID of
   the tool, and the principal. The chat tool alone can never claim a
   principal. The flow:
   - The chat user asks the chat tool for a link. The server makes a
     one-time token from at least 32 random bytes and stores a
     `chatops.LinkRequest`: the SHA-256 of the token and never the token
     itself, the chat user (`chatops.ChatUser`), and an expiry 5 minutes
     later (`chatops.LinkRequestLifetime`). The chat tool shows the URL
     with the token to that chat user only. The token is in the fragment
     of the URL, so that no access log holds it.
   - The person opens the URL in the console and signs in. The accept
     page names the chat tool, the workspace and the chat user of the
     request, shows the principal that is signed in and that the link
     will name, says that each action of that chat user will act as
     that principal, and shows the principal of the current link of
     that chat user, if any. So a person whom an attacker signed in to
     the attacker's own account (login cross-site request forgery) sees
     the wrong principal before the accept.
   - The person accepts with a `POST` of the session that carries the
     token of the session against cross-site requests. A link of the
     request never comes from the `POST`: the server takes the chat user
     from the stored request (`LinkRequest.Link`).
   - The server checks the request with `LinkRequest.Check` (`ErrUsed`,
     `ErrExpired`, `ErrWorkspace`), marks its use with
     `LinkRequests.UseLinkRequest`, and stores the link with
     `Links.PutLink`. The use comes first, so one token never makes two
     links.
   - The server sends a direct message, through the chat tool, to the
     chat user of each new link. The message names the principal of the
     link and tells the chat user how to delete it. So a chat user whose link names a
     principal that the chat user does not know sees it at once.
4. **A link names one workspace, and the IDs are exact.** A user ID of
   another workspace of the same tool does not match. Two IDs that differ
   only in case are two users. A link of a disabled principal does not
   work. A break-glass account (ADR 70) cannot make a link.
5. **An allowlist of workspaces.** The installation lists the workspaces
   that it accepts for each chat tool (`chatops.Workspaces`). An empty
   list accepts none. `Decide` refuses every request from another
   workspace with `ErrWorkspace`, and so do `NewLinkRequest` and
   `LinkRequest.Check` in the link flow.
6. **A link never replaces another link silently.** `Links.PutLink` takes
   the principal of the current link that the accept page showed, or
   empty when it showed none. It returns `ErrConflict` when the stored
   link differs from it, so a person replaces a link only after the page
   named the principal that loses it. Each new link sends a notification
   (ADR 145, package `notify`) to the principal of the new link, and a
   replaced link also sends one to the principal that lost it. The
   notification names the chat tool, the workspace and the chat user, and
   the console page where the person deletes a link.

   When somebody tricks a person into an accept of the link request of
   another chat user, the person gets the notification at once. Until the person deletes the link,
   the other chat user can acknowledge as that person, and it cannot
   approve without the person's fresh authentication (decision 9).

   The server deletes the intents of a chat user with no use when it
   deletes the link of that chat user: `Intents.IntentsOf` finds them,
   and `Intents.DeleteIntent` deletes each one with the fence of the
   namespace of its job. Until then `chatops.CheckIntent` refuses them
   (decision 9).
7. **The chat tool signs its requests.** The `chatops.Verifier` port
   checks the signature of each request with the signing secret of the
   installation for that tool. An adapter for each tool implements it:
   - it checks the signature before it parses the body, and compares it
     in constant time (`hmac.Equal`),
   - the signature covers the timestamp and the body. A timestamp older
     than 5 minutes (`chatops.SignatureMaxAge`) or more than 1 minute in
     the future (`chatops.SignatureMaxSkew`) fails
     (`chatops.CheckTimestamp`),
   - a request with a missing header or a signature that does not match
     is `ErrSignature`, also when its body does not parse, and the server
     answers 401. A signed body that does not parse is `ErrInvalid`,
   - the adapter sets the chat tool of the request (`Request.Platform`)
     to its own tool and ignores a tool that the body names. So a
     request signed for one tool never acts for the link of a user of
     another tool,
   - the request carries the ID that the chat tool gives it
     (`Request.DeliveryID`). A repeated request is safe: an acknowledge
     changes nothing the second time, and an approve makes a new intent
     that only a new fresh authentication of the linked person completes.
     The used intent of the first request does not change. A case of the
     suite `chatopstest.Run` covers this.

### Approval from chat

8. **Chat alone never completes an approval.** An approve button makes an
   intent (`chatops.Intent`): the principal, the namespace of the job, and
   the target: the job, the index of the phase, the type of the gate, and
   the plan binding that the message showed (`job.PlanBinding`, ADR 102
   decision 7). `Intents.CreateIntent` stores it with the fence of the
   namespace of the job and appends its audit entry in the same
   transaction. The server answers a link that only the person sees. The
   person opens it in the browser, in a console session, and
   authenticates again: a new MFA step, or a new authentication at the
   identity provider with a maximum age, as ADR 103 decision 2 says for a
   confirmation. Then the server records the approval with
   `job.Service.Approve`, with the plan binding of the intent.
9. **The rules of an intent.** `chatops.CheckIntent` holds them, and a
   table-driven test covers them:
   - the server uses the intent at most once (`ErrUsed`),
   - it expires after 5 minutes (`ErrExpired`),
   - the request comes from a console session, never with an access
     token (`ErrNeedsSession`). The server fills `chatops.Auth` from the
     console session of the request: the session, the token, the
     principal, the agent, the break-glass flag, and the kind and time of
     the last fresh authentication. No client gives them,
   - only the principal of the intent can use it, and no agent and no
     break-glass session can (`ErrDenied`),
   - the chat user of the intent is still linked to the principal of the
     intent (`ErrNotLinked`): the server reads the link with
     `Links.LinkOf` before the check, so a deleted or replaced link stops
     the intent,
   - the job still waits on the same phase and the same gate
     (`ErrGateChanged`), so an intent never approves a later gate of the
     same job, and the plans did not change (`job.ErrPlanChanged`),
   - the authentication is after the button and at most 15 minutes old
     (`ErrAuthStale`). An authentication from before the button never
     counts.

   `Intents.UseIntent` marks the use in one transaction before the
   approval. So a second request can never approve twice.
10. **Every rule of the job model applies.** The approval goes through
    `job.Service.Approve`, as a request of the console does. The rules of
    the gate apply: the starter rule, the plan binding (`ErrPlanChanged`),
    the age of the plan, and the policy check. The audit entry of the
    approval holds the kind and the time of the fresh authentication.
    `job.Approval` holds them only for a confirmation today, so the
    approval record of a chat approval does not hold them (see "Requests
    to other designs").

### Messages

11. **Buttons grant nothing.** A chat message shows two kinds of button:
    - an action button (`acknowledge`, `approve`). The chat tool sends its
      click to the action route, signed with its signing secret (decision
      7). The button holds the target and no secret. An `approve` button
      shows only on a job that waits on an approval gate, and only in a
      chat sink of the namespace of the job,
    - an `open` button: a plain URL of a console page. It holds no
      signature and no token, and it grants nothing: the console checks
      the session and the access of the person.
12. **A message shows no secret.** A chat message shows the summary, the
    links and the buttons of its notification (ADR 145 decision 2). It
    never shows a plan, a log or a value. A member of the channel with no
    access sees what the notification says, and nothing more after a
    click.

### API, actions and audit

13. **Routes.**

    | Route | Action | Object |
    | --- | --- | --- |
    | `POST /api/chat/{tool}/actions` | public, with the signature of the tool | none |
    | `POST /api/chat/link-requests/show` (the token in the body, for the accept page) | `caller:read` | caller |
    | `POST /api/chat/links` (accept a link request from a session) | `caller:manage-chat-links` | caller |
    | `GET /api/me/chat-links` | `caller:read` | caller |
    | `DELETE /api/me/chat-links/{platform}/{workspace}/{user}` | `caller:manage-chat-links` | caller |
    | `DELETE /api/principals/{id}/chat-links/{platform}/{workspace}/{user}` | `server:manage-principals` | principal |
    | `GET /api/chat/intents/{id}` and `POST .../complete` | `job:approve` | unit |

    The action route is on the public list of the route test, because the
    chat tool sends it with no session. The signature of the tool takes
    the place of a session. The route then checks the action of the linked
    principal on the target, as `Decide` names it.
    `caller:manage-chat-links` and the complete route of an intent accept
    a console session only, never an access token.
14. **One new action.** `caller:manage-chat-links` lets a person make,
    replace and delete the chat links of its own principal. Its object is
    the caller. It is session-only. With it, ADR 140 to ADR 147 add 14
    actions (ADR 147 decision 17).
15. **Audit entries.** A request from chat writes its entry with the new
    channel `chat`:

    | Event | Subject | Action | Kind |
    | --- | --- | --- | --- |
    | Link made, replaced or deleted | the person, or the administrator | `caller:manage-chat-links` or `server:manage-principals` | `change` |
    | Acknowledge | the linked principal | `namespace:respond-incidents` | `change` |
    | Intent made, in the transaction of `CreateIntent` | the linked principal | `job:approve` | `change` |
    | Approval | the linked principal | `job:approve` | `change` |
    | Refused signed request (no link, no action, a workspace not on the allowlist) | the chat user, as text | the action asked for | `change`, with the outcome `denied` |

    The entry holds the tool, the workspace and the user ID of the chat
    tool, and never the signing secret or a token. A request with no
    valid signature writes no audit entry, because anybody can send one.
    The server counts it in a metric for each tool instead.
16. **Ports with conformance first.** `chatops.Links`,
    `chatops.LinkRequests` and `chatops.Intents` get the suite
    `pkg/chatops/chatopstest`, with a reference adapter in memory. Links
    and link requests are in the server scope: their writes take the zero
    fence and refuse any other fence with `ErrInvalid`. An intent is in
    the namespace of its job: its writes take the fence of that namespace
    and check it in their transaction. `CreateIntent` returns
    `ErrConflict` for an ID that exists and never replaces an intent. A
    write refuses an audit entry of another scope than its row.
    `UseLinkRequest` and `UseIntent` are single-use under concurrent
    requests: of many requests at the same time, exactly one succeeds.
    `chatops.Verifier` gets the suite `chatopstest.RunVerifier`, which a
    fake adapter with HMAC-SHA256 runs in the tests of the suite: a
    signature with another secret, a changed body, a missing header, a
    timestamp too old or in the future, an old signature with a new
    timestamp, a body that names another chat tool, a signed body that
    does not parse, and an unsigned body that does not parse.

## Requests to other designs

- **Audit.** `audit.Channel` gets the value `chat`.
- **Identity.** The action list gets `caller:manage-chat-links`
  (session-only, object the caller).
- **Job model (ADR 103).** `job.Service.Approve` records no fresh
  authentication for a verdict on an approval gate. A chat approval
  always has one. This ADR asks that `Approve` record `Actor.ConfirmAuth`
  and `Actor.AuthAt` in `Approval.Auth` and `Approval.AuthAt` when the
  server set them for the request, so that the approval record shows the
  fresh authentication. Until then, only the audit entry of the approval
  shows it.
- **Notifications (ADR 145).** The catalog gets the types "chat link
  made" and "chat link replaced", sent to the principals of decision 6.
- **Metrics.** A counter of the requests with no valid signature, for
  each chat tool.

## Consequences

- A person approves from chat in two clicks and one authentication, and
  never with less proof than for a confirmation in the console.
- Each chat tool needs two adapters: a `Verifier` for its requests, and a
  `Sender` for its messages (ADR 145).
- A team whose identity provider cannot ask for a maximum age, and that has
  no built-in MFA, cannot approve from chat. It opens the console instead.
- An installation accepts no chat request until an administrator lists
  the workspaces of each chat tool.
- `GLOSSARY.md` needs the terms Chat link, Link request and Chat action.

## Open questions

- Can a person reject from chat? A rejection only stops a change. The owner
  listed three actions, so this ADR leaves it out.
- Which chat tools get adapters first?
