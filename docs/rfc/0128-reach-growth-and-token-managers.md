---
rfc: 128
title: Reach growth and token managers
status: discussing
created: 2026-11-08
---

# RFC 128. Reach growth and token managers

Status: discussing. This RFC touches ADR 67 decision 3, ADR 70 decision 2,
decision 3, decision 6, decision 7 and the row "67, decision 3" of
decision 32. It does not edit an accepted ADR. An owner decision on it gives
a new ADR.

## Problem

### The facts

- A holder H can hold `principal:manage-tokens` on a service principal P.
  The hold comes from a direct grant on P, from a grant to a group of H, or
  from `namespace:manage-principal-tokens` on the namespace of P (ADR 67).
  The author of a trust rule that names P has the same act-as edge
  (ADR 70 decision 10).
- **The accepted ADRs chose growth.** ADR 67 decision 3: "A holder of
  `principal:manage-tokens` on a principal can act as that principal, with
  every grant that the principal has now or gets later. Issue #531 describes
  this behavior, and this decision accepts it." ADR 70 decision 32 did not
  change this sentence.
- ADR 70 decision 6 already handles the growth case with a flag: "The same
  flag applies to a token that decision 2 would refuse today, because the
  principal got more grants after its maker made the token."
- The role catalog repeats the accepted rule. The descriptions of
  `account-administrator` and `token-manager` in `pkg/access/roles.go`
  (lines 45 to 58) say "with every grant that the principal has now or gets
  later".
- ADR 70 decision 3 checks the granter of the edge: its reach must cover the
  reach of P when the edge is made.
- ADR 70 decision 2 compares reach at token creation only for a holder on
  the installation. The row "67, decision 3" of ADR 70 decision 32 says it:
  "It does not check a holder on one principal when it makes a token."
  Pull request #2877 (issue #608) implements this rule.
- A token acts with the live grants of its principal at each request
  (ADR 67, context). A token from a trust-rule exchange is a stored access
  token of P (ADR 284). So every token of P grows when P grows.
- ADR 67, consequences: "An account administrator can delegate the tokens
  of one service principal to a team without giving that team the access of
  the principal." So the reach of H alone is not meant to bound P. The
  approval of the granter bounds the edge when it is made, and ADR 67
  decision 3 accepts that P grows after that.

### The question

The owner accepted growth on 2026-10-14. The review of #2877 asks again
whether growth after the grant needs a decision. This RFC states the cost
of growth, and the options to keep it or to change it. Options (a), (b),
(c), (e) and (f) reverse ADR 67 decision 3. Options (d) and (g) keep it.

The approval of decision 3 covers the reach of P at the time of the grant.
P can grow after that. The paths are in "Code paths" below. Each path widens
the edge H to P. Nobody whose reach covers the new reach of P approves the
wider edge. On a grant, A holds the new actions, because the escalation
check of a grant asks for them. So property P2 of ADR 70 ("no
amplification") holds for the actions that A adds. The approval of the edge
as a whole is split between two people, G for the old reach and A for the
new actions, and A may not know that H exists.

The access-review export (`pkg/access` `Service.Review`) returns grant
rows only: the grant of the edge on P and the grants of P. It has no row for
the reach of H and no flag. The flag of ADR 70 decision 6 is not built. The
audit entry of a grant to a service principal names the holders in the
change `tokenManagers` (`withTokenManagers`). `AddMember` writes the same
change for a service-principal member (`pkg/principal/service.go`). So a
record of the holders exists for a grant and for a member add. A decision
about the edge does not exist.

The reviewer of #2877 also names a second path: H revokes its own reach.
Under ADR 67 the own reach of H does not bound P. A smaller reach of H does
not widen the edge. This RFC treats that path as a gap only if the owner
wants the own reach of H to bound P (open question 1). That would reverse
the ADR 67 consequence above.

Severity, from the reviewer of #2877: medium. It needs a second actor (A),
a release, or the identity provider.

### Definitions

- **Reach over grants.** The set of pairs (action, object) that the grants
  of a principal name, through its own grants, its groups and its act-as
  edges. The objects are the objects of the grants (the installation, a
  namespace, a collection, a unit, a principal). The set does not resolve
  the units inside a namespace or a collection.
- **P grows.** The reach over grants of P after a change holds a pair that
  it did not hold before. So a grant that P already has, the create of a
  unit or a collection, and the move of a unit under an existing grant do
  not grow P. This is deliberate: P can act on more units, but no grant
  changed. The re-enable of a disabled P is not growth under this
  definition. Option (g) flags it as its own cause.
- **Transitive edges.** The code follows act-as edges transitively and
  stops on a cycle (`pkg/access/reach.go`, type `Reach`). ADR 70
  decision 1 says "the grants of each principal that it can act as". This
  RFC reads decision 1 as the code does.

### Who it affects

- **Platform team administrator (A).** Grants roles to service principals.
  May not see who holds `principal:manage-tokens` on the principal.
- **Team lead with token rights (H).** Holds `principal:manage-tokens` on
  one service principal, or `namespace:manage-principal-tokens` on its
  namespace. Makes and rotates tokens. Writes trust rules for CI.
- **Automation.** CI jobs and runners that use a token of P, or exchange a
  workload token through a trust rule for a token of P. A refusal at
  exchange or at rotation stops a pipeline.

## Code paths

Each option touches some of these paths. The table names them once. The
column "Request" says whether a request with a caller exists that an option
can refuse or ask to list the edges.

| Path | Where | How P grows or an edge appears | Request |
| --- | --- | --- | --- |
| Grant to P | `pkg/access` `Service.Grant` | P gets a role. | Yes |
| Grant to a group of P | `Service.Grant` | Each service principal member grows. | Yes |
| Member add | `pkg/principal` `Service.AddMember`, port `GroupReach` | P joins a group with grants. | Yes |
| Role update, more actions | `Service.UpdateRole` | Each holder of the role grows. | Yes |
| Role update, adds `namespace:manage-principal-tokens` | `Service.UpdateRole`; the action is in the data family (`pkg/access/access.go`), so `checkPlacement` lets a role granted on a namespace hold it | **Creates edges**: each holder of the role gets an act-as edge to each principal of each namespace where the role is granted. The check of ADR 70 decision 3 already runs: `roleCheck` (`pkg/access/check.go`) calls `checkEscalation`, which calls `checkNamespaceTokenGranter` first (`pkg/access/service.go`). With the installation as the object, that check needs `server:manage-access`, and the escalation loop needs `namespace:manage-principal-tokens` on the installation. That hold gives an act-as edge to each principal of each namespace, so the reach of the caller covers each new target. | Yes |
| Release that adds actions to built-in roles | `pkg/access/roles.go`, `builtInActions`: `admin` is `Actions(FamilyData)`; `account-administrator` and `site-administrator` append whole families | Each principal that holds these roles grows when a new release adds an action to a family. | **No.** No caller and no request. |
| Identity provider and SCIM adds a person | `pkg/principal` membership function (ADR 70 decisions 12 to 22) | A person is not a target of `principal:manage-tokens`, so these never grow P. They **add holders**: a person added to a group that holds an edge gets the edge. ADR 70 decision 13 protects a group for "an act-as right on a principal of the installation" only. A group that holds `principal:manage-tokens` on a principal of a namespace is not protected, so the identity provider adds members to it with no granter. All trust rules name principals of a namespace (`internal/trustrulesvc/exchange.go` refuses `p.Namespace != r.Namespace`). A group trusted from the identity provider (decision 16) had its reach checked when the trust was set, not after P grew. | **No** person decides. The sync has no caller who can choose. |
| Revoke, member removal | `Service.Revoke`, `RemoveMember` | H or P shrinks. Never widens an edge. | Yes |
| Restore | the restore of the access tables (ADR 70 decisions 7 and 8) | Replaces all access tables at once, with the edges and the reach in the snapshot. Needs `server:restore-access`. Of the built-in roles, only `site-administrator` holds it, but the action is in the administration family, so a custom role can hold it and a site administrator can grant that role to a principal whose reach does not cover everything (`siteOnlyActions` limits only the built-in roles). Decision 8 also lets a setting give the restore of access tables to each holder of `server:restore`. | Yes, but a restore cannot refuse half of a snapshot. |
| Token create and rotate | `pkg/accesstoken` `mayActFor` | Mints a token of P. | Yes |
| Trust-rule create and change | `internal/trustrulesvc` | Writes an act-as edge. | Yes |
| Trust-rule exchange | `internal/trustrulesvc` exchange | Mints a token of P at each run. Does not check the author today. | No person |

One of these paths is a gap under every option, also under (d) and (g).
The role update that adds `namespace:manage-principal-tokens` is not a gap:
the check of ADR 70 decision 3 already covers each target, as the row above
says.

- The identity provider adds holders to an unprotected group that holds an
  edge to a principal of a namespace, with no granter (open question 9).
  Option (g) flags it.

## Options

### (a) Refuse growth when a holder does not cover the new reach

A change that grows P is refused, unless each holder of an act-as edge to P
has a reach that covers the new reach of P. The reach of H for this compare
leaves out the edges through P, or the compare always passes. A change that
shrinks H gets the same check.

- **Administrator.** Most grants to a service principal with a team lead
  fail. That is the normal case of ADR 67: the team lead does not hold the
  reach of P. A must first widen H, or remove the hold of H. A cannot see
  why without the list of holders in the error.
- **Team lead.** The team lead blocks grants to "its" principal without any
  action. A revoke of its own grants fails.
- **Automation.** No change to running tokens. New grants that CI needs
  fail until an administrator acts.
- **Paths.** Grant, member add, role update, and the reverse closure of
  act-as edges for transitive growth. The check on a shrink touches revoke,
  member removal, disable, the identity provider and SCIM.
- **Failure modes.**
  - A refusal of a removal conflicts with ADR 70 decision 19 (a termination
    applies at once), decision 20, NIST SP 800-63C and PCI DSS 8.2.5. A
    shrink cannot be refused. So (a) works only for growth.
  - A release and a restore have no request to refuse.
  - It reverses ADR 67 decision 3 and contradicts the ADR 67 consequence.
  - The cost of a check grows with holders times principals for a grant to
    a group, and with the closure for transitive edges.

### (b) On growth, drop the holds that no longer cover P

The change commits. In the same transaction, the server revokes each hold
on P whose holder does not cover the new reach. It writes an audit entry
for each revoke and sends a notice to the holder.

- **Administrator.** Grants never fail. A grant can revoke grants that
  other people made. That is an action at a distance.
- **Team lead.** Loses the right to rotate with no warning before the
  change. The tokens it made stay and grow (ADR 70 decision 6 keeps them).
- **Automation.** Running tokens keep working and grow. The next rotation
  fails. Trust rules of H: open. If the exchange stays, the gap stays open
  through the exchange.
- **Paths.** Grant, member add, role update, the revoke code, notices, and
  the trust-rule store. A release would revoke holds at startup.
- **Failure modes.** It does not close the gap for existing tokens. It
  makes the effect of a grant hard to predict, against "No magic". It drops
  holds of `namespace:manage-principal-tokens`, which also cover other
  principals of the namespace. So it must revoke the namespace grant or
  carve an exception. It reverses ADR 67 decision 3.

### (c) Mark the edge "uncovered" and refuse new tokens through it

The change commits. The server marks each act-as edge to P that no
covering approval holds now as uncovered. Through an uncovered edge, the
server refuses token create, token rotate, trust-rule create and
trust-rule change. A person whose reach covers the new reach of P clears
the mark, or revokes the edge. The access review lists each mark.

- **Administrator.** Grants never fail. A must clear marks later, often
  without knowing of them at the time of the grant.
- **Team lead.** Cannot rotate until an administrator acts.
- **Automation.** Running tokens keep working and grow. Rotation stops.
  The trust-rule exchange is the hard choice: refuse it and every grant to
  P can stop CI, or keep it and the gap stays open through the exchange.
- **Paths.** Grant, member add, role update (to set the mark); token and
  trust-rule services (to read it); access review; a new API to clear it.
  It also covers a release and a restore, because the mark needs no
  request: the server computes it after the change.
- **Failure modes.** The mark is state that a restore must carry. The
  person who made the change is not the person who sees the effect. It
  reverses ADR 67 decision 3.

### (d) Keep ADR 67 decision 3 and accept the risk

No new check and no new record. The escape is the record that exists:

- the `tokenManagers` change of the audit entry of each grant;
- the access-review export, which lists the grant of the edge and the
  grants of P as rows, but not the reach of H;
- the flag of ADR 70 decision 6, which covers a token whose principal got
  more grants after its maker made the token. It is not built yet.

- **Administrator.** No friction. Must read the audit entry or the review
  to know.
- **Team lead.** No change.
- **Automation.** No change.
- **Paths.** None.
- **Failure modes.** The growth stays undecided. Detection only, as in
  Google Cloud lateral movement insights.

### (e) A ceiling on each edge

Each act-as edge records a ceiling: the reach of P that its granter
approved. A token made through the edge acts with the reach of P,
intersected with the ceiling. This is the shape of an AWS session policy,
passed at `AssumeRole` for one session, and of the ceiling grants of an
agent (ADR 354).

- **Administrator.** A grant to P does not reach tokens made through older
  edges. CI that needs the new grant fails with a denial that names the
  ceiling. A must raise the ceiling of each edge.
- **Team lead.** No change until the ceiling blocks a run.
- **Automation.** The new grant does not apply to the running tokens. This
  is safe but surprising.
- **Paths.** The token store (a ceiling on each token), the authorizer (an
  intersection at each request), every edge write, restore and export. It
  covers a release, the identity provider and a restore without a request,
  because the ceiling applies at each request.
- **Failure modes.** The largest change. Each token of P can act
  differently. ADR 70 decision 6 lets a token outlive the edge of its
  maker, so the ceiling must stay on the token. It reverses ADR 67
  decision 3.

### (f) Treat growth as a new grant of each edge

**This option reverses an accepted owner decision, ADR 67 decision 3.** It
also changes the role descriptions in `pkg/access/roles.go`.

A change that grows P widens every act-as edge to P. So it follows the rule
of ADR 70 decision 3 for each edge, in the same request:

1. The caller's reach must cover the new reach of P. The escalation check
   already asks for the added actions. This adds the old reach of P.
2. The request must name the edges that it widens. If it does not, the
   server refuses with `409` and lists them. The caller sends the request
   again with the list. For each edge it chooses "keep" or "revoke". The
   list works as a compare-and-set: a list that differs from the current one
   is refused. This is the pattern of ADR 70 decision 22: "A grant that
   makes a group protected while the group has members from the identity
   provider fails, unless the same request decides the trust of the group."
3. A change that shrinks H or P is never refused and needs no list.

The list holds each edge from a holder H to P, direct or transitive, with
these exclusions:

- a hold on the installation: decision 2 checks it at token creation;
- the caller;
- each H whose own reach over grants, without the edges through P, covers
  the new reach of P: no approval is missing for that H;
- an edge of a delegation (ADR 354): its ceiling grants bound it, so P
  growing does not widen it.

Each item in the list shows the holder, the kind of hold (direct, group,
namespace, trust rule, transitive through Q), and the count of tokens made
through the edge. For a group edge it also shows:

- the member source: local, identity provider, SCIM, or mixed;
- the trust state: not trusted, trusted from the identity provider
  (ADR 70 decision 16), or full sync (decision 17).

"Keep" on a group edge whose members come from the identity provider, or
on a trusted group, also keeps the edge for each member that the identity
provider adds later. The list says so.

A grant can do two things at once: grow P, and make a group that holds an
edge to P protected (decision 13). Then decision 22 needs the trust of the
group in the same request too. The request carries both: the edge list and
the trust decision. The server checks decision 22 first, then the list.

The paths without a request need their own rule:

- **A release.** No caller exists, so step 2 cannot apply. Proposed rule:
  the release notes name each action that a release adds to a built-in
  role. One durable operation, keyed by the release version, writes an
  audit entry by the server and a reach flag of option (g) for each edge
  that the release widens, once for the installation. Or the owner names
  this as an explicit exception (open question 10).
- **The identity provider and SCIM.** They never grow P. They add holders
  to a group. Under (f), "keep" of a group edge covers later adds, as said
  above. Whether ADR 70 decision 3 applies to these adds is open
  question 9.
- **A restore.** Open question 11.

- **Administrator.** One more step when the list is not empty. The console
  shows the list before the grant. A sees who gains, and decides.
- **Team lead.** No change when A keeps the edge. A notice when A revokes
  it.
- **Automation.** Running tokens and exchanges keep working under a kept
  edge. Management as code must send the list.
- **Paths.** Grant, member add, role update, the reverse closure for
  transitive growth, the startup of a release, and the access review.
- **Failure modes.**
  - A caller that holds the added actions but not the old reach of P can no
    longer grant to P. For a service principal of a namespace this is rare,
    because its grants stay inside the namespace.
  - The `409` appears only when a holder with a smaller reach exists: a
    holder of `token-manager` on P, a custom role, a trust-rule author or a
    transitive holder. The built-in `admin` on the namespace of P holds
    `namespace:manage-principal-tokens`, but its reach usually covers a
    principal whose grants stay inside the namespace, so it is not in the
    list. For a team that uses `token-manager`, as ADR 67 intends, each
    grant to P returns `409` once.
  - A grant to a large group lists many edges.
  - A revoke chosen in the list does not revoke the tokens made through the
    edge (open question 4).
  - It does not close the release path or the identity-provider path with a
    decision. It records them.

### (g) Keep ADR 67 decision 3 and flag each widened edge

ADR 67 decision 3 stays. No request is refused. The server writes a stored
**reach flag** for each act-as edge that a change widens or creates without
a covering approval. A person whose reach covers the current reach of P
acknowledges the flag or revokes the edge. The flag of ADR 70 decision 6 is
not built yet, so (g) defines its own record. The list of widened edges uses
the scope and the columns of the list of (f).

#### What (g) flags on each path

One line for each row of "Code paths". "Response" is the response that holds
the list of widened edges, or "none". "Actor" is the actor of the audit
entry of the change, and of the flags that it opens.

| Path | Flagged | Response | Actor |
| --- | --- | --- | --- |
| Grant to P | Yes: each edge to P, with the added pairs. | The grant response: `widenedEdges` (at most 100 items), `widenedEdgeCount`, and the change ID to filter the flag list. | The caller. |
| Grant to a group of P | Yes: each edge to each service principal member that grows. | The grant response, as above. | The caller. |
| Member add | Yes: each edge to P when P joins a group with grants. | The member-add response, as above. | The caller. |
| Role update, more actions | Yes: each edge to each service principal that holds the role and grows. | The role-update response, as above. | The caller. |
| Role update, adds `namespace:manage-principal-tokens` | Yes, as cause `new-edge`: each new edge, with the full reach of P as the added pairs. The check of ADR 70 decision 3 already refuses a caller whose reach does not cover each target, so no open question remains. | The role-update response, as above. | The caller. |
| Release that adds actions to built-in roles | Yes: each edge to each service principal that grows. | None. No caller exists. The release notes name each added action. | The server. One durable operation, keyed by the release version, writes the flags and one audit entry for the installation. A restart or a second instance finds the operation done and writes nothing again. |
| Identity provider or SCIM adds a holder | Yes, as cause `new-holder`: the edge of the new member, with the full reach of P as the added pairs. Open question 9 asks whether a protection refuses it too. | None. A sign-in sync has no response to the identity provider. A SCIM response keeps the SCIM shape and carries no list. | The sync identity: the identity-provider connection, or the principal of the SCIM token. |
| Restore of the access tables | Yes, as cause `restore`: each edge in the restored tables whose reach of P is not in the baseline of the edge (see "Bounds"). Restored acknowledgments keep their covered reach. An edge whose restored reach of P is wider than that covered reach gets a new flag. Open question 11 asks whether a check refuses it too. | The result of the restore operation: the count of flags and the change ID to filter the flag list. | The caller of the restore. |
| Re-enable of a disabled P (`principal` `EnablePrincipal`) | Yes, as cause `re-enable`: each edge to P, with the full reach of P as the added pairs. The person who enables P decides that the grants act again. That person did not approve the edges. | The enable response, as above. | The caller. |
| H revokes its own reach | No. Under ADR 67 the own reach of H does not bound P (see "The question"). If open question 1 makes the own reach of H a bound, the revoke opens a flag with the pairs of P that H no longer covers. | None. | Not applicable. |
| Revoke, member removal, disable | No. These only shrink. | None. | Not applicable. |
| Token create and rotate | No. A token does not change an edge. | None. | Not applicable. |
| Trust-rule create and change | No. ADR 70 decision 3 checks the granter of a new edge already. | None. | Not applicable. |
| Trust-rule exchange | No. It does not change an edge. Open question 8. | None. | Not applicable. |

A flag never blocks a request. An open or acknowledged flag does not block a
revoke, a member removal, a disable, a token create, a rotation or an
exchange. A revoke of an edge is an ordinary `Revoke` in a second request.
The namespace hold of open question 5 cannot revoke one edge alone.

#### The flag record

The flag is a stored record. The API lists it. A rule in the review code is
not enough.

- **Home.** `pkg/access` owns the record, the port and the conformance
  tests, next to the edges and `Reach`. The record is in the access tables,
  so a backup, an export and a restore of the access tables carry it.
- **Fields.**
  - `id`;
  - the edge: the holder (a principal or `group:<id>#member`), P, and the
    kind of hold (direct, group, namespace, trust rule, or transitive
    through Q);
  - the cause: `growth`, `new-edge`, `new-holder`, `restore`, `re-enable`
    or `release`;
  - `changes`: the IDs of the audit entries of the changes that added pairs
    while the flag is open, at most 100, with a count;
  - `addedPairs`: the pairs (action, object) that the reach of P has and the
    baseline of the edge does not have;
  - `state`: `open`, `acknowledged`, `revoked` (the edge was revoked while
    the flag was open) or `superseded` (the edge or P stopped existing
    through another change);
  - `openedAt`, `closedAt`;
  - for an acknowledgment: `acknowledgedBy`, `acknowledgedAt` and
    `coveredReach`, the reach of P at the time of the acknowledgment.
- **Fence.** A flag of P of a namespace lives in the namespace of P. Each
  write carries the fence of that namespace, and the store checks it in the
  same transaction. A change in one namespace writes its flags in its own
  transaction. A change of installation scope (a role update of a role
  granted in many namespaces, a release, a restore) writes the flags through
  a durable operation, with one step for each namespace under the fence of
  that namespace. The response of such a role update holds the list and the
  ID of that operation. A flag of P of the installation has no namespace
  fence, as other installation grants.
- **List endpoints.**
  - `GET /api/server/namespaces/{namespace}/reach-flags`, action
    `namespace:read-access` on the namespace;
  - `GET /api/server/reach-flags`, action `server:read-access` on the
    installation.

  Filters: `state`, `principal`, `holder`, `change`. The access-review
  export of P and of H links to the open flags.
- **Acknowledge endpoints.** `POST .../reach-flags/{id}/acknowledge` and the
  bulk form in "Bounds", under the same two prefixes.

#### Bounds

- **One open flag for each edge.** A growth while the flag is open adds its
  change ID and its pairs to that flag. It does not open a second flag.
- **A baseline for each edge.** The baseline is the reach of P that a
  person last approved for the edge: the reach that ADR 70 decision 3
  checked when the edge was made, or the `coveredReach` of the last
  acknowledgment. A flag opens only when the reach of P has a pair outside
  the baseline. So an acknowledgment covers the reach at the time it is
  given, and the next flag opens only when P grows past it. For an edge that
  exists before this change ships, one durable operation sets the baseline
  to the reach of P at that time and writes an audit entry for each edge.
  The causes `new-edge`, `new-holder` and `re-enable` use an empty baseline.
- **Metrics.** A gauge of the count of open flags, and a gauge of the age
  of the oldest open flag in seconds. Both are for the installation, with no
  label for a namespace or a principal.
- **Bulk acknowledgment for each change.** `POST
  .../reach-flags/acknowledge` with a `change` ID acknowledges each open
  flag whose `changes` holds that ID, one by one, with the checks of
  "Acknowledgment" for each flag. The audit entry records the change ID.
  The response lists the flags that stay open and the reason for each. No
  form acknowledges every flag.

#### Acknowledgment

An acknowledgment is a privileged action. The proposal, for open
question 12:

- **Action and object.** The placement rule (`checkPlacement`) lets a role
  that is granted below the installation hold data actions only, and the
  built-in `admin` holds data actions only. So one action in the
  administration family could never be used by a namespace administrator.
  The proposal has two actions. `namespace:acknowledge-reach` is a data
  action, on the namespace of P, for a P that belongs to a namespace. The
  catalog gives it to the built-in roles that hold
  `namespace:manage-access` today. `principal:acknowledge-reach` is an
  administration action, on the installation, for a P of the installation.
  The catalog gives it to the built-in roles that hold
  `server:manage-access` today.
- **Reach check at the point of use.** In the write transaction of the
  acknowledgment, under the fence of the namespace of P: the reach over
  grants of the acknowledger, without the edges through P, covers the
  current reach over grants of P, not only the `addedPairs`. Else the server
  refuses with `403`, names the count of pairs that the acknowledger does
  not cover, and writes the denial to the audit log. The store also refuses
  a flag that is not `open`, as a compare-and-set on the state.
- **Audit.** Each acknowledgment writes an audit entry with the flag ID, the
  edge, the change IDs, the count of covered pairs and the actor. The flag
  stores `acknowledgedBy`, `acknowledgedAt` and `coveredReach`.
- **Who may not acknowledge.**
  - H, for a flag on its own edge;
  - the actor of a change in `changes`, in any request. So the author of a
    grant cannot clear its own flag, and (g) does not work as (f) with an
    optional list;
  - a service principal or an agent: only a person acknowledges;
  - a person without a fresh step-up, when the installation has step-up
    (`internal/stepup`; the ports are not built yet).
- **Fail closed.** An unknown state, cause or kind of hold refuses the
  acknowledgment and writes the denial to the audit log.

#### Impact

- **Administrator.** No friction. The response and the console show who
  gains, after the change. A can revoke an edge in a second request. Another
  person acknowledges the flag.
- **Team lead.** No change.
- **Automation.** No change. Management as code can read the list.
- **Paths.**
  - Grant, member add, role update, enable, restore and the release
    startup compute the list. `withTokenManagers` (`pkg/access/service.go`)
    runs on `Grant`, and `AddMember` (`pkg/principal/service.go`) reads the
    holders for its audit entry. (g) needs the same read on `UpdateRole`,
    `EnablePrincipal` and restore.
  - The flag store and its conformance tests in `pkg/access`.
  - The list and acknowledge endpoints, the new action, the two gauges.
  - The durable operations for a release, the first baseline, and changes
    of installation scope.
- **Failure modes.**
  - The growth applies before a person decides. The window is the time
    until the acknowledgment.
  - The flags can collect when nobody acknowledges them. The gauge of the
    oldest open flag shows that. The flags do not expire (open
    question 13).
  - A deliberate gap of the definition of "P grows": a new unit in a
    namespace where P holds `admin` does not grow the reach over grants, so
    it opens no flag, but P can act on more units.

## Precedent

| Product | Act-as right | Growth of the target after the grant |
| --- | --- | --- |
| AWS IAM | `sts:AssumeRole`. The role's trust policy must trust the caller, and the caller needs an identity policy that allows the action: "you must attach both a trust policy and an identity-based policy to an IAM role" [1]. | No check. The session acts with the policies of the role. The pages read do not say what happens to an open session when the role changes. A session policy, passed at `AssumeRole`, bounds one session: "The resulting session's permissions are the intersection of the role's identity-based policy and the session policies." [2] |
| Google Cloud | `roles/iam.serviceAccountTokenCreator` lets a principal create short-lived credentials for a service account [3]. "Impersonating a service account lets an authenticated principal access whatever the service account can access." [4] | No check. Lateral movement insights "identify roles that allow a service account in one project to impersonate a service account in another project" [5]. They are detection, with the states ACTIVE and ACCEPTED. |
| Kubernetes | The `impersonate` verb. The impersonated request is evaluated as the impersonated user [6]. | The RBAC escalation check runs at write time. A role create or update needs each permission of the role, or `escalate`. A binding needs each permission of the role, or `bind` [7]. The page does not say what happens to bindings when a role widens. A binding names the role, so it likely takes the new rules. |
| GitHub, fine-grained tokens | "A token cannot grant additional access capabilities to a user." Owners can require approval of fine-grained tokens [8]. | The token is bound by its owner's live access. |
| GitHub Apps | An app installation acts with the permissions that the account approved. The app managers who hold the private key act as the app. | "Updated permissions won't take effect on an installation or user authorization until the new permissions are approved." A removal applies at once [9]. The approval comes from each account where the app is installed, that is, the owner of the resources. It decides what the app reaches. It does not decide who acts as the app: the app managers get the new permissions with no new decision. |

What the precedent shows:

- The clouds and Kubernetes check act-as at the time of the grant. A later
  growth of the target flows to each holder. None refuses the growth. Google
  Cloud detects it, and its insights have the states ACTIVE and ACCEPTED.
  ADR 67 decision 3 chose this model on 2026-10-14.
- An AWS session policy and GitHub fine-grained tokens bound an act-as
  right by an intersection. That is option (e), and the agent ceiling of
  ADR 354.
- GitHub Apps ask the owner of the resources to approve a growth of the
  app. They do not ask again who may act as the app. That is the model of
  the clouds for the act-as right, not the model of (f).
- No surveyed product decides again who holds an act-as right when its
  target grows. Option (f) has no direct precedent. Option (g) is close to
  the detection of Google Cloud, with a record at the time of the change.
  The ACCEPTED state of an insight is the analog of an acknowledged flag.

Sources:

1. AWS IAM User Guide, "Policies and permissions in AWS Identity and
   Access Management",
   https://docs.aws.amazon.com/IAM/latest/UserGuide/access_policies.html
2. AWS STS API Reference, "AssumeRole",
   https://docs.aws.amazon.com/STS/latest/APIReference/API_AssumeRole.html
3. Google Cloud IAM, "Service account permissions",
   https://docs.cloud.google.com/iam/docs/service-account-permissions
4. Google Cloud IAM, "Service account impersonation",
   https://docs.cloud.google.com/iam/docs/service-account-impersonation
5. Google Cloud Policy Intelligence, "Lateral movement insights",
   https://docs.cloud.google.com/policy-intelligence/docs/lateral-movement-insights
6. Kubernetes, "User impersonation",
   https://kubernetes.io/docs/reference/access-authn-authz/user-impersonation/
7. Kubernetes, "Using RBAC authorization", section "Privilege escalation
   prevention and bootstrapping",
   https://kubernetes.io/docs/reference/access-authn-authz/rbac/
8. GitHub Docs, "Managing your personal access tokens",
   https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens
9. GitHub Docs, "Modifying a GitHub App registration",
   https://docs.github.com/en/apps/maintaining-github-apps/modifying-a-github-app-registration

All read on 2026-11-08. The Kubernetes RBAC section was read from a summary
of the page, not in full.

## Proposal

The first version of this RFC preferred (f). With the facts above, the
author now prefers option (g): keep ADR 67 decision 3, and show and record
each widened edge at the time of the change.

- The owner accepted growth in ADR 67 decision 3 on 2026-10-14. (f) and
  every other option that refuses or bounds growth reverse it. No new fact
  since then shows that the accepted risk is larger than the owner thought.
  The new facts are the paths that no request can stop.
- (f) cannot close the gap where it opens. A release and the identity
  provider grow P or add holders with no request. A restore cannot refuse
  half of a snapshot. So (f) needs the record of (g) for those paths
  anyway.
- No surveyed product decides again who holds an act-as right when the
  target grows. The clouds keep growth and detect it. (g) does that, with
  a record at the time of the change, not after an offline scan.
- (g) never refuses a request. So it fits ADR 70 decisions 19 and 20, NIST
  SP 800-63C and PCI DSS 8.2.5, and it stops no automation.
- "Why can H do this" has an answer: the grant of the edge, its granter,
  the change that grew P, the list in its audit entry, and the flag with
  its acknowledgment.
- The flag is a stored record with a list endpoint, an authorization
  action and an audit entry for each acknowledgment. The review-only rule of
  the first version of (g) did not exist in the code.

Option (f) stays the choice if the owner wants growth to need a decision
before it applies. Option (e) is the strongest bound, but it changes the
token model, and ADR 70 decision 6 makes it harder. Option (d) is the
choice if the owner wants no change at all.

Independent of the option, the gap of "Code paths" needs a rule: the
identity-provider adds into an unprotected group (open question 9). Under
(g), it is flagged. The owner can add a protection on top.

(g) also needs two owner decisions of its own: who may acknowledge a flag
(open question 12), and the bound on the flags (open question 13).

The proposal changes these parts, by a new ADR:

| ADR or file | Part | Change under (g) | Change under (f) |
| --- | --- | --- | --- |
| 67 | Decision 3, "now or gets later ... this decision accepts it" | None. | Reversed: growth of P is a new grant of each edge. |
| 70 | Decision 2 | None. | A hold on one principal is still not compared at token creation. The edge is checked when P grows. |
| 70 | Decision 3 | None. | A change that grows the target of an act-as edge is a grant of that edge, and needs the same reach. |
| 70 | Decision 6, the flag of a token whose principal got more grants | A stored reach flag for each widened edge, with its list endpoints, the actions `namespace:acknowledge-reach` and `principal:acknowledge-reach`, the bounds and the rules of acknowledgment of option (g). | The flag covers the edges that a release or a restore widens. |
| 70 | Decision 7 and decision 8, restore | Per open question 11. | Per open question 11. |
| 70 | Decision 32, row "67, decision 3" | The server lists and flags the widened edges on each path of the (g) table. | The server compares reach in a third place: a change that grows a principal with act-as edges. |
| `pkg/access/roles.go` | Descriptions of `account-administrator` and `token-manager` ("now or gets later") | None. | Change to say that a growth needs a decision of each edge. |

## Open questions

Each question stands alone. "P" is a service principal, "H" holds an
act-as right on P (`principal:manage-tokens` on P, directly, through a
group, or through `namespace:manage-principal-tokens` on its namespace, or
as the author of a trust rule that names P).

1. **What bounds an edge from H to P?** The approval of its granter at the
   time of the grant (ADR 67, as this RFC reads it), or the own reach of H
   too? If the own reach, a holder that loses reach also needs a rule, and
   options (a) to (c) apply to that path.
2. **Keep growth (ADR 67 decision 3), or require a decision before P
   grows?** Keep: option (g), or (d) with no new record. Require: option
   (f), which reverses ADR 67 decision 3. The author recommends (g).
3. **Under (f), cover the old reach of P too, or only the added actions?**
   The (f) text asks for the full new reach, as ADR 70 decision 3 does for
   a new edge. The added actions alone are already checked today.
4. **When a person revokes an edge from H to P, under (f) or (g), what
   happens to the tokens and trust rules made through it?** ADR 70
   decision 6 keeps tokens and flags them. The author suggests the same,
   with the choice to revoke them in the same request.
5. **Holds of `namespace:manage-principal-tokens`.** A revoke of such an
   edge revokes a namespace grant that also covers other principals of the
   namespace. Is the choice there "keep" (or "acknowledge") only, with
   "refuse the grant" under (f)?
6. **Transitive growth.** Does the list name edges two or more steps away
   (H to P to Q, when Q grows)? The author says yes, because reach follows
   them.
7. **Management as code under (f).** Is a request field that names the
   edges acceptable for a client that applies grants from configuration,
   or does it need a setting that accepts every edge, recorded in the audit
   log?
8. **The trust-rule exchange.** Should the exchange check the author's
   act-as edge at each run, as token creation does? Today it does not.
9. **The identity provider adds a person to a group that holds an act-as
    right on a principal of a namespace.** ADR 70 decision 13 does not
    protect such a group, because it protects only an act-as right on a
    principal of the installation. So the add has no granter, and ADR 70
    decision 3 does not run. Choices: (i) decision 13 also protects a group
    with an act-as right on a principal of a namespace; (ii) accept the add
    and flag the edge of the new holder as option (g) does, with the sync
    identity as the actor; (iii) both.
10. **A release that adds an action to a built-in role.** `admin`,
    `account-administrator` and `site-administrator` hold whole action
    families, so a new action in a family grows every service principal
    with these roles, and no request exists. Is the rule: release notes
    that name the added actions, plus one durable operation, keyed by the
    release version, that writes one audit entry by the server for the
    installation and a reach flag for each widened edge, once, with no
    new write on a restart or on a second instance? Or is it an explicit
    exception?
11. **A restore of the access tables.** It brings back edges and reach that
    nobody decided at the time of the restore. Only `site-administrator`
    holds `server:restore-access` among the built-in roles, but a custom
    role can hold it, and the setting of ADR 70 decision 8 can give it to
    each holder of `server:restore`. Which rule applies: the reach of the
    caller must cover each restored edge, or the restore records each edge
    that it widens (compared with the tables before the restore) in the
    audit log and opens a reach flag of option (g) for it?

12. **Who may acknowledge a reach flag?** Option (g) stores a flag for each
    act-as edge from H to P that a change widens, until a person
    acknowledges it or revokes the edge. The proposal: two new actions
    (`namespace:acknowledge-reach` on the namespace of P, and
    `principal:acknowledge-reach` on the installation for a P of the
    installation; see "Action and object"); a check in the write transaction
    that the acknowledger's reach over grants, without the edges through P,
    covers the current reach of P; an audit entry for each acknowledgment.
    Choices: (i) a person only, not H, not the actor of a change that the
    flag names, with a fresh step-up where the installation has it (the
    proposal); (ii) as (i), but the actor of the change may acknowledge in
    a later request; (iii) each principal with the action and a covering
    reach, service principals and agents included; (iv) only a holder on
    the installation acknowledges, with one administration action, and a
    namespace administrator can list the flags but cannot acknowledge them.
13. **What bounds the reach flags?** Without a bound, each grant to P with a
    `token-manager` holder opens a flag for each holder, and a release can
    open many flags at once. The proposal: one open flag for each edge,
    which collects the added pairs while it is open; an acknowledgment that
    covers the reach of P at its time, so that the next flag opens only when
    P grows past it; two gauges, the count of open flags and the age of the
    oldest open flag; a bulk acknowledgment for one change ID, never for
    all flags. Choices: (i) the proposal; (ii) the proposal, plus an expiry
    that closes an open flag after a set time with an audit entry by the
    server; (iii) no bound.

## Checklist

- [ ] Tolerates asynchronous replication between installations: each
  namespace writes only its own half, under its own fence, and no write
  waits for another installation (ADR 248 decisions 3, 6 and 9). Not
  checked yet. The edge list reads holders on the installation and on the
  namespace in the transaction of the grant. Under (g), a flag of P lives in
  the namespace of P under its fence. A change in one namespace writes its
  flags in its own transaction. A change of installation scope (a role
  update of a role granted in many namespaces, a release, a restore) writes
  the flags through a durable operation, one step for each namespace under
  that namespace's fence. So no write spans two namespaces. The read of
  holders on the installation for a namespace change is still to check.

## Outcome

Empty until the RFC is accepted (link the ADR) or dropped (say why).
