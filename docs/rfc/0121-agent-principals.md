---
rfc: 121
title: Agent principals, delegation and the kind agent
status: discussing
created: 2026-11-07
---

# RFC 121. Agent principals, delegation and the kind agent

Status: discussing, round 3 (final round). Design only: this RFC changes no
code. It answers issue #2321 for the project owner, and it is the design
gate of milestone 10 (agents: Dusty, MCP and agent principals). Round 3
rewrites the proposal from two adversarial review rounds and from new
research on standards, vendors, infrastructure platforms, agent harnesses
and threats (see "Review findings and their resolution" and "Benchmarks").
Until the owner decides, `main` keeps ADR 240 decision 3: the server never
sets `principal.kind` to `agent`, and a gate tells an agent apart through
`source.kind == "agent"`.

The owner's goal: no hole in the access model for one kind of principal,
and a feature that is pleasant to use. Each decision below serves one of
the two, and the "Friction budget" section counts the cost.

## The owner's three questions

**Does Dusty get its own service principal?** Dusty gets its own
principal, but not a service principal. It is one principal of a new kind,
`agent`, with the fixed name `dusty`, made when an administrator connects
a model. Its grants are a **ceiling** only. They never give access alone,
and no credential of `dusty` exists that is not bound to a live delegation
from a person. A service principal acts on its own authority. Dusty never
does.

**Does it impersonate a user?** No. Impersonation means that the server
cannot tell the agent from the person. Dusty acts **for** a person, through
a stored, revocable **delegation** that the person created. Each request
names both: the person as the subject (`sub`) and Dusty as the actor
(`act`). The server checks the person, checks Dusty's ceiling, checks the
scope of the delegation, and allows only what all three allow. The audit
log says "Alice through Dusty".

**Which person does it act for, and when?** Always the person who started
the work, and no one else:

- In a conversation, the person who asked. The run names that person's
  delegation to `dusty` and resolves it before each tool call.
- In work that outlives the conversation (a job that applies tomorrow, a
  run trigger, a drift schedule), the person whose delegation made it. The
  server resolves that delegation again at each later step. When the person
  revokes it, leaves, or lets it expire, the work stops.
- Never another person, never a service principal, never an administrator
  "for convenience". An agent with no person (for example an autonomous
  agent in CI) is out of scope. A later RFC can propose it as a separate
  record with separate grants.
- Some steps are never the agent's: a sign-in, an MFA step, a
  confirmation, an approval, a change of who can do what, a break-glass.
  The person does each of these in the console. The agent can ask for them
  with a held request (decision 23) and wait.

## ELI5 for the owner

Today, when Dusty or another AI agent works for Alice, the server sees only
Alice. The agent wears Alice's badge. A gate cannot ask "is a robot doing
this?", and an outside agent that uses Alice's personal token is invisible.

The proposal gives each agent its own badge, and a signed note from Alice
that says "this agent can help me, in this way, up to here, until this
date". The note comes in three sizes: "only look", "look and suggest
changes", and "operate, but ask me before each change". When the agent
acts, it shows both the badge and the note. The server lets the action
through only when Alice is allowed, the agent's badge is allowed, and the
note covers it. The badge alone opens nothing.

Some doors never open for an agent, whatever the note says: the doors that
change who can do what. When the agent needs Alice for something, it leaves
a request at the desk and waits. Alice reads the request in the console,
with the facts that the server wrote at the top and the agent's own words
below them in a separate box, and says yes once. If the agent read text
that somebody else wrote, the server marks the run, and the agent cannot
act directly any more: it must ask Alice. When Alice tears up the note, or
leaves the company, everything the agent started for her stops.

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
   ADR 223 decision 4 still list `agent`.
3. The project manager proposed to remove the value. The owner overruled
   that: the agent kind stays, and it gets a design pass.
4. `source.kind == "agent"` is not an identity. The job model sets it only
   when a change starts through the agent tools (ADR 125). An outside agent
   that calls the REST API, the Pulumi Cloud-compatible API or the
   TFE-compatible API with a personal token of its person gives
   `source.kind == "api"` or `"cli"`. The server cannot see that an agent
   acts. Every rule that keys on the channel misses it.
5. An agent has no ceiling of its own. An administrator cannot say "Dusty
   never touches the namespace `payments`" except with a limit on each
   person.
6. The intersection of rights is necessary, and not enough. Prompt
   injection does not ask for rights that the person and the agent lack. It
   steers the agent inside the rights that both have (see "Threat model").

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
| Issued OIDC tokens | `actor_type` can be `agent`. `act` names the agent with `sub: agent:<agent ref>`. | ADR 205 decision 3 |
| Confirmation step | Only the person who asked, in a console session with no agent and a fresh authentication. | ADR 125 decision 5, ADR 128 decision 5 |
| `direct` changes | Three switches (installation, namespace, person). A destroy is never `direct`. | ADR 125 decisions 1 to 3 |
| Change orders | An agent drafts a change order, and a person confirms it. Which person confirms is open. | ADR 344 |
| Approval rules | `authorMayApprove` is `false` by default. Only a person in a session approves. | ADR 225 |
| Break-glass | An agent never uses break-glass. | ADR 125 decision 15 |
| Secrets | Masked values. A gated escape hatch, with a consent for each use by an outside agent. | ADR 124 |

No tag exists yet. So no stored gate, audit chain or token of a tagged
release holds the old meaning (ADR 72, ADR 73). Milestone 10 holds the
code. The first tag comes after milestone 7 (ADR 279). So the shapes that a
tag would freeze must be fixed before the first tag, even though most of
the code comes later (see "Before the first tag").

## Threat model

An agent reads text from parties other than its person: state attributes,
resource tags, commit and update messages, job logs, audit entries, job
reasons and comments, and, for an outside client, web pages, issues and
mail. A language model cannot separate instructions from data in that
text. The UK NCSC calls a model an "inherently confusable deputy"
(<https://www.ncsc.gov.uk/blog-post/prompt-injection-is-not-sql-injection>,
2025-12-08). Adaptive attacks beat 12 published defenses with a success
rate above 90% for most of them (Nasr, Carlini, Tramèr et al.,
<https://arxiv.org/abs/2510.09023>). NIST AI 100-2 E2025, section 3.4.4,
tells designers to assume injection whenever a model sees untrusted input
(<https://nvlpubs.nist.gov/nistpubs/ai/NIST.AI.100-2e2025.pdf>).

So this RFC assumes: **an agent that has read text from others may act for
those others, inside every right that the intersection allows.** The
intersection bounds how far a hijacked agent reaches. Three more controls
bound whether it acts:

1. A fixed list of actions that no agent does (decision 20).
2. A mark on each run that has read untrusted input, and a rule that such
   a run cannot act directly (decisions 28 to 30). This is the "Rule of
   Two" of Meta (<https://ai.meta.com/blog/practical-ai-agent-security/>,
   2025-10-31): an agent that processes untrusted input, has access to
   sensitive systems, and changes state needs human approval.
3. A confirmation by the person for each change, which shows facts that the
   server computed above the text that the agent wrote (decision 25).

A model that judges another model is not one of these controls. Vendors of
such classifiers say that they are not a security boundary (see
"Benchmarks").

## Benchmarks

The research for this round read each source from its public page on
2026-11-07.

### Standards

- **OAuth 2.0 Token Exchange, RFC 8693**
  (<https://www.rfc-editor.org/rfc/rfc8693>). Section 1.1 separates
  delegation (the actor keeps its own identity) from impersonation (the
  actor cannot be told apart). Section 4.1: `act` names the current actor,
  and nested `act` claims are "informational only". Section 4.4: `may_act`.
  RFC 8693 leaves revocation of a delegation out.
- **Rich Authorization Requests, RFC 9396**
  (<https://www.rfc-editor.org/rfc/rfc9396>): `authorization_details` with a
  type, locations and actions. A standard shape for a fine scope.
- **DPoP, RFC 9449** (<https://www.rfc-editor.org/rfc/rfc9449>): tokens
  bound to a key of the client, so a stolen token is useless alone.
- **OAuth 2.0 Security Best Current Practice, RFC 9700**
  (<https://www.rfc-editor.org/rfc/rfc9700>): a refresh token of a public
  client must be sender-constrained or rotated.
- **MCP authorization, revision 2026-07-28**
  (<https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization>):
  OAuth 2.1, Protected Resource Metadata (RFC 9728), the `resource`
  parameter (RFC 8707) "MUST be included", and a server "MUST NOT accept or
  transit any other tokens". Client ID Metadata Documents first. Dynamic
  Client Registration is deprecated. This revision also removed
  protocol-level sessions and the `initialize` handshake (SEP-2567 and
  SEP-2575 in the changelog of the revision). The MCP
  security best practices say that Client ID Metadata Documents "cannot
  prevent `localhost` URL impersonation by themselves"
  (<https://modelcontextprotocol.io/docs/2026-07-28/tutorials/security/security_best_practices>).
  The elicitation page says that a server "MUST NOT rely on URL mode
  elicitation to authorize users for themselves", and the schema says that
  tool annotations are hints, not decisions.
- **OpenID Foundation, "Identity Management for Agentic AI"**, October 2025
  (<https://openid.net/wp-content/uploads/2025/10/Identity-Management-for-Agentic-AI.pdf>):
  two identities in an on-behalf-of token, the user in `sub` and the agent
  in `act` or `azp`. Revocation is "largely unsolved".
- **IETF draft-ietf-wimse-aims-01**, 2026-11-02
  (<https://datatracker.ietf.org/doc/draft-ietf-wimse-aims/>): one identifier
  for each agent, short-lived credentials, static keys are an antipattern,
  user confirmation for higher-risk access, and an audit record with the
  agent and the delegated subject.
- **NIST NCCoE concept paper on agent identity**, 2026-02-05
  (<https://csrc.nist.gov/pubs/other/2026/02/05/accelerating-the-adoption-of-software-and-ai-agent/ipd>):
  agents are identifiable entities, not automation under shared credentials.
- **UMA 2.0, GNAP (RFC 9635), CIBA and the OpenID AuthZEN drafts** all let
  a client ask, wait and retry while a person answers elsewhere: UMA
  `request_submitted`, GNAP continuation with `wait`, CIBA poll. The held
  request of decision 23 borrows this shape, not these protocols.

No standard defines a stored, revocable delegation record, an agent
ceiling, or the rule "person and agent and delegation". This RFC defines
them. The stored delegation that each check resolves is stronger than any
of the standards on revocation.

### Identity vendors and clouds

- **Microsoft Entra Agent ID**
  (<https://learn.microsoft.com/en-us/entra/agent-id/agent-identities>): an
  agent identity is a special service principal with a required sponsor,
  and sponsorship moves to the manager when the sponsor leaves
  (<https://learn.microsoft.com/en-us/entra/agent-id/agent-owners-sponsors-managers>).
  Some permissions are blocked for agents: "Users and administrators aren't
  allowed to consent to those powerful permissions for an agent", and admin
  roles "can't be assigned to agent identities"
  (<https://learn.microsoft.com/en-us/entra/agent-id/authorization-agent-id>).
  The gap: in an on-behalf-of flow, Conditional Access evaluates the user
  only, and "Selecting an agent identity as the policy subject doesn't cover
  agent requests made on behalf of the user"
  (<https://learn.microsoft.com/en-us/entra/identity/conditional-access/agent-id>).
  The two evaluations of decision 43 close that gap.
- **Google Cloud Agent Identity**
  (<https://docs.cloud.google.com/iam/docs/agent-identity-overview>): an agent
  is an IAM principal of its own, and a delegated call gets the
  "least-privilege intersection" of the agent and the user. A deleted agent
  leaves its IAM bindings behind. Decision 6 avoids that.
- **Okta for AI Agents** and **Auth0 for AI Agents**: `sub` is the user and
  `act.sub` is the agent (<https://developer.okta.com/docs/guides/ai-agent-token-exchange/-/main/>).
  Okta recommends two or more owners for each agent. Auth0 ships CIBA with
  RAR for human approval at run time.
- **GitHub Apps on behalf of a user**
  (<https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/authenticating-with-a-github-app-on-behalf-of-a-user>):
  "The app can only access resources that the user has access to", and the
  audit log names the user and the app.

### Infrastructure and DevOps platforms

- **GitLab Duo Agent Platform, composite identity**
  (<https://docs.gitlab.com/user/duo_agent_platform/composite_identity/>):
  a service account and the user in one token, and access is "whichever is
  more restrictive". This is the nearest shipped precedent of option 1D.
- **Coding agents on a forge**
  (<https://docs.github.com/en/copilot/concepts/agents/cloud-agent/risks-and-mitigations>):
  some agents push only to their own branches, the person who asked for a
  pull request cannot approve it, and workflows wait for an approval. The
  audit names the agent and the user.
- **Agents in infrastructure products**
  (<https://www.pulumi.com/docs/ai/concepts/neo/permissions/>):
  some agent products act as the user, with no identity of their own, and
  offer task modes from review to automatic. Others use any API token or a
  service account with wide access. Their users ask for short-lived
  credentials, a read-only mode, attribution of agent runs and finer access
  control.

Lessons: users start with "look around" and want reads with no prompt.
Products that ask before every step get the prompt turned off. Products
that make the approval cheap and tunable keep it on. Approval belongs at
the apply, with the plan on the screen.

### Agent harnesses

Agent harnesses (<https://code.claude.com/docs/en/permission-modes>,
<https://cursor.com/docs/agent/security/run-modes.md>,
<https://kiro.dev/docs/permissions.md>) converge on allow, ask and deny,
with deny first, on named modes as presets, and on an administrator tier
that only tightens. Each enterprise lock is on the client, so a local
administrator can defeat it. OpenDustpan enforces on the server. A review
step on the client is not a security boundary. Allow lists on shell command
text failed again and again, with many published bypasses. A common rule is
that a cloned repository cannot add permission rules.

### Threat research

- **Rule of Two** (Meta, above) and the **lethal trifecta** (Simon
  Willison, <https://simonwillison.net/2025/Jun/16/the-lethal-trifecta/>).
- **Approval fatigue.** Habituation drops attention after the second
  exposure to a warning (Anderson, Vance, Kirwan et al., CHI 2015,
  <https://scholarsarchive.byu.edu/facpub/9306>). A
  prompt that makes the person engage with a fact on the screen resists
  habituation (Bravo-Lillo et al., SOUPS 2014,
  <https://www.usenix.org/conference/soups2014/proceedings/presentation/bravo-lillo>).
  Microsoft says that number matching "eliminated MFA fatigue attacks when
  enabled"
  (<https://learn.microsoft.com/en-us/entra/identity/authentication/how-to-mfa-number-match>).
- **OWASP LLM06 Excessive Agency**
  (<https://genai.owasp.org/llmrisk/llm062025-excessive-agency/>):
  "Implement authorization in downstream systems rather than relying on an
  LLM."

### Where the sources agree

1. An agent has an identity of its own, never a shared credential.
2. An on-behalf-of credential carries the person in `sub` and the agent in
   `act`, one level only.
3. The effective permission is the intersection of the person and the
   agent. Plain impersonation (Google Cloud service account impersonation,
   plain Kubernetes impersonation) evaluates the target only, and this RFC
   avoids it.
4. A person approves risky actions outside the agent, and an approval is a
   request that the client can wait on, not a refusal.
5. Some actions are blocked for agents, and no administrator can unlock
   them.
6. The audit record names both.

Not verified: the mitigation text of OWASP ASI03, whether MCP clients
support DPoP today, and the body of the individual IETF agent drafts.

## Terms

These terms are proposals. The ADR that accepts this RFC adds them to
`GLOSSARY.md`, and changes the entries **Act-as** and **Principal kind**.

- **Agent principal**: a principal of the kind `agent`: software that
  chooses its own actions, for example Dusty, an MCP client or a script
  that acts for a person. It never signs in, and it never acts on its own
  authority.
- **Sponsor**: a person who is accountable for an agent principal. Each
  agent has 1 to 5 sponsors.
- **Ceiling**: the grants of an agent principal. A ceiling never gives
  access alone. It only limits what the agent can do for a person.
- **Delegation**: a stored record that lets one agent principal act for one
  person, with a mode, a scope, a creator, a time and an expiry. The person
  can list it, renew it and revoke it.
- **Delegation mode**: the named size of a delegation: `read`, `propose` or
  `operate`. _Avoid_: preset (that is console branding), autonomy level.
- **Delegated work**: a job, and each stored object that later starts or
  continues an action, that a request under a delegation made. It carries
  the delegation.
- **Held request**: a stored, single-use request of an agent for one
  action that only its person can allow. The person confirms it in the
  console with a fresh authentication. _Avoid_: pending approval (an
  approval fills a requirement of an approval rule), step-up.
- **Untrusted input**: the mark on a run, a token family or delegated work
  that says it has read text that neither its person nor the project wrote.
  _Avoid_: taint (a taint set belongs to profiles).
- **Token family**: the access tokens and refresh tokens of one OAuth
  authorization of a client under one delegation. It takes the place of
  the MCP session, which the MCP revision 2026-07-28 removed.
- **Subject** and **actor**: on a delegated request, the subject is the
  person on whose authority the request runs. The actor is the agent that
  sends it (RFC 8693, ADR 205).

## Options

### Question 1: where the agent identity lives

- **1A. Keep act-as only.** The owner overruled the removal of the value,
  so this keeps the value with no producer. Issue #2321 stays open.
- **1B. A derived kind.** The server sets `principal.kind` to `agent` when
  a request has an agent reference. Not stored, so not listable. An agent
  with a personal token on the REST API still has no reference. No ceiling.
- **1C. An agent record with its own grants as live authority.** This does
  not model "for a person". Dusty would need grants as wide as the widest
  person, and a stolen credential of Dusty gets them. It breaks ADR 120
  (Dusty never has more access than its user). Vendors that ship it (a
  team token for the agent, a cluster-admin service account) draw
  complaints for it.
- **1D. An agent record whose grants are a ceiling, plus a stored
  delegation (proposal).** A delegated request carries both identities, and
  passes only when both pass, inside the scope of the delegation. This is
  the agreement of the sources.

### Question 2: what `principal.kind` means on a delegated request

This is the main one-way door. Gate authors store expressions that read the
fact.

- **2A. The subject.** No gate changes meaning, but a gate written for
  agents never matches. This is the gap of Entra Conditional Access in an
  on-behalf-of flow.
- **2B. The actor.** A limit "contractors never touch prod" lets the agent
  of a contractor through. It fails open.
- **2C. Both, with two evaluations (proposal).** Each check runs once as
  the subject and once as the actor, and passes only when both pass. Rules
  about the person still hold, and rules about agents match.

### Question 3: how an agent proves who it is

- **3A. Personal tokens (now).** The server cannot tell an agent from its
  person.
- **3B. Delegated tokens through OAuth (proposal).** An access token of the
  agent principal, bound to one delegation.
- **3C. A header that names the agent.** A hostile client leaves it out. It
  is not an identity.

### Question 4: who creates an agent principal

- **4A. An administrator registers each agent (proposal).** A person's
  first consent to an unknown client creates an access request to the
  administrators, not a record (decision 38).
- **4B. A person registers an agent at consent.** A phishing client gets a
  record from one click. Not proposed. It has no one-way door, so a later
  RFC can add it.

## Proposal

Options 1D, 2C, 3B and 4A. The decisions below are the design for the ADR.

### The record

1. **The kind.** `agent` is a principal kind (`identity.KindAgent`, in
   `identity.Kinds`), next to `person`, `service` and `workload`. An agent
   record has a name, 1 to 5 sponsors, a principal home (ADR 215), at most
   one client registration (decision 38), a creator and a time. It has no
   password, no passkey, no MFA factor, no console session and no sign-in
   method. `principal.Validate` refuses each of them for the kind `agent`.
2. **A ceiling only.** The grants of an agent never give access alone. The
   server refuses each credential of an agent that is not bound to a live
   delegation:
   - an access token that `principal:manage-tokens` would make (ADR 67
     decision 2): the server refuses to make it,
   - a trust rule that names an agent (ADR 54, ADR 70 decision 9): the
     server refuses to save it, and refuses each exchange that resolves to
     an agent,
   - a job token of a job with an agent and no delegation: the job model
     refuses the start.

   The authenticator also refuses, at the point of use, each stored
   credential of an agent with no live delegation. So a row that a restore
   or a bug makes stays useless. This is a defense-in-depth layer: it does
   not rely on the refusals at creation.
3. **No groups, no break-glass.** An agent is never a member of a group,
   never holds a protected role, and is never a break-glass account. Its
   ceiling is the set of its direct grants, on one page.
4. **Sponsors.** The record holds 1 to 5 sponsors (persons) and a fallback
   group. The sponsors get the notifications of the agent. When the last
   active sponsor is disabled or deleted, the server writes an audit entry,
   notifies the members of the fallback group, and flags the agent in the
   access review. If no new sponsor is set within the setting
   `agentSponsorGraceDays` (default 30, shown in the API), the server
   disables the agent with the reason `no-sponsor`. A sponsor who leaves
   does not end the delegations of other persons. A person who leaves ends
   their own (decision 16).
5. **Disable.** A disabled agent stops at once: each delegation to it, each
   of its tokens and its delegated work stop at the next resolve. An agent
   that collects refusals of decision 20 or of the ceiling at a rate above
   the installation setting `agentRefusalAlert` (default 3 in a row or 20 in
   one hour) notifies its sponsors and the persons whose delegations it
   used. The installation agent policy (decision 19) can choose to suspend
   the delegation in that case. A suspended delegation stays listable and
   revocable.
6. **Delete.** Deleting an agent ends each delegation to it, revokes each of
   its tokens, and removes its ceiling grants, in one transaction. Each
   removal writes an audit entry. No grant of a deleted agent stays behind.

### Dusty

7. **Dusty is an agent principal** with the fixed name `dusty`. The model
   connect request creates it, with the connecting administrator as its
   creator and first sponsor. ADR 120 decision 15 ("Dusty is not a
   principal") no longer holds.
8. **A narrow default ceiling.** The connect request also creates Dusty's
   ceiling grants, which the administrator chooses. The API offers, and
   shows before the connect:
   - the built-in role `agent-tools-read` on the installation,
   - the built-in role `agent-tools` on no namespace. The administrator
     picks the namespaces where Dusty may propose and operate.

   The code defines the roles. The grants are stored, made by the
   administrator, and can be narrowed or revoked like every grant.
9. **A run names a delegation.** A person who wants to use Dusty creates a
   delegation to `dusty` once (decision 11). Each run names it. A run
   starts from a console session or from a personal access token, never
   from a delegated token (decision 20). The run records the delegation ID
   next to the credential ID and resolves both at each step and before each
   tool call. The limits of a run (ADR 120 decision 16) narrow further. The
   run still ends with its credential (ADR 120 decision 14).
10. **Dusty has no client registration** and no delegated token. A run is
    its only credential path. The credential of a Dusty run is
    `agent-run` (decision 44).

### Delegation

11. **The record.** A delegation holds the person, the agent, the mode, the
    expanded scope (decision 13), a creator (always the person), a time,
    an expiry, the client authentication (`public`, `confidential` or
    `server` for Dusty) and the audiences (decision 37). Only a person
    delegates. A service principal, a workload or an agent never delegates.
    Chains are not allowed: an agent never acts for an agent (RFC 8693
    section 4.1 gives nested actors no weight).
12. **Three modes.** The consent page offers three modes, not a blank form:

    | Mode | The agent can | Each change |
    | --- | --- | --- |
    | `read` | Use the read actions of its ceiling. | None: no change is possible. |
    | `propose` | Read, plan, draft a change order (ADR 344), open a review request. | The change goes to review. The agent starts no apply. |
    | `operate` | Also start an apply, a destroy or a refresh through a job, and ask for other writes of its ceiling. | Each job waits for one confirmation of the person (decision 24). Each other write is a held request (decision 23). |

    `read` is one button on the consent page. The installation agent
    policy (decision 19) chooses which modes a person can pick. No mode
    skips the subject part, the ceiling, decision 20 or a confirmation.
13. **The scope is stored expanded.** The scope has three parts:
    - the objects: a list of namespaces, or all namespaces, and a separate
      flag `installation`. All namespaces never covers an action whose
      object is the installation (`@server`) or a principal. Only
      `installation: true` covers it. Both are `false` by default. The
      consent page never preselects them from a client request, and the
      server never offers `installation: true` to a public client.
    - the actions: the mode expands to a list of catalog actions when the
      person consents. The server stores the list, not the mode name alone.
      A later change of what a mode means never widens an existing
      delegation.
    - the audiences (decision 37).

    The wire form of a scope request is an RFC 9396
    `authorization_details` entry with the type `opendustpan_delegation`.
    The intersection rule stays ours.
14. **Only in a console session, with a fresh authentication.** A person
    creates, renews or widens a delegation only in a console session, with
    a fresh authentication (ADR 227), on the consent page. The page shows
    the agent, its sponsors, its client type, its ceiling, the mode, the
    scope, the audiences and the expiry. A break-glass session cannot
    create a delegation, and a request from a break-glass session cannot
    use one. So no part of an agent call ever holds break-glass rights.
    This supersedes in part ADR 125 decision 15: a Dusty run cannot start
    in a break-glass session. A temporary administrator grant (ADR 216
    decision 12) widens the subject part of a live delegation for its
    lifetime, by design of the intersection, and the consent page and the
    docs say so.
15. **Lifetime and renewal.** The default lifetime is 30 days and the
    maximum is 90 (settings that the API shows. Open question 3 asks about
    public clients). The server notifies the person and the sponsors 7
    days and 1 day before the expiry, with the list of delegated work that
    will stop. Renewal extends the **same** delegation ID, in one click on
    the consent page, with a fresh authentication, with the same or a
    narrower scope. Each renewal writes an audit entry. A delegation never
    renews by itself.
16. **The ends.** A delegation and its work stop at the next resolve when
    any of these happens: the person revokes it, it expires, the agent is
    disabled or deleted, or the person is disabled, deleted, removed by
    SCIM or terminated by the identity provider (ADR 70 decision 19). Each
    end writes an audit entry with the cause.
17. **List and revoke.** The person lists their delegations, with the
    delegated work and the standing objects made under each one, and
    revokes each one. An administrator with `server:manage-access` lists
    and revokes every delegation, and the access review exports them.
18. **A delegation is not an act-as edge.** ADR 70 decision 1 computes
    reach as a union with the grants of each principal that a principal can
    act as. A delegation is a new edge kind:
    - It adds nothing to the reach of the agent. The effective reach of a
      delegated request is the intersection of the person, the ceiling and
      the scope.
    - It is not an act-as right of ADR 70 decisions 2, 3 and 13.
    - It does not make a group protected, because an agent is never a
      member of a group.
    - The access review shows each delegation as its own row: person,
      agent, mode, scope, expiry, last use.
19. **The installation agent policy.** One stored policy of the
    installation, read through the API and changed with
    `server:manage-access`, sets:
    - the modes that a person can pick, for each client type,
    - the maximum lifetime, for each client type,
    - the maximum ceiling of a public client (a lock, not only a default),
    - the namespaces that no delegation can cover,
    - whether the REST API audience can be offered (decision 37),
    - whether DPoP is required for public clients (decision 39),
    - whether a burst of refusals suspends a delegation (decision 5).

    The policy only narrows. The server enforces it at each check, so a
    change narrows existing delegations at once. Each change writes an
    audit entry. The API shows the effective value of each item.

### What no agent does

20. **A fixed list, with no flag.** A rule in the catalog of code rules
    refuses these actions for each delegated request and each piece of
    delegated work, with `ErrAgentDenied` and a denial in the audit log. No
    mode, scope, ceiling, policy or administrator can allow them:
    - grants, roles, groups and memberships,
    - access tokens: create, rotate or revoke,
    - trust rules,
    - principals: create, change, disable, delete, including agents,
      ceilings, sponsors and client registrations,
    - delegations: create, renew, widen or revoke (only the person, in the
      console),
    - gates, approval rules, access limits and the installation agent
      policy,
    - sign-in settings, identity providers, SCIM and MFA settings,
    - break-glass,
    - each action of the operator family,
    - a run start of Dusty, and each route that starts work under another
      delegation, for example a Catalog request on behalf of another agent.

    The `accessAdministration` flag of round 2 is removed. An agent can
    describe an access change in text, and the person makes it. A later RFC
    can propose access changes as drafts that a person confirms.
21. **Policy never comes from what the agent reads.** No scope, gate,
    ceiling or policy comes from content that the agent reads: a stack
    file, a module, a repository or a tool result. No scope or gate
    matches free text that the agent writes, such as a command line, a
    script body or a URL. Rules match typed catalog actions and objects.

### Held requests and confirmations

22. **Ask, wait, retry.** When a delegated request needs the person (a
    step that needs a fresh authentication, a write that the mode
    `operate` holds, a standing object of decision 32, a secret consent of
    ADR 124), the server does not only refuse. It answers with a held
    request ID and the console link. The agent waits, then retries. The
    person answers in the console.
23. **The held request.** A held request stores the delegation ID, the
    agent, the subject, the exact action and object, a digest of the
    arguments (or of the plan), a summary that the server computes, the
    agent's text, a creation time, an expiry (default 1 hour, at most 24,
    a setting that the API shows) and a state (`pending`, `confirmed`,
    `refused`, `expired`, `used`). Its rules:
    - Only the subject confirms it, in a console session with no agent and
      a fresh authentication, as for the confirmation step (ADR 128
      decision 5).
    - It is single use. The retry must carry its ID and match the digest.
      The server then decides again from the start, with the confirmation
      as one more input. A confirmation is never a credential.
    - It ends with its delegation. A confirmed, unused held request of an
      ended delegation is never used.
    - A confirmation never widens the delegation and never writes a rule
      for later requests. There is no "do not ask again".
    - The agent creates it and reads its state. It cannot confirm, refuse
      or change it.
    - Each create, confirmation, refusal, expiry and use writes an audit
      entry, and the person can list their held requests.
24. **One confirmation per job.** A change through a job keeps the
    confirmation step of ADR 125 decision 5: one confirmation for the job,
    bound to its plan, not one for each step. A change order that an agent
    drafts waits for the confirmation of ADR 344. The held request of
    decision 23 is for writes outside a job.
25. **What the person sees.** The confirmation page of a job and the page
    of a held request show, in this order:
    1. the facts that the server computed: the action, the units and the
       namespaces, the counts of creates, updates, replacements and
       destroys, the changed protection flags, each unit or namespace that
       the original request did not name, and the sources of untrusted
       input (decision 28).
    2. the agent's text, in a separate box with the label "Written by the
       agent. It can repeat text that the agent read."

    For a destroy, or a replacement of a stateful resource, the person
    types a fact that the page shows (open question 4).
26. **Four-eyes sees the person.** On a change that an agent proposes for a
    person, the person is the requester, the author, `Job.StartedBy` and
    the proposer of a change order. Each approver exclusion names the
    person: with `authorMayApprove: false` (the default of ADR 225), the
    person cannot be an approver of that change. "Dusty proposed, Alice
    approved" never counts as two people. The sponsor of the agent is not
    excluded. Each gate kind with an approver rule gets a test for this.
27. **The agent never holds step-up.** An agent never has a fresh
    authentication. A step that asks for one (ADR 56, ADR 227) makes a held
    request. It never passes for the agent.

### Untrusted input

28. **The mark.** The server sets `untrustedInput` on a Dusty run when a
    tool result carries text that neither the person nor the project wrote.
    Each catalog tool declares whether its results can carry such text, and
    the catalog test fails for a tool with no declaration. The list of ADR
    124 decision 13 gets `read_audit_log`, `explain_access` and
    `get_change`. The run records each source (the tool, the object and the
    author). The mark never clears during a run.
29. **An outside client is marked from its first call.** The server cannot
    see what an outside client read elsewhere. So each request of a token
    family carries `untrustedInput: true`.
30. **The rule.** A request or delegated work with `untrustedInput` cannot
    act directly:
    - a job that it starts cannot use `direct` (ADR 125 decision 3). The
      job falls back to `approve-first`, and the answer says why,
    - a write of the mode `operate` outside a job is always a held request.

    The mark is a fact (`delegation.untrustedInput`), so a gate can also
    read it. Dusty in a run with no untrusted input keeps every mode that
    the three switches of ADR 125 allow.

### Delegated work

31. **Work carries the delegation.** A job and its job token, a run trigger
    (ADR 267), a drift schedule (ADR 263), a Catalog request, a scheduled
    change and each later kind of stored starter record the delegation ID
    and the mark of untrusted input when a delegated request makes them. A
    conformance test fails when a starter type has no delegation field. At
    each later evaluation (`change.planned`, `change.apply`, `state.write`
    and each call with the job token), the server resolves the delegation
    and runs both parts.
32. **Standing objects need a confirmation.** A run trigger, a drift
    schedule or a scheduled change that a delegated request creates is a
    held request: it exists only after the person confirms it. The
    delegation page lists them.
33. **Work fails closed when the delegation ends** (decision 16):
    - a job that waits, or has not started a phase that changes, is
      canceled with the stop reason `delegation-ended`,
    - a running phase gets the refusal at its next call with the job
      token (open question 2 asks about a confirmed apply),
    - a trigger or a schedule is disabled with the same reason.

    The confirmation of the person does not take the authority over. The
    job keeps its actor part after the confirmation, and the decision
    record names the confirmation too.
34. **Rebind or take over.** The person can rebind a disabled trigger or
    schedule to a live delegation to the same agent (a held request), or
    take it over as their own work with no actor part (in the console, with
    a fresh authentication). Each writes an audit entry.

### Agent rules key on the delegation

35. **`delegation.present`, never the channel.** Each agent rule applies
    when `delegation.present` is true, whatever `source.kind` says: the
    agent checks of the job model (`job.CheckAgent`), the confirmation
    step, the `direct` switches, "a destroy is never `direct`", "no propose
    after a reveal", the secret gate and the fixed list. An outside client
    on the REST API gets the same rules as Dusty and the MCP endpoint.
36. **The guide of gates** says: to match agents, read `principal.kind ==
    "agent"` or `delegation.present`. `source.kind == "agent"` is a channel,
    not an identity.

### Clients and tokens

37. **Delegated tokens and audiences.** A delegated token is an access
    token of an agent, bound to one delegation and one token family. It
    lives 1 hour by default (ADR 53 token policy). A refresh token never
    outlives its delegation. Its audience is the MCP resource by default
    (RFC 8707). The tool-neutral REST API is a second audience that the
    person sees and picks at consent, when the installation agent policy
    allows it. The Pulumi Cloud-compatible API, the TFE-compatible API and
    the OpenTofu HTTP backend never take a delegated token: an agent
    changes state only through a job. The server is the OAuth 2.1
    authorization server, with Protected Resource Metadata (RFC 9728),
    PKCE and `iss` in the response. The token exchange of ADR 284 stays a
    separate endpoint.
38. **The registration binds the client.** An agent record holds one client
    registration:
    - **Confidential**: `private_key_jwt` (RFC 7523) against a JWKS that the
      registration holds or names. Each refresh needs the key too.
    - **Public**: an MCP client on a laptop. The registration holds the exact
      redirect URIs (loopback with an exact path, RFC 8252) and, for a
      Client ID Metadata Document, its exact URL and host. Each audit entry
      and console view names a public client "as the client claims", and
      the API answers `clientAuth: public`.

    A person's consent to an unknown client creates an access request to
    the administrators, not a record. One registration then serves every
    person.
39. **Public clients.** A public client gets:
    - refresh token rotation with reuse detection: a reused refresh token
      revokes the whole token family and writes an audit entry,
    - DPoP-bound tokens (RFC 9449), required by default through the
      installation agent policy, which can set it to optional for clients
      that lack it, and shows the effective value,
    - the default ceiling `agent-tools-read`, with no audit or access-map
      tools (`read_audit_log` and `explain_access` are in a separate role,
      `agent-tools-audit`),
    - a consent page that starts with no namespace picked and offers `read`
      first.
40. **Fetches are egress-checked.** The server fetches a JWKS URL and a
    Client ID Metadata Document only through the egress rules
    (`pkg/egress`, `internal/egress/egresshttp`): HTTPS only, a check of the
    resolved IP address when the connection opens, a size limit of 64 KiB,
    a timeout of 5 seconds, and a cache with a maximum age. A failed fetch
    refuses the authorization. Each layer gets its own test.
41. **The MCP endpoint takes delegated tokens only**, from the slice that
    adds OAuth. It then refuses personal tokens and service tokens, as the
    MCP specification requires. Until then, the endpoint keeps ADR 123
    decision 3, and the docs name this as a known deviation. No setting
    turns the old behavior back on, because no tag shipped it.
42. **No MCP session ID.** The MCP revision 2026-07-28 removed protocol
    sessions. The agent reference, the `agent/2` block and the `act` claim
    name the token family for a client, and the run for Dusty, plus a
    request ID for each call.

### Facts and evaluations

43. **Two evaluations.** The server evaluates each access check, each
    access limit and each gate twice for a delegated request and for
    delegated work: once **as the subject** (`principal` is the person,
    with the grants, groups and limits of the person) and once **as the
    actor** (`principal` is the agent, with the ceiling and the limits of
    the agent). The scope of the delegation is checked once, before both.
    The request passes only when every check passes. Gate outcomes add up,
    and a gate that fires in both parts gives one requirement (by gate ID).
    No outcome relaxes another (ADR 222 decision 5).
44. **The facts of each part.**

    | Fact | Subject part | Actor part |
    | --- | --- | --- |
    | `principal.kind` | the record kind of the person (`person`) | `agent` |
    | `principal.id`, `principal.name` | the person | the agent |
    | `principal.credential` | `delegated-token`, `agent-run` (Dusty) or `job-token` (delegated work) | the same |
    | `principal.groups` (access limits) | the groups of the person | empty |
    | authentication age (access limits) | not available | not available |
    | `delegation.present` | `true` | `true` |
    | `delegation.id` | the delegation ID | the same |
    | `delegation.subject` | the ID and the kind of the person | the same |
    | `delegation.actor` | the ID of the agent | the same |
    | `delegation.mode` | `read`, `propose` or `operate` | the same |
    | `delegation.clientAuth` | `public`, `confidential` or `server` | the same |
    | `delegation.channel` | `agent`, `mcp`, `mcp-proxy` or `api` | the same |
    | `delegation.untrustedInput` | `true` or `false` | the same |

    A request with no delegation has one part, with `delegation.present`
    `false` and empty members. The credential of a delegated request is
    never `session`, also when a person started a Dusty run in the console.
    So a gate "only a person in the console", written as
    `principal.credential == "session"`, refuses every agent.
45. **Where an allow came from.** Each part of the decision record names the
    source of each allow: `grant` (a grant ID of the person), `ceiling` (a
    grant ID of the agent), `scope` (the delegation ID and its mode), or
    `held-request` (the held request ID and its confirmation ID), and
    `confirmation` for a job. The answer to "why was this allowed" names
    them. The record has one part for a request with no delegation, and two
    parts for a delegated one.
46. **`principal.kind` and `source.kind`.** `principal.kind` is **who**: the
    record kind of the identity in the evaluation, from the credential and
    the stored record. A client cannot set it. `source.kind` is **where
    from**: the path through which a change started.

    | Request | Subject part | Actor part | `source.kind` |
    | --- | --- | --- | --- |
    | Alice in the console | `person` | none | `api` |
    | Alice with a personal token, `tofu apply` | `person` | none | `cli` |
    | Dusty for Alice, `propose_change` | `person` | `agent` | `agent` |
    | The job of that change, at `change.apply` | `person` | `agent` | `agent` |
    | An MCP client for Alice, through the MCP endpoint | `person` | `agent` | `agent` |
    | An MCP client for Alice, with its token on the REST API | `person` | `agent` | `api` |
    | A program that uses Alice's personal token | `person` | none | `api` or `cli` |
    | A service token | `service` | none | as now |

47. **The limit of the fact.** No server can tell a program that uses a
    personal token from its person. The gate guide and the gate editor say
    this in the text of the fact, with an example, and give the rule that
    holds for both: a gate that must allow only a person at the console
    uses `principal.credential == "session"`. A mark "not for agents" on a
    personal token is not proposed: a client declares nothing that the
    server can check.
48. **Unknown kinds fail closed.** Each switch on the kind names every
    case:

    | Principal | Kind | At a gate |
    | --- | --- | --- |
    | The bootstrap admin token | The authenticator sets `person`, with the credential `access-token`. | as a person |
    | An embedder's authenticator (ADR 35) that leaves the kind empty | empty | refused, with a denial in the audit log. The error names the kind field that the authenticator must set. |
    | Any authenticator with a kind that the server does not know | the value | refused, with a denial |
    | Any authenticator outside the server that gives `agent` | `agent` | refused: only the server makes an agent credential |

    This follows issue #2321 step 2: an unknown value never counts as
    `person`. It changes ADR 240 decision 4 for gates. An embedder sets the
    kind in one line, and no tag shipped the old rule. ADR 240 decision 4
    stays for the choice of the audit entry of a download.
49. **Schema versions.** The new values and the new group are additions,
    a minor version of each schema (ADR 226 decision 3). The meaning of
    `principal` on a delegated request is fixed in the first version that a
    tag ships, so no major version is needed.

### Secrets

50. `identity.Principal.ActsAsPerson` answers false for a delegated
    request, so ADR 240 decision 1 does not apply. A state download by an
    agent is refused unless the secret gate of ADR 124 allows it, and then
    it writes `reveal` with the subject and the actor. The consent of ADR
    124 decision 10 becomes a held request bound to the delegation and the
    unit. The person types the unit name, not "I accept".

### Audit and issued tokens

51. **Audit.** The subject of an entry stays the person (ADR 126 decision
    2). The agent block becomes `agent/2`: the fields of `agent/1` without
    the MCP session, then the agent ID, the delegation ID, the mode, the
    token family or the run, the credential kind, `clientAuth` and
    `untrustedInput`. Agent audit is always on. The audit query gets
    filters for the agent and for the delegation ("everything Dusty did").
    Each delegation create, renew, use, revoke, expiry and end, each held
    request event, each agent create, disable, delete, sponsor and ceiling
    change, and each refusal of decisions 2, 20 and 48 writes an entry.
52. **Issued OIDC tokens** (ADR 205), for a job of delegated work:
    - `actor_type` stays the kind of the person.
    - `act` gets `sub: agent:<agent principal ID>`. The run or the token
      family moves to a member of its own inside `act`.
    - `sub` gets a new key `agent`, at the end of the key order (ADR 205
      decision 6). Its value is the name of the agent, `@` and its ID. The
      claim of the same name is `agent`, so the Google Cloud CEL mapping can
      read it. The server leaves the key out of a job with no agent.
    - The default subject template gets the key `agent`. An exact trust
      policy written for a person's jobs then does not match an agent job
      (fail closed). An administrator can give agent jobs a narrower cloud
      role, which matters because `tofu plan` and `pulumi preview` run
      provider code.
    - The 600-character check of ADR 205 decision 6 counts the `agent` key
      for every template. The docs show trust policy examples for both
      forms, and say that each agent adds subjects for each unit against
      the Azure limit of federated credentials.

### Notes for the console repository

None of these is a one-way door.

- A principal list with the kind `agent` and its sponsors.
- A page of an agent with its ceiling, its client type and its delegations.
- "Agents that act for you" for each person, with mode, scope, expiry,
  standing objects, renew and revoke.
- A consent page with three mode buttons, `read` first.
- A queue of held requests, with the server facts first and the agent text
  in a labeled box.
- "Alice through Dusty" on each history item, job, audit row and approval,
  with a link to the run.
- The two parts and the allow sources in the gate editor and in "why".
- Dusty task modes in the chat (ask before plan, ask before apply, or no
  chat asks) as a UX setting on top of the server rules. The page says that
  these modes give no access.

## What this RFC rejects

| Rejected | Why |
| --- | --- |
| Impersonation, token passthrough, a header that names the agent | The server cannot tell the agent apart, or the client declares it (RFC 8693 section 1.1, MCP "MUST NOT accept or transit"). |
| An agent with standing authority of its own (1C) | It drops the person, and a stolen credential gets the full grants. |
| A bypass, "yolo" or "skip everything" delegation | Harnesses offer it for a disposable machine. A delegation reaches shared infrastructure and state. |
| `accessAdministration` or any flag that opens decision 20 | It lets a hijacked agent mint a credential outside the delegation or widen a third party. |
| A classifier or a second model as an authorization control | Vendors say it is not a boundary. It is not a stored, listable rule. It can add a signal to a confirmation page later. |
| "Do not ask again" for a session | A third lifetime that nobody can list or revoke. Use the delegation, or a single-use held request. |
| Rules on command text, scripts or URLs | Every vendor's allow list on shell text was bypassed. Rules match typed actions. |
| Policy from content that the agent reads | Configuration in a repository is the most common way in for harness attacks. |
| Capability tokens (Biscuit, macaroons, UCAN) as the authority | They make permissions that the server never stored and cannot list, and they need server state to revoke anyway. |
| A hidden agent: "you only" attribution, a setting that removes the actor | Each audit entry, history item and `act` claim names the agent. Display branding is fine. |
| Auto-registration of a client at consent (4B) | A phishing client gets a record from one click. |
| Admin policy as the only source of a delegation | It drops the person's stored, revocable statement. The ceiling and the installation agent policy give the administrator control. |

## Friction budget

The owner asked that the feature does not suck. These are all the prompts
that a person meets, and how often:

| Prompt | When | Cost |
| --- | --- | --- |
| Consent | Once for each agent, then one click to renew every 30 days. `read` is one button. | Low |
| Confirmation of a job | Once for each change, with the plan on the page. People read a plan. They wave through single steps. | Medium, the main barrier on purpose |
| Held request | A write outside a job in mode `operate`, a standing object, a step that needs a fresh authentication, a secret consent | Rare |
| Typed fact | A destroy or a stateful replacement only (open question 4) | Rare |
| Nothing | Every read in mode `read`, every plan, every proposal in mode `propose` | None |

The agent never fails a step that needs the person: it makes a held
request and waits. Each refusal names the console page and the missing
piece (a delegation, a scope, a ceiling, a confirmation).

## ADRs that this touches

| ADR | Effect |
| --- | --- |
| ADR 35, ADR 240 decisions 1, 3 and 4 | The bootstrap token sets `person`. An empty or unknown kind fails closed at gates. `ActsAsPerson` is false for a delegated request. |
| ADR 52 decisions 1, 3 and 4 | A fourth record kind. An agent holds ceiling grants only and is never a member of a group. |
| ADR 53 | Delegated tokens, their lifetime, rotation and DPoP. No token of an agent without a delegation. |
| ADR 54 | A trust rule never names an agent. |
| ADR 67 decision 2 | `principal:manage-tokens` makes no token of an agent. |
| ADR 70 decisions 1, 2, 3, 6, 9, 13 and 19 | A delegation is a new edge kind with intersection semantics. "Changes ADR 70 decision 13": the word "delegation" there becomes "an act-as right". The GLOSSARY entry changes in place. |
| ADR 120 decisions 13 to 16 | Dusty becomes an agent principal. A run names a delegation. |
| ADR 122 decision 4, ADR 123 decision 3 | The MCP endpoint takes delegated tokens only, after the OAuth slice. Each tool declares whether its results carry untrusted input. |
| ADR 124 decisions 10 and 13 | The consent is a held request bound to the delegation. Three more injection inputs. |
| ADR 125 decisions 3, 5, 9 and 15 | No `direct` with untrusted input. Agent rules key on the delegation. Supersedes ADR 125 decision 15 in part: no run in a break-glass session. |
| ADR 126 decisions 2 and 10 | The `agent/2` block. |
| ADR 128 decisions 1, 5 and 17 | `job.AgentRef` gets the agent, the delegation and the token family. The confirmation does not take the authority over. |
| ADR 190 decision 14, ADR 344 | The proposer of an agent's change order is the person. |
| ADR 205 decisions 3 and 6 | The content of `act`, the `sub` key `agent`, the claim `agent` and the default template. |
| ADR 222 decision 5, ADR 223 decision 4, ADR 224, ADR 225, ADR 226 decisions 3 and 4 | Two evaluations, the allow sources, the new facts, and approver exclusions name the subject. |
| ADR 227 | An agent never has a fresh authentication. |
| ADR 263, ADR 267 | Triggers and drift schedules carry the delegation, and need a confirmation when an agent makes them. |
| ADR 284 | The authorization server sits next to the token exchange. |
| ADR 297 decision 19 | The run resolves its delegation with the sign-in. |

The accepting ADR names each of these in its "Changes" list, and each one
gets the reverse item (ADR 296).

## One-way doors

These are hard to change after the first tag:

1. **The meaning of `principal.*` in each part of a delegated evaluation**
   (decision 44). A later change to 2A or 2B would change the outcome of
   stored gates with no edit.
2. **The values** `agent`, `delegated-token`, `agent-run`, the modes `read`,
   `propose` and `operate`, and the names of the members of the group
   `delegation`. Stored gates name them.
3. **The shape of the decision record**: one or two parts, and the allow
   sources (decision 45).
4. **The value `agent` in principal rows and in the audit hash chain**, and
   the `agent/2` block. They are inside the hash of each entry.
5. **The subject of Dusty**: the name `dusty` in audit entries, history
   items and blame. Branding (ADR 235) changes only the display name.
6. **The claims of an issued token**: `actor_type`, `act`, the `sub` key
   `agent`, its place in the key order, and the claim `agent`.
7. **The OAuth surface**: the issuer, the metadata documents, the token
   prefix, the `authorization_details` type and the scope names.
8. **The role names** `agent-tools`, `agent-tools-read` and
   `agent-tools-audit`, if grants store a built-in role by name.
9. **The held request**: its states, its link to a delegation and the rule
   that a confirmation is single use.

Two-way doors: the content of the roles and of the modes (a delegation
stores its expanded scope), the lifetimes, the policy defaults, the console
copy, 4B and the refusal alert.

### Before the first tag

Milestone 10 holds the code, and it is not in the gate of the first release.
These small changes land before the first tag, so that no tag ships a shape
that this design must break:

1. The fact schema reserves the group `delegation` and documents the
   two-part meaning. `principal.kind` never takes `agent` until slice 1.
2. The decision record has a list of parts and an allow source, with one
   part for every decision today.
3. The `sub` key order of ADR 205 reserves the key `agent` at the end, and
   `act` drops the MCP session.
4. The bootstrap token sets `person`, and an empty or unknown kind fails
   closed at gates.
5. The `agent/1` block drops the MCP session, so that `agent/2` adds fields
   only.

## Implementation slices

Each slice is a separate pull request, after the ADR. Slice 0 comes before
the first tag. Slices 1 to 8 are milestone 10.

0. The shapes of "Before the first tag", with tests.
1. The record kind, decision 2, sponsors, delete and the tests of each kind
   in decision 48.
2. Delegations: the store, the conformance suite, the API, modes, the
   expanded scope, renewal, the ends, the installation agent policy, the
   fixed list of decision 20 with a test for each item, the audit entries.
3. Two evaluations in the gate engine and in access limits, the decision
   record with allow sources, the approver exclusions, and delegated work.
4. Held requests: the store, the API, the console link, the audit.
5. Untrusted input: the tool declarations, the mark, the rule of decision
   30.
6. Dusty as an agent principal, and a run that names a delegation.
7. OAuth for the MCP endpoint: delegated tokens, client registrations,
   rotation, DPoP, egress-checked fetches, access requests for unknown
   clients.
8. The `sub` key `agent` and the `act` content in issued tokens.
9. The console pages (in the console repository).

The guide `docs/guides/state-write-gates.md` and the user documentation of
agents change in the slice that first sets each behavior (ADR 214).

## Review findings and their resolution

### Round 1 (head 38491b1)

| Finding | Resolution |
| --- | --- |
| B1 Ceiling grants were also live authority | Decision 2: ceiling only. No token, trust rule or job token of an agent without a delegation. A check at the point of use. |
| B2 Delegated work outlived the delegation and lost the actor | Decisions 31 and 33: work records the delegation, both parts run at each later evaluation, and work fails closed. |
| M3 Delegation as an act-as edge | Decision 18: a new edge kind with intersection semantics, no reach, no protection. |
| M4 Facts of each part not defined | Decision 44: full table, with `principal.id` and `principal.name`. The credential is never `session`. |
| M5 `principal.kind == "agent"` misses agents with personal tokens | Decisions 36 and 47: the limit is in the gate guide and the editor. A gate for the console uses `principal.credential == "session"`. No "not for agents" mark. Decision 41 closes it on the MCP endpoint. |
| M6 Weak MCP client identity | Decisions 38 to 40: exact registrations, "as the client claims", DPoP, rotation, a narrow default ceiling. |
| M7 A delegated request could change access | Decision 20: a fixed list with no flag. |
| M8 Contradictions in delegation creation and break-glass | Decisions 9 and 14: only a console session with a fresh authentication creates a delegation. No break-glass part. |
| M9 Agent jobs invisible to cloud trust policies | Decision 52: the `sub` key and claim `agent`, and the default template. |
| M10 Unknown kind breaks the bootstrap token and embedders | Decision 48: the bootstrap token sets `person`. Embedders set the kind. |
| M11 A disabled person did not end delegations | Decision 16. |
| m12 Over-design | Removed: an agent on its own authority, 4B, a group as sponsor in the first version (a fallback group only notifies), the show-once automation token. Console items are notes. |
| m13 Incomplete one-way doors | "One-way doors" items 2, 3, 6, 7 and 8. |
| m14 Overstated benchmarks | "Where the sources agree" item 3 excludes impersonation. The MCP deviation is named in decision 41. |
| m15 The word "delegation" collides with ADR 70 decision 13 | "ADRs that this touches": a "Changes" item in the new ADR. Only the GLOSSARY changes in place. |

### Round 2 (head 1cca162)

| Finding | Resolution |
| --- | --- |
| B1 `accessAdministration` lets an agent mint a credential outside the delegation | Decision 20: the flag is removed, and the list also covers principals, gates, approval rules, the agent policy and delegations. |
| B2 A delegated token can start a Dusty run | Decisions 9 and 20: a run start, and each route that starts work under another delegation, refuses a delegated request. |
| M3 Self-approval through the actor part | Decision 26: the requester, the author, `Job.StartedBy` and each approver exclusion name the person. |
| M4 The empty kind counts as a person at gates | Decision 48: the empty kind fails closed at gates, as issue #2321 asks. |
| M5 No renewal, so work dies silently | Decisions 15 and 34: the same delegation ID renews in one click with a fresh authentication. Warnings at 7 days and 1 day list the work. Rebind or take over. |
| M6 Public-client refresh, SSRF in fetches, flag defaults | Decisions 13, 39 and 40: rotation with reuse detection, DPoP, egress rules with limits and a test for each layer, `installation` false and never preselected. |
| m7 Break-glass wording and temporary admin | Decision 14: "supersedes in part ADR 125 decision 15", and the temporary administrator effect is stated. |
| m8 Dusty has no client registration | Decision 10, and the credential `agent-run` in decision 44. |
| m9 Each later starter must carry the delegation | Decision 31, with a conformance test. |
| m10 `principal.id` and `principal.name` per part | Decision 44. |
| m11 Details of the `sub` key | Decision 52: the 600-character count, the claim name, both policy examples and the Azure limit. |
| m12 ADR 70 cannot be edited | "ADRs that this touches": a "Changes" item, not an edit. |

## Open questions

Each question stands alone. Each has a recommendation, so the owner can
accept the proposal as it is.

1. **Can Alice confirm the change that her own agent proposed?**
   - ELI5: Dusty writes a change for Alice. Is Alice's own "yes" enough, or
     must another person say yes?
   - A (recommended): Alice confirms, as the confirmation step of ADR 125
     and ADR 128 says today. Four-eyes comes from a stored approval rule:
     with `authorMayApprove: false`, Alice counts as the author, so a
     second person must approve (decision 26). A one-person installation
     keeps working.
   - B: never Alice. Another person always confirms an agent's change, as
     GitHub Copilot does for its pull requests. The draft of milestone 10's
     exit test (step 3) says this. It blocks every agent change on a
     one-person installation.
   - With A, the exit test step 3 runs with a two-person approval rule on
     its namespace.
2. **A running apply when the delegation ends.**
   - ELI5: Alice confirmed an apply, and it is half done when her note is
     torn up. Stop now, or finish this step?
   - A (recommended): a phase that the person confirmed finishes, because
     the person saw and allowed that exact plan. No new phase starts. Every
     phase that was not confirmed stops at its next call.
   - B: every phase stops at its next call, as a cancel does. An apply can
     stop half done.
3. **The lifetime of a delegation for a public client.**
   - ELI5: a note for a program on a laptop is easier to steal. Should it
     expire sooner?
   - A (recommended): 7 days by default and 30 at most for a public client,
     30 and 90 for Dusty and confidential clients. Renewal is one click.
   - B: 30 and 90 for every client.
4. **A typed fact for the riskiest changes.**
   - ELI5: for a delete, Alice types the name of the thing she deletes,
     not just "yes", so that she reads the page.
   - A (recommended): for a destroy and for a replacement of a stateful
     resource that an agent proposed, the person types the unit name or
     the count of destroys that the page shows. Every other confirmation
     stays one click.
   - B: one click for every confirmation.
5. **A narrower task scope for each run.**
   - ELI5: Alice asks Dusty about one namespace. Should Dusty then be kept
     to that namespace for this conversation, even though the note allows
     more?
   - A (recommended for later): no derived narrowing in the first version.
     The mark of untrusted input, the held requests and the page fact "a
     unit you did not ask about" (decision 25) cover the risk, and a wrong
     guess would make Dusty refuse real work.
   - B: each run narrows itself to the namespaces of the objects that the
     person named, widens only while it has no untrusted input, and records
     the narrowing on the run.

## Notes

- The GLOSSARY entry **Principal kind** says that a token from a trust rule
  gives `service`. ADR 240 decision 3 and `identity.KindWorkload` say
  `workload`. The ADR of this RFC can fix the entry.
- Review round 1 found two blockers, and round 2 found two more. "Review
  findings and their resolution" answers each finding of both rounds.
- Round 3 adds research on standards, identity vendors, infrastructure
  platforms, agent harnesses and threats. "Benchmarks" cites the public
  sources.

## Checklist

- [x] Tolerates asynchronous replication between installations: each namespace writes only its own half, under its own fence, and no write waits for another installation (ADR 248 decisions 3, 6 and 9). Agent principals, delegations, held requests and the installation agent policy are installation records, as persons and service principals are. A delegation is checked at the point of use in the namespace of the action, and needs no write in another installation. The design adds no cross-namespace write.

## Outcome

Empty until the RFC is accepted (link the ADR) or dropped (say why).
