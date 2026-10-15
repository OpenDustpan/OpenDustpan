---
rfc: 70
title: "Authorization model findings: act-as reach, restores, trust rules and a model checker"
status: accepted
created: 2026-10-15
---

# RFC 70. Authorization model findings: act-as reach, restores, trust rules and a model checker

Status: accepted as ADR 70.

## Problem

Issue #531 showed that a token can give its maker authority that no grant
gave. ADR 67 fixed that case for service principals. The project owner then
asked for a way to model access with formal logic, so that the same kind of
flaw cannot come back.

A study modeled the principals, groups, roles, grants, tokens, trust rules
and restores of ADRs 51 to 57, 60, 64, 65 and 67 to 69. It checked four
general properties. None of them names a role or an issue:

- **P1 accountability.** What a person can do with every credential that it
  holds is what the access review says it can do: its grants, its groups'
  grants, and the authority of each principal that it can act as through a
  declared act-as edge.
- **P2 no amplification.** No change makes anyone able to do, or to act as,
  more than the actor holds.
- **P3 catalog claims.** A role means what its description says, after every
  act-as edge.
- **P4 confinement.** A right acts only on the kinds of object that it was
  meant for.

The checks found #531 again with no hint, and found 14 more paths. Two were
fixed before their pull requests merged (the unit move and the revoke of a
personal token through the service route). Two were settled by the decision
on namespace principals. The others are below, each with its issue.

## Findings that need a decision

| Item | Finding | Issue |
| --- | --- | --- |
| D1 | account-administrator can act as every service principal, so it reaches the operator family, against ADR 65. It can also pass that reach on. | #551 |
| D2 | A personal token made for another person keeps the flaw of #531. | #552 |
| D3 | A service token keeps working after its maker loses the right that allowed it. | #555 |
| D4 | A restore by installation-operator brings back revoked grants and tokens. | #553 |
| D5 | A trust rule can name any principal, with no act-as check. | #554 |
| D6 | Option A of RFC 68 would put sign-in provider settings behind the operator family. An operator could then sign in as anyone. | RFC 68 |
| D7 | Groups from the identity provider skip the membership check, and the access review does not show this path. | #557 |
| D8 | A model checker in a separate repository. | none |

One finding needs no decision: re-enabling a principal has no grant-by-proxy
check (#556). Another adds to #541: the grant-time warning of ADR 67
decision 6 misses grants to a group that a service principal is a member of.

## Proposals

### D1. Who holds `principal:manage-tokens` on the installation

Site-administrator only. Account-administrator keeps principals, groups and
grants, but cannot act as every service principal.

A grant of an act-as right, directly or through a group, needs the reach of
its target: the granter must hold every action that the grants of the target
principal give. This is the rule that the owner set for namespace principals,
applied to the installation.

This changes ADR 67 decisions 2 and 4, and the built-in role catalog.

### D2. Personal tokens for another person

Remove the feature. A person makes only its own personal tokens. An
administrator can revoke them, and cannot make them. GitLab allows the other
way for instance administrators, as impersonation tokens.

This changes ADR 53 decision 10 and ADR 67 decision 5.

### D3. A token ends with the right that allowed it

When the maker of a service token loses the last act-as path to its
principal, the server revokes the token. A team that wants a credential to
outlive a person uses a workload trust rule.

The other option keeps today's behavior as a declared residual: the access
review shows "Tina can act as ci-deploy until 2027-10-14".

### D4. Restores and the access tables

A restore by a role that does not hold every action keeps the current
principals, groups, members, roles, grants and tokens. A restore replaces
them only on an empty installation, or when site-administrator runs it.

### D5. Who may write a trust rule

Writing a rule that names a principal needs `principal:manage-tokens` on that
principal, from a signed-in session. The exchanged token acts only inside the
namespace of the rule.

### D6. Sign-in provider settings

The settings of OIDC, OAuth, LDAP and SCIM can change through the API only
with a separate action that only site-administrator holds. The operator
family never holds it. RFC 68 takes this into its option A.

### D7. Directory groups in the access review

Each identity provider and each SCIM token is a declared root of authority.
The access review lists, for each root, the groups that it can fill.

### D8. A model checker

Create a repository, `OpenDustpan/authz-model`, with three parts:

1. A Datalog gate (Souffle). A Go extractor takes the actions, roles and
   routes from the code. ADR authors keep short design files: what each
   action means, which act-as edges are intended, what each role claims. The
   gate checks P1 to P4 in less than 1 second. It over-approximates, so it
   can report a path that a guard outside the model blocks.
2. A bounded temporal model (Alloy 6) of the transition rules, with checks
   that fail when a property passes with no instance.
3. A generated relation graph (OpenFGA DSL), for people to read and for tests
   side by side with Google Cloud, AWS, Kubernetes and GitLab.

OpenDustpan imports one Go module from it in tests only. The module has no
CGO and uses only the standard library. It holds the action meaning table, a
function that finds who gains authority from a change, and a reference
evaluator for a differential fuzz test of `pkg/access`.

With the gate in `make check`:

- a new action with no declared meaning fails;
- an action that issues credentials with no declared act-as edge fails P1;
- a policy write that does not check the granter fails P2;
- a role that reaches a family that its description excludes fails P3.

The gate cannot prove that a declaration matches the code, or that code
outside the route table calls the authorizer. Safety in general is
undecidable (Harrison, Ruzzo and Ullman, 1976).

Tools in the CI of that repository only, never shipped with OpenDustpan:

| Tool | License |
| --- | --- |
| Souffle 2.5 | UPL-1.0 |
| Alloy 6.2 | MIT; bundles SAT4J under LGPL-2.1 |
| OpenJDK 21 (runs Alloy) | GPL-2.0 with the Classpath Exception |
| OpenFGA CLI 0.7.5 | Apache-2.0 |

If the license rule of the project covers CI tools too, the temporal part can
use Apalache (Apache-2.0) or a small checker in Go.

## Open questions

1. D1 to D7: accept, change or refuse each proposal.
2. D8: create the repository, and accept the CI tools and their licenses.
3. Whether one ADR records all accepted items, or one ADR for each.
