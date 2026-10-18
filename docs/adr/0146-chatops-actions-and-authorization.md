# 146. ChatOps: three safe actions, linked identities and approval with a fresh authentication

Status: accepted · 2026-10-18

## Context

On 17 October 2026 the project owner decided that chat actions are limited
to safe ones: acknowledge, open, and approve. An approval from chat follows
the same fresh-authentication rule as the console.

[ADR 17](0017-api-authorization-and-policy-for-every-feature.md) says that
every feature has a REST API, that every endpoint declares its actions and
the object of each action, and that every change passes the policy check.
[ADR 103](0103-job-gates-actions-and-audit.md) decision 2 makes a
confirmation need a fresh authentication of at most 15 minutes: a new MFA
step, or a new authentication at the SSO identity provider with a maximum
age.

A chat tool tells the server which of its users clicked a button. It cannot
tell the server which principal that user is, and it cannot give a fresh
MFA step. A message in a channel is visible to every member of the
channel.

## Decision

### The actions

1. **Three actions, and no more.** `chatops.Decide` holds the list, and a
   table-driven test covers it:

   | Action | Target | Outcome | Needs |
   | --- | --- | --- | --- |
   | `open` | any | an answer with the console link of the target | nothing: the console checks access |
   | `acknowledge` | an incident | the person takes the incident, and its escalation stops | a link, and `incident:respond` |
   | `approve` | a job that waits on an approval gate | a link, visible to the person only, to a fresh authentication | a link, and `job:approve` |

   Any other action, or an action on a target that it does not fit, is
   `ErrUnsupported`. Chat never starts, cancels, applies or breaks a freeze.
   It never records a break-glass verdict.
2. **No rejection from chat.** A rejection is safe, because it only stops a
   change. But the owner listed three actions, and this ADR keeps to them.
   An open question asks for the owner's answer.

### Linked identities

3. **A chat user maps to a principal through a link.** A link holds the
   chat tool, the workspace of the tool, the user ID of the tool, and the
   principal. The person makes it from a session of the console: the chat
   tool shows a one-time link, the person signs in, and the server stores
   the link. The chat tool alone can never claim a principal.
4. **A link names one workspace.** A user ID of another workspace of the
   same tool does not match. A link of a principal that is disabled does
   not work.
5. **Requests from the chat tool are signed.** The `chatops.Verifier` port
   checks the signature of each request with the signing secret of the
   installation for that tool, and refuses a timestamp outside the
   tolerance of the tool. An adapter for each tool implements it. A request
   with no valid signature is `ErrSignature`, and the server answers 401.

### Approval from chat

6. **Chat alone never completes an approval.** An approve button makes an
   intent: the principal, the job, the gate, and the binding of the plans
   that the message showed (ADR 102 decision 7). The server answers a link
   that only the person sees. The person opens it in the browser and
   authenticates again: a new MFA step, or a new authentication at the
   identity provider with a maximum age, as ADR 103 decision 2 says. Then
   the server records the approval with `job.Service.Approve`.
7. **The rules of an intent.** `chatops.CheckIntent` holds them, and a
   table-driven test covers them:
   - the intent is used at most once (`ErrIntentUsed`),
   - it expires after 5 minutes (`ErrIntentExpired`),
   - only the principal of the intent can use it, and no agent can
     (`ErrDenied`),
   - the authentication is after the button and at most 15 minutes old
     (`ErrAuthStale`). An authentication from before the button never
     counts.

   `Intents.UseIntent` marks the use in one transaction before the
   approval. So a second request can never approve twice.
8. **Every rule of the job model applies.** The approval goes through
   `job.Service.Approve`, as a request of the console does. The rules of
   the gate apply: the starter rule, the plan binding (`ErrPlanChanged`),
   the age of the plan, and the policy check. The approval records the
   kind and the time of the fresh authentication.

### Messages

9. **Buttons are signed URLs.** A chat message shows each action as a
   button with a URL of the server that holds the target and a signature.
   An `open` button opens the console. An `approve` button shows only on a
   job that waits on an approval gate, and only in a chat sink of the
   namespace of the job.
10. **A message shows no secret.** A chat message shows the summary, the
    links and the buttons of its event (ADR 145 decision 2). It never shows
    a plan, a log or a value. A member of the channel with no access sees
    what the event says, and nothing more after a click.

### API, actions and audit

11. **Routes.**

    | Route | Action | Object |
    | --- | --- | --- |
    | `POST /api/chat/{tool}/actions` | public, with the signature of the tool | none |
    | `POST /api/chat/links` (finish a link from a session) | `caller:read` | caller |
    | `GET`, `DELETE /api/me/chat-links[/{id}]` | `caller:read` | caller |
    | `DELETE /api/principals/{id}/chat-links/{link}` | `server:manage-principals` | principal |
    | `GET /api/chat/intents/{id}` and `POST .../complete` | `job:approve` | unit |

    The action route is on the public list of the route test, because the
    chat tool sends it with no session. The signature of the tool takes
    the place of a session. The route then checks the action of the linked
    principal on the target, as `Decide` names it.
12. **Audit entries.** A request from chat writes its entry with the new
    channel `chat`:

    | Event | Subject | Action | Kind |
    | --- | --- | --- | --- |
    | Link made or deleted | the person, or the administrator | `caller:read` or `server:manage-principals` | `change` |
    | Acknowledge | the linked principal | `incident:respond` | `change` |
    | Intent made | the linked principal | `job:approve` | `change` |
    | Approval | the linked principal | `job:approve` | `change` |
    | Refused request (no link, no action, no signature) | the chat user, as text | the action asked for | `change`, with the outcome `denied` |

    The entry holds the tool, the workspace and the user ID of the chat
    tool, and never the signing secret or a token.
13. **Ports with conformance first.** `chatops.Links` and `chatops.Intents`
    get the suite `pkg/chatops/chatopstest`, with a reference adapter in
    memory. `chatops.Verifier` gets its suite with the first adapter of a
    chat tool.

## Requests to other designs

- **Audit.** `audit.Channel` gets the value `chat`.
- **Job model (ADR 103).** No change. Chat calls `job.Service.Approve` with
  an actor whose authentication is the fresh one of decision 6.

## Consequences

- A person approves from chat in two clicks and one authentication, and
  never with less proof than in the console.
- Each chat tool needs two adapters: a `Verifier` for its requests, and a
  `Sender` for its messages (ADR 145).
- A team whose identity provider cannot ask for a maximum age, and that has
  no built-in MFA, cannot approve from chat. It opens the console instead.
- `GLOSSARY.md` needs the terms Chat link and Chat action.

## Open questions

- Can a person reject from chat? A rejection only stops a change. The owner
  listed three actions, so this ADR leaves it out.
- Can a person acknowledge a break-glass review notice from chat, to stop
  its escalation for one business day?
- Which chat tools get adapters first?
