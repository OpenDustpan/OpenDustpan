---
rfc: 122
title: Gates at every step of a plan that runs untrusted code with a privileged credential
status: discussing
created: 2026-11-07
---

# RFC 122. Gates at every step of a plan that runs untrusted code with a privileged credential

Status: discussing. Design only. An open question for the project owner.
Until the owner decides, `main` keeps the rules of the ADRs that this RFC
names. This RFC depends on ADR 341 and ADR 347, which are in pull request
#2562. Merge this RFC after that pull request.

This RFC supersedes the open question about scanners as a step of the plan
job. That question was a conflict between the owner's "yes" to scanners at
the plan step and two earlier rules: [RFC 49](0049-run-tasks.md) (the owner
direction "jobs do not run anything except IaC code") and ADR 226
decision 10 ("an external check is never a job step"). The owner did not
decide that conflict directly. The owner described the problem below and
asked for gates and policies at every step, as far as is possible and
logical. On 7 November 2026 the owner added: "having steps run on the
runners isn't an issue, as long as the gating happens on the trusted side
(our backend)." Section "The scanner question" gives the answer that this
RFC proposes, and how it amends ADR 226 decision 10.

## Problem

### The persona

A platform team runs a self-service Pulumi stack:

- Users of other teams configure their own resources. They open a review
  request that adds or changes a manifest (a data file, for example YAML)
  in the repository of the stack.
- A code owners file on the Git host limits each manifest to its owners.
  The platform team owns the program of the stack.
- The downstream system has no permission model of its own. It has one
  root token. So the preview must run with that root token, or the preview
  cannot read the current values of the downstream system.
- The preview runs when the review request opens, before any person
  approves the change.

### The attack

A user adds a manifest or a program change that does something hostile
during the preview. Examples:

1. The program reads the root token from its environment, or from the
   provider configuration, and sends it to a host of the attacker.
2. The program calls a destructive API of the downstream system, for
   example a delete, directly and not through a resource.
3. The program reads data from the downstream system that the user may
   not read, and writes it to the plan or to the job log, which the user
   can read. With a root token, that data can hold other credentials.
4. The program uses the runner for other work: it scans the internal
   network, uses the CPU, or attacks the server.
5. The program behaves well in the preview and badly in the apply. The
   Pulumi CLI runs the program again in the apply, and a program can ask
   if it runs a preview.

Example 1 is the worst. A stolen root token stays valid after the job
ends. Each other example ends with the job, except when example 3 reads
another long-lived credential.

### Why the policy tools of each tool do not help

- **Pulumi policy packs** check each resource that the program registers.
  The program runs first. The policy pack sees the resource that the
  program asks for. It does not see an HTTP call that the program makes
  by itself. So a policy pack cannot stop examples 1 to 4.
- **OpenTofu plan-time checks** have the same limit. A plan runs the code
  of each provider and each data source. An `external` data source runs a
  command of the module. A check of the plan JSON runs after that code.
- **Approvals on the Git host** come after the preview. The Atlantis docs
  say: "terraform apply approvals are not enough" when malicious pull
  requests are in the threat model, because code can run during the plan
  ([Atlantis security](https://www.runatlantis.io/docs/security.html)).

So a control that stops examples 1 to 4 must act before the program runs,
or around the program while it runs. A control that acts after the plan
can stop only the apply.

### Who is the attacker

| Attacker | Example | Today (ADR 299 decisions 20 and 21) |
| --- | --- | --- |
| A1. An outsider | A review request from a fork | No plan until a trusted person comments. The plan has no credentials. |
| A2. A user with no grant to plan | A self-service user that is not linked, or is linked with no `unit:run-plan` | A plan with no credentials, no network, that can never be approved. |
| A3. A user with the grant to plan | A self-service user linked to a principal with `unit:run-plan` | A plan with all the credentials of the unit. |
| A4. A stolen account of A3 | A token of a user that an attacker has | As A3. |
| A5. A code owner | A member of the platform team | As A3. Out of scope: this person can change the program and the rules. |

The persona needs A3 to get a useful preview. A2 gets a preview that
cannot read the downstream system, so it shows nothing useful. A3 gets
the root token in the job. Today there is no step between "no credential"
and "all credentials". This RFC is mostly about A3 and A4.

A3 does not need a review request to start a plan. The grant
`unit:run-plan` also starts a plan through the REST start route, a change
order revision (ADR 191 decision 2, ADR 197), a manual drift check (ADR
263 decision 17), the `propose_change` tool of the agent (ADR 125) and a
move check (ADR 244). A3 can push a branch and start a plan of it on each
of these paths. So each control of this RFC keys on the trust of the
source of the job, not on the way that the job starts.

## The principle

Each step of the chain is a layer. Each layer must stop its part of the
attack alone, with no help from the other layers. "Another step checks it"
is never a reason to leave a check out.

**A trusted source.** A job has a trusted source when the server resolved
its commit at the start from the head of a branch that the source filter
lists in `protectedBranches`. This is the rule of ADR 213 decision 2 for
`unit:decrypt`, and the fact `source.protected`. Each other job has an
**untrusted source**, whatever path started it.

Four invariants hold at each step:

- **I1. The change does not govern its own run.** Each setting that
  governs a job of an untrusted source comes from the server, or from the
  base branch of the review request. It never comes from the commit of
  the job. Examples: proxy rules, profiles, gates, ownership rules, path
  sets, a code owners file, the image and the configuration of a scanner,
  and the provider lock file that a render step uses. GitHub reads
  `CODEOWNERS` from the base branch for the same reason (ADR 225
  decision 1).
- **I2. The credential is never in the job.** With `proxy-only`
  credentials (O1), the program can use the credential only through the
  job proxy, for the requests that a proxy rule allows for the phase. It
  cannot read the credential. Section "Hardening that I2 depends on" lists
  each way to reach the credential that the design must close.
- **I3. The sandbox reports, the server decides.** Each value that a
  process in a sandbox sends to the server is an unverified fact. A gate
  in the server decides. An unverified fact alone never grants reach.
- **I4. Each decision has a record.** Each step writes who or what
  decided, on which facts, and why (see "No magic" in `CLAUDE.md`).

## The chain

The chain has seven steps. For each step, this section says what can be
gated, with which facts, what it stops, what exists now and what is
missing. The section "Options" has the options for each missing part.

| Step | When | Existing decisions | Missing parts |
| --- | --- | --- | --- |
| 1. Trigger and source | The start of the job | ADR 267, ADR 299 decisions 20 to 23, ADR 300 decision 9, ADR 213 decision 2, ADR 226 decision 9, ADR 135 decision 6 | G1 |
| 2. Static check | Before any code runs | ADR 225 decision 9, ADR 226 decisions 10 and 13, ADR 224 decision 14 | G2, G3, G4, G5 |
| 3. Credential | The job start | ADR 327, ADR 330, ADR 200 to 203, ADR 341 | G6, G7, G8, G9 |
| 4. Sandbox | The plan phase | ADR 330 decisions 2 and 3, ADR 137 decisions 6 and 7, ADR 213 decision 2 | G10 |
| 5. After the plan | `change.planned` | ADR 226 decisions 4 and 7, ADR 225, ADR 299, ADR 141 | G11, G12 |
| 6. Apply | `change.apply` and the dispatch | ADR 224 decision 3, ADR 191 decision 7, ADR 102 decision 7, ADR 299 decisions 9, 10 and 18 | G13, G14, G15 |
| 7. Records | Each step | ADR 229, ADR 330 decision 10, the audit log | G16 |

### Step 1. The trigger and the source

**What can be gated.** Who can start a job, from which repository and
branch, and with which credentials.

**Facts.** The principal that started the job. For a review request: the
opener and each pusher of the head, and the principals they link to, the
repository ID (a fork or not), and whether a bot pushed. `source.kind`,
`source.ref`, `source.protected`, `source.commit`. All of these come from
the server.

**What exists.**

- ADR 299 decision 20: a plan of a review request runs with credentials
  only when the opener and each pusher of the head link to a principal
  with `unit:run-plan`. Else the plan has no provider credentials, no
  state, no secret outputs and no outbound network, and can never be
  approved. Credentials never follow a trusted comment alone.
- ADR 299 decision 21: a review request from a fork gets no plan until a
  trusted person comments on the head commit. A new push voids the
  comment. The plan of a fork has no credentials.
- ADR 299 decision 22: bots by app ID, and the person behind each push.
- ADR 299 decision 23: "plan with credentials as me", for the same
  repository only.
- ADR 213 decision 2 and ADR 300 decision 9: a plan gets `unit:decrypt` or
  an unmasked read of state only from a trusted source or a principal with
  `unit:read-secrets`, and a plan that starts from the Git host never gets
  them.
- ADR 226 decision 9 and ADR 135 decision 6: the source filter decides
  which submodules and Git LFS objects a snapshot holds. Its
  `protectedBranches` give `source.protected`.

**What it stops.** A1 (forks), A2 (users with no grant), and a bot that a
user controls. It also stops the secret config of the stack from reaching
a plan of a review request.

**What it does not stop.** A3 and A4. Their plan gets the full
credentials of the unit, on each path that starts a plan.

**Missing (G1).** A middle level between "no credential" and "all
credentials" for each job of an untrusted source: a job that can reach
the downstream system only through the job proxy. See option set O1.

### Step 2. A static check before any code runs

**What can be gated.** The changed files and the people who changed them,
before any code of the change runs.

**Facts.** `change.paths`, `paths.touches["<set>"]` and
`paths.only["<set>"]` (ADR 226 decision 13). The principal of the change.
The ownership facts of O3. The result of a check step (an unverified fact)
or of an external check, see "The scanner question".

**What exists.**

- Path sets and the path facts (ADR 226 decision 13).
- Ownership rules: stored in the server, with globs, groups, users and a
  count (ADR 225 decision 9). They give approval requirements.
- External checks: a signed request to an external service, a signed
  callback, a timeout, and the result as the `externalChecks` fact. ADR
  226 decision 10 allows the stage "after `change.propose`".
- Gates at `change.propose` can `deny` a change before the plan.

**What it stops.** With a gate that denies, a change that touches a path
set that the user may not change. For the persona: a self-service user
who changes the program, the lock file of the dependencies, the Pulumi
project file or a dynamic provider.

**Missing.**

- **G2. A requirement cannot hold the plan.** ADR 224 decision 14 says:
  "A requirement from `change.propose` never blocks the plan phase", and
  the change waits for its requirements "after the plan, so approvers sign
  a plan". So an ownership rule asks for approvals after the program has
  already run. There is no "approve and run", which GitHub has for forks
  and for environments with required reviewers. ADR 299 decision 21 has it
  for forks only, as a trusted comment, and that plan has no credentials.
- **G3. No fact says that the changer owns what they changed.** An
  ownership rule names who must approve. No fact says "each changed path
  is owned by the person who changed it". The persona needs this fact: a
  user who changes only their own manifest may plan with the proxied
  credential, and a user who changes another team's manifest or the
  program may not. The code owners file on the Git host does not help at
  plan time, because the Git host enforces it only at the merge.
- **G4. No decision point reads a pre-plan check.** The fact
  `externalChecks` is in the schema of `change.planned` only (ADR 226
  decision 4). So the result of a check before the plan has no gate that
  reads it before the plan phase starts.
- **G5. The path facts do not say how they stay correct.** A change can
  look like "manifests only" when it is not:
  - a rename changes two paths, the old one and the new one.
  - a symlink, a mode change or a submodule entry under `manifests/` is
    not a manifest, for example `manifests/a.yaml -> ../index.ts`.
  - the Git host lists at most a fixed count of changed files for a
    review request. A list that the Git host cuts gives fewer paths, not
    an error.

See option sets O2, O3, O4 and O5.

### Step 3. The credential

**What can be gated.** Which credential the job can use, for which hosts,
methods and paths, in which phase.

**Facts.** The group `proxy` at `change.propose`, `change.planned` and
`change.apply`: `enabled`, `rules`, `ruleSetDigest`, `credentialHosts`,
`plainHttp` and `learnMode` (ADR 330 decision 9). The profile facts at
`profile.open` (ADR 226 decision 5). The server renders all of these.

**What exists.**

- The job proxy (ADR 327, ADR 330). It runs next to each job, as its own
  process and user, outside the sandbox. It is the only exit of the
  sandbox. A proxy rule matches the host, the method and the path, and
  has an `operations` list (`plan`, `apply`, `destroy`, `refresh`).
- Credentials for the job proxy only: the export key `proxy` of a profile
  goes only to the consumer `job-proxy`, never to the consumer `launcher`.
  The tool gets a placeholder, for example `dustpan-placeholder` (ADR 330
  decision 5).
- The job proxy deletes `Authorization` and the other credential headers
  of the request, adds the credential, never follows a redirect, and scans
  each response for the credential in its plain, base64, JSON and percent
  forms (ADR 330 decision 7).
- The job start renders the proxy rules once. The job spec holds their
  SHA-256, so each phase runs the same proxy rules (ADR 330 decision 4).
- A routing change of a profile needs `profile:open` as well as
  `profile:write` (ADR 202 decision 2). A taint that reaches a secret
  source in any export key is a routing change (ADR 341). So a person who
  can write a profile, but not open it, cannot move the root token from
  `exports.proxy` to an environment variable of the job.
- Profiles and proxy rules live in the server, not in the repository, so
  a change cannot edit them (invariant I1).

**What it stops.** With `proxy-only` credentials and the hardening of
section "Hardening that I2 depends on":

- Example 1 for the credential of the job proxy: that credential is not
  in the job. A host that no proxy rule names gets 403 before any TLS. A
  change of the stack config file that points the provider at another
  host sends the placeholder, and the job proxy refuses the host.
- Example 2 only when the plan proxy rules list read paths, not a whole
  host, and the read-only checks of H5 hold. A plan rule that allows
  `GET` on a whole host is not read-only: see G8.

It does not stop example 3, and a read can be a credential theft: see
"Residual risk".

**Missing.**

- **G6. A credential can still reach the job by a second path.** The same
  credential can reach the consumer `launcher` through a second export, a
  copy in another profile or a Vault path, a value that is not marked
  secret, or a token that a login block issues from it. ADR 341 stops a
  person without `profile:open` from making a new routing change. It does
  not stop a profile that already has one, or a copy of the value under
  another source. See option set O6.
- **G7. `POST` reads.** Some APIs read with `POST` (a search, GraphQL).
  For these, a rule on the method and the path allows writes too. Body
  matching (RFC 16 item P14) is a proposal, not a decision. See O7.
- **G8. A `GET` rule does not make a phase read-only.** ADR 330 decision 7
  matches the method and the path of the request line only. A request can
  still write with:
  - `GET` with `Upgrade: websocket`, or the extended CONNECT of HTTP/2
    (RFC 8441). The stream becomes a two-way channel after the match. On
    Kubernetes, `GET .../pods/{pod}/exec` with an upgrade runs commands.
    With a root token, that is a root shell in the downstream system
    during the preview.
  - a method-override header (`X-HTTP-Method-Override`, `X-HTTP-Method`,
    `X-Method-Override`) or a `_method` parameter.
  - a `GET` with a body (APIs in the style of Elasticsearch), a GraphQL
    mutation over `GET`, or an RPC endpoint such as `GET ...?action=delete`.

  The first two kinds are generic, and the job proxy can refuse them (H5).
  The third kind is specific to each API. Only an allowlist of methods and
  paths in the proxy rules limits it. So under `proxy-only`, a plan proxy
  rule of an untrusted source is an allowlist of methods and paths, never
  a whole host (O8-b).

  Examples of `GET` endpoints that are not safe, and how an allowlist
  leaves them out:

  | API | `GET` that is not safe | How the allowlist covers it |
  | --- | --- | --- |
  | Kubernetes | `.../pods/{pod}/exec`, `.../attach`, `.../portforward`, `.../proxy`, `/api/v1/nodes/{node}/proxy` (with an upgrade, they run commands or open a channel) | List the read paths of the resources that the stack manages, for example `/apis/apps/v1/namespaces/{{ unit.labels.ns }}/deployments/*`. Never `/api/**`. H5 refuses the upgrade too. |
  | Kubernetes | `.../secrets`, `.../serviceaccounts/{sa}/token` (a read that returns credentials) | Leave the paths of secrets and tokens out of the list. |
  | Elasticsearch and OpenSearch | `GET .../_search` and other endpoints that take a body | H5 refuses a body on `GET`. A search that needs a body uses a `POST` rule with body matching (O7). |
  | GraphQL servers that accept `GET` | `GET /graphql?query=mutation...` | Body or query matching (O7), or no `GET` rule for `/graphql`. |
  | RPC-style APIs | `GET ...?action=delete`, `GET .../run` | List only the read paths. A path rule cannot match a query value, so an endpoint whose action is in the query is not listed. |
  | Secret stores and identity APIs | `GET /api/tokens`, `/users/{id}/keys`, `/v1/secret/**` | Leave them out of the list. Residual risk 2. |
- **G9. The same proxy rules for a trusted and an untrusted job.** A proxy
  rule has `operations`, but no condition on the source. See option set
  O8.

### Step 4. The sandbox of the plan

**What can be gated.** What the plan process can reach and use: network,
CPU, memory, time, the server API.

**Facts.** The isolation kind of the pool, the limits of the sandbox, the
scope of the job token.

**What exists.**

- One route out of the sandbox, to the job proxy (ADR 330 decisions 2 and
  3). The coordinator drops each other packet, DNS too. The launcher
  probes the isolation at each claim and refuses to run a step when it can
  reach the server directly (`network-not-isolated`). A unit with proxy
  rules never runs on `process` isolation.
- The launcher and the tool reach the server through a built-in tunnel
  rule for the host of the server only (ADR 330 decision 3).
- CPU and memory limits, and a grace period before the kill (ADR 137
  decision 7).
- The job token of a plan of an untrusted source gets no `unit:decrypt`,
  no unmasked state read, no state write and no read of another unit
  except shared outputs (ADR 213 decisions 1 and 2, ADR 300 decision 9).

**What it stops.** Example 4: a scan of the internal network, a direct
call to the server that skips the job proxy, a write of state.

**Missing (G10).** A budget for the job proxy in each phase: a count of
requests and a count of bytes. Today a plan can send each request that
the proxy rules allow, as often as it likes, until the job times out.
Run limits and the time limit of a job are in
[RFC 107](0107-run-limits-and-the-run-queue.md), which is a stub. See
option set O9.

### Step 5. After the plan

**What can be gated.** Whether the change can go to the apply, and who
must approve it.

**Facts.** The groups of `change.planned` (ADR 226 decision 4): `plan`,
`resourcePolicy`, `externalChecks`, `cost`, `paths`, `proxy`,
`dependents`. Section "Facts that the server verifies, and unverified
facts" says which of them the server can trust.

**What exists.**

- Gates at `change.planned`, approval rules, approval defaults and
  ownership rules (ADR 224, ADR 225).
- The review modes, the binding levels of an approval and the approval
  floor (ADR 299 decisions 9 and 10).
- Freeze schedules (ADR 141).
- The built-in gates `builtin/plan-readable` and
  `builtin/mandatory-resource-policy` (ADR 226 decision 7).
- A scanner over the plan display as an external check (RFC 90, option
  A3).

**What it stops.** An apply of a plan that a person did not approve, or
that fails a policy, a scanner or a cost limit.

**Missing.**

- **G11. The proxy records of the plan phase are not a fact.** A plan that
  tried a request that no proxy rule allows is a strong signal of a
  hostile change, or of a missing proxy rule. See option set O10.
- **G12. The plan counts are unverified.** The tool in the sandbox writes
  both parts of the plan: `plan-display` and `plan-apply` (ADR 102
  decision 2). The server hashes each part, but nothing checks that they
  agree. A hostile preview can show "1 add" and upload a `plan-apply`
  that allows deletes. The approvers sign the display, and the apply
  enforces the file. The policy pack runs in the same sandbox, so its
  report is unverified too. See option set O11.

### Step 6. The apply

**What can be gated.** The dispatch of each phase that changes.

**Facts.** The groups of `change.apply`, with the values of that moment.

**What exists.**

- The recheck at `change.apply`: a new `deny` refuses the dispatch, a new
  requirement puts the change back into the approval wait (ADR 224
  decision 3).
- The staleness check: a plan is stale when the state version changed,
  when it is older than the maximum plan age (24 hours by default), or
  when it was redacted (ADR 191 decision 7, ADR 102 decision 7).
- The binding to the plan: OpenTofu applies the saved plan file. For
  Pulumi, the plan step runs `pulumi preview --save-plan` and the apply
  step runs `pulumi up --plan`, which stops the update at a resource
  change that is not in the plan (ADR 102 decision 7). The binding levels
  L0 to L2 and the approval floor (ADR 299 decisions 9 and 10).
- Each gate decision reads the head commit fresh from the Git host, and
  a difference fails closed (ADR 299 decision 18).
- The apply proxy rules are the proxy rules with `apply` in
  `operations` (ADR 330 decision 4).

**What it stops.** An apply of a commit or of a state that the approvers
did not see. A resource change that is not in the plan file. An apply that
reaches a host or a path that no apply proxy rule allows.

**Missing.**

- **G13. The program runs again in the apply.** `pulumi up --plan` limits
  the resource changes. It does not limit the HTTP calls that the program
  makes by itself in the apply. A program can test if it runs a preview
  and do other work when it does not. OpenTofu still runs the code of each
  provider. Only the apply proxy rules and a person who reads the program
  diff limit this. See option set O12.
- **G14. The apply of an untrusted source.** An apply of the head of a
  review request before the merge runs the program of that head. Today it
  gets the credentials of the unit directly. The setting of O1 must cover
  each phase of a job of an untrusted source, not only the plan.
- **G15. The staleness check does not cover the profiles or the proxy
  rules.** A profile can get a new revision between the plan and the
  apply. A person can tighten the proxy rules after an alert, but the
  waiting apply keeps the rules that the job rendered at its start (ADR
  330 decision 4). See option set O13.

### Step 7. Audit and decision records

**What exists.**

- One decision record for each evaluation of a decision point, with the
  fact document, each gate and its outcome, the requirements and the
  signatures (ADR 229 decision 1).
- One proxy record for each request of the job proxy, a job log kind of
  its own, and one audit entry for each phase with the counts and the
  digest of the proxy rule set (ADR 330 decision 10).
- External checks write their request and their callback.
- The comment and the status on the Git host (ADR 299 decisions 18 and
  25).

**Missing (G16).** One view of the full chain of one change. A person who
asks "why did this plan run with this credential" must read the trigger,
the credential decision, the decision record of each decision point, the
proxy records and the approvals, in different places. See option set O14.

## Hardening that I2 depends on

"The credential is never in the job" is a promise that teams will build
on. It holds only when each item below holds. Each item is a check of its
own, with its own test that sends hostile input to that check alone (ADR
250). Items H1 to H4 close a path to the credential. Items H5 and H6 make
a plan phase read-only and keep the credential out of a response. Items
H7 and H8 limit the channels out.

| Item | Rule | Test |
| --- | --- | --- |
| H1 | The server derives the consumer `job-proxy` from the identity of the proxy session, never from a field of the request. The launcher claim token in the sandbox can open only the consumer `launcher`. This makes ADR 203 decision 1 stricter: the consumer is not a parameter that a caller chooses. | An open with the launcher token that asks for `job-proxy` gets `identity.ErrDenied` and an audit entry. |
| H2 | The listener of the job proxy serves only CONNECT, and the plain HTTP of a loopback opt-in. It has no health, metrics or profiling handler on an address that the sandbox can reach. | A `GET /`, `GET /metrics` and `GET /debug/pprof/` from the sandbox get a refusal. |
| H3 | A job of an untrusted source with `proxy-only` credentials runs only on `firecracker`, on `gvisor`, or on `kubernetes` with a sandboxed runtime class that the pool names. On `kubernetes` with the default runtime, the job pod and the proxy pod can be on one node, and a container escape reaches the credential. The start fails with `runner.ErrIsolation`, which names the unit and the pool. | A job of an untrusted source on a pool with the default runtime fails at start. |
| H4 | The secret that starts the proxy session is not readable through the Kubernetes API by the service account of the job pod, and is not mounted into the job pod. | The service account of a job pod gets 403 on a read of the proxy secret. |
| H5 | In a plan phase of an untrusted source, the job proxy refuses: `Upgrade` and `Connection: upgrade`, the extended CONNECT of HTTP/2 (`:protocol`), the method-override headers `X-HTTP-Method-Override`, `X-HTTP-Method` and `X-Method-Override`, a `_method` query or form parameter, and a body on `GET`, `HEAD` or `OPTIONS` (a `Content-Length` above 0, or a `Transfer-Encoding`). | One test for each kind, each with a request that a `GET` proxy rule matches. |
| H6 | The response scan of ADR 330 decision 7 also covers the response headers, `Location` included, in each form. Under `proxy-only`, a proxy rule cannot use a `query` credential or a credential in the URL. The job start fails with `jobproxy.ErrCredential`. | A `301` whose `Location` holds the credential breaks the stream. A `query` credential under `proxy-only` fails the start. |
| H7 | In a phase of an untrusted source, a `tunnel` proxy rule is refused by default. A `tunnel` rule has no SNI and no `Host` check, so it allows domain fronting, and a host that many tenants share (a package registry, a Git host, object storage) is an exit for data. A unit that needs packages uses a mirror through an `inspect` proxy rule with a list of paths. | A `tunnel` rule in a plan of an untrusted source fails the start, unless a fact and a gate allow it (O8). |
| H8 | The values that the attacker chooses and that go into records, facts and messages are checked: a host in `planDeniedHosts` matches the host grammar, the set holds at most 32 hosts, and `{{fact.path}}` escapes them in a gate message. A path in a proxy record follows ADR 330 decision 10 (only a canonical path). | A CONNECT host with control characters, and 1,000 distinct hosts, give a valid fact document. |

## Options

Each option set has options, their consequences, a recommendation and a
short explanation for the owner (ELI5).

### O1. A middle level of credentials for an untrusted source (G1, G14)

- **O1-a. Do nothing.** A3 gets the root token in the job. A2 gets a
  useless preview. The persona must choose between a useful preview and a
  safe one.
- **O1-b. Proxy-only credentials.** A unit setting
  `untrustedSourceCredentials` with the values `direct` and `proxy-only`.
  With `proxy-only`, each phase of each job of an untrusted source, on
  each path that starts it, opens only the export key `proxy` and the
  values that are not secret. The job start refuses a job when an attached
  profile gives the credential to the consumer `launcher` by any path
  (O6). The unit setting cascade (installation, namespace, collection,
  unit) gives the effective value, and a level can force it.
  - For: the persona gets a preview that reads the downstream system, and
    the credential never enters the job. The rule follows the source, so
    a branch that A3 pushes and plans through the REST route gets the
    same rule as a review request.
  - Against: a credential that signs each request (for example AWS
    Signature Version 4) cannot be added by a proxy. A unit with such a
    credential cannot use `proxy-only`. It uses workload identity, or
    `direct`. H3 needs a pool with a strong sandbox.
- **O1-c. Only for review requests.** As O1-b, but for a plan that starts
  from the Git host only. This leaves each other path of `unit:run-plan`
  open, so it does not stop A3.
- **O1-d. Proxy-only also for A2.** As O1-b, and a plan of A2 (no grant to
  plan) also gets proxy-only credentials when a gate at `change.plan`
  allows it. This changes ADR 299 decision 20, which is an owner
  decision, and opens the proxied credential to each linked user.

**Recommendation.** O1-b. Keep ADR 299 decision 20 as it is. The default
is `direct`, because most cloud credentials sign requests. When a unit has
a proxy rule set with a credential and the setting is `direct`, the API
and the console warn. This warning is about a choice of the operator, so
ADR 347 applies to it. A new fact `proxy.untrustedSourceCredentials` lets
a gate deny a job of an untrusted source that is not `proxy-only`.

**ELI5.** Today the helper who does the preview either gets the master key
or gets no key. With this option the helper gets no key. A guard at the
door holds the key, and opens only the doors on the guard's list, and only
to look. This holds for each way the helper can come in, not only the
front door.

### O2. Approve and run (G2)

- **O2-a. Do nothing.** A requirement never blocks the plan. A gate can
  only deny. The persona must deny each change of the program by a user,
  or let it run.
- **O2-b. Approve and run, a signature of its own kind.** A gate at
  `change.plan` (O4) can name an approval rule. The plan phase does not
  dispatch until the requirement is met. The signature is a **run
  signature**:
  - It binds to the head commit (the level L0 of ADR 299 decision 9). A
    new push voids it.
  - It meets only a requirement of `change.plan`. It never meets a
    requirement of `change.planned` or `change.apply`, also when both name
    the same approval rule. So a signature that was given before any plan
    existed never counts as the approval of a plan.
  - The console shows the code diff and says that no plan exists yet.

  This is the trusted comment of ADR 299 decision 21, made general.
- **O2-c. Let a requirement at `change.propose` block the plan.** This
  changes the meaning of each existing gate at `change.propose`.

**Recommendation.** O2-b. It changes ADR 224 decision 14: the
requirements of `change.plan` block the plan phase, and the sum of the
requirements of a change counts a run signature only for `change.plan`.
"Approvers sign a plan" stays true for each requirement after the plan.

**ELI5.** Before the helper runs the new recipe, a cook who owns that
part of the kitchen reads it and says "yes, you can try it". That "yes"
is not the "yes" for the dish. The cook must look at the dish too.

### O3. A fact that says the changer owns what they changed (G3)

- **O3-a. Do nothing.** Path sets only. The persona writes one path set
  for each team and one gate for each team.
- **O3-b. Ownership facts from the stored ownership rules.** A new fact
  group `ownership` at `change.plan`: `available`, `changerOwnsAll` (each
  changed path matches an ownership rule whose groups or users hold the
  opener and each pusher), `unownedPaths` (the count of changed paths that
  no rule matches) and `foreignPaths` (the count of changed paths whose
  rules do not hold a changer). `available` is `false` when the server
  does not know each changer: a job with no review request, a push with no
  known person, or `paths.available == false`. A gate that reads the
  group must read `available`, as ADR 226 decision 3 already says.
- **O3-c. As O3-b, and the code owners file of the Git host as an input.**
  The server reads the code owners file of the base branch, never of the
  head (invariant I1), through the Forge port. It maps each owner of the
  Git host to a principal through the account links of ADR 299. An owner
  with no link counts as not a changer. A file that does not parse gives
  `available == false`.
  - For: the persona keeps one list of owners.
  - Against: a second source of owners, with the format of each Git host.
    GitLab, Gitea and Forgejo have other formats or none.

**Recommendation.** O3-b first. O3-c later, as a producer behind its own
port with a conformance suite.

**ELI5.** The list on the wall says which shelf belongs to whom. A person
who moves only things on their own shelf can go on. A person who touches
another shelf must ask first. If we do not know who moved things, nobody
goes on.

### O4. A decision point before the plan (G4)

- **O4-a. Do nothing.** No gate reads a pre-plan check before the plan.
- **O4-b. A new decision point `change.plan`.** It runs before the
  dispatch of each plan phase, after each check step and each external
  check of the stage before the plan has a result or has timed out. Its
  fact schema holds the groups of `change.propose`, and `checkSteps`,
  `externalChecks`, `ownership` (O3) and `proxy`, each with its origin. A
  `deny` refuses the plan. A requirement holds the plan (O2-b). A new
  decision point needs a new ADR (ADR 224 decision 2).
- **O4-c. Evaluate `change.propose` two times.** Once at the start, and
  again after the pre-plan checks. One decision point with two meanings.
- **O4-d. Only the facts of the server.** No scanner result before the
  plan.

**Recommendation.** O4-b. It gives a clear name to the moment that the
persona needs: "may this code run with this credential". It keeps
`change.propose` as it is.

**ELI5.** We add one more checkpoint just before the helper starts the
work. At this checkpoint we read the results of the inspectors.

### O5. Path facts that stay correct (G5)

- **O5-a. Do nothing.** The path facts come from the list of changed
  files that the Git host gives.
- **O5-b. The server computes the paths from two trees.** The server
  compares the tree of the base commit and the tree of the head commit
  that it fetched for the source snapshot (ADR 135), not a list from the
  Git host. Then:
  - a rename gives both paths.
  - a new fact `paths.special`: the count of changed entries that are not
    a regular file, or that change the mode (a symlink, a submodule entry,
    the executable bit). An entry of this kind inside a path set makes
    `paths.only` of that set `false` and `ownership.changerOwnsAll`
    `false`.
  - a compare that the server cannot finish in its limits gives
    `paths.available == false`, never a shorter list.

**Recommendation.** O5-b. The facts that unlock a plan must come from
the server alone.

**ELI5.** We do not trust the list that says which drawers changed. We
open each drawer and look ourselves. A drawer that is really a door to
another room does not count as a drawer.

### The scanner question

The owner said "yes" to scanners at the plan step. On 7 November 2026 the
owner added: "having steps run on the runners isn't an issue, as long as
the gating happens on the trusted side (our backend)." This section
designs for that input.

#### The rule: the sandbox reports, the server decides

A scanner can run as a job step on a runner. Its output is an
**unverified fact**: a value that a process in a sandbox sent to the
server. The server never trusts an unverified fact by itself. The gate in
the server decides, over the facts that the server verifies and over the
unverified facts, with the origin of each value in the fact document.

#### The risk, and how the order of the steps removes it

The risk: a scanner in the same sandbox as the program can be tricked. If
the untrusted program runs first, or at the same time, it can change the
files that the scanner reads, replace the scanner binary, or write the
report itself. The result is then a forgery.

The design removes this with the order of the phases, the sandbox and the
binding of the result:

1. **A check phase of its own.** A job of an untrusted source gets a phase
   `check` before the plan phase. The check phase runs one or more check
   steps. No step of the check phase runs the program, a provider, a
   package install or a script of the repository.
2. **A fresh sandbox.** The check phase runs in its own sandbox, on its
   own claim. The coordinator destroys the sandbox at the end of the claim
   (ADR 137 decision 1). The plan phase gets a new sandbox. So no code of
   the change runs before the scanner or next to it.
3. **No credential and no route.** The check phase opens no profile
   export, also not `exports.proxy`. Its proxy rule set holds no rule. The
   only route out is the built-in tunnel to the server (ADR 330
   decision 3). The job token of the check phase can read the source
   snapshot of the job and send the result of its own steps, and nothing
   more.
4. **The scanner comes from the server.** The image of the check step and
   its digest, the scanner configuration and the policy files come from
   the server or from the base branch, never from the commit of the job
   (invariant I1). A `.checkov.yaml` or a `policy/` directory in the head
   commit does not change the check.
5. **The server binds the result, once.** The server accepts a result only
   from the claim of the check phase of that job, and only once for each
   check step. At the claim of the check phase, the server issues a nonce
   of 32 random bytes for each check step, and the launcher gets it with
   the step. The result names the job ID, the claim token of the check
   phase, the check step ID, the nonce, the image digest, the
   configuration digest and the SHA-256 of the source snapshot. The server refuses a result
   when any of these is not the value of the job. So a result of an
   earlier job on the same commit, with an older image or older policy
   files, does not count. The claim of a plan, apply or verify phase can
   never write `checkSteps`, at any decision point. The server stores the
   raw report as a history item, and its SHA-256 goes into the decision
   record.
6. **A missing result is not a pass.** The fact `checkSteps` has the sets
   `passed`, `failed`, `missing`, `timedOut` and `error`. A check step
   with no result at the end of the check phase is in `missing`. A file
   that makes the scanner crash, hang or time out is not a pass. A
   built-in gate `builtin/check-steps-passed`, in `enforce` mode at
   `change.plan`, denies when a check step that the unit configures is
   not in `passed`. So a crash fails closed with no gate that a person
   writes. A person can switch it to `evaluate` mode only with the fresh
   authentication and the audit entry of ADR 226 decision 7.

What stays: the scanner reads the files of the change, and these files
are hostile input. A file made to exploit a bug in the scanner can make
the scanner lie. The sandbox, the missing credential and the missing
route limit what that exploit can reach. The gate rule below limits what
the lie can unlock.

#### Facts that the server verifies, and unverified facts

| Value | Origin | Why |
| --- | --- | --- |
| `change.paths`, `paths.*` | Server | The server compares two trees that it fetched itself (O5-b). |
| `source.commit`, `source.protected`, `source.kind` | Server | The server resolves the commit and the branch at the start (ADR 213 decision 2). |
| The principal, the opener, each pusher and their links | Server | Stored grants and links (ADR 299 decisions 20 and 22). |
| `ownership.*` (O3) | Server | Stored ownership rules matched against the paths that the server computed. |
| `proxy.enabled`, `rules`, `ruleSetDigest`, `credentialHosts`, `planWriteMethods`, `untrustedSourceCredentials` | Server | The server renders the proxy rules at the job start. |
| `proxy.planRequests`, `planDenied`, `planDeniedHosts` (O10) | Job proxy | The job proxy runs outside the sandbox, as its own user (ADR 330 decision 1). The program cannot write its records. |
| The hash of each plan part and the plan binding | Server | The server stores the parts and their hashes (ADR 102 decision 2). A hash proves which bytes the approvers saw. It does not prove that the bytes are true. |
| `plan.add`, `change`, `replace`, `destroy`, `resourceTypes`, and the set of operations of a binding level | Runner, unverified | The tool in the sandbox writes both parts of the plan (G12). |
| `resourcePolicy.*` | Runner, unverified | The policy pack runs in the same sandbox as the program. |
| The events of an apply | Runner, unverified | The engine and the program run in the same sandbox. |
| `checkSteps.*` | Runner, unverified | A scanner in the check phase, with the order, the sandbox and the binding above. |
| `externalChecks.*` | External service | A signed callback from a service that the team runs (ADR 226 decision 10). It is as trusted as that service. |

The plan counts were unverified before this RFC too. This RFC makes it
visible.

#### What an unverified fact can do in a gate

In the gate model, a gate unlocks by not matching. ADR 224 decision 1
allows the outcomes `deny`, `approval:<rule>` and `warn` only. Decision 14
gives `allow` when no gate matches. So a gate whose `unless` reads
`checkSteps.passed`, or whose `when` reads a negated value, can be
unlocked by a forged value.

The rule:

- **A `deny` gate can read unverified facts in each position.** A forged
  "passed" then removes only the layer that the scanner adds. A forged
  "failed" stops only the change of the person who forged it.
- **An `approval:` gate at `change.plan` reads only facts of the origins
  `server` and `job-proxy`.** This is the gate of approve and run, so it
  decides who can run code with the proxied credential. The check at the
  save refuses an `approval:` gate at `change.plan` whose `when` or
  `unless` names a group of the origins `runner` or `external`, with
  `gate.ErrInvalid`, which names the group.
- **The credential level is not a gate.** `untrustedSourceCredentials`
  (O1) is a fixed setting that no fact changes.

The check at the save is a security control, so it refuses. It does not
warn. ADR 347 decision 4 says that ADR 347 does not change a security
control. Open question 11 asks the owner to confirm this.

**The fact schema shows the origin.** Each group in the schema of a
decision point names its origin: `server`, `job-proxy`, `runner` or
`external`. The decision record shows it for each value that a gate
read.

#### The external check stays

An external check (ADR 226 decision 10) stays an option for each scanner.
A team can run its scanner as a service, or in its own CI, and answer
with a signed callback. The external check gets the commit, the changed
paths and a token that can read the source snapshot of that commit only.
It gets no credential of the unit (open question 5).

A scanner over the plan runs after the plan, either as an external check
on the stored plan display (RFC 90, option A3), or as a check step in a
fresh sandbox that reads the stored plan parts. It never runs in the
sandbox of the plan phase.

#### How this amends ADR 226 decision 10

ADR 226 decision 10 says: "This accepts RFC 49: a run task is an external
check, never a job step." The owner direction on RFC 49 said that jobs do
not run anything except IaC code. The owner input of 7 November 2026
replaces that direction for checks. The proposed amendment:

- A run task, a call to a service of the team, stays an external check.
- A check can also run as a **check step** in a check phase of a job,
  with the rules of this section: its own fresh sandbox, before any step
  that runs the program, no credential, no route to a host of the unit,
  the scanner and its configuration from the server or the base branch,
  and a result that only the check phase can send, once.
- The result of a check step is an unverified fact, with its origin in
  the fact schema. A gate in the server decides. An unverified fact alone
  never grants reach.
- A check step never runs in the sandbox of a plan or an apply phase.
- The check phase is a fifth phase kind, next to `plan`, `apply`,
  `deliver` and `verify` (ADR 100 decision 4, ADR 148 decision 3). The
  `verify` phase is a precedent: it holds runner steps that change nothing
  and check the change after the apply.

This needs the owner's yes (open question 10). The new ADR must name ADR
226 decision 10 in its "Changes" list, and RFC 49 in its context.

**ELI5.** A helper may check the drawings in a clean, empty room before
the stranger's machine is brought in. The helper has no keys. The helper
writes a note. The note can stop the work, but the note alone cannot
open a door. The guard at the front desk decides, and trusts what the
guard checked with their own eyes more than the note.

### O6. A credential that also reaches the job (G6)

- **O6-a. Do nothing.** ADR 341 stops new routing changes by people
  without `profile:open`. An old profile, or a copy of the value, can
  still give the credential to the job.
- **O6-b. A check at the job start, by source only.** The job start fails
  when one secret source feeds both `exports.proxy` and a value for the
  consumer `launcher`. A copy under another source passes.
- **O6-c. A check at the job start, by taint and by value.** The job start
  fails with `jobproxy.ErrCredentialExposed` when any of these is true:
  - the taint of ADR 341 joins a secret source of `exports.proxy` to a
    value for the consumer `launcher`, also through a login block that
    issues a token from that source.
  - a value for `launcher`, secret or not, is equal to a value of
    `exports.proxy`, or to its base64, JSON escape or percent form. The
    server compares the SHA-256 of each form, and never logs a value.

  The error names the profile and the export, never the value.

**Recommendation.** O6-c, for each job, not only for an untrusted source.
It is a fixed control: no gate can turn it off. Defense in depth: the
routing rule of the profile stops the change at the save. This check
stops it at the job start, and does not rely on the save.

**ELI5.** If the guard holds the key, nobody may also put a copy of the
key in the helper's pocket. We check the pocket at the door, and we look
for copies, not only for the original key.

### O7. Reads that use `POST` (G7)

- **O7-a. Do nothing.** A unit whose downstream API reads with `POST`
  allows all `POST` in the plan.
- **O7-b. Accept body matching.** RFC 16 item P14 lets a proxy rule
  require a field of the body to equal a value, for example the GraphQL
  operation type `query`. RFC 16 item P13 (the rule tester) replays past
  proxy records against a changed proxy rule set.
- **O7-c. A path allowlist of read endpoints only.** Works when the API
  has distinct paths for search, and fails for GraphQL.

**Recommendation.** O7-b, with the rule tester. This RFC does not accept
them. It names them as needed for the persona.

**ELI5.** Some doors look the same for "look" and "change". The guard
must read the note in the person's hand, not only see which door they
use.

### O8. Narrower proxy rules for an untrusted source (G8, G9)

- **O8-a. Do nothing.** The proxy rules of a plan are the same for each
  source.
- **O8-b. Facts and the read-only checks.** The job proxy applies H5 to
  each plan phase of an untrusted source. New facts:
  `proxy.planWriteMethods` (the count of proxy rules for `plan` that allow
  a method other than `GET`, `HEAD` and `OPTIONS`), `proxy.planHostRules`
  (the count of proxy rules for `plan` with `inspect` and no path list, or
  with `paths: ["/**"]`) and `proxy.planTunnelRules` (the count of
  `tunnel` rules for `plan`). A starter gate denies a plan of an untrusted
  source when any count is above 0.
  - A fixed control too, in addition to the gate: under `proxy-only`, the
    job start of an untrusted source refuses a plan proxy rule with
    `inspect` and no path list, a path pattern that ends in `**` at the
    first segment, or a method other than `GET`, `HEAD` and `OPTIONS`
    with no body condition (O7). The error is `jobproxy.ErrRender`, and it
    names the proxy rule. Defense in depth: the starter gate denies the
    same rules, and this check does not rely on the gate.
- **O8-c. A source condition on a proxy rule.** A field `sources` with the
  values `trusted` and `untrusted`. A proxy rule with
  `sources: [trusted]` does not render for a job of an untrusted source.
  - Against: a new field in the proxy rule format, and two sets of rules
    to keep.

**Recommendation.** O8-b now. O8-c only when a real case needs a plan of
a trusted source to have more reach than a plan of an untrusted source
(ADR 327 decision 4: "or" and "not" conditions only when a real case
needs them).

**ELI5.** We count the "change" doors and the "whole building" passes on
the guard's list for the preview. A rule can say: for a preview of an
untrusted change, these numbers must be zero.

### O9. A budget for the job proxy (G10)

- **O9-a. Do nothing.** The job timeout is the only limit.
- **O9-b. A budget in the proxy rule set.** `budget` for each operation:
  the maximum count of requests and of response bytes. When a budget is
  spent, the job proxy refuses each next request with the fixed reason
  `budget-exceeded`, and the record says so.

**Recommendation.** O9-b, with defaults that the API shows: 10,000
requests and 1 GiB of response bytes for a plan phase. These numbers are
a guess. The proxy core work measures them.

**ELI5.** The guard lets the helper look through the door only so many
times.

### O10. The proxy records of the plan as facts (G11)

- **O10-a. Do nothing.** A person reads the proxy records.
- **O10-b. Facts at `change.planned` and `change.apply`.**
  `proxy.planRequests`, `proxy.planDenied` (counts) and
  `proxy.planDeniedHosts` (a set of at most 32 hosts, checked as H8
  says). A starter gate asks for a security approval when
  `proxy.planDenied > 0`. An event notifies the owners of the unit.

**Recommendation.** O10-b. A denied request in the plan is the clearest
sign that this RFC can give. The owners get the event by default.

**ELI5.** If the helper tried a door that was not on the list, we stop
and ask a grown-up before going on.

### O11. The two plan parts must agree (G12)

ADR 102 decision 7 already binds the apply to the plan with the plan file
of OpenTofu and the Pulumi update plan. The gap is that the server does
not check that `plan-display`, which the approvers read, and `plan-apply`,
which the apply enforces, say the same thing.

- **O11-a. Do nothing, and list it as a residual risk.**
- **O11-b. The server reads `plan-apply` itself.** The Pulumi update plan
  is a JSON document. The server parses it in Go, with the limits of a
  document from outside, and computes the set of operations (each
  resource address and its kind of change). A difference from the set of
  operations of `plan-display` fails closed: the plan is not readable for
  approval (`builtin/plan-readable`). The set of operations that the
  server computed is a fact of the origin `server`.
- **O11-c. A render step in a fresh sandbox.** A check step after the plan
  makes the display from `plan-apply`, with no program and no credential:
  `tofu show -json` on the plan file. The providers come from the lock
  file of the base branch (invariant I1), not of the head. The result is
  an unverified fact, but no code of the change ran in that sandbox.

**Recommendation.** O11-b for Pulumi, because the server can read the
format in Go. O11-c for OpenTofu, because its plan file format is internal
to OpenTofu. Until each one exists, O11-a for that tool.

**ELI5.** The helper gives us a photo of the dish and the recipe card that
the kitchen will follow. We check that the photo and the card show the
same dish.

### O12. The apply runs the program again (G13)

- **O12-a. Do nothing, and document it.** The approval must cover the
  program diff, not only the plan.
- **O12-b. A required owner review of the program.** A starter pack: an
  ownership rule in `enforce` mode on the path set of the program (all
  paths not in the manifest path set), with the platform team as owners.
  The approver signs the code diff and the plan.
- **O12-c. Narrow the apply proxy rules.** The apply proxy rules allow
  only the paths of the resources in the plan. This needs a template
  variable from the plan, and a map from each resource to its API paths,
  which is different for each provider.

**Recommendation.** O12-a and O12-b now. O12-c not now. `pulumi up
--plan` (ADR 102 decision 7) limits the resource changes. The apply proxy
rules and the review of the program limit the rest. A code owner who
approves a hostile program diff (A5) stays a residual risk.

**ELI5.** The helper does the real work later, and could do something
other than the preview showed. So a cook who owns the recipe must read
the recipe, not only the photo of the dish.

### O13. The profiles and the proxy rules in the plan binding (G15)

- **O13-a. Do nothing.** A profile can change between the plan and the
  apply, and the apply keeps the proxy rules of the job start.
- **O13-b. Add both to the plan binding.** The plan binding holds the
  revision of each profile that the job opened and the SHA-256 of the
  proxy rule set that the server renders for the unit now. At the dispatch
  of an apply phase, a changed revision or a changed digest fails with
  `ErrPlanStale`, and the job plans again with the new values (ADR 102
  decision 7).

**Recommendation.** O13-b. It is two more fields in a check that exists.

**ELI5.** If somebody changed the guard's list after the preview, we do
the preview again with the new list.

### O14. One view of the chain (G16)

- **O14-a. Do nothing.** The records exist in different places.
- **O14-b. A read route.** `GET .../jobs/{id}/chain` answers, for each
  step: the decision, its source (the rule, the grant or the gate, with
  its version) and the link to the record. It stores nothing new. It
  needs `unit:read` on each unit of the job.

**Recommendation.** O14-b. It answers the "No magic" question "why did
this run with this credential" in one call.

**ELI5.** One page that says, step by step, who let the helper in and
why.

## Proposal

The proposal is the recommendation of each option set:

1. `untrustedSourceCredentials` with `direct` and `proxy-only`, for each
   phase of each job of an untrusted source (O1-b), and the hardening H1
   to H8.
2. A new decision point `change.plan` before each plan phase (O4-b). It
   can deny, and can hold the plan for approve and run, with a run
   signature that never counts for a plan (O2-b).
3. Ownership facts from the stored ownership rules, `false` when a
   changer is unknown (O3-b).
4. Path facts from two trees that the server fetched, with renames,
   special entries and no silent cut (O5-b).
5. Scanners as check steps in a check phase of their own, or as external
   checks. Their output is an unverified fact, bound to the job and sent
   once. Only `deny` gates and gates after the plan can read it. This
   amends ADR 226 decision 10 (the scanner question).
6. A fixed check at the job start for a credential that also reaches the
   job, by taint and by value (O6-c).
7. The facts `proxy.planWriteMethods`, `planHostRules`,
   `planTunnelRules` (O8-b), `planRequests`, `planDenied` and
   `planDeniedHosts` (O10-b).
8. A budget for the job proxy (O9-b).
9. A check that the two plan parts agree (O11-b and O11-c).
10. A starter pack for an owner review of the program (O12-b).
11. The profile revisions and the proxy rule digest in the plan binding
    (O13-b).
12. A read route for the chain (O14-b).
13. RFC 16 items P13 and P14 are named as needed for this persona.

### The decisions that this proposal changes

| Decision | Change |
| --- | --- |
| ADR 224 decision 2 | A seventh decision point, `change.plan`. |
| ADR 224 decision 14 | The requirements of `change.plan` block the plan phase. A run signature counts only for `change.plan`. |
| ADR 226 decisions 2 and 4 | Each fact group names its origin. New groups `checkSteps` and `ownership`, and new `proxy` and `paths` fields. |
| ADR 226 decision 10 | A check can also run as a check step (the scanner question). |
| ADR 226 decision 13 | The paths come from two trees that the server fetched, with `paths.special`. |
| ADR 100 decision 4 | A fifth phase kind, `check`. |
| ADR 102 decision 7 | The two plan parts must agree. The binding holds the profile revisions and the proxy rule digest. |
| ADR 203 decision 1 | The server derives the consumer `job-proxy` from the proxy session (H1). |
| ADR 330 decisions 2, 4 and 7 | H3 (isolation for an untrusted source), H5 (read-only checks), H6 (the header scan, no `query` credential), H7 (no `tunnel` rule for an untrusted source by default). |

### The persona, end to end

With the proposal, the persona configures:

- A path set `manifests` (`manifests/**`) and a path set `program` (each
  other path).
- An ownership rule for each team on its manifests, and one for the
  platform team on `program`, in `enforce` mode.
- A profile with the root token in `exports.proxy` only.
- Proxy rules: for `plan`, `GET` on a list of read paths of
  `api.downstream.example`, for example `/v1/items/*` and
  `/v1/items/*/settings`, not the whole host. For `apply`, the methods
  and paths of the resources that the stack manages.
- `untrustedSourceCredentials: proxy-only`, forced at the collection, on
  a pool with `gvisor` isolation.
- A check step `manifest-schema` (conftest, with the policy files of the
  base branch) in the check phase.
- Gates at `change.plan`, each with the outcome `deny` or `approval:`:
  - `deny` unless `checkSteps.passed` holds `manifest-schema`. A missing,
    failed or timed-out check denies.
  - `deny` when `proxy.planWriteMethods > 0`, `proxy.planHostRules > 0`
    or `proxy.planTunnelRules > 0`.
  - `approval:platform-team` unless `ownership.available`,
    `ownership.changerOwnsAll` and `paths.only["manifests"]`. This gate
    reads only facts of the origin `server`.
- A gate at `change.planned`: `approval:security` when
  `proxy.planDenied > 0`.

Then the attack goes like this:

| Example | Stopped at | How |
| --- | --- | --- |
| 1. Steal the root token | Step 3, with H1 to H6 | The token is not in the job. Each host of the attacker gets 403. |
| 2. Call a delete in the preview | Step 3 with H5, and step 2 when the program changes | The plan proxy rules list read paths only, and H5 refuses upgrades, overrides and bodies. A program change needs approve and run. A `GET` with a side effect on a listed path stays possible. |
| 3. Read data and print it | Partly, steps 2 and 4 | A manifest-only change cannot change what the program reads. A program change needs approve and run. The budget limits the volume. See "Residual risk". |
| 4. Use the runner | Step 4 | One route, to the job proxy. CPU, memory and time limits. |
| 5. Act differently in the apply | Steps 5 and 6, partly | `pulumi up --plan` limits the resource changes. The owner of the program reviews the program diff. The apply proxy rules limit the reach. See "Residual risk". |

## Residual risk

These stay possible with the full proposal. The docs must say each one.

1. **Abuse inside the proxy rules.** The job proxy does not judge intent
   (ADR 327). A plan can make each request that the plan proxy rules
   allow.
2. **A read with a root token can be a credential theft.** A `GET` on a
   path such as `/api/tokens`, `/users/{id}/keys` or a secret store can
   return other long-lived credentials. The response scan does not know
   them. The program can print them to a log that the author can read.
   Only a list of read paths that leaves these paths out limits this.
3. **A `GET` with a side effect.** H5 refuses the generic ways to write
   with `GET`: upgrades, method overrides and bodies. An API that changes
   data, or opens a channel, on a `GET` of a listed path is specific to
   that API, for example Kubernetes `exec` and `proxy` paths or an RPC
   endpoint with its action in the query. Only the allowlist of methods
   and paths limits it, and only when the person who writes it leaves
   these paths out. The table in G8 lists the known cases.
4. **Data that the plan reads can leak through the plan and the log.**
   The plan and the job log mask only values that the server knows as
   secret. A program that reads a value of the downstream system and
   prints it puts it where the author of the change may read it. Defense:
   a manifest-only change cannot change the program, and a program change
   needs approve and run. The plan comment holds no values (ADR 299
   decision 25).
5. **Other channels out.** The CONNECT host strings and the paths of a
   request go into the proxy records, `planDeniedHosts`, notifications and
   audit entries. A program can put a little data in them. H8 limits the
   size and the characters. A `tunnel` rule, when a gate allows it (H7),
   is a wide channel.
6. **Hostile data in a manifest.** When the program uses a value of a
   manifest unsafely (for example as a URL, a path or a command), a
   manifest alone can be hostile. The program and the schema check are
   the duty of the platform team. The server cannot judge them.
7. **The apply runs code.** See G13. A code owner who approves a hostile
   program diff (A5) is out of scope.
8. **The plan parts until O11 exists.** For a tool with no check of O11,
   the display and the plan file can differ.
9. **Credentials that sign each request** (for example AWS Signature
   Version 4) cannot go through the job proxy as a credential. Such a
   unit must use workload identity with a short life, or `direct`.
10. **Response scan limits.** A downstream system that echoes the
    credential in a form that the scan does not know passes the scan (ADR
    327).
11. **Sandbox escape.** A kernel or hypervisor bug can break the
    isolation. The isolation probe finds a wrong network setup, not a
    kernel bug. H3 keeps an untrusted source off the weakest isolation.
12. **Name constraints.** A client that does not honor the name
    constraint of the CA of a claim trusts more names than it should (RFC
    16, open).
13. **Scanners are heuristic, and they see only the commit.** A scanner
    over source finds known patterns. Obfuscated code can pass it. A
    package or a plugin that the plan downloads arrives after the check
    phase, so the scanner never sees it. The vetted supply chain of
    [RFC 93](0093-vetted-supply-chain-for-providers-modules-and-plugins.md)
    is the layer for that.
14. **A stolen account of a code owner** has the same reach as the owner.
    A fresh authentication on the approval (ADR 227) narrows it.

## One-way doors

Before 1.0 the tool-neutral API can change (ADR 73), so few parts of this
proposal are one-way in the API. These parts are harder to undo:

- **A new decision point.** Users write gates for it. The removal of a
  decision point makes each of those gates invalid. A new decision point
  needs a new ADR (ADR 224 decision 2).
- **The security promise of `proxy-only`.** When the docs say "the
  credential never enters the job", teams build on it. A later exception
  breaks their threat model without a change in their configuration. So
  H1 to H8 must hold before the docs make the promise.
- **The origin of each fact.** Once gates rely on the origin, a change of
  the origin of a group can turn a refused gate into an allowed one.
- **Ownership facts from the code owners file of the Git host (O3-c).**
  When the server reads it, each Git host format becomes a contract.
- **Proxy records as facts (O10-b).** The record fields become a part of
  the fact schema, with a version.

These parts are two-way: the default of `untrustedSourceCredentials`, the
budget defaults, the starter packs, the read route of the chain.

## Benchmark

| Product | What it does at each step | Source |
| --- | --- | --- |
| GitHub Actions, `pull_request` | A workflow of a fork gets a read-only `GITHUB_TOKEN` and no secrets. Fork approval policies limit who runs a workflow. | [Securely using pull_request_target](https://docs.github.com/en/actions/reference/security/securely-using-pull_request_target) |
| GitHub Actions, `pull_request_target` | Runs in the context of the base, with secrets. The docs warn against a checkout of the head of a fork in it. A required label, or an environment with required reviewers, can gate it. | Same page, and [DVC on environments for forks](https://blog.dvc.org/blog/testing-external-contributions-using-github-actions-secrets) |
| GitHub environments and OIDC | A job that names an environment with required reviewers waits for an approval. The OIDC subject claim names the environment, so a cloud trust policy can require it. | [OpenID Connect reference](https://docs.github.com/en/actions/reference/security/oidc) |
| Atlantis | `--allow-fork-prs` is off by default. The docs say that apply approvals are not enough, because a plan runs code through an `external` data source or a provider. | [Atlantis security](https://www.runatlantis.io/docs/security.html) |
| HCP Terraform | No speculative plan for a pull request from a fork, "to avoid running untrusted code or exposing sensitive information". Terraform Enterprise has a setting to allow it. | [UI and VCS-driven run workflow](https://developer.hashicorp.com/terraform/cloud-docs/run/ui) |
| Spacelift | An initialization policy ran before the run and blocked custom code, without the plan. It is deprecated in favor of the approval policy. A plan policy runs after the plan. | [Spacelift policies](https://docs.spacelift.io/concepts/policy), [deprecated policies](https://docs.spacelift.io/concepts/policy/deprecated) |
| env0 | Approval policies (OPA) run after the plan and the cost estimate. Custom flows run commands before or after the plan, in the deployment. | [env0 approval policies](https://docs.envzero.com/guides/policies-governance/approval-policies) |
| OPA conftest | Tests structured files (YAML, JSON, HCL and others) with Rego. It fits a check over manifests before a plan. | [conftest](https://github.com/open-policy-agent/conftest) |
| Pulumi policy packs | Check each resource during the preview, after the program registers it. Stack policies run after all resources. They see resources, not HTTP calls of the program. | [Pulumi policy core concepts](https://www.pulumi.com/docs/iac/crossguard/core-concepts/) |
| Pulumi ESC | An environment gives short-lived credentials when it is opened. `open` is a permission of its own, apart from `write`. | [Pulumi ESC environments](https://www.pulumi.com/docs/esc/concepts/environments/) |
| Smokescreen | An HTTP CONNECT proxy with an ACL for each client and a check that the resolved address is public. No credential injection. | [stripe/smokescreen](https://github.com/stripe/smokescreen) |
| Envoy credential injector | An HTTP filter that adds a credential to each request, for the identity of the workload behind the proxy. | [Envoy credential injector](https://www.envoyproxy.io/docs/envoy/latest/configuration/http/http_filters/credential_injector_filter) |
| Teleport application access | The proxy adds a signed JWT, or an ID token, to a header of each request to the app, through a rewrite rule. | [Teleport JWT for application access](https://goteleport.com/docs/enroll-resources/application-access/jwt/introduction/) |

What the benchmark shows:

- No product runs a speculative plan of untrusted code with a privileged
  credential by default. Each one gives "no plan" or "no credentials" for
  a fork, and "an approval first" as the way out. ADR 299 already does
  this. The proxy-only level of O1 is new.
- Spacelift and env0 have a hook before the plan. Spacelift moved away
  from a separate kind of policy for it. The decision point of O4 is the
  same idea, in the one gate model of ADR 222.
- Credential injection exists in Envoy and Teleport, but neither limits a
  credential to read paths for one phase of one job. The job proxy of
  ADR 330 does, with the hardening of H5.
- Tailscale was in the brief. The author did not check its docs for
  credential injection, so it is not in the table.

## Open questions

The owner keeps these. Each question says what it decides, and the
recommendation.

1. **A middle credential level.** Should a unit be able to set
   `untrustedSourceCredentials: proxy-only`, so that each job whose commit
   is not the head of a protected branch reaches the downstream system
   only through the job proxy, and never holds the credential? The default
   stays `direct`, with a warning when a unit has a proxy credential.
   Recommendation: yes (O1-b).
2. **A checkpoint before the plan.** Should a new decision point,
   `change.plan`, run before each plan phase, so that gates can deny a
   plan or hold it until a person approves the code diff ("approve and
   run")? This changes ADR 224 decisions 2 and 14. Recommendation: yes
   (O4-b, O2-b).
3. **The approve-and-run signature.** Should the approval of the code
   diff before a plan be a separate kind of signature, which never counts
   as the approval of a plan, also for the same approval rule?
   Recommendation: yes (O2-b).
4. **Who owns the changed files.** Should the server compute "the person
   who changed these files owns each of them" from its stored ownership
   rules now, and from the code owners file of the Git host later? The
   fact is `false` when the server does not know each person who changed
   a file. Recommendation: stored rules now (O3-b), the code owners file
   later (O3-c).
5. **What an external check before the plan can read.** The changed
   paths only, the diff, or the full source snapshot of the commit? With
   which token, and for how long? Recommendation: a token that reads the
   snapshot of that commit only, and ends with the timeout of the check.
6. **Body matching and the rule tester.** RFC 16 item P14 lets a proxy
   rule require a field of the request body, for example a GraphQL
   `query`, so that an API that reads with `POST` can stay read-only. Item
   P13 replays past requests against a changed proxy rule set. Accept both
   for this persona? Recommendation: yes (O7-b).
7. **The budget of the job proxy.** Defaults of 10,000 requests and 1 GiB
   of response bytes for a plan phase, shown in the API and changeable for
   each unit. Accept these numbers until the proxy work measures real
   jobs? Recommendation: yes (O9-b).
8. **The two plan parts.** ADR 102 decision 7 already binds the apply to
   the Pulumi update plan and the OpenTofu plan file. Should the server
   also check that the plan that people read and the plan file that the
   apply follows show the same changes: in Go for Pulumi, and by a render
   step in a fresh sandbox for OpenTofu? Recommendation: yes (O11-b,
   O11-c).
9. **Isolation for an untrusted source.** Should a job of an untrusted
   source with `proxy-only` credentials run only on `firecracker`,
   `gvisor` or a sandboxed Kubernetes runtime class, and fail on the
   default Kubernetes runtime? Recommendation: yes (H3).
10. **The scanner question.** Should a scanner be allowed to run as a
    check step on a runner, in a check phase of its own: a fresh sandbox,
    before any step that runs the program, no credential, the scanner from
    the server, a result bound to the job and sent once? Its output is an
    unverified fact, and only the server decides. This amends ADR 226
    decision 10 and adds a fifth phase kind. Recommendation: yes.
11. **Refuse, not warn.** Should the save of an approve-and-run gate that
    reads an unverified fact be refused? This guards who can run code with
    the proxied credential, so it is a security control, and ADR 347
    decision 4 keeps security controls out of "warn, do not block".
    Recommendation: refuse.
12. **The origin of each fact.** Should each fact group of each decision
    point name its origin (`server`, `job-proxy`, `runner`, `external`),
    also for the plan and the policy pack facts that exist today?
    Recommendation: yes.

## Proposed glossary lines

For `GLOSSARY.md`, after acceptance. This PR does not edit the glossary.

- **Untrusted source**: the source of a job whose commit the server did
  not resolve from the head of a branch in `protectedBranches`, whatever
  path started the job.
  _Avoid_: Untrusted code, external source
- **Proxy-only credentials**: the value `proxy-only` of the unit setting
  `untrustedSourceCredentials`. A job of an untrusted source opens only
  the credentials for the job proxy, so no credential enters the job.
  _Avoid_: Read-only mode, safe mode
- **Check step**: a step of a check phase of a job. It runs a scanner or a
  linter on a runner, in a fresh sandbox, with no credential and before any
  step that runs the program. Its result is an unverified fact.
  _Avoid_: Scanner step, plan step scanner, pre-flight check
- **Unverified fact**: a value that a process in a sandbox sends to the
  server, for example the result of a check step or the counts of a plan.
  A gate can read an unverified fact. An unverified fact alone never
  grants reach.
  _Avoid_: Claim (a claim is the durable claim of a phase), assertion,
  attestation (a signed statement, which an unverified fact is not)
- **Run signature**: the signature of approve and run. It meets a
  requirement of `change.plan` only, and binds to the head commit.
  _Avoid_: Pre-approval, run approval

## Checklist

- [x] Tolerates asynchronous replication between installations: each namespace writes only its own half, under its own fence, and no write waits for another installation (ADR 248 decisions 3, 6 and 9). Each new record and fact belongs to the namespace of the unit.

## Outcome

Empty until the RFC is accepted or dropped.
