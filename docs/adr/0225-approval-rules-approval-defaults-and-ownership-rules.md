# 225. Approval rules, approval defaults and ownership rules

Status: accepted · 2026-10-18. Part of ADR 222. Supersedes in part ADR 103 decision 2 (the approval gate), ADR 110 decision 9 and item 5 of its job model changes. Supersedes in part ADR 116 decisions 5 and 6, and ADR 190 decisions 6 and 7. Supersedes in part ADR 192 decisions 4 and 5, ADR 202 decisions 6 and 7, and ADR 203 decision 5. Amends ADR 191 decision 6 (decision 12)

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
   - `id`: unique in its scope. A gate names it as `approval:<id>`. The
     server resolves `<id>` at the scope of the gate, then at each wider
     scope, and never at a narrower scope. A narrower scope cannot save a
     rule with the ID of a rule of a wider scope that holds it
     (`gate.ErrFinal`). An ID matches the gate ID grammar of ADR 224
     decision 1, `[a-z0-9][a-z0-9-]{0,62}`. The IDs `default`,
     `default/<scope>` and `owners/<id>` are reserved for the requirements
     of decisions 4 and 9.
   - `scope`: the installation, a namespace, a collection or a unit. The
     route path names it (ADR 230 decision 3).
   - `approvers`: groups, users and `unitOwners` (the owners of the unit in
     its context, ADR 161). At least one entry.
   - `count`: 1 to 10.
   - `authorMayApprove`: default `false`.
   - `freshAuthSeconds`: none, 0 or a window of 60 to 900 seconds (ADR
     227). None (the field is absent) means that the rule asks for no fresh
     authentication of its own. Decision 5 can still ask for one. 0 asks
     for one at each approval.
   - `timeoutSeconds`: 1 hour to 30 days, default 7 days (decision 8).

   A new plan voids every signature, unless the carry-over setting of
   decision 12 is on.

   The removal of an approval rule that a stored gate names fails with
   `gate.ErrInvalid` and names each gate. A gate save that names a rule
   which does not resolve fails with `gate.ErrInvalid`.

   **`unitOwners` reads only the context of the rule's scope or wider
   (PM).** `unitOwners` resolves only from the context layers (ADR 160
   decision 11, ADR 161) at the scope of the rule or at a wider scope. A
   rule of a namespace reads the owners from the context of the namespace,
   never from the context of a collection or of the unit. A rule of a
   collection reads the collection and the namespace. The installation has
   no context layer, so a rule of the installation that names
   `unitOwners` is refused at save with `gate.ErrInvalid`. So the holder of
   the context of a unit cannot name the approvers of a rule of a wider
   scope.

   Benchmark: GitHub reads the `CODEOWNERS` file of the base branch of a
   pull request, not of its head, so a change cannot name its own owners.
2. **Sources of requirements (owner, 2026-10-18).** A requirement comes
   from one of three sources: a gate whose outcome names an approval rule
   (ADR 224 decision 14), an approval default (decision 4), or an ownership
   rule in `enforce` mode that applies (decision 9). An ownership rule needs
   no gate.
3. **One rule, one requirement.** Two gates that name the same rule give one
   requirement. Two rules give two requirements, also with the same groups.
   So an `approval:security` and an `approval:dba` need one approval by
   `security` and one by `dba`, never one approval by either group. This
   keeps the rule of ADR 114 decision 4.
4. **Approval defaults (owner).** An approval default can be set on a
   namespace, a collection or a unit. The installation has none. With none
   set, the default is off and nothing adds a minimum of one approval. This
   supersedes `changeorder.MinApprovals` of ADR 192 decision 4. A default
   has a count, an optional `freshAuthSeconds`, optional `approvers` and an
   optional `unless` Cedar body over the facts of `change.planned`.

   Each default holds on its own. The `unless` of a default exempts a
   change from that default only, never from a default of a wider scope.
   Defaults that name no approvers merge into one requirement named
   `default`, with the highest count and the shortest fresh window. A
   default that names approvers is its own requirement, `default/<scope>`.

   **Who may approve a default with no approvers (owner, 2026-10-18).** Any
   holder of the approve action of the object (decision 7) who is not the
   author. This keeps the rule of `MinApprovals` (ADR 192).
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
   eligible only when `authorMayApprove` is `true`. An approver is a person
   in a session. A service principal, a job token and an access token never
   approve, also when they hold the approve action. An agent never approves
   (ADR 128 decision 10 stays). ADR 193 decisions 4 and 9 read "eligible
   approver" with this meaning.
8. **Where requirements live.** When a job runs under a change order, its
   requirements live on the change order (ADR 190 decision 6 stays). Else
   they live on the job, and the job waits in the approval wait. A profile
   save waits as a draft, and an open waits as an access request.

   A change in the approval wait expires after 7 days. An approval rule can
   set `timeoutSeconds` from 1 hour to 30 days. The shortest timeout of the
   requirements of a change holds. A rejection by an eligible approver of
   any requirement rejects the change. The job state value stays
   `awaiting-approval`, and the word for the state is "approval wait"
   (owner, 2026-10-18).
9. **Ownership rules (owner).** An ownership rule is stored in the server and
   configured through the API. It is inspired by a code owners file, with
   more than a team and a count:

   ```json
   {
     "id": "net-cidr",
     "mode": "evaluate",
     "paths": ["modules/network/**"],
     "when": "context.change.inputKeys.contains(\"cidr\") && context.plan.replace == 0",
     "groups": ["netops"], "users": [], "count": 2,
     "freshAuthSeconds": 0
   }
   ```

   This body is the `PUT` of
   `.../namespaces/acme/collections/prod/ownership-rules/net-cidr`. The
   route path names the scope (ADR 230 decision 3).

   - `id` matches the gate ID grammar (ADR 224 decision 1).
   - `paths` holds glob patterns that the server matches in Go against the
     changed paths, because Cedar has no quantifier over a set. Empty means
     any change.
   - `when` is a Cedar condition over the facts of `change.planned`. It is
     type-checked at save with the same schema as the gates. Empty means
     true.
   - Each rule that applies is one requirement named `owners/<id>`. The
     record shows its count, the signatures so far and who may approve.
   - A rule applies when its `paths` and its `when` match. It needs no gate
     (owner, 2026-10-18). `owners` is not an approval rule, and a gate
     cannot name `approval:owners` (`gate.ErrInvalid`).
   - `mode` is `enforce` or `evaluate`, as for a gate (ADR 224 decision 7).
     A new rule starts in `evaluate` mode unless the request names
     `enforce`. A rule in `evaluate` mode is in the record with no
     requirement. So a person sees what a rule would ask for before it asks.
10. **Profiles.** The change rule of a profile becomes a gate at
    `profile.save` with the outcome `approval:<rule>`. The open rule becomes
    a gate at `profile.open` with the outcome `approval:<rule>`, and the
    access request of ADR 202 decision 7 holds its approvals. A job of a
    unit whose profile needs an approval at `profile.open` waits in the
    approval wait. It does not fail with `profile.ErrOpenRule`. This answers
    the question that ADR 203 decision 5 leaves to RFC 5.
11. **Changes in git.** A Catalog request that a team applies in its own CI
    gets no OpenDustpan approval (ADR 116 decision 9 stays). The render
   records the requirements in the decision record. A `deny` gate in
   `enforce` mode at the render of such a request fails the render, with
   `gate.ErrDenied` and the record ID.
12. **Signatures can carry over to an identical plan (owner, 2026-10-18).**
    By default, a new plan revision voids every signature (ADR 191 decision
    6). A setting, `signatureCarryOver`, lets a signature carry over to a
    new revision whose plan digest inputs are identical. This amends ADR
    191 decision 6.
    - **Identical plan digest inputs.** The new revision has the same set of
      units as the revision that holds the signature. For each unit plan,
      each input of the digest `cho-digest/1` (ADR 191 decision 4) is the
      same, except the revision number and the plan ID: the unit ID, the
      unit reference, the operation, the code location, the commit, the two
      part hashes and the state version. A new state version, a new commit
      or a change of one byte of a plan part is a different input.
    - **What never carries over.** A signature carries over only when each
      of these holds at the attach. Else it stays void with the cause
      `new-plan`.
      - The evaluation of `change.planned` for the new revision gives the
        same requirement, from the same version of each gate, approval
        rule, approval default and ownership rule. A changed policy never
        carries over.
      - The signer is still an eligible approver of that requirement
        (decision 7), with the same approvers of the rule. A changed
        approver set never carries over.
      - The approval has not expired (decision 8).
      - The signer is not disabled, has not lost access, and the credential
        of the signature is not revoked. A revoked signer never carries
        over.
    - **Levels.** The setting is on a namespace, a collection or a unit,
      with the values `off` (the default), `on` and `forbidden`. The
      installation also holds the setting, with the values `off` (the
      default) or `forbidden` only. A signature carries over only when the
      narrowest level that sets `on` or `off` sets `on`, and no level that
      holds the unit sets `forbidden`. So a parent, also the installation,
      can forbid it for each scope below it.
    - **Who changes it.** `namespace:manage-gates` on the namespace, for the
      namespace, collection and unit levels. `installation:manage-gates`,
      for the installation level (ADR 230 decision 3). A change to `on` is a weaker setting, so it needs
      a fresh authentication (ADR 212 decision 3, ADR 227). Each change
      writes an audit entry of the kind `change`, with the old and the new
      value.
    - **The record.** A carried signature is a new signature on the new
      revision, with `carriedFrom`: the revision number, the digest and the
      time of the first signature. The void signature of the earlier
      revision stays, with the cause `new-plan`. The decision record shows
      each carried signature with `carriedFrom` and the level of the setting
      that allowed it. The console marks it as carried.

    Benchmark: GitHub "Dismiss stale pull request approvals when new commits
    are pushed" is a setting of a branch rule. GitLab has the project
    setting "Remove all approvals when commits are added to the source
    branch". With either setting off, an approval stays over new content.
    OpenDustpan takes the stricter default, and carries a signature only
    over identical inputs.

## Consequences

- A job and a change order have one requirement model. `AllowSelf` becomes
  `authorMayApprove`, with the default `false` for both.
- An ownership rule that a person saves with `enforce` asks for approvals at
  once, with no gate. The `evaluate` default lets a team check it first.
- An installation with no approval default and no gate asks for no
  approval. A team that wants the earlier change order rule sets a default
  of one on its namespace.
