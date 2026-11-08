# 222. One model for access, gates, approvals and facts

Status: accepted · 2026-10-18. ADRs 223 to 230 give the parts. Supersedes parts of ADRs 17, 52, 56, 71, 103, 110 and 112 to 117. Supersedes parts of ADRs 128, 135, 142, 144, 190 to 194, 196, 197 and 200 to 203. Amends ADR 191 decision 6. Extends ADR 54 decision 5 and ADR 70 decisions 16, 17 and 23. See "What this ADR set replaces". Accepts RFC 77.

Changed by:

- ADR 286 changes: the words catalog and promotion become self-service and
  failover.
- ADR 335 extends decision 7: Cedar is the default engine, and another engine can be an adapter.
- ADR 351 extends: the gates over facts also decide the admission of a package.
- ADR 354 changes decision 5: two evaluations for a delegated request.
.
## Context

Many ADRs answer the same three questions in different ways: "may this
happen", "who must agree" and "warn or block". A spike (`spike/policy-model`,
not for merge) built one model for the three questions in `pkg/gate`, with
`cedar-go` v1.8.0, and ran it against the SQLite store and a Git repository.
This ADR set records that model.

The project owner gave the model and decided its main points on
2026-10-18. Each decision of the owner has the label "(owner)". The project
manager decided the points that the spike found under the owner's decision rule. Each of them has a "Benchmark:" line. The owner then decided
the open points of RFC 77 and the carry-over of signatures. Each of those
decisions has the label "(owner, 2026-10-18)" or names that date. This ADR set holds design
only: ADRs, RFC outcomes and glossary entries, and no code.

### The inventory before this ADR set

The accepted ADRs hold 13 mechanisms that refuse an action:

| Mechanism | Where | What refuses |
| --- | --- | --- |
| Access through grants | ADR 52 decision 5 | `identity.ErrDenied` |
| Job policy decision at start | ADR 103 decision 1 | `job.ErrPolicyDenied` |
| Job approval gate | ADR 103 decision 2 | the phase does not dispatch |
| Freeze and break-glass | ADR 103 decisions 4 and 6, ADR 141 | `job.ErrFrozen` |
| Manual lock | ADR 213 decision 10 | wait reason `locked` |
| Catalog policy rules in Expr | ADR 112 decision 7, ADR 114 decision 5 | `deny` |
| Catalog recheck points | ADR 113 decisions 8 and 9 | `catalog.ErrPolicyChanged` |
| Quotas | ADR 115 decisions 2 and 5 | `quota.ErrExceeded` |
| Change order requirements and policy reports | ADR 192 decision 4, ADR 194 decision 8 | `changeorder.ErrPolicyDenied` |
| Profile `checks` | ADR 201 decision 11 | `profile.ErrCheckFailed` |
| Profile change rule and open rule | ADR 202 decisions 6 and 7 | a draft, an access request |
| Source policy | ADR 135 decision 6, ADR 213 decision 2 | `source.fetch` fails |
| Safe delete | ADR 153 decision 5 | `409` |

They hold 5 approval systems: the job approval gate (ADR 103 decision 2),
the confirmation gate of an agent change (ADR 103 decision 2, ADR 128
decisions 4 to 7), the change order requirement (ADR 192), the profile
change rule (ADR 202 decision 6) and the profile access request (ADR 202
decision 7).

Expr runs as a rule in 4 places: blueprint `validate` rules (ADR 112
decision 6), blueprint and namespace `policy` rules (ADR 112 decision 7,
ADR 114 decision 5), quota `expr` rules (ADR 115 decision 2) and profile
`checks` (ADR 201 decision 11).

Three decision points take different inputs: `catalog.request` and
`job.start` (ADR 114 decision 2), and `changeorder.review` (ADR 192
decision 4).

Five rules ask for a fresh authentication: ADR 56 decisions 1 to 4, ADR 71
decisions 1 and 2, ADR 103 decision 2 with ADR 128 decision 7, ADR 142
decision 9, and ADR 212 decision 3.

No ADR has a "warn" outcome. ADR 17 names only deny and wait for approval.
ADR 114 decision 2 gives `allow`, `deny` and `approve`.

### Contradictions

1. A fresh-authentication window of 0 asks for a step at each operation in
   ADR 56 decision 4. It turns the check off in ADR 71 decision 2. ADR 212
   decision 3 lists "set to 0" as a change that makes the installation less
   secure.
2. `policy.Combine` (ADR 114 decision 4) gives one requirement for each
   `approve`. A job gate (ADR 103 decision 2) has its own count, groups and
   `AllowSelf`. Two models of one requirement exist.
3. ADR 112 decision 12 leaves `join`, `string` and `toJSON` out of the Expr
   builtins. ADR 201 decision 8 gives them to profiles.
4. Cost cannot reach a decision. The job decision runs once at start, before
   the plan (ADR 103 decision 1). ADR 144 decision 14 says that only a
   policy can block on cost, and asks the job model for a later decision.
5. An open rule of a profile fails each job (ADR 203 decision 5). RFC 5
   keeps open how it works with the gates of a job.
6. A change order always needs one approval (ADR 192 decision 4). With no
   policy, a job of a person needs none (ADR 103, Consequences).
7. The author never approves a change order (ADR 192 decision 5). A job gate
   can let the starter approve (`AllowSelf`, ADR 103 decision 2).
8. The Cedar adapter of ADR 114 decision 7 takes the count and the groups of
   an approval from a namespace setting, and the Expr and OPA adapters take
   them from the rule. One rule has two homes.
9. A recheck at `apply` reads the decision again (ADR 113 decision 8). The
   job decision never runs again (ADR 103 decision 1).
10. A `mandatory` policy report blocks a change order (ADR 194 decision 8).
    Nothing reads a policy report for a job outside a change order.
11. The plan dependents can reach the policy check (ADR 153 decision 17),
    but no decision point runs after the plan.
12. A Catalog request in git gets no OpenDustpan approval (ADR 116 decision
    9). A profile rule always holds (ADR 202 decision 6).
13. Attribute conditions on grants are planned (ADR 52 decision 9). No ADR
    says how they stop a grant.
14. The word "gate" names a job gate (ADR 103), a CI model check (ADR 70
    decision 30) and a verb (ADR 70 decision 11). "Policy" names a
    decision, a source allowlist (ADR 135 decision 6) and a settings group
    (ADR 71).
15. A break-glass passes a freeze (ADR 103 decision 6) but not a manual lock
    (ADR 213 decision 10), and no ADR says what passes a policy that fails.

## Decision

1. **Five parts (owner).** One model answers the three questions:
   - **Access** says who may call an action. It is the grants of ADR 52,
     with no change, and **access limits**: Cedar `forbid` policies for
     each scope (ADR 223).
   - **Gates** say whether a change may go on, who must agree and when to
     warn. A gate is a stored rule at a fixed decision point (ADR 224).
   - **Approval rules** say who may approve and how many people must
     approve (ADR 225).
   - **Facts** are typed values that the server produces before a decision
     point. A fact never decides (ADR 226).
   - **Expr** computes values only (ADR 228).
2. **Six decision points (owner).** `change.propose`, `change.planned`,
   `change.apply`, `profile.save`, `profile.open` and `state.write` (ADR 224
   decision 2). The first three replace the kinds `catalog.request`,
   `job.start` and `changeorder.review`. `state.write` covers writes from a
   person's own CLI or CI and the tool-neutral state operations (ADR 224
   decision 16, owner decision of 2026-10-18).
3. **One decision record for each evaluation (owner).** The record explains
   every block, every wait and every warn (ADR 229).
4. **Fixed controls stay fixed (owner).** Workspace locks, manual locks,
   freezes with break-glass, override switches, module switches, quota
   reservations and quota counts, the source filter, safe delete (ADR 153
   decision 5), the plan staleness check with the maximum plan age (ADR
   102, ADR 191 decision 7) and the agent controls (modes, the confirmation
   step and the secret consent) are not gates. Quota counts stay fixed
   controls by an owner decision of 2026-10-18. The decision record shows the state of
   each fixed control that it checked (ADR 229 decision 4). An agent is a
   subject of gates through the fact `source.kind == "agent"` (ADR 226).
5. **No outcome relaxes another (owner).** Each part can only add a refusal,
   a requirement or a warning. A wider rule carves out its own exemptions
   with `unless` (ADR 224 decision 6).
6. **Stored in the server, configured through the API (owner).** Gates,
   access limits, approval rules, approval defaults and ownership rules are
   objects of the server. The console, the CLI and the IaC providers
   configure them through the API. No repository file holds them (ADR 230).
7. **Cedar, linked and as a service (owner).** The server links `cedar-go`
   and can also call an external Cedar service, behind one deep engine port
   (ADR 224 decision 9).
8. **Words.** "Gate" names the stored rule. The job state that waits for
   people is the **approval wait**, and its wire value stays
   `awaiting-approval` (owner, 2026-10-18). The fixed agent controls that
   ADRs 103, 124 and 128 call the confirmation gate and the secret gate are
   now the **confirmation step** and the **secret consent** (owner,
   2026-10-18). Those ADRs keep their text (ADR 1). "Source policy" becomes **source
   filter** (ADR 226 decision 9). The word "policy" alone names no object
   (`GLOSSARY.md`).
9. **Never claim compliance (owner).** A starter pack is "designed against"
   a control framework, never "compliant with" it (ADR 229 decision 7).

## What this ADR set replaces

Each earlier ADR keeps its text (ADR 1). Its status line names the ADR of
this set that replaces a part of it.

| Earlier decision | Replaced by | What changes |
| --- | --- | --- |
| ADR 17, "A policy check on every change" | ADR 224 | the check is the gates of a decision point, with a warn outcome |
| ADR 52 decision 9 | ADR 223 | attribute conditions are access limits, not conditions on a grant |
| ADR 54 decision 5 (extended) | ADR 223 decision 6 | an access limit also holds for a token from a trust rule |
| ADR 56 decisions 2 and 3, ADR 71 decisions 1 and 2 | ADR 227 | one window, and 0 asks each time |
| ADR 70 decisions 16, 17 and 23 (extended) | ADR 227 | the fresh step uses the one window |
| ADR 103 decisions 1 and 2 | ADRs 224 and 225 | the start decision and the approval gate become gates and approval rules |
| ADR 110 decision 9 and the changes to the job model, item 5 | ADR 225 | approval requirements come from approval rules |
| ADR 112 decisions 6, 7 and 12 | ADRs 224 and 228 | blueprint rules become gates, one builtin set |
| ADR 113 decisions 8 and 9 | ADR 224 decision 3 | the recheck points are the decision points |
| ADR 114 | ADR 224 | the gate engine port replaces `policy.Engine` |
| ADR 115 decision 2 (`expr` rules) and decision 13 | ADRs 224 and 226 | an `expr` quota rule becomes a gate over the quota fact |
| ADR 116 decisions 5 and 6 | ADR 225 | the Catalog decision gives approval requirements |
| ADR 117 decisions 2, 5, 6 and 7 | ADR 230 | actions, routes, audit entries and limits of gates |
| ADR 128 decision 7 | ADR 227 | the confirmation uses the one window |
| ADR 135 decision 6 | ADR 226 decision 9 | the name "source filter" |
| ADR 144 decision 14 | ADR 226 decision 4 | a cost gate at `change.planned` |
| ADR 190 decisions 6 and 7, ADR 192 decisions 4 and 5 | ADR 225 | approval defaults replace `MinApprovals` |
| ADR 191, open question on fresh authentication | ADR 227 decision 5 | answered |
| ADR 191 decision 6 | ADR 225 decision 12 | a signature can carry over to a revision with identical plan digest inputs, as an opt-in setting |
| ADR 193 decisions 4 and 9 | ADR 225 decision 7 | "eligible approver" comes from approval rules |
| ADR 194 decisions 6 and 8 | ADR 226 decision 7 | policy reports and the plan summary are facts, with built-in gates |
| ADR 196 decisions 1 and 5, ADR 197 decisions 5 and 6 | ADR 230 | errors, status codes and audit entries of gates |
| ADR 200 decision 3, ADR 201 decisions 4, 8 and 11 | ADRs 224 and 228 | profile `checks` become gates at `profile.open`, one Expr package |
| ADR 202 decisions 6 and 7, ADR 203 decision 5 | ADR 225 decision 10 | the change rule and the open rule become gates with approval rules |

ADR 111 and ADR 195 hold no decision that this ADR set changes.

## Implementation slices

Each slice is one pull request that passes `make check` and adds its user
documentation page (ADR 214):

1. **The thin slice.** `pkg/gate` with the fact schema of `change.planned`,
   gates, access limits, approval rules, decision records and the `cedar-go`
   engine adapter. The end-to-end steps of the spike run through `make e2e`
   on an OpenTofu job. `state.write` at the OpenTofu lock and at the
   tool-neutral state operations, with their e2e steps.
2. **Pulumi jobs.** The same decision points on a Pulumi job: `change.propose`
   at the start, `change.planned` after the preview, `change.apply` before
   the update phase. `state.write` at the create of an update from the
   Pulumi CLI, with its e2e step.
3. **Change orders.** Approval requirements of a change order come from
   approval rules. `MinApprovals` goes.
4. **Profiles.** `profile.save` and `profile.open` replace `checks`, the
   change rule and the open rule.
5. **The Catalog.** Blueprint rules become gates. `pkg/policy` goes.
6. **The external Cedar service, external checks and starter packs.** The
   second engine adapter, the external-check producer (RFC 49) and the
   starter packs in `evaluate` mode.

## Consequences

- One domain package, `pkg/gate`, holds the model. The thin slice adds it to
  the depguard list of domain packages and to the layout in `CLAUDE.md`.
- `pkg/policy` and its four adapters go in slice 5. No tag shipped them, so
  no data conversion exists (ADR 73).
- The tool-compatible APIs do not change. A refusal on a tool path uses the
  error shape of that tool.
