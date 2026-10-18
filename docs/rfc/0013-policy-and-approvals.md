---
rfc: 13
title: Policy and approvals
status: draft
created: 2026-10-10
---

# RFC 13. Policy and approvals

Status: draft. The names, the scope and the split into three kinds of policy
are proposals. Which policy engines OpenDustpan supports, and how, is to be
decided.

## Problem

RFC 12 lets a principal edit a unit through the API. RFC 6 lets the API start
a job. Both raise the same questions:

- May this principal make this edit, on this unit, now?
- Does the edit go straight in, or does someone approve it first? Who?
- Does it go to git as a review request, as a direct commit, or not to git at
  all?
- Does the plan that follows break a rule (a public bucket, a missing tag, a
  machine size that is too large)?

Teams want to answer these with policy code in an engine that they already
use, for example Open Policy Agent (OPA) with Rego, Cedar, or Semgrep. Which
engines OpenDustpan supports, and how, is to be decided (see "Policy
engines").

Teams also want to skip the review request for safe, repeated work, for
example "add a Kubernetes namespace for a new team". And they want to approve
from their own tools, such as an internal developer portal, through the API.

## Three kinds of policy

"Policy" means three different things here. Each has its own input, its own
time and its own place where it runs. Mixing them is the main risk in this
RFC.

| Kind | Question | Input | Where it runs |
| --- | --- | --- | --- |
| Access | May this principal do this action on this unit? | principal, action, unit reference | backend, on every request |
| Edit | What happens to this edit or job? | principal, unit reference, the keys and values that change | backend, when an edit or job starts |
| Resource | Does this plan break a rule? | the tool's plan or preview | where the tool runs |

### Access policy

The `identity.Authorizer` port already answers this with tool-neutral actions
(ADR 13). An adapter for that port can call a policy engine. This RFC adds no
new concept for it.

### Edit policy

When a principal makes an edit (RFC 12) or starts a job (RFC 6), the backend
asks the policy for a **decision**. The input is tool-neutral and documented
as a versioned JSON schema, because policies depend on it like a client
depends on an API:

- the principal and its groups,
- the unit reference and the unit's labels,
- for an edit: each key, its old and new value, and its home (git or a
  profile). Secret values are never in the input. The input only says that a
  secret changed.
- for a job: the operation (`preview`, `up`, `plan`, `apply` and so on).

The decision is one of:

- **deny**, with a reason that the console and the API show,
- **allow**: the edit goes in now. If its home is git, the backend commits
  directly to the source's ref, with no review request.
- **approve first**: the edit waits for approvals. The decision says how many,
  and from which groups. After the approvals, the edit goes in as with
  **allow**.
- **review request**: the edit goes to git as a review request (RFC 12). The
  forge is where people approve. OpenDustpan shows the state of the review
  request but adds no approvals of its own to it.

One edit has one place for approval: OpenDustpan or the forge, never both.

The default policy, with no policy installed, is "review request" for edits
whose home is git and "approve first, one approval by another principal" for
apply jobs. A team can change both. Following RFC 9's posture, a team can
also write a policy that lets a principal approve its own edits. The console
then shows that the edit had no second person.

### Resource policy

Resource policy reads a plan, so it runs where the plan is: in the tool's
process. The backend never runs a tool (RFC 6), and a plan can hold secret
values, so the backend does not take plans to check them.

- **Pulumi CLI.** The Pulumi Cloud-compatible API can name required policy
  packs at the start of every update (`requiredPolicies` in the response that
  begins an update, with a download location for each pack). The CLI
  downloads and runs them. Policy packs can be written in TypeScript, Python
  or Rego (`runtime: opa`). So OpenDustpan can require policies on every update,
  from a runner or from a workstation. The CLI enforces them, so a modified
  CLI can skip them. That is also true for Pulumi Cloud. It is a guardrail,
  not a security boundary.
- **OpenTofu.** The CLI has no hook that asks its backend for policies when
  it runs on a workstation. TFE policy checks apply only to remote runs. So
  OpenTofu resource policy runs only in jobs: the runner evaluates the policy
  on the output of `tofu show -json` for the saved plan, reports the result,
  and does not apply when the result is deny.
- **Never on state.** The backend must not reject a state write because of a
  resource policy. The resources already changed when the state arrives. A
  rejected state write loses the record of real resources, and the project
  blocks only on data loss or corruption (RFC 9).

A Pulumi preview and an OpenTofu plan are different documents. One policy
cannot check resources of both tools without two inputs. Support for an
engine means one language and one engine, not one policy for every tool.

## Approvals through the API

- An edit that waits has an **approval** list in the API. A principal with the
  approve action adds an approval or a rejection, with a comment.
- An internal developer portal (for example Backstage) or a chat bot uses the
  same endpoints as the console, with its own access token.
- OpenDustpan sends outgoing webhooks when an edit or a job waits, gets an
  approval, goes in or fails. A portal then needs no polling.
- Approvals expire. If the unit or the source changed since the policy
  decision, the backend asks the policy again before the edit goes in.

Example: a team adds a Kubernetes namespace for a new tenant. The tenant list
is a config key whose home is git. The policy allows the edit directly when
the principal is in `platform-users`, the only key that changes is `tenants`,
and the change only adds an item. The portal calls the API. The backend
commits to `main` and starts an `up` job. No review request, no wait. An edit
that removes a tenant gets "approve first" from the same policy.

## Policy engines

To be decided. This RFC only proposes that edit policy goes through a
**`Policy` port** (ADR 2): it takes an input document and returns a decision.
The port gets a conformance suite before its first adapter. Every decision is
in the audit log with the policy version, the input (without secrets) and the
result.

The candidate engines do different jobs, so "support" can mean different
things for each:

- **OPA (Rego)** evaluates JSON input to a decision. It fits edit policy and
  access policy, and Pulumi policy packs and `conftest` already use it for
  resource policy. Possible forms: call the team's own OPA server through its
  REST API (no new dependency), embed OPA's Go module (a large dependency), and
  serve policies in OPA's bundle format to OPA servers and runners.
- **Cedar** is a language for access decisions about principals, actions and
  resources. It fits access policy and maybe edit policy. It is not made to
  read plans.
- **Semgrep** finds patterns in source code. It makes no decision about an
  edit and does not read plans. It is a fourth kind of check: on the program
  code in a review request, or in a job before the plan. If OpenDustpan
  supports it, it is most likely a step that a runner runs, with the result
  reported like a plan's.

A team's own service can also be an adapter, as for any port.

## Options

1. **The three kinds above, with a `Policy` port** (the proposal). The
   engines come later.
2. **Do nothing.** Access rules only. Teams run `conftest` in their own CI.
   Edits through the API then have no workflow, which makes RFC 12 risky.
3. **A policy language of our own.** Rejected. Teams already know existing
   engines, and a new language is a large cost for us and for them.
4. **Resource policy in the backend.** The backend takes each plan and checks
   it. This breaks RFC 6 for OpenTofu (the plan comes from a run), puts secret
   values in the backend, and does nothing for Pulumi that required policy
   packs do not already do.

## Proposed glossary entries

**Policy**:
Code that the backend or a tool evaluates to decide about an action, an edit,
a job or a plan. Always say which kind: access, edit or resource policy.
_Avoid_: Rule (alone), guardrail, governance

**Decision**:
The result of one policy evaluation, recorded with the policy version and the
input.
_Avoid_: Verdict, outcome

**Edit**:
A change to config values that a principal asks for through the API. A
write-back (RFC 12) is an edit whose home is git.
_Avoid_: Change (which means resource changes), mutation, operation

**Approval**:
One principal's yes or no on an edit or a job that waits.
_Avoid_: Review (which happens in the forge), sign-off

## Open questions

- Which engines does OpenDustpan support, for which kind of policy, and in
  which form (an external server, embedded, or a step on a runner)?
- Is a check on source code (Semgrep) a fourth kind of policy, or a job step
  outside this RFC?
- What does the edit input schema look like, and how is it versioned?
- Are policies kept per namespace, per unit label, or both?
- Is there a break-glass path that skips edit policy, and who holds it? It
  must leave a loud audit entry.
- Is "approve first" for apply jobs the answer to RFC 6's open question on
  who approves an apply, or does that need roles (RFC 2) first?
- A direct commit to a protected branch needs the forge connection on the
  branch protection's bypass list. Is that acceptable as the documented setup?
- Does OpenDustpan serve Pulumi's policy pack endpoints (publish, list,
  enable) for the Pulumi CLI, or only `requiredPolicies` with packs from
  bundles?

## Gaps from the competitor review

Added 2026-10-10 from the research doc "IaC and TACOS landscape". Not yet
worked into the proposal above.

- Enforcement levels (advisory, soft and hard mandatory), exemptions and owner
  overrides (HCP Terraform, Terrakube, Stategraph).
- Policy stages beyond edits and plans: sign-in, push and trigger,
  notification, workspace save, state before and after apply (Spacelift,
  Harness).
- The Pulumi CLI's `pulumi policy publish` and `enable` endpoints, and
  versioned policy packs.
- Approval details: auto-approve when nothing changes, timeout, named
  approvers by email, the strictest result wins (env zero, Scalr, Harness).
- Authoring and testing tools are RFC 50; plan-bound approvals are RFC 28.

## Outcome

Not accepted as a whole. On 2026-10-18:

- The sentence "One edit has one place for approval: OpenDustpan or the
  forge, never both" is superseded. ADR 103 and ADR 110 decide that the
  approvals of OpenDustpan add to the approvals of the forge.
- ADR 203 decision 3: the policy input of `job.start` holds the old and the
  new value of each edit key that is not secret, and a flag for a secret
  key, as the project manager decided under the owner's decision rule.

The other questions stay open.
