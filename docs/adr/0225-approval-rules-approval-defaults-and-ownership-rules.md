# 225. Approval rules, approval defaults and ownership rules

Status: accepted · 2026-10-18. Part of ADR 222. Supersedes the approval gate of ADR 103 decision 2, ADR 110 decision 9 and item 5 of its job model changes. Supersedes ADR 116 decisions 5 and 6, and ADR 190 decisions 6 and 7 in part. Supersedes ADR 192 decisions 4 and 5, ADR 202 decisions 6 and 7, and ADR 203 decision 5 in part

## Context

Five approval systems exist (ADR 222). A job approval gate names a count,
groups and `AllowSelf` (ADR 103 decision 2). A change order always needs one
approval by a person who is not an author (`changeorder.MinApprovals`, ADR
192 decision 4), and a namespace cannot lower it. A profile change rule
names 1 to 5 approvals (ADR 202 decision 6). An open rule needs an approved
access request (ADR 202 decision 7). The Cedar adapter of ADR 114 takes the
count from a namespace setting, and the Expr adapter takes it from the
rule.

The approvals of one phase come from distinct people (ADR 116 decision 6).
A change order matches approvers to requirements with bipartite matching
(ADR 192 decision 6).

## Decision

1. **An approval rule is data (owner).** An approval rule has these fields:
   - `id`: unique in its scope. A gate names it as `approval:<id>`.
   - `scope`: the installation, a namespace, a collection or a unit.
   - `approvers`: groups, users and `owners` (the owners of the unit in its
     context, ADR 161). At least one entry.
   - `count`: 1 to 10.
   - `authorMayApprove`: default `false`.
   - `freshAuthSeconds`: none, 0 or a window (ADR 227).
   - `staleOnNewPlan`: default `true`. A new plan voids the signatures of
     the rule (ADR 191 decision 6).
2. **Sources of requirements.** A requirement comes from a gate whose outcome
   names an approval rule (ADR 224 decision 14), or from an approval default
   (decision 4). RFC 77 holds the question of whether an ownership rule is a
   third source with no gate (decision 9).
3. **One rule, one requirement.** Two gates that name the same rule give one
   requirement. Two rules give two requirements, also with the same groups.
   So an `approval:security` and an `approval:dba` need one approval by
   `security` and one by `dba`, never one approval by either group. This
   keeps the rule of ADR 114 decision 4.
4. **Approval defaults (owner).** An approval default can be set on a
   namespace, a collection or a unit. The installation has none. With none
   set, the default is off and nothing adds a minimum of one approval. This
   supersedes `changeorder.MinApprovals` of ADR 192 decision 4. When several
   defaults hold for a unit, the strictest wins: the highest count and the
   smallest fresh window. A default is one requirement named `default`. A
   default has an optional `unless` Cedar body over the facts of
   `change.planned`. RFC 77 holds who may approve a default.
5. **Fresh MFA for approvals (owner).** An approval of a change that holds a
   `destroy` needs a fresh authentication, always. A namespace setting,
   `freshAuthForApprovals`, off by default, extends this to each approval in
   the namespace. ADR 212 decision 3 stays, with the window of ADR 227.
6. **Distinct people (owner).** The approvals across all the requirements of
   one change come from distinct people. The server assigns approvers to
   requirements with the bipartite matching of ADR 192 decision 6, for jobs,
   change orders, profile drafts and access requests. So the order of the
   approvals never changes the result.
7. **Who may approve.** An eligible approver of a requirement holds the
   approve action of the object (`changeorder:approve`, `job:approve` or
   `profile:approve`) and is in the `approvers` of its rule. An author is
   eligible only when `authorMayApprove` is `true`. An agent never approves
   (ADR 128 decision 10 stays). ADR 193 decisions 4 and 9 read "eligible
   approver" with this meaning.
8. **Where requirements live.** When a job runs under a change order, its
   requirements live on the change order (ADR 190 decision 6 stays). Else
   they live on the job, and the job waits in the approval wait. A profile
   save waits as a draft, and an open waits as an access request.
9. **Ownership rules (owner).** An ownership rule is stored in the server and
   configured through the API. It is inspired by a code owners file, with
   more than a team and a count:

   ```json
   {
     "id": "net-cidr",
     "scope": {"level": "collection", "name": "acme/prod"},
     "paths": ["modules/network/**"],
     "when": "context.change.inputKeys.contains(\"cidr\") && context.plan.replace == 0",
     "groups": ["netops"], "users": [], "count": 2,
     "freshAuthSeconds": 0
   }
   ```

   - `paths` holds glob patterns that the server matches in Go against the
     changed paths, because Cedar has no quantifier over a set. Empty means
     any change.
   - `when` is a Cedar condition over the facts of `change.planned`. It is
     type-checked at save with the same schema as the gates. Empty means
     true.
   - Each rule that applies is one requirement named `owners/<id>`. The
     record shows its count, the signatures so far and who may approve.
   - In version 1, a rule applies when a gate with the outcome
     `approval:owners` matches, and its paths and `when` match.
10. **Profiles.** The change rule of a profile becomes a gate at
    `profile.save` with the outcome `approval:<rule>`. The open rule becomes
    a gate at `profile.open` with the outcome `approval:<rule>`, and the
    access request of ADR 202 decision 7 holds its approvals. A job of a
    unit whose profile needs an approval at `profile.open` waits in the
    approval wait. It does not fail with `profile.ErrOpenRule`. This answers
    the question that ADR 203 decision 5 leaves to RFC 5.
11. **Changes in git.** A Catalog request that a team applies in its own CI
    gets no OpenDustpan approval (ADR 116 decision 9 stays). The render
    records the requirements in the decision record.

## Consequences

- A job and a change order have one requirement model. `AllowSelf` becomes
  `authorMayApprove`, with the default `false` for both.
- An installation with no approval default and no gate asks for no
  approval. A team that wants the earlier change order rule sets a default
  of one on its namespace.
