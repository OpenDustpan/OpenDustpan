---
rfc: 121
title: Agent principals, delegation and the kind agent
status: discussing
created: 2026-11-07
---

# RFC 121. Agent principals, delegation and the kind agent

Status: discussing. Design only: this RFC changes no code. It answers issue
#2321 for the project owner. Until the owner decides, `main` keeps ADR 240
decision 3: the server never sets `principal.kind` to `agent`, and a gate
tells an agent apart through `source.kind == "agent"`.

## ELI5 for the owner

Today, when Dusty or another AI agent works for Alice, the server sees only
Alice. The agent wears Alice's badge. A gate cannot ask "is a robot doing
this?" in a reliable way, and an outside agent that uses Alice's personal
token is invisible.

The proposal gives each agent its own badge, and a signed note from Alice
that says "this agent can help me, up to here, until this date". When the
agent acts, it shows both. The server lets the action through only when
Alice is allowed to do it, the agent is allowed to do it, and the note
covers it. Every gate is checked for Alice and again for the agent, so a
rule about Alice still holds when her agent acts, and a rule about agents
now matches. The audit log says "Alice, through agent X". An agent never
signs in to the console, never passes an MFA step, never approves and
never uses break-glass. Alice does those herself.

## Problem

1. `pkg/gate/facts.go` defines `PrincipalAgent` (`"agent"`) for the fact
   `principal.kind`. No code sets it. `pkg/dustpan/stateguard.go` maps every
   principal to `person` or `service`. `identity.Kind` has `person`,
   `service` and `workload`, and no agent kind. A gate such as
   `context.principal.kind == "agent"` never matches, and nothing shows
   that (issue #2321).
2. ADR 240 decision 3 removed `agent` from the record kinds: "An agent acts
   as its user, so `principal.kind` is the kind of that user". ADR 120
   decision 15 says that Dusty is not a principal. ADR 226 decision 4 and
   ADR 223 decision 4 still list `agent`. The code constant is a leftover of
   ADR 226.
3. The project manager proposed to remove the value. The owner overruled
   that: the agent kind stays, and it gets a design pass.
4. `source.kind == "agent"` is not an identity. The job model sets it only
   when a change starts through the agent tools (ADR 125). An outside agent
   that calls the REST API, the Pulumi Cloud-compatible API or the
   TFE-compatible API with a personal token of its person gives
   `source.kind == "api"` or `"cli"`. The server cannot see that an agent
   acts. Its audit entries name the person only. The MCP specification
   gives the same reason to forbid token passthrough: the server "will be
   unable to identify or distinguish between MCP Clients" (MCP security
   best practices, 2025-11-25).
5. An agent has no ceiling of its own. Dusty can do every action of its
   person that the tool catalog offers. An administrator cannot say "Dusty
   never touches the namespace `payments`" except with a limit on each
   person.

This contradicts accepted ADRs, so it is an RFC first (CLAUDE.md, ADR 15).

## What exists now

| Part | Rule now | Source |
| --- | --- | --- |
| Dusty | Not a principal. No grant, no role, no token. Acts as the person who started the run. | ADR 120 decisions 13 to 15 |
| A run | Records the subject and the credential ID (sign-in ID or token ID). Resolves the credential at each step and before each tool call. Ends with the credential. | ADR 120 decision 13, ADR 297 decision 19 |
| Outside agent | Sends a personal token or a service token to the MCP endpoint. The actor is the principal of the token, with the channel `mcp`. | ADR 123 decision 3 |
| Agent reference | `agent.Ref` and `job.AgentRef`: the channel (`agent`, `mcp`, `mcp-proxy`) and the run ID or the MCP session ID. | ADR 125 decision 9, ADR 128 decision 1 |
| Audit | The person is the subject. The `agent/1` block holds the run, the session, the tool, the digest of the arguments, break-glass and the secret stage. | ADR 126 decisions 2 and 10, ADR 128 decision 17 |
| Facts | `principal.kind` is the record kind of the person. `source.kind == "agent"` marks a start through the agent tools. | ADR 240 decision 3, ADR 226 decision 4 |
| Issued OIDC tokens | `actor_type` can be `agent`. `act` names the agent with `sub: agent:<agent ref>`, as in RFC 8693 section 4.1. | ADR 205 decision 3 |
| Confirmations | Only a console session of the person who asked, with no agent reference and a fresh authentication. | ADR 128 decision 5 |
| Break-glass | An agent never uses break-glass. A run in a break-glass session acts with the rights of that session, and ends with it. | ADR 125 decision 15 |
| Secrets | Masked values. A gated escape hatch, with a consent for each use by an outside agent. | ADR 124 |

No tag exists yet. So no stored gate, audit chain or token of a tagged
release holds the old meaning (ADR 72, ADR 73).

## Benchmarks

Each system below was read from its primary source on 2026-11-07.

### OAuth 2.0 Token Exchange (RFC 8693)

<https://www.rfc-editor.org/rfc/rfc8693>

- Section 1.1 separates delegation from impersonation. With impersonation,
  the actor cannot be told apart from the subject. With delegation, the
  actor keeps its own identity.
- Section 2.1: the `subject_token` is the party "on behalf of whom the
  request is being made". The `actor_token` is "the acting party".
- Section 4.1: the `act` claim names the current actor. Nested `act` claims
  name prior actors and are "informational only". For access control, the
  consumer considers the top-level claims and the current actor.
- Section 4.4: `may_act` states that one party "is authorized to become the
  actor and act on behalf of another party".

Lesson: delegation, not impersonation. Two identities in one credential. A
stored statement of who can act for whom.

### GitHub Apps on behalf of a user

<https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/authenticating-with-a-github-app-on-behalf-of-a-user>

- "The app can only access resources that the user has access to", and
  only resources that the app has permission to access. "A token cannot
  grant additional access capabilities to a user." This is the
  intersection.
- Requests are attributed to the user. The UI shows the user with a badge
  of the app. The audit log lists the user as the actor, with the access
  type "GitHub App user-to-server token".
- A user access token expires after 8 hours. The refresh token expires
  after 6 months
  (<https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/refreshing-user-access-tokens>).

Lesson: the user is the subject of the audit, and the app is always named.
The app has its own permission set, which is the ceiling.

### Google Cloud service account impersonation

<https://docs.cloud.google.com/iam/docs/create-short-lived-credentials-direct>

- The caller needs `roles/iam.serviceAccountTokenCreator` on the target
  service account: a stored grant on the target. The token lives 1 hour by
  default.
- A delegation chain (`delegates`) needs the permission at each hop
  (<https://docs.cloud.google.com/iam/docs/create-short-lived-credentials-delegated>).
- The audit log puts the service account in `principalEmail`, and the user
  who made the credential in `serviceAccountDelegationInfo[].firstPartyPrincipal`
  (<https://docs.cloud.google.com/iam/docs/audit-logging/examples-service-accounts>).
  The same page says that some services do not log the user. That is a gap
  to avoid.
- Google Cloud Agent Identity makes an agent an IAM principal of its own,
  with a delegated identity that combines the authority of the agent with
  the authority of the user, and audit logs that name both
  (<https://docs.cloud.google.com/iam/docs/agent-identity-overview>).

Lesson: the right to act as another identity is a grant on that identity.
Both identities go into the log.

### AWS IAM role sessions and source identity

<https://docs.aws.amazon.com/IAM/latest/UserGuide/id_credentials_temp_control-access_monitor.html>

- A source identity is set once. It persists across role chaining and
  "cannot be changed during the role session". CloudTrail records it.
- Setting it needs the permission `sts:SetSourceIdentity`. Policies read
  it with `aws:SourceIdentity`.
- Session policies only narrow: "The permissions for a session are the
  intersection of the identity-based policies ... and the session policies"
  (<https://docs.aws.amazon.com/IAM/latest/UserGuide/access_policies.html>).
  The same page names an exception: a resource policy that names the
  session ARN is not limited by the session policy.

Lesson: the human origin is a fixed attribute of a session that a policy
can read. Narrowing is by intersection. An exception to the intersection is
a known source of surprise.

### Kubernetes impersonation

<https://kubernetes.io/docs/reference/access-authn-authz/user-impersonation/>

- `Impersonate-User`, `Impersonate-Group`, `Impersonate-Uid` and
  `Impersonate-Extra-<name>` headers. The caller needs the verb
  `impersonate` on each attribute.
- The audit event records the caller in `user` and the target in
  `impersonatedUser`.
- After impersonation, authorization runs for the impersonated user only.
  The page says: "impersonation cannot be limited or scoped. It either
  grants full impersonation or none at all." Constrained impersonation
  (KEP-5284, beta in 1.36) adds a permission for each action
  (<https://github.com/kubernetes/enhancements/issues/5284>).

Lesson: plain impersonation lost the ceiling of the caller. Kubernetes
added constraints later. Start with the constraint.

### Published guidance on AI agent identity

- **MCP authorization, revision 2026-07-28**
  (<https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization>):
  OAuth 2.1, Protected Resource Metadata (RFC 9728), the `resource`
  parameter (RFC 8707). A server "MUST validate that access tokens were
  issued specifically for them" and "MUST NOT accept or transit any other
  tokens". Dynamic Client Registration is deprecated in this revision.
  Client ID Metadata Documents are the recommended path.
- **OpenID Foundation, "Identity Management for Agentic AI"**, October 2025
  (<https://openid.net/wp-content/uploads/2025/10/Identity-Management-for-Agentic-AI.pdf>):
  an on-behalf-of token carries "two distinct identities: the user who
  delegated authority (e.g., in the sub claim) and the agent authorized to
  act (e.g., in the act or azp claim)". Scope narrows at each hop.
  Revocation is "largely unsolved". CIBA for human approval of high-risk
  actions.
- **NIST NCCoE concept paper**, "Accelerating the Adoption of Software and
  AI Agent Identity and Authorization", initial public draft, 2026-02-05
  (<https://csrc.nist.gov/pubs/other/2026/02/05/accelerating-the-adoption-of-software-and-ai-agent/ipd>):
  identification, authorization, audit and non-repudiation of agents.
  Agents are identifiable entities, not automation under shared
  credentials.
- **IETF draft-ietf-wimse-aims-01**, 2026-11-02
  (<https://datatracker.ietf.org/doc/draft-ietf-wimse-aims/>): one
  identifier for each agent, short-lived credentials, static API keys are
  an antipattern, the model must not see the credential of the agent, user
  confirmation for higher-risk access, and an audit record with the agent,
  the delegated subject, the resource, the action, the decision, the time
  and a correlation ID.
- **IETF draft-ietf-oauth-identity-assertion-authz-grant-06**, 2026-10-31
  (<https://datatracker.ietf.org/doc/draft-ietf-oauth-identity-assertion-authz-grant/>):
  the policy of the identity provider and the policy of the resource both
  apply. It replaces the consent prompt for each app with the policy of an
  administrator.
- **Microsoft Entra Agent ID**, page of 2026-03-30
  (<https://learn.microsoft.com/en-us/entra/agent-id/agent-identities>):
  an agent identity is "a special service principal". It has a sponsor, the
  person or group that is accountable for it. In a user token, "the subject
  of the token is a user, while the actor is the agent identity".
- **OWASP LLM06 Excessive Agency**
  (<https://genai.owasp.org/llmrisk/llm062025-excessive-agency/>): act "in
  the context of that specific user", least permission, a human approves
  high-impact actions, and "Implement authorization in downstream systems
  rather than relying on an LLM."

Where the sources agree:

1. An agent has an identity of its own, never a shared credential.
2. An on-behalf-of credential carries the person and the agent.
3. The effective permission is narrower than the person and narrower than
   the agent.
4. A person approves high-risk actions outside the agent.
5. The audit record names both.

Not verified: the mitigation text of OWASP ASI03 (Top 10 for Agentic
Applications 2026), and whether Entra Agent ID states the intersection in
words.

## Terms

These terms are proposals. The ADR that accepts this RFC adds them to
`GLOSSARY.md`, and changes the entry **Act-as**, whose _Avoid_ list names
"delegation" and "agent identity" today.

- **Agent principal**: a principal of the kind `agent`. It is software that
  chooses its own actions, for example Dusty, an MCP client or a script
  that acts for a person. It never signs in to the console.
- **Sponsor**: the person or group that is accountable for an agent
  principal. Each agent principal has one.
- **Delegation**: a stored record that lets one agent principal act for one
  person, with a scope, a creator, a time and an expiry. The person can
  list it and revoke it.
- **Ceiling**: the grants of an agent principal. The agent never does more
  than its ceiling, whoever it acts for.
- **Subject** and **actor**: on a delegated request, the subject is the
  person on whose authority the request runs. The actor is the agent that
  sends it. These are the words of RFC 8693 and ADR 205.

## What is an agent principal

An agent principal is a principal record of a fourth kind, `agent`, next to
`person`, `service` and `workload` (ADR 52 decision 1, ADR 240 decision 3).

The kind tells why a separate kind is needed. A service principal is a
machine that does a fixed job. Its author chose its actions in advance. An
agent chooses its actions at run time, from text that it reads. Text that
an attacker controls can steer it (ADR 124 decision 13). So the platform
needs to treat it as a separate risk class, in gates, in audit and in the
console.

The three cases of the owner:

| Case | Today | Proposed |
| --- | --- | --- |
| Dusty | Not a principal. Acts with the sign-in of its person. | One agent principal for each installation, `dusty`, made when an administrator connects a model. Each run is a delegation of its person to it. |
| An MCP client (outside agent) | Uses a personal or service token. Invisible outside the MCP endpoint. | An agent principal that an administrator registers. A person delegates to it through an OAuth consent, and the client gets a delegated token. |
| Automation for a person | Uses a personal token. Invisible. | A delegated token, as for an MCP client. Automation that acts for nobody stays a service principal or a workload. |

An agent that acts on its own authority, with no person, is also possible:
an agent principal with grants and no delegation, for example an agent in
CI that a trust rule names. Then `principal.kind` is `agent` and there is
no subject other than the agent.

## Options

### Question 1: where the agent identity lives

**1A. Keep act-as only (do nothing).** The owner overruled the removal of
the value, so this option keeps the value with no producer. Issue #2321
stays open. Gates that name `agent` never match.

**1B. A derived kind.** The server sets `principal.kind` to `agent` when a
request has an agent reference. No new record.
- Gives: a small change.
- Costs: the kind is not stored. "No magic" asks for a stored, listable
  property. An outside agent with a personal token on the REST API still
  has no reference, so the kind stays wrong for it. The agent has no
  ceiling.

**1C. An agent record kind with its own grants only.** An agent principal
acts on its own authority, like a service principal.
- Gives: a stored kind, a ceiling, an audit subject.
- Costs: it does not model "for a person". Dusty would need grants as wide
  as the widest person, and the run would not narrow to its person. That
  breaks ADR 120: Dusty never has more access than its user.

**1D. An agent record kind, plus a stored delegation (proposal).** An agent
principal has its own grants (the ceiling). A person delegates to it with a
stored delegation. A delegated request carries both identities, and passes
only when both pass.
- Gives: the agreement of every benchmark. A stored grant for every right.
  One identity for each agent on every API.
- Costs: a new record, a new credential kind, a second evaluation of each
  check, console work.

### Question 2: what `principal.kind` means on a delegated request

This is the main one-way door. Gate authors store expressions that read the
fact.

**2A. The subject.** `principal` is the person. A new group `actor` names
the agent. `principal.kind` is `agent` only for an agent on its own
authority.
- Gives: no existing gate changes meaning. It matches ADR 126 (the person
  is the subject) and ADR 205 (`actor_type` and `act`).
- Costs: a gate "only a person destroys", written as
  `principal.kind != "person"`, lets an agent of a person destroy. The
  author must also know the group `actor`. That is the trap of #2321 again.

**2B. The actor.** `principal` is the agent. A new member `onBehalfOf`
names the person.
- Gives: `principal.kind == "agent"` matches each agent, as authors expect.
  It is the model of AWS and Google Cloud, where the policy principal is the
  session or the service account.
- Costs: each limit and gate that names the person, the kind of the person
  or a group of the person stops matching when the person uses an agent.
  A limit "contractors never touch prod" lets the agent of a contractor
  through. That fails open.

**2C. Both, with two evaluations (proposal).** The server evaluates each
access check, each access limit and each gate twice for a delegated
request:
- once **as the subject**: `principal` is the person, with the grants,
  groups and limits of the person,
- once **as the actor**: `principal` is the agent, with the grants and
  limits of the agent.

Both facts documents also hold a new group `delegation`: `present`, the
kind and ID of the subject, the kind and ID of the actor, and the channel.
The request passes only when both evaluations pass. Gate outcomes add up,
and a gate that fires in both evaluations gives one requirement (by gate
ID).
- Gives: the intersection, stated in the evaluation itself. A rule about
  the person still holds when the agent acts. A rule about agents matches.
  No outcome relaxes another (ADR 222 decision 5): two evaluations can only
  be stricter than either one alone. `principal.kind == "agent"` matches
  every agent, which closes #2321.
- Costs: two evaluations for each delegated call. One decision record holds
  two parts, so the answer to "why" names the part that refused. A gate
  such as `principal.kind == "person"` now fires in the subject part of an
  agent call. That is the intended meaning ("the authority of this person"),
  and the docs must say it.

### Question 3: how an agent proves who it is

**3A. Personal tokens (now).** The server cannot tell an agent from its
person.

**3B. Delegated tokens (proposal).** A delegated token is an access token
of the agent principal, bound to one delegation. The credential kind is a
new value, `delegated-token`.
- An MCP client gets it through the OAuth 2.1 flow of the MCP authorization
  specification, with PKCE and the `resource` parameter. The consent screen
  is in the console. The person signs in, takes a fresh authentication, and
  sees the agent, its sponsor, its ceiling and the scope and expiry of the
  delegation. The consent creates the delegation.
- Automation gets it through the same consent in the console, which shows a
  token once, for a client that cannot run a browser flow.
- Dusty needs no token. Its run carries the delegation ID, and the server
  resolves the delegation at each step and before each tool call, as ADR
  120 decision 13 resolves the credential now. Its credential kind is
  `delegated-token` too, so that one value covers every delegated request.

The token is short-lived: 1 hour by default, with a refresh token that
never outlives the delegation. The token policy of ADR 53 sets both, and
the API shows the effective values.

**3C. A header that names the agent.** A client sends its personal token
and a header such as `Dustpan-Agent`.
- Costs: the client declares it, so a hostile client leaves it out. It is
  not an identity. ADR 240 decision 2 already treats the channel header as
  a declared field only.

### Question 4: who creates an agent principal

**4A. An administrator registers each agent (proposal).** A holder of
`server:manage-principals`, or `namespace:manage-principals` for an agent
of a namespace (ADR 67 decision 7), creates it, names its sponsor and gives
its ceiling grants. For Dusty, the administrator who connects the model
creates `dusty` in the same request. The response shows the record and its
grants.

**4B. A person registers an agent at consent.** The server makes a record
at the first OAuth consent of an unknown client, for example from a Client
ID Metadata Document.
- Gives: no wait for an administrator.
- Costs: a principal that a person makes, with a ceiling that somebody
  must choose. Sprawl. A phishing client gets a record from one click. An
  installation setting can allow it later, off by default. Open question 3.

## Proposal

Options 1D, 2C, 3B and 4A. The parts below are the design for the ADR.

### The record

1. `agent` is a principal kind (`identity.KindAgent`, in `identity.Kinds`).
   An agent record has a name, a sponsor (one person or one group), a
   principal home (ADR 215), a creator and a time. It has no password, no
   passkey, no MFA factor, no console session and no sign-in method.
   `principal.Validate` refuses each of them for the kind `agent`.
2. An agent is never a member of a group, and never a break-glass account.
   Its ceiling is the set of its direct grants, so a reader sees it on one
   page.
3. A disabled agent stops at once: each delegation to it and each of its
   tokens stops working at the next resolve.
4. The sponsor gets the notifications of the agent (expiry, a disable, a
   new delegation in bulk). When the sponsor leaves, the access review flags
   the agent, as ADR 70 decision 6 flags a service token.

### Dusty

5. Dusty is an agent principal with the fixed name `dusty`. The model
   connect request creates it, with the connecting administrator as its
   creator and sponsor. ADR 120 decision 15 ("Dusty is not a principal")
   no longer holds.
6. The connect request also creates one ceiling grant: the built-in role
   `agent-tools` on the installation. The role holds the actions of the
   agent tool catalog (ADR 122). The code defines the role. The grant is
   stored, made by the administrator, and can be narrowed or revoked like
   every grant. The status route and the record answer the effective
   ceiling.
7. A run is a delegation of its person to `dusty`. The run start writes the
   delegation, with the run as its scope and the deadlines of the run (ADR
   297 decision 19) as its expiry. The run ends with its delegation, and
   the delegation ends with the run. ADR 120 decisions 13 and 14 stay, with
   the delegation resolved together with the credential.

### Delegation

8. A delegation holds the person, the agent, a scope (all namespaces, or a
   list of namespaces, and an optional role that narrows the ceiling), a
   creator (always the person), a time, an expiry and the channel. Only a
   person delegates. A service principal, a workload or an agent never
   delegates. Chains are not allowed: an agent never acts for an agent.
   The facts and claims keep room for a nested `act`, but the server
   refuses a chain.
9. A delegation is created only in a console session of the person, with a
   fresh authentication (ADR 227). The person can list their delegations
   and revoke each one. An administrator with `server:manage-access` can
   list and revoke every delegation, and the access review exports them.
10. A delegation is an act-as edge of ADR 70 decision 1, from the agent to
    the person, bounded by the intersection. The access review shows it.
11. A delegation from a break-glass session is refused for a delegated
    token, because a token would outlive the time limit of the session.
    For a Dusty run, ADR 125 decision 15 stays: the run acts with the
    rights of the session and ends with it. Open question 5.

### Authorization

12. A delegated request passes an access check only when all three pass,
    at the time of the call:
    - the person can do the action on the object (grants, groups, limits),
    - the agent can do the action on the object (ceiling grants, limits),
    - the delegation covers the namespace and the action.
13. Each access limit (ADR 223) and each gate (ADR 222) runs as the subject
    and as the actor (option 2C). The decision record has one part for
    each, and names the part that refused.
14. An unknown principal kind fails closed. In `principalSubjects.Subject`
    and in each switch on the kind, an unknown value is refused, never
    counted as `person` (issue #2321, step 2). This changes ADR 240
    decision 4 for the gates only: the audit kind of a download keeps the
    stricter `reveal`.

### `principal.kind` and `source.kind`

15. They answer different questions:
    - `principal.kind` is **who**: the record kind of the identity in the
      evaluation. It comes from the credential and the stored record. A
      client cannot set it.
    - `source.kind` is **where from**: the path through which a change
      started (`api`, `cli`, `forge`, `self-service`, `agent`, `schedule`,
      `trigger`). The service that starts the change sets it.
16. When each is set:

    | Request | `principal.kind` (subject part) | `principal.kind` (actor part) | `source.kind` |
    | --- | --- | --- | --- |
    | Alice in the console | `person` | no actor part | `api` |
    | Alice with a personal token, `tofu apply` | `person` | no actor part | `cli` |
    | Dusty for Alice, `propose_change` | `person` | `agent` | `agent` |
    | An MCP client for Alice, through the MCP endpoint | `person` | `agent` | `agent` |
    | An MCP client for Alice, with its delegated token on the REST API | `person` | `agent` | `api` |
    | An agent on its own authority (a trust rule in CI) | `agent` | no actor part | `cli` or `api` |
    | A service token | `service` | no actor part | as now |

    So `principal.kind == "agent"` answers "an agent acts", on every API.
    `source.kind == "agent"` answers "the change came through the agent
    tools". A gate that must stop agents uses `principal.kind`.
17. An installation setting, `agent.requireDelegatedTokens`, refuses a
    personal token and a service token on the MCP endpoint and the stdio
    proxy. Off at first, so that existing MCP clients keep working, with a
    warning in the status route. Open question 2 asks when it turns on by
    default.

### Fresh authentication, confirmations and secrets

18. An agent never has a fresh authentication. A step that asks for one
    (ADR 56, ADR 227) refuses a delegated request with the error of a
    fresh authentication, and the answer names the console page where the
    person does the step.
19. ADR 128 decisions 4, 5 and 10 stay: only the person, in a console
    session with no agent and a fresh authentication, confirms a change of
    an agent. An agent records no verdict.
20. `identity.Principal.ActsAsPerson` answers false for a delegated
    request, so ADR 240 decision 1 does not apply. A state download by an
    agent is refused unless the secret gate of ADR 124 allows it, and then
    it writes `reveal` with the subject and the actor.
21. The consent of ADR 124 decision 10 binds to the delegation, not to a
    personal token.

### Audit and decision records

22. The subject of an entry stays the person when an agent acts for a
    person (ADR 126 decision 2). The agent block becomes `agent/2` (ADR 126
    decision 10 reserves this): the fields of `agent/1`, then the ID of the
    agent principal, the ID of the delegation and the credential kind.
    `principalKind` stays the kind of the subject.
23. An agent on its own authority is the subject of its entries, with
    `principalKind: agent` and no delegation.
24. Each delegation create, use of a consent, revoke, expiry and end writes
    an entry. Each agent create, disable and grant change writes an entry
    of the kind `change`.
25. An issued OIDC token (ADR 205) for a delegated job keeps `actor_type`
    as the kind of the subject, and `act` gets `sub: agent:<agent principal
    ID>` in place of the agent reference. The run or session ID moves to a
    claim of its own inside `act`. This is a one-way door of ADR 205 and
    must land before the first tag.

### Console

26. The principal list shows the kind `agent`, the sponsor and the count of
    active delegations. The page of an agent shows its ceiling grants and
    its delegations.
27. A person has a page of "Agents that act for you", with each delegation,
    its scope, its expiry, its last use and a revoke button.
28. Each history item, job, audit row and approval shows "Alice through
    Dusty" or "Alice through <agent name>", never the agent alone and never
    Alice alone.
29. The gate editor explains `principal.kind`, the two parts and the group
    `delegation`, and the preview of a gate shows both parts.

## ADRs that this touches

| ADR | Effect |
| --- | --- |
| ADR 52 decisions 1, 3 and 4 | A fourth record kind. An agent holds grants and is never a member of a group. |
| ADR 53 | A new token kind for delegated tokens, with its lifetime and refresh rules. |
| ADR 54 | A trust rule can name an agent principal, for an agent on its own authority. |
| ADR 67 | Who creates an agent principal: the same actions as for a service principal. |
| ADR 70 decisions 1, 6 and 25 | Delegation is an act-as edge. The access review shows it. An agent is never a break-glass account. |
| ADR 120 decisions 13 to 15 | Dusty becomes an agent principal. A run is a delegation. |
| ADR 122 decision 4, ADR 123 decision 3 | The MCP endpoint takes delegated tokens through OAuth. The setting of decision 17. |
| ADR 124 decision 10 | The consent binds to the delegation. |
| ADR 125 decisions 9 and 15 | `agent.Actor` names the agent principal and the delegation. Break-glass rule of decision 11. |
| ADR 126 decisions 2 and 10 | The `agent/2` block. |
| ADR 128 decisions 1, 5 and 17 | `job.AgentRef` gets the agent principal ID and the delegation ID. |
| ADR 205 decisions 3 and 6 | The content of `act`. |
| ADR 215 | The principal home of an agent. |
| ADR 222 decision 5, ADR 223 decision 4, ADR 226 decision 4, ADR 240 decisions 3 and 4 | `principal.kind` takes `agent`. Two evaluations. The group `delegation`. Unknown kinds fail closed at the gates. |
| ADR 227 | An agent never has a fresh authentication. |
| ADR 291 | Insights count failures of an agent by the agent principal. |
| ADR 297 decision 19 | The run resolves its delegation with the sign-in. |

The accepting ADR names each of these in its "Changes" list, and each one
gets the reverse item (ADR 296).

## One-way doors

These are hard to change after the first tag:

1. **The meaning of `principal.kind` in a delegated evaluation.** Users
   store gates and limits that read it. Option 2C fixes it as "the identity
   of this part". A later change to 2A or 2B would change the outcome of
   stored gates with no edit.
2. **The value `agent` in the stored principal rows and in the audit hash
   chain.** `principalKind` and the `agent/2` block are inside the hash of
   each entry. A chain keeps them for as long as it exists.
3. **The subject of Dusty.** Each audit entry, history item and blame that
   names `dusty` keeps that name. The name must be fixed, and operator
   branding (ADR 235) changes only the display name.
4. **The claims of an issued token** (ADR 205): `actor_type` and `act`.
   Cloud trust policies match them.
5. **The OAuth surface of the MCP endpoint**: the metadata URLs, the token
   prefix of a delegated token and the scope names. MCP clients store them.

Two-way doors: the content of the role `agent-tools`, the lifetimes, the
console copy, whether a person can register an agent (4B), and when the
setting of decision 17 turns on by default.

## Implementation slices

Each slice is a separate pull request, after the ADR. None is part of this
RFC.

1. The record kind, the fail-closed switch of #2321 and its tests for each
   kind and for an unknown kind. The guide `docs/guides/state-write-gates.md`
   changes in the slice that first sets `agent`.
2. Delegations: the store, the conformance suite, the API, the audit
   entries.
3. Two evaluations in the gate engine and in access limits, with one
   decision record of two parts.
4. Dusty as an agent principal, and a run as a delegation.
5. OAuth for the MCP endpoint and delegated tokens.
6. The console pages (in the console repository).

## Open questions

1. Does the owner accept two evaluations (2C), with two parts in each
   decision record, or prefer 2A with a lint that warns on a gate that
   reads `principal.kind` and not `delegation`?
2. When does `agent.requireDelegatedTokens` turn on by default: at the
   first tag, or one release after the OAuth flow lands?
3. Can a person register an agent at consent (4B), behind a setting that is
   off by default?
4. Is the default ceiling of Dusty the role `agent-tools` on the
   installation, or no grant, so that an administrator must choose a
   namespace?
5. Can a delegation come from a break-glass session at all, or does a run
   in such a session also stop (a change of ADR 125 decision 15)?
6. Is the sponsor one person, or can it be a group, as in Entra Agent ID?
7. Does a delegation to an agent of another principal home need a rule of
   its own (ADR 215)?
8. The GLOSSARY entry **Principal kind** says that a token from a trust
   rule gives `service`. ADR 240 decision 3 and `identity.KindWorkload`
   say `workload`. The ADR of this RFC can fix the entry.

## Checklist

- [x] Tolerates asynchronous replication between installations: each namespace writes only its own half, under its own fence, and no write waits for another installation (ADR 248 decisions 3, 6 and 9). Agent principals and delegations of the installation are installation records, as persons and service principals are. A delegation scoped to a namespace is checked at the point of use in that namespace, and needs no write in another installation. The design adds no cross-namespace write.

## Outcome

Empty until the RFC is accepted (link the ADR) or dropped (say why).
