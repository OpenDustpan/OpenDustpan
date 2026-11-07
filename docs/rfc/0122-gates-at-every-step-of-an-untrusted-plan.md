---
rfc: 122
title: Gates at every step of a plan that runs untrusted code with a privileged credential
status: discussing
created: 2026-11-07
---

# RFC 122. Gates at every step of a plan that runs untrusted code with a privileged credential

Status: discussing. Design only. An open question for the project owner.
Until the owner decides, `main` keeps the rules of the ADRs that this RFC
names.

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
  The program of the stack is owned by the platform team.
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
   can read.
4. The program uses the runner for other work: it scans the internal
   network, uses the CPU, or attacks the server.
5. The program behaves well in the preview and badly in the apply. The
   Pulumi CLI runs the program again in the apply, and a program can ask
   if it runs a preview.

Example 1 is the worst. A stolen root token stays valid after the job
ends. Each other example ends with the job.

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

## The principle

Each step of the chain is a layer. Each layer must stop its part of the
attack alone, with no help from the other layers. "Another step checks it"
is never a reason to leave a check out.

Three invariants hold at each step:

- **I1. The change does not govern its own run.** Each setting that
  governs a plan of a review request comes from the server, or from the
  base branch of the review request. It never comes from the head commit.
  Examples: proxy rules, profiles, gates, ownership rules, path sets and a
  code owners file. GitHub reads `CODEOWNERS` from the base branch for the
  same reason (ADR 225 decision 1).
- **I2. The credential is never in the job.** The program can use the
  root token only through the job proxy, for the requests that a proxy
  rule allows for the phase. It cannot read the token.
- **I3. Each decision has a record.** Each step writes who or what
  decided, on which facts, and why (see "No magic" in `CLAUDE.md`).

## The chain

The chain has seven steps. For each step, this section says what can be
gated, with which facts, what it stops, what exists now and what is
missing. The section "Options" has the options for each missing part.

| Step | When | Existing decisions | Missing parts |
| --- | --- | --- | --- |
| 1. Trigger and source | The push or the comment | ADR 267, ADR 299 decisions 20 to 23, ADR 300 decision 9, ADR 213 decision 2, ADR 226 decision 9, ADR 135 decision 6 | G1 |
| 2. Static check | Before any code runs | ADR 225 decision 9, ADR 226 decisions 10 and 13, ADR 224 decision 14 | G2, G3, G4 |
| 3. Credential | The job start | ADR 327, ADR 330, ADR 200 to 203, ADR 341 | G5, G6, G7 |
| 4. Sandbox | The plan phase | ADR 330 decisions 2 and 3, ADR 137 decisions 6 and 7, ADR 213 decision 2 | G8 |
| 5. After the plan | `change.planned` | ADR 226 decisions 4 and 7, ADR 225, ADR 299, ADR 141 | G9 |
| 6. Apply | `change.apply` and the dispatch | ADR 224 decision 3, ADR 191 decision 7, ADR 102 decision 7, ADR 299 decision 18 | G10, G11 |
| 7. Records | Each step | ADR 229, ADR 330 decision 10, the audit log | G12 |

### Step 1. The trigger and the source

**What can be gated.** Who can cause a plan of a review request, from
which repository and branch, and with which credentials.

**Facts.** The opener and each pusher of the head, and the principals
they link to. The repository ID (a fork or not). Whether a bot pushed.
`source.kind`, `source.ref`, `source.protected`, `source.commit`.

**What exists.**

- ADR 299 decision 20: a plan runs with credentials only when the opener
  and each pusher of the head link to a principal with `unit:run-plan`.
  Else the plan has no provider credentials, no state, no secret outputs
  and no outbound network, and can never be approved. Credentials never
  follow a trusted comment alone.
- ADR 299 decision 21: a review request from a fork gets no plan until a
  trusted person comments on the head commit. A new push voids the
  comment. The plan of a fork has no credentials.
- ADR 299 decision 22: bots by app ID, and the person behind each push.
- ADR 299 decision 23: "plan with credentials as me", for the same
  repository only.
- ADR 300 decision 9 and ADR 213 decision 2: a plan that starts from the
  Git host never gets `unit:decrypt` or an unmasked read of state.
- ADR 226 decision 9 and ADR 135 decision 6: the source filter decides
  which submodules and Git LFS objects a snapshot holds. Its
  `protectedBranches` give `source.protected`.

**What it stops.** A1 (forks), A2 (users with no grant), and a bot that a
user controls. It also stops secret config of the stack from reaching a
plan of a review request.

**What it does not stop.** A3 and A4. Their plan gets the full
credentials of the unit.

**Missing (G1).** A middle level between "no credential" and "all
credentials": a plan that can read the downstream system through the job
proxy, and nothing more. See option set O1.

### Step 2. A static check before any code runs

**What can be gated.** The changed files and the people who changed them,
before the runner gets the job.

**Facts.** `change.paths`, `paths.touches["<set>"]` and
`paths.only["<set>"]` (ADR 226 decision 13). The principal of the change.
The result of an external check at the stage after `change.propose` (ADR
226 decision 10), or of a check step on a runner (an unverified fact, see "The
scanner question").

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
  "A requirement from `change.propose` never blocks the plan phase." So
  an ownership rule asks for approvals after the plan, when the program
  has already run. There is no "approve and run", which GitHub has for
  forks and for environments with required reviewers. ADR 299 decision 21
  has it for forks only, as a trusted comment, and that plan has no
  credentials.
- **G3. No fact says that the changer owns what they changed.** An
  ownership rule names who must approve. No fact says "each changed path
  is owned by the person who changed it". This is the fact that the
  persona needs: a user who changes only their own manifest may plan with
  the read credential, and a user who changes another team's manifest or
  the program may not. The code owners file on the Git host does not help
  at plan time, because the Git host enforces it only at the merge.
- **G4. No decision point reads a pre-plan check.** The fact
  `externalChecks` is in the schema of `change.planned` only (ADR 226
  decision 4). `change.propose` holds the groups that exist before a plan.
  So the result of an external check at the stage after `change.propose`
  has no gate that reads it before the plan phase starts.

See option sets O2, O3 and O4.

### Step 3. The credential

**What can be gated.** Which credential the job can use, for which hosts,
methods and paths, in which phase.

**Facts.** The group `proxy` at `change.propose`, `change.planned` and
`change.apply`: `enabled`, `rules`, `ruleSetDigest`, `credentialHosts`,
`plainHttp` and `learnMode` (ADR 330 decision 9). The profile facts at
`profile.open` (ADR 226 decision 5).

**What exists.**

- The job proxy (ADR 327, ADR 330). It runs next to each job, as its own
  process and user, outside the sandbox. It is the only exit of the
  sandbox. A proxy rule matches the host, the method and the path, and
  has an `operations` list (`plan`, `apply`, `destroy`, `refresh`). So a
  proxy rule for the plan can allow only `GET` to one host, and a proxy
  rule for the apply can allow more.
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
  a review request cannot change them (invariant I1).

**What it stops.** Example 1 (theft of the token) in each phase: the
token is not in the job. Example 2 in the plan phase, when the plan proxy
rules allow only read methods. Each host of the attacker: a host that no
proxy rule names gets 403 before any TLS. A change of the stack config
file in the repository that points the provider at another host: the
provider sends the placeholder, and the job proxy refuses the host.

**Missing.**

- **G5. A credential can still reach the job by a second path.** The same
  secret source can feed `exports.proxy` and also an environment variable
  of the launcher, through a second export of the same or of another
  attached profile. ADR 341 stops a person without `profile:open` from
  making this change. It does not stop an existing profile that already
  has both. See option set O5.
- **G6. "Read only" is not always a method.** Some APIs read with `POST`
  (a search, GraphQL). For these, a rule on the method and the path
  allows writes too. Body matching (RFC 16 item P14) is proposed and not
  accepted.
- **G7. The same proxy rules for a trusted and an untrusted plan.** A
  proxy rule has `operations`, but no condition on the source of the
  change. A plan of a protected branch and a plan of a review request get
  the same plan proxy rules. See option set O7.

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
- The job token of a plan of the Git host gets no `unit:decrypt`, no
  unmasked state read, no state write and no read of another unit except
  shared outputs (ADR 213 decisions 1 and 2, ADR 300 decision 9).

**What it stops.** Example 4: a scan of the internal network, a direct
call to the server that skips the job proxy, a write of state.

**Missing (G8).** A budget for the job proxy in each phase: a count of
requests and a count of bytes. Today a plan can send each request that
the proxy rules allow, as often as it likes, until the job times out. A
budget limits the volume of example 3 (data read through the read
credential) and the load on the downstream system. Run limits and the
time limit of a job are in [RFC 107](0107-run-limits-and-the-run-queue.md),
which is a stub. See option set O8.

### Step 5. After the plan

**What can be gated.** Whether the change can go to the apply, and who
must approve it.

**Facts.** The groups of `change.planned` (ADR 226 decision 4): `plan`,
`resourcePolicy` (the report of the policy pack), `externalChecks` (for
example a scanner over the plan display), `cost`, `paths`, `proxy`,
`dependents`.

**What exists.**

- Gates at `change.planned`, approval rules, approval defaults and
  ownership rules (ADR 224, ADR 225).
- The review modes and the binding levels of an approval to a plan (ADR
  299).
- Freeze schedules (ADR 141).
- The built-in gates `builtin/plan-readable` and
  `builtin/mandatory-resource-policy` (ADR 226 decision 7).
- A scanner over the plan display as an external check (RFC 90, option
  A3).

**What it stops.** An apply of a plan that a person did not approve, or
that fails a policy, a scanner or a cost limit. It stops example 5 only
when the approver reads the program diff too (see step 6).

**Missing (G9).** The proxy records of the plan phase are not a fact. A
plan that tried a request that no proxy rule allows is a strong signal of
a hostile change, or of a missing proxy rule. A gate cannot read it
today. See option set O9.

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
- Each gate decision reads the head commit fresh from the Git host, and
  a difference fails closed (ADR 299 decision 18).
- The apply proxy rules are the proxy rules with `apply` in
  `operations`. The rendered proxy rules are the same in each phase of
  one job (ADR 330 decision 4).

**What it stops.** An apply of a commit or of a state that the approvers
did not see. An apply that reaches a host or a path that no apply proxy
rule allows.

**Missing.**

- **G10. The Pulumi CLI runs the program again in the apply.** The plan
  does not bind what the program does in the apply. A program can test
  if it runs a preview and do other work when it does not. OpenTofu
  applies a saved plan, but it still runs the code of each provider. So
  facts of the plan do not prove what the apply does. Only a person who
  reads the program diff, and the apply proxy rules, limit it. See option
  set O10.
- **G11. The staleness check does not cover the profiles.** A profile can
  get a new revision between the plan and the apply. The plan binding
  holds the plan hashes and the state version (ADR 102 decision 7), not
  the revisions of the profiles that the job opened. See option set O11.

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

**Missing (G12).** One view of the full chain of one change. A person who
asks "why did this plan run with the read credential" must read the
trigger, the credential decision of step 1, the decision record of each
decision point, the proxy records and the approvals, in different places.
See option set O12.

## Options

Each option set has options, their consequences, a recommendation and a
short explanation for the owner (ELI5).

### O1. A middle level of credentials for a plan of a review request (G1)

- **O1-a. Do nothing.** A3 gets the root token in the job. A2 gets a
  useless preview. The persona must choose between a useful preview and a
  safe one.
- **O1-b. Proxy-only credentials.** A unit setting
  `reviewPlanCredentials` with the values `direct` and `proxy-only`. With
  `proxy-only`, a plan that starts from the Git host opens only the export
  key `proxy` and the values that are not secret. The job start refuses a
  plan when an attached profile gives a secret value to the consumer
  `launcher` (`jobproxy.ErrCredentialExposed`, which names the profile and
  the export). The unit setting cascade (installation, namespace,
  collection, unit) gives the effective value, and a level can force it.
  - For: the persona gets a preview that reads the downstream system, and
    the token never enters the job.
  - Against: a credential that signs each request (for example AWS
    Signature Version 4) cannot be added by a proxy. A unit with such a
    credential cannot use `proxy-only`. It uses workload identity, or
    `direct`.
- **O1-c. Proxy-only for A3, and a third level for A2.** As O1-b, and a
  plan of A2 (no grant to plan) also gets proxy-only credentials when a
  gate at the new decision point of O4 allows it. This changes ADR 299
  decision 20, which is an owner decision.
  - For: a self-service user needs no `unit:run-plan` grant.
  - Against: it opens the read credential to each linked user with no
    grant. It weakens a rule that the owner set.

**Recommendation.** O1-b. Keep ADR 299 decision 20 as it is. The default
of `reviewPlanCredentials` is `direct`, because most cloud credentials sign
requests. When a unit has a proxy rule set with a credential and the
setting is `direct`, the API and the console warn (ADR 347). A new fact
`proxy.reviewPlanCredentials` lets a namespace gate deny a review plan
that is not `proxy-only`.

**ELI5.** Today the helper who does the preview either gets the master key
or gets no key. With this option the helper gets no key. A guard at the
door holds the key, and opens only the doors on the guard's list, and only
to look.

### O2. A requirement that holds the plan (G2)

- **O2-a. Do nothing.** A requirement never blocks the plan. A gate can
  only deny. The persona must deny each change of the program by a user,
  or let it run.
- **O2-b. Approve and run.** A gate at the decision point of O4 can name
  an approval rule. The plan phase does not dispatch until the
  requirement is met. The signature binds to the head commit, and a new
  push voids it. This is the trusted comment of ADR 299 decision 21, made
  general, and with the credential level of O1 in place of "no
  credentials".
- **O2-c. Change ADR 224 decision 14.** Let a requirement at
  `change.propose` block the plan. This changes the meaning of each
  existing gate at `change.propose`.

**Recommendation.** O2-b. It does not change an existing gate. The
signer approves the code diff, not a plan, so the console shows the diff
of the program and of the manifests, and says that no plan exists yet.

**ELI5.** Before the helper runs the new recipe, a cook who owns that
part of the kitchen reads it and says "yes, you can try it".

### O3. A fact that says the changer owns what they changed (G3)

- **O3-a. Do nothing.** Path sets only. The persona writes one path set
  for each team and one gate for each team.
- **O3-b. Ownership facts from the stored ownership rules.** A new fact
  group `ownership` at the decision point of O4: `available`,
  `changerOwnsAll` (each changed path matches an ownership rule whose
  groups or users hold the opener and each pusher), `unownedPaths` (the
  count of changed paths that no rule matches) and `foreignPaths` (the
  count of changed paths whose rules do not hold a changer).
- **O3-c. As O3-b, and the code owners file of the Git host as an input.**
  The server reads the code owners file of the base branch, never of the
  head (invariant I1), through the Forge port. It maps each owner of the
  Git host to a principal through the account links of ADR 299. An owner
  with no link counts as not a changer. A file that does not parse gives
  `available == false`.
  - For: the persona keeps one list of owners.
  - Against: a second source of owners, with the format of each Git host.
    GitLab, Gitea and Forgejo have other formats or none.

**Recommendation.** O3-b first, O3-c later as a producer behind its own
port with a conformance suite. A gate can then say: "a review plan gets
`proxy-only` credentials when `ownership.changerOwnsAll` and
`paths.only["manifests"]`. Else it needs approve and run."

**ELI5.** The list on the wall says which shelf belongs to whom. A person
who moves only things on their own shelf can go on. A person who touches
another shelf must ask first.

### O4. A decision point that reads pre-plan checks (G4)

- **O4-a. Do nothing.** Pre-plan external checks exist, but no gate reads
  them before the plan.
- **O4-b. A new decision point `change.plan`.** It runs before the
  dispatch of each plan phase, after each external check of the stage
  after `change.propose` has an answer or has timed out. Its fact schema
  holds the groups of `change.propose`, and `externalChecks`,
  `checkSteps`, `ownership` (O3) and `proxy`, each with its origin. A `deny` refuses the plan. A requirement holds the
  plan (O2-b). A new decision point needs a new ADR (ADR 224 decision 2).
- **O4-c. Evaluate `change.propose` two times.** Once at the start, and
  again after the pre-plan checks. One decision point with two meanings.
- **O4-d. Only the facts of the server.** No scanner result before the
  plan. Gates read paths, ownership and proxy facts only.

**Recommendation.** O4-b. It gives a clear name to the moment that the
persona needs: "may this code run with this credential". It keeps
`change.propose` as it is. The results of a check step (next section) and
of an external check both reach `change.plan`.

**ELI5.** We add one more checkpoint just before the helper starts the
work. At this checkpoint we read the results of the inspectors.

### The scanner question

The owner said "yes" to scanners at the plan step. On 7 November 2026 the
owner added: "having steps run on the runners isn't an issue, as long as
the gating happens on the trusted side (our backend)." This section
designs for that input.

#### The rule: the runner reports, the server decides

A scanner can run as a job step on a runner. Its output is an **unverified fact**: a
value that a process in a sandbox sent to the server. An unverified fact is never a
fact that the server trusts by itself. The gate in the server decides,
over the facts that the server verifies and over the unverified facts, with the
origin of each value in the fact document.

#### The risk, and how the order of the steps removes it

The risk: a scanner in the same sandbox as the program can be tricked. If
the untrusted program runs first, or at the same time, it can change the
files that the scanner reads, replace the scanner binary, or write the
report itself. The result is then a forgery.

The design removes this with the order of the steps and with the sandbox:

1. **A check phase of its own.** A job of a review request gets a phase
   `check` before the plan phase. The check phase runs one or more check
   steps. No step of the check phase runs the program, a provider, a
   package install or a script of the repository.
2. **A fresh sandbox.** The check phase runs in its own sandbox, on its
   own claim. The coordinator destroys the sandbox at the end of the phase
   (ADR 137 decision 1). The plan phase gets a new sandbox. So no code of
   the change runs before the scanner or next to it.
3. **No credential and no route.** The check phase opens no profile
   export, also not `exports.proxy`. Its proxy rule set holds no rule for
   the downstream host, and no rule with a credential. The only route out
   is the built-in tunnel to the server (ADR 330 decision 3). The job
   token of the check phase can read the source snapshot of the job and
   send the result of the step, and nothing more.
4. **The scanner comes from the server.** The image of the check step and
   its digest, the scanner configuration and the policy files come from
   the server or from the base branch, never from the head commit
   (invariant I1). A `.checkov.yaml` or a `policy/` directory in the head
   commit does not change the check.
5. **The server binds the result.** The launcher sends the result with the
   SHA-256 of the source snapshot that it scanned. The server refuses a
   result whose snapshot hash is not the hash of the job. The server
   stores the raw report as a history item, and its SHA-256 goes into the
   decision record.

What stays: the scanner reads the files of the change, and these files
are hostile input. A file made to exploit a bug in the scanner can make
the scanner lie. The sandbox, the missing credential and the missing
route limit what that exploit can reach. The rule below limits what
the lie can unlock.

#### Facts that the server verifies, and unverified facts

| Value | Origin | Why |
| --- | --- | --- |
| `change.paths`, `paths.touches`, `paths.only` | Server | The server reads the changed paths from the Git host, or computes them from the source snapshot that it builds itself (ADR 135). |
| `source.commit`, `source.protected`, `source.kind` | Server | The server resolves the commit and the branch at the start (ADR 213 decision 2). |
| The principal, the opener, each pusher and their links | Server | Stored grants and links (ADR 299 decisions 20 and 22). |
| `ownership.*` (O3) | Server | Stored ownership rules matched against the paths that the server read. |
| `proxy.enabled`, `rules`, `ruleSetDigest`, `credentialHosts`, `planWriteMethods` | Server | The server renders the proxy rules at the job start. |
| `proxy.planRequests`, `planDenied`, `planDeniedHosts` (O9) | Job proxy | The job proxy runs outside the sandbox, as its own user (ADR 330 decision 1). The program cannot write its records. |
| The plan hash and the plan binding | Server | The server stores the plan and its hashes. The apply checks the binding (ADR 102 decision 7). The hash proves which plan the approvers saw. It does not prove that the content is true. |
| `plan.add`, `change`, `replace`, `destroy`, `resourceTypes` | Runner, unverified | The tool in the sandbox writes the plan. A hostile program can make the plan show less than it does. |
| `resourcePolicy.*` | Runner, unverified | The policy pack runs in the same plan, next to the program. |
| `checkSteps.*` (new, the result of a check step) | Runner, unverified | A scanner in the check phase. Order and sandbox as above. |
| `externalChecks.*` | External service | A signed callback from a service that the team runs (ADR 226 decision 10). It is as trusted as that service. |

A plan count is an unverified fact too. This is true today for each plan. This RFC
makes it visible.

#### What an unverified fact can do in a gate

- **An unverified fact can block.** A failed unverified fact can `deny`, or add a requirement.
  A forged "failed" only stops the change of the person who forged it.
- **An unverified fact alone cannot unlock.** A gate that grants more reach (the
  `proxy-only` credentials of O1, a plan with no approve and run of O2)
  must also hold on facts that the server verifies. The check at the save
  of a gate warns when an `allow` outcome at `change.plan` reads only
  unverified facts (ADR 347). A forged "passed" then removes one layer, and the
  layers of the server still hold.
- **The fact schema shows the origin.** Each group in the schema of a
  decision point names its origin: `server`, `job-proxy`, `runner` or
  `external`. The decision record shows it for each value that a gate
  read.

#### The external check stays

An external check (ADR 226 decision 10) stays an option for each scanner.
A team can run its scanner as a service, or in its own CI, and answer
with a signed callback. The external check gets the commit, the changed
paths and a token that can read the source snapshot of that commit only.
It gets no credential of the unit (open question 4).

A scanner over the plan runs after the plan, either as an external check
on the stored plan display (RFC 90, option A3), or as a check step in a
fresh sandbox that reads the stored plan display. It never runs in the
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
  the scanner and its configuration from the server or the base branch.
- The result of a check step is an unverified fact, with its origin in the fact
  schema. A gate in the server decides. An unverified fact alone never grants reach.
- A check step never runs in the sandbox of a plan or an apply phase.
- The check phase is a fifth phase kind, next to `plan`, `apply`,
  `deliver` and `verify` (ADR 100 decision 4, ADR 148 decision 3). The
  `verify` phase is a precedent: it holds runner steps that change nothing
  and check the change after the apply.

This needs the owner's yes (open question 9). The new ADR must name ADR
226 decision 10 in its "Changes" list, and RFC 49 in its context.

**ELI5.** A helper may check the drawings in a clean, empty room before
the stranger's machine is brought in. The helper has no keys. The helper
writes a note. The note can stop the work, but the note alone cannot
open a door. The guard at the front desk decides, and trusts what the
guard checked with their own eyes more than the note.

### O5. A credential that also reaches the job (G5)

- **O5-a. Do nothing.** ADR 341 stops new routing changes by people
  without `profile:open`. An old profile can still give the token to the
  job.
- **O5-b. A check at the job start.** When a secret source feeds an
  `exports.proxy` entry of a profile attached to the unit, and the same
  source also feeds a value for the consumer `launcher`, the job start
  fails with `jobproxy.ErrCredentialExposed`. The error names the source
  and the two exports. The check runs for each job, not only for review
  plans.
- **O5-c. A fact.** `proxy.credentialExposed` as a boolean, and a
  built-in gate in `enforce` mode that denies.

**Recommendation.** O5-b. It is a fixed control: a gate in `evaluate`
mode must not be able to turn it off. Defense in depth: the profile
routing rule stops the change at the save. This check stops it at the
job start, and does not rely on the save.

**ELI5.** If the guard holds the key, nobody may also put a copy of the
key in the helper's pocket. We check the pocket at the door.

### O6. Reads that use `POST` (G6)

- **O6-a. Do nothing.** A unit whose downstream API reads with `POST`
  allows all `POST` in the plan.
- **O6-b. Accept RFC 16 item P14 (body matching).** A proxy rule can
  require a field of the body to equal a value, for example the GraphQL
  operation type `query`.
- **O6-c. A path allowlist of read endpoints only.** Works when the API
  has distinct paths for search, and fails for GraphQL.

**Recommendation.** O6-b, with RFC 16 item P13 (the rule tester), so that
a person can test the plan proxy rules against past jobs. This RFC does
not accept them. It names them as needed for the persona.

**ELI5.** Some doors look the same for "look" and "change". The guard
must read the note in the person's hand, not only see which door they
use.

### O7. Narrower proxy rules for an untrusted plan (G7)

- **O7-a. Do nothing.** A gate at `change.plan` reads the proxy facts and
  denies a plan whose plan proxy rules allow a write method.
- **O7-b. A fact.** `proxy.planWriteMethods`: the count of proxy rules
  for `plan` that allow a method other than `GET`, `HEAD` and `OPTIONS`.
- **O7-c. A source condition on a proxy rule.** A field `sources` with the
  values `protected` and `review`. A proxy rule with `sources: [protected]`
  does not render for a plan of a review request.
  - Against: a new field in the proxy rule format, and two sets of plan
    rules to keep.

**Recommendation.** O7-b now. O7-c only when a real case needs a plan of
a protected branch to have more reach than a plan of a review request
(ADR 327 decision 4: "or" and "not" conditions only when a real case
needs them).

**ELI5.** We count how many "change" doors are on the guard's list for
the preview. A rule can say: for a preview, that number must be zero.

### O8. A budget for the job proxy (G8)

- **O8-a. Do nothing.** The job timeout is the only limit.
- **O8-b. A budget in the proxy rule set.** `budget` for each operation:
  the maximum count of requests and of response bytes. When a budget is
  spent, the job proxy refuses each next request with the fixed reason
  `budget-exceeded`, and the record says so.

**Recommendation.** O8-b, with a default that the API shows (for example
10,000 requests and 1 GiB for a plan). The values are a guess. The proxy
core work measures them.

**ELI5.** The guard lets the helper look through the door only so many
times.

### O9. The proxy records of the plan as facts (G9)

- **O9-a. Do nothing.** A person reads the proxy records.
- **O9-b. Facts at `change.planned` and `change.apply`.**
  `proxy.planRequests`, `proxy.planDenied` (counts) and
  `proxy.planDeniedHosts` (a set of hosts). A starter pack in `evaluate`
  mode asks for a security approval when `proxy.planDenied > 0`. An event
  notifies the owners of the unit.

**Recommendation.** O9-b. A denied request in the plan is the clearest
sign that this RFC can give.

**ELI5.** If the helper tried a door that was not on the list, we stop
and ask a grown-up before going on.

### O10. The apply runs the program again (G10)

- **O10-a. Do nothing, and document it.** The approval must cover the
  program diff, not only the plan.
- **O10-b. A required owner review of the program.** A starter pack: an
  ownership rule in `enforce` mode on the path set of the program (all
  paths not in the manifest path set), with the platform team as owners.
  The approver signs the code diff and the plan.
- **O10-c. Narrow the apply proxy rules.** The apply proxy rules allow
  only the paths of the resources in the plan. This needs a template
  variable from the plan, and a map from each resource to its API paths,
  which is different for each provider.
- **O10-d. Bind the apply to the plan in the tool.** OpenTofu applies the
  saved plan. The Pulumi CLI has update plans (`pulumi preview
  --save-plan` and `pulumi up --plan`), which the Pulumi docs mark as
  experimental, as far as the author could find. A spike must check what
  they constrain.

**Recommendation.** O10-a and O10-b now. O10-d as a spike. O10-c not now.
Residual risk: a code owner who approves a hostile program diff (A5).

**ELI5.** The helper does the real work later, and could do something
other than the preview showed. So a cook who owns the recipe must read
the recipe, not only the photo of the dish.

### O11. The profiles in the plan binding (G11)

- **O11-a. Do nothing.** A profile can change between the plan and the
  apply. The routing rule (ADR 202 decision 2) and the gates of
  `profile.save` limit who can change it.
- **O11-b. Add the revision of each opened profile to the plan binding.**
  The apply dispatch fails with `ErrPlanStale` when a revision changed,
  and the job plans again (ADR 102 decision 7).

**Recommendation.** O11-b. It is one more field in a check that exists.

**ELI5.** If somebody changed the guard's list after the preview, we do
the preview again.

### O12. One view of the chain (G12)

- **O12-a. Do nothing.** The records exist in different places.
- **O12-b. A read route.** `GET .../jobs/{id}/chain` answers, for each
  step: the decision, its source (the rule, the grant or the gate, with
  its version) and the link to the record. It stores nothing new. It
  needs `unit:read` on each unit of the job.

**Recommendation.** O12-b. It answers the "No magic" question "why did
this run with this credential" in one call.

**ELI5.** One page that says, step by step, who let the helper in and
why.

## Proposal

The proposal is the recommendation of each option set:

1. `reviewPlanCredentials` with `direct` and `proxy-only` (O1-b), and the
   fact `proxy.reviewPlanCredentials`.
2. A new decision point `change.plan` before each plan phase (O4-b), that
   reads pre-plan external checks, ownership facts and proxy facts, can
   deny, and can hold the plan for an approval (O2-b).
3. Ownership facts from the stored ownership rules (O3-b).
4. Scanners as check steps on a runner, in a check phase of their own
   with a fresh sandbox and no credential, or as external checks. Their
   output is an unverified fact. The gate in the server decides, and an unverified fact alone
   never grants reach. This amends ADR 226 decision 10 (the scanner
   question).
5. A fixed check at the job start for a credential that also reaches the
   job (O5-b).
6. The facts `proxy.planWriteMethods` (O7-b), `proxy.planRequests`,
   `proxy.planDenied` and `proxy.planDeniedHosts` (O9-b).
7. A budget for the job proxy (O8-b).
8. A starter pack for an owner review of the program (O10-b), and a spike
   on Pulumi update plans (O10-d).
9. The profile revisions in the plan binding (O11-b).
10. A read route for the chain (O12-b).
11. RFC 16 items P13 and P14 are named as needed for this persona.

### The persona, end to end

With the proposal, the persona configures:

- A path set `manifests` (`manifests/**`) and a path set `program` (each
  other path).
- An ownership rule for each team on its manifests, and one for the
  platform team on `program`, in `enforce` mode.
- A profile with the root token in `exports.proxy` only.
- Proxy rules: for `plan`, `GET` on `api.downstream.example` only. For
  `apply`, the methods and paths of the resources that the stack manages.
- `reviewPlanCredentials: proxy-only`, forced at the collection.
- A check step `manifest-schema` (conftest, with the policy files of the
  base branch) in the check phase.
- Gates at `change.plan`:
  - deny when `checkSteps.failed` holds `manifest-schema` (an unverified fact that
    blocks).
  - deny when `proxy.planWriteMethods > 0`.
  - allow when `ownership.changerOwnsAll` and `paths.only["manifests"]`.
  - else ask for the approval rule `platform-team` before the plan.
- A gate at `change.planned`: ask for `security` when
  `proxy.planDenied > 0`.

Then the attack goes like this:

| Example | Stopped at | How |
| --- | --- | --- |
| 1. Steal the token | Step 3 | The token is not in the job. Each host of the attacker gets 403. |
| 2. Call a delete in the preview | Step 3, and step 2 when the program changes | The plan proxy rules allow `GET` only. A program change needs approve and run. |
| 3. Read data and print it | Partly, steps 2 and 4 | A manifest-only change cannot change what the program reads. A program change needs approve and run. The budget limits the volume. See "Residual risk". |
| 4. Use the runner | Step 4 | One route, to the job proxy. CPU, memory and time limits. |
| 5. Act differently in the apply | Steps 5 and 6, partly | The owner of the program reviews the program diff. The apply proxy rules limit the reach. See "Residual risk". |

## Residual risk

These stay possible with the full proposal. The docs must say each one.

1. **Abuse inside the proxy rules.** The job proxy does not judge intent
   (ADR 327). A plan can make each request that the plan proxy rules
   allow. With a root token, a read rule reads all data of the downstream
   system.
2. **Data that the plan reads can leak through the plan and the log.**
   The plan and the job log mask only values that the server knows as
   secret. A program that reads a value of the downstream system and
   prints it puts it where the author of the review request may read it.
   Defense: a manifest-only change cannot change the program. A program
   change needs approve and run. The plan comment holds no values (ADR
   299 decision 25). A person with read access to the job still sees the
   log.
3. **Hostile data in a manifest.** When the program uses a value of a
   manifest unsafely (for example as a URL, a path or a command), a
   manifest alone can be hostile. The program and the schema check are
   the duty of the platform team. The server cannot judge them.
4. **The apply runs code.** See G10. A code owner who approves a hostile
   program diff (A5) is out of scope.
5. **Credentials that sign each request** (for example AWS Signature
   Version 4) cannot go through the job proxy as a credential. Such a
   unit must use workload identity with a short life, or `direct`.
6. **Response scan limits.** A downstream system that echoes the
   credential in a form that the scan does not know passes the scan (ADR
   327).
7. **Sandbox escape.** A kernel or hypervisor bug can break the
   isolation. The isolation probe finds a wrong network setup, not a
   kernel bug.
8. **Name constraints.** A client that does not honor the name constraint
   of the CA of a claim trusts more names than it should (RFC 16, open).
9. **Scanners are heuristic.** A scanner over source finds known
   patterns. Obfuscated code can pass it. The scanner is a layer, not a
   proof.
10. **A stolen account of a code owner** has the same reach as the owner.
    Fresh authentication on the approval (ADR 227) narrows it.

## One-way doors

Before 1.0 the tool-neutral API can change (ADR 73), so few parts of this
proposal are one-way in the API. These parts are harder to undo:

- **A new decision point.** Users write gates for it. The removal of a
  decision point makes each of those gates invalid. A new decision point
  needs a new ADR (ADR 224 decision 2).
- **The security promise of `proxy-only`.** When the docs say "the
  credential never enters the job", teams build on it. A later exception
  breaks their threat model without a change in their configuration.
- **Ownership facts from the code owners file of the Git host (O3-c).**
  When the server reads it, each Git host format becomes a contract.
- **Proxy records as facts (O9-b).** The record fields become a part of
  the fact schema, with a version.

These parts are two-way: the default of `reviewPlanCredentials`, the
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
  credential to read methods for one phase of one job. The job proxy of
  ADR 330 does.
- Tailscale was in the brief. The author did not check its docs for
  credential injection, so it is not in the table.

## Open questions

The owner keeps these. Each has a recommendation in the section
"Options".

1. Accept the proxy-only level (O1-b), with `direct` as the default and a
   warning?
2. Accept a new decision point `change.plan` (O4-b), with approve and run
   (O2-b)?
3. Ownership facts from stored rules first (O3-b), and the code owners
   file of the Git host later (O3-c)?
4. What can an external check before the plan read: the changed paths
   only, the diff, or the full source snapshot of the commit? With which
   token, and for how long? Recommendation: a token that reads the
   snapshot of that commit only, and ends with the timeout of the check.
5. Accept RFC 16 items P13 and P14 for this persona (O6-b)?
6. The budget defaults of the job proxy (O8-b): which numbers?
7. Does a denied request in the plan notify the owners by default (O9-b)?
8. The spike on Pulumi update plans (O10-d): in milestone 6 with the job
   proxy, or later?
9. The scanner question: accept check steps on a runner, with the rules
   of "The scanner question" (a check phase of its own, a fresh sandbox, no
   credential, the scanner from the server, the output as an unverified fact, an unverified fact
   alone never unlocks), as an amendment of ADR 226 decision 10?
10. The origin of each fact group in the fact schema (`server`,
    `job-proxy`, `runner`, `external`): accept it for all decision points,
    also for the plan and the policy pack facts that exist today?

## Proposed glossary lines

For `GLOSSARY.md`, after acceptance. This PR does not edit the glossary.

- **Proxy-only credentials**: the value `proxy-only` of the unit setting
  `reviewPlanCredentials`. A plan of a review request opens only the
  credentials for the job proxy, so no credential enters the job.
  _Avoid_: Read-only mode, safe mode
- **Check step**: a step of a check phase of a job. It runs a scanner or a
  linter on a runner, in a fresh sandbox, with no credential and before any
  step that runs the program. Its result is an unverified fact.
  _Avoid_: Scanner step, plan step scanner, pre-flight check
- **Unverified fact**: a value that a process in a sandbox sends to the server, for
  example the result of a check step or the counts of a plan. A gate can
  read an unverified fact. An unverified fact alone never grants reach.
  _Avoid_: Claim (a claim is the durable claim of a phase), assertion,
  attestation (a signed statement, which an unverified fact is not)
- **Approve and run**: an approval that a gate at `change.plan` asks for
  before the plan phase runs. It binds to the head commit.
  _Avoid_: Pre-approval, run approval

## Checklist

- [x] Tolerates asynchronous replication between installations: each namespace writes only its own half, under its own fence, and no write waits for another installation (ADR 248 decisions 3, 6 and 9). Each new record and fact belongs to the namespace of the unit.

## Outcome

Empty until the RFC is accepted or dropped.
