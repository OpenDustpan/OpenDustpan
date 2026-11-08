---
rfc: 128
title: Reach growth and token managers
status: discussing
created: 2026-11-08
---

# RFC 128. Reach growth and token managers

Status: discussing. This RFC touches ADR 70 decision 2, with decision 3,
decision 6 and the row "67, decision 3" of decision 32. It does not edit an
accepted ADR. An owner decision on it gives a new ADR.

## Problem

### The facts

- A holder H can hold `principal:manage-tokens` on a service principal P.
  The hold comes from a direct grant on P, from a grant to a group of H, or
  from `namespace:manage-principal-tokens` on the namespace of P (ADR 67).
  The author of a trust rule that names P has the same act-as edge
  (ADR 70 decision 10).
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
  approval of the granter bounds it.

### The gap

The approval of decision 3 covers the reach of P at the time of the grant.
P can grow after that:

- another administrator A grants a role to P, or to a group of P;
- A adds P to a group that holds grants;
- A adds an action to a custom role that P holds, directly or through a
  group (`UpdateRole`);
- P holds an act-as edge to a principal Q, and Q grows. Reach follows
  act-as edges transitively, so P and H grow too.

Each of these widens the edge H to P. Nobody whose reach covers the new
reach of P approved the wider edge. A holds the new actions, because the
escalation check of a grant asks for them. So property P2 of ADR 70 ("no
amplification") holds for the actions that A adds. The approval of the edge
as a whole is split between two people, G for the old reach and A for the
new actions, and A may not know that H exists.

The access review shows the wider reach of H, because reach includes act-as
edges. The audit entry of a grant to a service principal already names the
holders in the change `tokenManagers`. So the record exists. A decision
about the edge does not.

The reviewer of #2877 also names a second path: H revokes its own reach.
Under ADR 67 the own reach of H does not bound P. A smaller reach of H does
not widen the edge. This RFC treats that path as a gap only if the owner
wants the own reach of H to bound P (open question 1). That would reverse
the ADR 67 consequence above.

Severity, from the reviewer: medium. It needs a second actor (A), or the
path of the paragraph above.

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

Each option touches some of these paths. The table names them once.

| Path | Where | How P grows or H changes |
| --- | --- | --- |
| Grant to P | `pkg/access` `Service.Grant` | P gets a role. |
| Grant to a group of P | `Service.Grant` | Each service principal member grows. |
| Member add | `pkg/principal` `Service.AddMember`, port `GroupReach` | P joins a group with grants. |
| Role update | `Service.UpdateRole` | Each holder of the role grows. |
| Revoke, member removal | `Service.Revoke`, `RemoveMember` | H or P shrinks. Never widens an edge. |
| Restore | the restore of the access tables (ADR 70 decision 7) | Replaces all access tables at once. Needs `server:restore-access`. Only `site-administrator` holds it. |
| Identity provider and SCIM | `pkg/principal` membership function (ADR 70 decisions 12 to 22) | Adds and removes persons. A person is not a target of `principal:manage-tokens`, so these never grow P directly. They can change H. |
| Token create and rotate | `pkg/accesstoken` `mayActFor` | Mints a token of P. |
| Trust-rule create and change | `internal/trustrulesvc` | Writes an act-as edge. |
| Trust-rule exchange | `internal/trustrulesvc` exchange | Mints a token of P at each run. Does not check the author today. |

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
  - A restore cannot refuse half of a snapshot.
  - It contradicts the ADR 67 consequence.
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
  the trust-rule store.
- **Failure modes.** It does not close the gap for existing tokens. It
  makes the effect of a grant hard to predict, against "No magic". It drops
  holds of `namespace:manage-principal-tokens`, which also cover other
  principals of the namespace. So it must revoke the namespace grant or
  carve an exception.

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
- **Failure modes.** The mark is state that a restore must carry. The
  person who made the change is not the person who sees the effect.

### (d) Keep ADR 70 decision 2 and accept the risk

No new check. The escape is the record that exists:

- the `tokenManagers` change of the audit entry of each grant;
- the access review, which shows reach through act-as edges;
- the flag of ADR 70 decision 6, extended to a hold on P whose holder P has
  grown past.

- **Administrator.** No friction. Must read the audit entry or the review
  to know.
- **Team lead.** No change.
- **Automation.** No change.
- **Paths.** The access review only.
- **Failure modes.** The gap stays. Detection only, as in Google Cloud
  lateral movement insights.

### (e) A ceiling on each edge

Each act-as edge records a ceiling: the reach of P that its granter
approved. A token made through the edge acts with the reach of P,
intersected with the ceiling. This is the shape of an AWS permissions
boundary, and of the ceiling grants of an agent (ADR 354).

- **Administrator.** A grant to P does not reach tokens made through older
  edges. CI that needs the new grant fails with a denial that names the
  ceiling. A must raise the ceiling of each edge.
- **Team lead.** No change until the ceiling blocks a run.
- **Automation.** The new grant does not apply to the running tokens. This
  is safe but surprising.
- **Paths.** The token store (a ceiling on each token), the authorizer (an
  intersection at each request), every edge write, restore and export.
- **Failure modes.** The largest change. Each token of P can act
  differently. ADR 70 decision 6 lets a token outlive the edge of its
  maker, so the ceiling must stay on the token.

### (f) Treat growth as a new grant of each edge

A change that grows P widens every act-as edge to P. So it follows the rule
of ADR 70 decision 3 for each edge, in the same request:

1. The caller's reach must cover the new reach of P. The escalation check
   already asks for the added actions. This adds the old reach of P.
2. The request must name the edges that it widens. If it does not, the
   server refuses with `409` and lists them: holder, kind of hold (direct,
   group, namespace, trust rule), and the count of tokens made through
   each. The caller sends the request again with the list. For each edge
   it chooses "keep" or "revoke". The list works as a compare-and-set: a
   list that differs from the current one is refused. This is the pattern
   of ADR 70 decision 22: "A grant that makes a group protected while the
   group has members from the identity provider fails, unless the same
   request decides the trust of the group."
3. A change that shrinks H or P is never refused and needs no list.

- **Administrator.** One more step when edges exist. The console shows the
  list before the grant. A sees who gains, and decides.
- **Team lead.** No change when A keeps the edge. A notice when A revokes
  it.
- **Automation.** Running tokens and exchanges keep working under a kept
  edge. Management as code must send the list.
- **Paths.** Grant, member add, role update, and the reverse closure for
  transitive growth. Restore needs no new check: only `site-administrator`
  restores the access tables, and it covers all. The identity provider and
  SCIM never grow P.
- **Failure modes.**
  - A caller that holds the added actions but not the old reach of P can no
    longer grant to P. For a service principal of a namespace this is rare,
    because its grants stay inside the namespace.
  - A grant to a large group lists many edges.
  - A revoke chosen in the list does not revoke the tokens made through the
    edge (open question 4).

## Precedent

| Product | Act-as right | Growth of the target after the grant |
| --- | --- | --- |
| AWS IAM | `iam:PassRole`. "The service then checks whether that user has the `iam:PassRole` permission." "For most services, you only have to pass the role to the service once during setup." [1] | No check. The service acts with the live policies of the role. A permissions boundary caps it: "The effective permissions are the intersection of both policy types." [2] AWS shows boundaries "to delegate permissions management tasks", with the condition key `iam:PermissionsBoundary` on user creation. |
| Google Cloud | `roles/iam.serviceAccountTokenCreator` lets a principal create short-lived credentials for a service account [3]. "Impersonating a service account lets an authenticated principal access whatever the service account can access." [4] | No check. Lateral movement insights "identify roles that allow a service account in one project to impersonate a service account in another project" [5]. They are detection, with the states ACTIVE and ACCEPTED. |
| Kubernetes | The `impersonate` verb. The impersonated request is evaluated as the impersonated user [6]. | The RBAC escalation check runs at write time. A role create or update needs each permission of the role, or `escalate`. A binding needs each permission of the role, or `bind` [7]. The page does not say what happens to bindings when a role widens. A binding names the role, so it likely takes the new rules. |
| GitHub, fine-grained tokens | "A token cannot grant additional access capabilities to a user." Owners can require approval of fine-grained tokens [8]. | The token is bound by its owner's live access. |
| GitHub Apps | An app installation acts with the permissions that the account approved. | "Updated permissions won't take effect on an installation or user authorization until the new permissions are approved." A removal applies at once [9]. |

What the precedent shows:

- The clouds and Kubernetes check act-as at the time of the grant. A later
  growth of the target flows to each holder. None refuses the growth. Google
  Cloud detects it. ADR 67 already chose this model on 2026-10-14.
- AWS permissions boundaries and GitHub tokens bound an act-as right by an
  intersection. That is option (e), and the agent ceiling of ADR 354.
- GitHub Apps ask the owner of each installation to approve a growth, and
  apply a removal at once. That is the nearest precedent for (f): growth
  needs a decision in the open, a shrink never waits.

Sources:

1. AWS IAM User Guide, "Grant a user permissions to pass a role to an AWS
   service", https://docs.aws.amazon.com/IAM/latest/UserGuide/id_roles_use_passrole.html
2. AWS IAM User Guide, "Permissions boundaries for IAM entities",
   https://docs.aws.amazon.com/IAM/latest/UserGuide/access_policies_boundaries.html
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

The author prefers option (f): treat a growth of P as a new grant of each
act-as edge to P, decided in the same request.

- It keeps the model of ADR 67: the approval of a granter bounds an edge,
  not the own reach of the holder.
- It closes the gap where it opens. The person who grows P is the person
  who decides who gains, with the list in front of it. No mark waits for a
  person who does not know of it, as in (c). No grant changes another
  person's grants without a choice, as in (b).
- It never refuses a removal. So it fits ADR 70 decisions 19 and 20, NIST
  SP 800-63C and PCI DSS 8.2.5. Option (a) does not.
- It keeps running tokens and trust-rule exchanges working under a kept
  edge. So automation does not stop because of a grant.
- It needs no new check on restore, the identity provider or SCIM.
- It follows ADR 70 decision 22, an owner rule of the same kind, and the
  approval of growth of GitHub Apps.
- Every edge decision writes an audit entry with the list. So "why can H
  do this" has an answer: the grant, its granter, and the edge decision.

Option (d) stays the fallback if the owner finds the friction of (f) too
high. Option (e) is the strongest bound, but it changes the token model,
and ADR 70 decision 6 makes it harder. The author does not propose it now.

The proposal changes these parts, by a new ADR:

| ADR | Part | Change |
| --- | --- | --- |
| 70 | Decision 2 | A hold on one principal is still not compared at token creation. The edge is checked when P grows. |
| 70 | Decision 3 | A change that grows the target of an act-as edge is a grant of that edge, and needs the same reach. |
| 70 | Decision 32, row "67, decision 3" | The server compares reach in a third place: a change that grows a principal with act-as edges. |

## Open questions

1. **What bounds an edge?** The approval of its granter (ADR 67, as this
   RFC reads it), or the own reach of the holder too? If the own reach, a
   holder that loses reach also needs a rule, and options (a) to (c) apply
   to that path.
2. **Cover the old reach of P too, or only the added actions?** The
   proposal asks for the full new reach, as decision 3 does. The added
   actions alone are already checked today.
3. **Must the request name the edges, or only see them?** Naming them
   (compare-and-set, `409`) is the proposal. A softer form returns the list
   in the response and the audit entry, and refuses nothing. That is (d)
   with a better record.
4. **When a caller chooses "revoke" for an edge, what happens to the tokens
   and trust rules made through it?** ADR 70 decision 6 keeps tokens and
   flags them. The author suggests the same, with the choice to revoke them
   in the same request.
5. **Holds of `namespace:manage-principal-tokens`.** A "revoke" of such an
   edge revokes a namespace grant that also covers other principals. Is the
   choice there "keep" or "refuse the grant" only?
6. **Transitive growth.** Does the list name edges two or more steps away
   (H to P to Q)? The proposal says yes, because reach follows them.
7. **Management as code.** Is a field that names the edges acceptable for a
   client that applies grants from configuration, or does it need a
   setting that accepts every edge, recorded in the audit log?
8. **The trust-rule exchange.** Should the exchange check the author's edge
   at each run, as token creation does? Today it does not.

## Checklist

- [ ] Tolerates asynchronous replication between installations: each
  namespace writes only its own half, under its own fence, and no write
  waits for another installation (ADR 248 decisions 3, 6 and 9). Not
  checked yet. The edge list reads holders on the installation and on the
  namespace in the transaction of the grant. An installation-wide grant or
  role update reads holders of many namespaces.

## Outcome

Empty until the RFC is accepted (link the ADR) or dropped (say why).
