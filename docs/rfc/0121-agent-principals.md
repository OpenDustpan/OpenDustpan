---
rfc: 121
title: Agent principals, delegation and the kind agent
status: discussing
created: 2026-11-07
---

# RFC 121. Agent principals, delegation and the kind agent

Status: discussing, round 2. Design only: this RFC changes no code. It
answers issue #2321 for the project owner. Round 2 closes the findings of
an architecture review (see Notes). Until the owner decides, `main` keeps ADR 240
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
covers it. The agent's own badge opens nothing by itself: without a live
note from a person, the server refuses it. Every gate is checked for Alice
and again for the agent, so a rule about Alice still holds when her agent
acts, and a rule about agents now matches. Work that the agent starts, for
example a job that applies tomorrow, keeps the note. When Alice tears up
the note, or leaves the company, that work stops. The audit log says
"Alice, through agent X". An agent never signs in to the console, never
passes an MFA step, never approves, never changes who can do what, and
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
  RBAC `resourceNames` can limit which identities a caller impersonates,
  but not which actions it does as them. The constrained impersonation
  section of the same page says: "With the impersonate verb, impersonation
  cannot be limited or scoped. It either grants full impersonation or none
  at all." Constrained impersonation (KEP-5284, beta in 1.36) adds a
  permission for each action
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
  The MCP endpoint of ADR 123 takes personal tokens today. That is a known
  deviation, which decision 12 ends.
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
3. For delegation, the effective permission is narrower than the person
   and narrower than the agent: GitHub Apps, AWS session policies, Google
   Cloud Agent Identity and OWASP. This does not hold for Google Cloud
   service account impersonation or for plain Kubernetes impersonation.
   Both evaluate the target identity only. That is impersonation, which
   this RFC avoids.
4. A person approves high-risk actions outside the agent.
5. The audit record names both.

Not verified: the mitigation text of OWASP ASI03 (Top 10 for Agentic
Applications 2026), and whether Entra Agent ID states the intersection in
words.

## Terms

These terms are proposals. The ADR that accepts this RFC adds them to
`GLOSSARY.md`. It also changes the GLOSSARY entry **Act-as**, whose _Avoid_
list names "delegation" and "agent identity" today, and the text of ADR 70
decision 13, which uses "delegation" for an act-as right. After the change,
ADR 70 decision 13 says "an act-as right", and "delegation" has the one
meaning below.

- **Agent principal**: a principal of the kind `agent`. It is software that
  chooses its own actions, for example Dusty, an MCP client or a script
  that acts for a person. It never signs in to the console, and it never
  acts on its own authority.
- **Sponsor**: the person who is accountable for an agent principal. Each
  agent principal has one.
- **Ceiling**: the grants of an agent principal. A ceiling never gives
  access by itself. It only limits what the agent can do for a person.
- **Delegation**: a stored record that lets one agent principal act for one
  person, with a scope, a creator, a time and an expiry. The person can
  list it and revoke it.
- **Delegated work**: a job, and each stored object that later starts or
  continues an action, that a request under a delegation made. It carries
  the delegation.
- **Subject** and **actor**: on a delegated request, the subject is the
  person on whose authority the request runs. The actor is the agent that
  sends it. These are the words of RFC 8693 and ADR 205.

## What is an agent principal

An agent principal is a principal record of a fourth kind, `agent`, next to
`person`, `service` and `workload` (ADR 52 decision 1, ADR 240 decision 3).

A separate kind is necessary because the risk is different. A service
principal is a machine that does a fixed job. Its author chose its actions
in advance. An agent chooses its actions at run time, from text that it
reads. Text that an attacker controls can steer it (ADR 124 decision 13).
So gates, audit and the console must be able to treat it as a separate
risk class.

An agent principal never acts on its own authority. Its grants are a
ceiling only. Every credential of an agent is bound to a live delegation
from a person, and the server refuses each credential of an agent that has
none. An agent that needs no person, for example an autonomous agent in CI,
is out of scope. A later RFC can propose it, as a separate record with
separate grants, never as the record that holds a ceiling.

The three cases of the owner:

| Case | Today | Proposed |
| --- | --- | --- |
| Dusty | Not a principal. Acts with the sign-in of its person. | One agent principal for each installation, `dusty`, made when an administrator connects a model. A person delegates to it once. Each run uses that delegation. |
| An MCP client (outside agent) | Uses a personal or service token. Invisible outside the MCP endpoint. | An agent principal that an administrator registers. A person delegates to it through an OAuth consent, and the client gets a delegated token. |
| Automation for a person | Uses a personal token. Invisible. | A confidential OAuth client, registered as an agent, with a delegation. Automation that acts for nobody stays a service principal or a workload. |

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

**1C. An agent record kind with its own grants as live authority.** An
agent principal acts on its own authority, like a service principal.
- Gives: a stored kind and an audit subject.
- Costs: it does not model "for a person". Dusty would need grants as wide
  as the widest person, and anyone who gets a credential of Dusty gets
  them. That breaks ADR 120: Dusty never has more access than its user.

**1D. An agent record kind whose grants are a ceiling, plus a stored
delegation (proposal).** A person delegates to an agent with a stored
delegation. A delegated request carries both identities, and passes only
when both pass. The grants of the agent never give access alone.
- Gives: the agreement of the delegation benchmarks. A stored grant for
  every right. One identity for each agent on every API. No credential of
  an agent is worth anything without a live delegation.
- Costs: a new record, a new credential kind, a second evaluation of each
  check, an OAuth surface, console work.

### Question 2: what `principal.kind` means on a delegated request

This is the main one-way door. Gate authors store expressions that read the
fact.

**2A. The subject.** `principal` is the person. A new group names the
agent.
- Gives: no existing gate changes meaning. It matches ADR 126 (the person
  is the subject) and ADR 205 (`actor_type` and `act`).
- Costs: a gate "only a person destroys", written as
  `principal.kind != "person"`, lets an agent of a person destroy. The
  author must also know the new group. That is the trap of #2321 again.

**2B. The actor.** `principal` is the agent. A new member names the person.
- Gives: `principal.kind == "agent"` matches each agent, as authors expect.
  It is the model of AWS and Google Cloud, where the policy principal is the
  session or the service account.
- Costs: each limit and gate that names the person, the kind of the person
  or a group of the person stops matching when the person uses an agent.
  A limit "contractors never touch prod" lets the agent of a contractor
  through. That fails open.

**2C. Both, with two evaluations (proposal).** The server evaluates each
access check, each access limit and each gate twice for a delegated
request or for delegated work:
- once **as the subject**: `principal` is the person, with the grants,
  groups and limits of the person,
- once **as the actor**: `principal` is the agent, with the ceiling and the
  limits of the agent.

Both fact documents also hold a new group `delegation` (decision 22). The
request passes only when both evaluations pass. Gate outcomes add up, and a
gate that fires in both evaluations gives one requirement (by gate ID).
- Gives: the intersection, stated in the evaluation itself. A rule about
  the person still holds when the agent acts. A rule about agents matches.
  No outcome relaxes another (ADR 222 decision 5): two evaluations can only
  be stricter than either one alone. `principal.kind == "agent"` matches
  every agent that has an agent credential, which closes #2321.
- Costs: two evaluations for each delegated call. One decision record holds
  two parts, so the answer to "why" names the part that refused. A gate
  such as `principal.kind == "person"` now fires in the subject part of an
  agent call. That is the intended meaning ("the authority of this person"),
  and the docs must say it.

### Question 3: how an agent proves who it is

**3A. Personal tokens (now).** The server cannot tell an agent from its
person.

**3B. Delegated tokens through OAuth (proposal).** A delegated token is an
access token of the agent principal, bound to one delegation, with the
credential kind `delegated-token`. Decisions 9 to 12 give the details.

**3C. A header that names the agent.** A client sends its personal token
and a header such as `Dustpan-Agent`.
- Costs: the client declares it, so a hostile client leaves it out. It is
  not an identity. ADR 240 decision 2 already treats the channel header as
  a declared field only.

### Question 4: who creates an agent principal

**4A. An administrator registers each agent (proposal).** A holder of
`server:manage-principals`, or `namespace:manage-principals` for an agent
of a namespace (ADR 67 decision 7), creates it and gives its ceiling.

**4B. A person registers an agent at consent.** The server makes a record
at the first consent of an unknown client. Not proposed: a phishing client
gets a record from one click, and somebody must choose its ceiling. It has
no one-way door, so a later RFC can add it.

## Proposal

Options 1D, 2C, 3B and 4A. The decisions below are the design for the ADR.

### The record

1. `agent` is a principal kind (`identity.KindAgent`, in `identity.Kinds`).
   An agent record has a name, a sponsor (one person), a principal home
   (ADR 215), a client registration (decision 10), a creator and a time. It
   has no password, no passkey, no MFA factor, no console session and no
   sign-in method. `principal.Validate` refuses each of them for the kind
   `agent`.
2. **A ceiling only.** The grants of an agent never give access alone. The
   server refuses each credential of the kind `agent` that is not bound to
   a live delegation:
   - an access token of an agent that `principal:manage-tokens` would make
     (ADR 67 decision 2): the server refuses to make it,
   - a trust rule that names an agent (ADR 54, ADR 70 decision 9): the
     server refuses to save it, and refuses each exchange that resolves to
     an agent,
   - a job token of a job with an agent and no delegation: the job model
     refuses the start.

   Each refusal is also checked at the point of use: the authenticator
   refuses a stored credential of an agent with no live delegation, so a
   row that a restore or a bug makes stays useless.
3. An agent is never a member of a group, and never a break-glass account.
   Its ceiling is the set of its direct grants, so a reader sees it on one
   page.
4. A disabled agent stops at once: each delegation to it, each of its
   tokens and its delegated work stop at the next resolve (decision 15).
5. The sponsor gets the notifications of the agent. When the sponsor
   leaves, the access review flags the agent, as ADR 70 decision 6 flags a
   service token.

### Dusty

6. Dusty is an agent principal with the fixed name `dusty`. The model
   connect request creates it, with the connecting administrator as its
   creator and sponsor. ADR 120 decision 15 ("Dusty is not a principal")
   no longer holds.
7. The connect request also creates one ceiling grant that the
   administrator chooses. The API offers the built-in role `agent-tools`
   (the actions of the agent tool catalog, ADR 122) on the installation as
   the default value, and shows it before the connect. The code defines the
   role. The grant is stored, made by the administrator, and can be
   narrowed or revoked like every grant. By decision 2, this grant gives no
   access alone.
8. A person who wants to use Dusty creates a delegation to `dusty` once
   (decision 13). Each run names that delegation. A run start, from a
   console session or from an access token (ADR 120 decision 13), needs a
   live delegation of its person to `dusty`, else it fails with an error
   that names the console page of delegations. The run records the
   delegation ID next to the credential ID, and resolves both at each step
   and before each tool call. The limits of a run (ADR 120 decision 16)
   narrow further. The run still ends with its credential (ADR 120
   decision 14).

### Delegated tokens and clients

9. A delegated token is an access token of an agent, bound to one
   delegation. It lives 1 hour by default. A refresh token never outlives
   its delegation. The token policy of ADR 53 sets both, and the API shows
   the effective values. The server is the OAuth 2.1 authorization server
   of the MCP endpoint, with Protected Resource Metadata (RFC 9728), the
   `resource` parameter (RFC 8707) and PKCE, as the MCP authorization
   specification of 2026-07-28 asks. The token exchange of ADR 284 stays a
   separate endpoint.
10. **The registration binds the client.** An agent record holds one client
    registration of one of two types:
    - **Confidential**: the client proves a key with `private_key_jwt`
      (RFC 7523) against a JWKS that the registration holds or names. It is
      needed for automation and for each client that runs on a server. Each
      refresh needs the key too, so a stolen refresh token alone is
      useless.
    - **Public**: an MCP client on a laptop. The registration holds the
      exact redirect URIs (a loopback URI with an exact path, RFC 8252) and,
      for a Client ID Metadata Document, its exact URL and host. A public
      client cannot prove that it is the program that it says it is. So
      each audit entry and console view names it "as the client claims",
      and the API answers `clientAuth: public` on the delegation.
11. **A narrower default ceiling for a public client.** The API offers the
    built-in role `agent-tools-read` (the read actions of the tool catalog)
    as the default ceiling of a public client. An administrator can choose
    more, and the console then says that any local program can claim the
    client.
12. **The MCP endpoint takes delegated tokens only**, from the slice that
    adds OAuth (slice 5). It then refuses personal tokens and service
    tokens, as the MCP specification requires ("MUST NOT accept or transit
    any other tokens"). Until that slice lands, the endpoint keeps ADR 123
    decision 3, and the docs name this as a known deviation from the
    specification. No setting turns the old behavior back on, because no
    tag shipped it.

### Delegation

13. A delegation holds the person, the agent, the scope (decision 18), a
    creator (always the person), a time, an expiry (30 days by default, 90
    at most, a setting that the API shows) and the channel. Only a person
    delegates. A service principal, a workload or an agent never
    delegates. Chains are not allowed: an agent never acts for an agent.
14. **Only in a console session, with a fresh authentication.** A
    delegation is created only in a console session of the person, with a
    fresh authentication (ADR 227), on the consent page, which shows the
    agent, its sponsor, its client type, its ceiling, the scope and the
    expiry. A break-glass session cannot create a delegation, and a request
    from a break-glass session cannot use one. So no part of an agent call
    ever holds break-glass rights. This changes ADR 125 decision 15: a Dusty
    run cannot start in a break-glass session.
15. **The person ends it too.** A delegation and its work stop at the next
    resolve when any of these happens: the person revokes it, it expires,
    the agent is disabled, or the person is disabled, deleted, removed by
    SCIM or terminated by the identity provider (ADR 70 decision 19). Each
    end writes an audit entry with the cause.
16. The person can list their delegations and revoke each one. An
    administrator with `server:manage-access` can list and revoke every
    delegation, and the access review exports them.
17. **A delegation is not an act-as edge.** ADR 70 decision 1 computes reach
    as a union with "the grants of each principal that it can act as". A
    delegation is a new edge kind, with these rules:
    - It adds nothing to the reach of the agent. The effective reach of a
      delegated request is the intersection of the reach of the person,
      the ceiling of the agent and the scope of the delegation.
    - It is not an act-as right in ADR 70 decisions 2, 3 and 13. A holder
      of `principal:manage-tokens` on an agent, or the author of a trust
      rule, gets nothing through it (decision 2 refuses such credentials).
    - It does not make a group protected (ADR 70 decision 13), because an
      agent is never a member of a group.
    - The access review shows each delegation as its own row: person,
      agent, scope, expiry, last use. It never adds the reach of the person
      to the agent.

### Scope and sensitive actions

18. **The scope.** A scope has two parts:
    - the objects: a list of namespaces, or all namespaces, and a separate
      flag `installation`. All namespaces never covers an action whose
      object is the installation (`@server`) or a principal of the
      installation. Only `installation: true` covers it.
    - an optional role that narrows the ceiling for this delegation.
19. **A delegated request never does a sensitive action.** A rule in the
    catalog of code rules refuses these actions for each delegated request
    and each piece of delegated work:
    - access management: grants, roles, groups and memberships,
    - tokens: create, rotate or revoke any access token,
    - trust rules and principals: create, change, disable,
    - delegations: create or revoke,
    - sign-in settings, identity providers and SCIM,
    - each action of the operator family.

    A delegation can allow the first three items with the scope flag
    `accessAdministration: true`, which the person sets on the consent
    page. Even with it, the server refuses each change of the ceiling of
    the acting agent, of a delegation to it, of a token of it and of its
    client registration. Nothing allows that. The refusal writes an audit
    entry.

### Delegated work

20. **Work carries the delegation.** A job, and each stored object that
    later starts or continues an action, records the delegation ID when a
    delegated request makes it: a job and its job token, a run trigger
    (ADR 267), a drift schedule (ADR 263), a Catalog request and a
    scheduled change. At each later evaluation (`change.planned`,
    `change.apply`, `state.write`, and each call with the job token), the
    server resolves the delegation and runs both parts. So an agent gate
    fires on `change.apply` and on `state.write` for a change that an agent
    proposed.
21. **Work fails closed when the delegation ends** (decision 15):
    - a job that waits, or has not started a phase that changes, is
      canceled with the stop reason `delegation-ended`,
    - a running phase gets the refusal at its next call with the job token,
      so the phase fails, as for a revoked job token today,
    - a trigger or a schedule is disabled with the same reason, and starts
      nothing more.

    The confirmation of the person (ADR 128 decision 5) does not take the
    authority over. The job keeps its actor part after the confirmation,
    and the decision record names the confirmation too.

### Facts

22. **The facts of each part.**

    | Fact | Subject part | Actor part |
    | --- | --- | --- |
    | `principal.kind` | the record kind of the person (`person`) | `agent` |
    | `principal.credential` | `delegated-token` for a request, `job-token` for delegated work | the same as the subject part |
    | `principal.groups` (access limits) | the groups of the person | empty |
    | authentication age (access limits) | not available: an agent has no fresh authentication | not available |
    | `delegation.present` | `true` | `true` |
    | `delegation.subject` | the ID and the kind of the person | the same |
    | `delegation.actor` | the ID of the agent | the same |
    | `delegation.clientAuth` | `public` or `confidential` | the same |
    | `delegation.channel` | `agent`, `mcp`, `mcp-proxy` or `api` | the same |

    A request with no delegation has one part, with `delegation.present`
    `false` and empty members. The credential of a delegated request is
    never `session`, also when a person started a Dusty run in the
    console. So a gate "only a person in the console", written as
    `principal.credential == "session"`, refuses every agent.
23. The new value `agent`, the new credential and the new group are
    additions, so they are a minor version of each schema (ADR 226
    decision 3). The change of meaning of `principal` on a delegated request
    would need a new major version after a tag. No tag exists, so the ADR
    fixes the meaning in the first version that a tag ships.

### `principal.kind`, `source.kind` and what they do not see

24. They answer different questions:
    - `principal.kind` is **who**: the record kind of the identity in the
      evaluation. It comes from the credential and the stored record. A
      client cannot set it.
    - `source.kind` is **where from**: the path through which a change
      started (`api`, `cli`, `forge`, `self-service`, `agent`, `schedule`,
      `trigger`). The service that starts the change sets it.
25. When each is set:

    | Request | `principal.kind`, subject part | `principal.kind`, actor part | `source.kind` |
    | --- | --- | --- | --- |
    | Alice in the console | `person` | no actor part | `api` |
    | Alice with a personal token, `tofu apply` | `person` | no actor part | `cli` |
    | Dusty for Alice, `propose_change` | `person` | `agent` | `agent` |
    | The job of that change, at `change.apply` and `state.write` | `person` | `agent` | `agent` |
    | An MCP client for Alice, through the MCP endpoint | `person` | `agent` | `agent` |
    | An MCP client for Alice, with its delegated token on the REST API | `person` | `agent` | `api` |
    | A program that uses Alice's personal token | `person` | no actor part | `api` or `cli` |
    | A service token | `service` | no actor part | as now |

26. **The limit of the fact.** `principal.kind == "agent"` matches each
    agent that uses an agent credential. It cannot match a program that
    uses a personal token on the REST API, the Pulumi Cloud-compatible API
    or the TFE-compatible API. No server can tell such a program from its
    person. The guide of gates and the gate editor say this in the text of
    the fact, and give the rule that holds for both: a gate that must allow
    only a person at the console uses `principal.credential == "session"`.
    A mark "not for agents" on a personal token is not proposed: a client
    declares nothing that the server can check, so the mark would stop
    nothing. On the MCP endpoint, decision 12 closes the gap.

### Unknown kinds and outside authenticators

27. Each switch on the kind names every case. No case fails open:

    | Principal | Kind | At a gate |
    | --- | --- | --- |
    | The bootstrap admin token | The authenticator sets `person` explicitly, with the credential `access-token`. It no longer gives the empty kind. | as a person |
    | An authenticator of an embedder (ADR 35) that leaves the kind empty | empty | as a person (ADR 240 decision 4), and the decision record and the audit entry get `kindAssumed: true` |
    | Any authenticator with a kind that the server does not know | the value | refused, with a denial in the audit log |
    | Any authenticator outside the server that gives `agent` | `agent` | refused: only the server makes an agent credential, from a stored delegation |

    The empty kind keeps working, so the embedders of today keep working.
    The person treatment is the stricter one for a download (ADR 240
    decision 1), and it applies every rule about persons. Issue #2321
    step 2 asks that an unknown value never counts as `person`. This
    proposal reads "unknown" as a non-empty value that the server does not
    know, and keeps ADR 240 decision 4 for the empty value, with the flag.

### Fresh authentication, confirmations and secrets

28. An agent never has a fresh authentication. A step that asks for one
    (ADR 56, ADR 227) refuses a delegated request with the error of a
    fresh authentication, and the answer names the console page where the
    person does the step.
29. ADR 128 decisions 4, 5 and 10 stay: only the person, in a console
    session with no agent and a fresh authentication, confirms a change of
    an agent. An agent records no verdict.
30. `identity.Principal.ActsAsPerson` answers false for a delegated
    request, so ADR 240 decision 1 does not apply. A state download by an
    agent is refused unless the secret gate of ADR 124 allows it, and then
    it writes `reveal` with the subject and the actor.
31. The consent of ADR 124 decision 10 binds to the delegation, not to a
    personal token.

### Audit and issued tokens

32. The subject of an entry stays the person (ADR 126 decision 2). The
    agent block becomes `agent/2` (ADR 126 decision 10 reserves this): the
    fields of `agent/1`, then the ID of the agent principal, the ID of the
    delegation, the credential kind and `clientAuth`. `principalKind` stays
    the kind of the subject.
33. Each delegation create, use of a consent, revoke, expiry and end writes
    an entry. Each agent create, disable and ceiling change writes an entry
    of the kind `change`. Each refusal of decisions 2, 19 and 27 writes a
    denial.
34. **Issued OIDC tokens** (ADR 205). For a job of delegated work:
    - `actor_type` stays the kind of the person, the principal on whose
      authority the job runs.
    - `act` gets `sub: agent:<agent principal ID>`, in place of the agent
      reference. The run or MCP session ID moves to a member of its own
      inside `act`.
    - `sub` gets a new key `agent`, at the end of the key order (ADR 205
      decision 6 allows a new key there). Its value is the name of the
      agent, `@` and its ID. The server leaves the key out of a job with no
      agent, as for each key with no fact. AWS reads only `sub` of our
      tokens, so this key is the only way for an AWS trust policy to refuse
      or allow a job that an agent proposed.
    - The default subject template gets the key `agent`. Then the subject
      of an agent job differs from the subject of a job of the person, so an
      exact trust policy written for the person does not match an agent job
      (fail closed). A trust policy with `*` after the last key still
      matches it, and the docs say so.

### Notes for the console repository

These belong in the console repository, after the ADR. None of them is a
one-way door.

- A principal list with the kind `agent` and its sponsor.
- A page of an agent with its ceiling, its client type and its delegations.
- A page of "Agents that act for you" for each person, with revoke.
- "Alice through Dusty" on each history item, job, audit row and approval.
- The two parts in the gate editor and in its preview.

## ADRs that this touches

| ADR | Effect |
| --- | --- |
| ADR 35, ADR 240 decision 4 | The kinds of the bootstrap token and of an outside authenticator (decision 27). |
| ADR 52 decisions 1, 3 and 4 | A fourth record kind. An agent holds ceiling grants only and is never a member of a group. |
| ADR 53 | Delegated tokens, their lifetime and their refresh. No access token of an agent without a delegation. |
| ADR 54 | A trust rule never names an agent. |
| ADR 67 decision 2 | `principal:manage-tokens` makes no token of an agent. |
| ADR 70 decisions 1, 2, 3, 6, 9, 13 and 19 | A delegation is a new edge kind with intersection semantics. The word "delegation" leaves decision 13. A terminated person ends their delegations. |
| ADR 120 decisions 13 to 16 | Dusty becomes an agent principal. A run names a delegation. |
| ADR 122 decision 4, ADR 123 decision 3 | The MCP endpoint takes delegated tokens only, after the OAuth slice. |
| ADR 124 decision 10 | The consent binds to the delegation. |
| ADR 125 decisions 9 and 15 | `agent.Actor` names the agent and the delegation. No run in a break-glass session. |
| ADR 126 decisions 2 and 10 | The `agent/2` block. |
| ADR 128 decisions 1, 5 and 17 | `job.AgentRef` gets the agent ID and the delegation ID. The confirmation does not take the authority over. |
| ADR 205 decisions 3 and 6 | The content of `act`, the `sub` key `agent` and the default template. |
| ADR 222 decision 5, ADR 223 decision 4, ADR 224, ADR 226 decisions 3 and 4, ADR 240 decision 3 | `principal.kind` takes `agent`. Two evaluations. The credential `delegated-token`. The group `delegation`. |
| ADR 227 | An agent never has a fresh authentication. |
| ADR 263, ADR 267 | Triggers and drift schedules carry the delegation. |
| ADR 284 | The authorization server sits next to the token exchange. |
| ADR 297 decision 19 | The run resolves its delegation with the sign-in. |

The accepting ADR names each of these in its "Changes" list, and each one
gets the reverse item (ADR 296).

## One-way doors

These are hard to change after the first tag:

1. **The meaning of `principal.*` in each part of a delegated
   evaluation** (decision 22). Users store gates and limits that read it. A
   later change to 2A or 2B would change the outcome of stored gates with
   no edit.
2. **The value `agent`, the credential value `delegated-token` and the
   names of the members of the group `delegation`.** Stored gates name
   them.
3. **The shape of the two-part decision record**, because the server stores
   decision records.
4. **The value `agent` in the stored principal rows and in the audit hash
   chain.** `principalKind` and the `agent/2` block are inside the hash of
   each entry. A chain keeps them for as long as it exists.
5. **The subject of Dusty.** Each audit entry, history item and blame that
   names `dusty` keeps that name. Operator branding (ADR 235) changes only
   the display name.
6. **The claims of an issued token** (ADR 205): `actor_type`, `act`, the
   `sub` key `agent` and its place in the key order. Cloud trust policies
   match them.
7. **The OAuth surface**: the issuer, the authorization server metadata,
   the Protected Resource Metadata, the token prefix of a delegated token
   and the scope names. MCP clients and automation store them.
8. **The role names `agent-tools` and `agent-tools-read`**, if grants store
   a built-in role by name.

Two-way doors: the content of the two roles, the lifetimes, the console
copy and 4B.

## Implementation slices

Each slice is a separate pull request, after the ADR. None is part of this
RFC. Slices 1 to 3 and 5 are the core.

1. The record kind, decision 2, the switch of decision 27 and its tests:
   one for each kind, one for the empty kind, one for an unknown value, one
   for `agent` from an outside authenticator. The guide
   `docs/guides/state-write-gates.md` changes in the slice that first sets
   `agent`.
2. Delegations: the store, the conformance suite, the API, the scope, the
   rule of decision 19, the ends of decision 15, the audit entries.
3. Two evaluations in the gate engine and in access limits, one decision
   record of two parts, and delegated work (decisions 20 and 21).
4. Dusty as an agent principal, and a run that names a delegation.
5. OAuth for the MCP endpoint, delegated tokens and client registrations.
6. The `sub` key `agent` and the `act` content.
7. The console pages (in the console repository).

## Open questions

Each question stands alone. The proposal gives an answer to each, so the
owner can also accept the proposal as it is.

1. **Two evaluations.** Does the owner accept that every check of an agent
   call runs twice, once as the person and once as the agent, with a
   decision record of two parts (option 2C)? The alternative is one
   evaluation as the person (2A) plus a warning in the gate editor.
2. **A running apply when a delegation ends.** The proposal stops a running
   phase at its next call, so an apply can stop half done, as a cancel can.
   The alternative lets a running phase finish and starts no new phase.
   Which one?
3. **The default ceiling of Dusty.** The proposal offers the role
   `agent-tools` on the whole installation as the default, which gives no
   access alone. The alternative offers no default, so the administrator
   must choose the namespaces at connect. Which one?
4. **The default subject template.** The proposal adds the key `agent` to
   the default template, so existing exact trust policies refuse agent jobs
   until a person allows them. The alternative leaves the default as it is
   and documents the key. Which one?
5. **The default lifetime of a delegation.** The proposal is 30 days by
   default and 90 days at most. Is that right?

## Notes

- The GLOSSARY entry **Principal kind** says that a token from a trust rule
  gives `service`. ADR 240 decision 3 and `identity.KindWorkload` say
  `workload`. The ADR of this RFC can fix the entry.
- Review round 1 (an architecture review of 2026-11-07) found two blockers:
  ceiling grants that were also live authority, and delegated work that
  lost its actor. Decisions 2, 20 and 21 close them. The project manager
  directed both fixes, and the rule of decision 19.

## Checklist

- [x] Tolerates asynchronous replication between installations: each namespace writes only its own half, under its own fence, and no write waits for another installation (ADR 248 decisions 3, 6 and 9). Agent principals and delegations are installation records, as persons and service principals are. A delegation is checked at the point of use in the namespace of the action, and needs no write in another installation. The design adds no cross-namespace write.

## Outcome

Empty until the RFC is accepted (link the ADR) or dropped (say why).
