# 287. No contributor license agreement and no copyright assignment

Status: accepted · 2026-10-31. Decided by the project owner

## Context

In 2023 HashiCorp changed the license of Terraform from MPL 2.0 to BUSL 1.1.
That change was possible because one company held the copyright of the
contributed code through a contributor license agreement (CLA). The
contributors gave the company the right to relicense their work.

The project owner wants OpenDustpan itself to be safe from this kind of
change. The users of OpenDustpan keep their state, history, locks and secrets
in it. They need to know that the license of the code cannot change against
them later.

OpenDustpan is under the Apache License 2.0. Section 5 of that license says
that a contribution is under the terms of the license, unless the contributor
says otherwise. The project already asks for a Developer Certificate of Origin
(DCO) sign-off on each commit (`git commit -s`).

On 31 October 2026 the project owner decided how the project accepts
contributions.

## Decision

1. OpenDustpan never asks a contributor to sign a CLA. OpenDustpan never asks
   a contributor to assign copyright. (owner)
2. A contribution comes in under the Apache License 2.0. The inbound license
   is the same as the outbound license (Apache-2.0 section 5). (owner)
3. Each commit carries a DCO sign-off (`git commit -s`). The sign-off is the
   only formal step that a contributor does. (owner)
4. Each contributor keeps the copyright of their contribution. The project
   does not hold the right to relicense contributed code. A change of the
   license of that code needs the agreement of every contributor whose code
   it covers. (owner)
5. Section 3 of the Apache License 2.0 gives each user a patent license from
   each contributor, for the contribution of that contributor. (owner)
6. The hosted service and any closed add-on are separate code. The Apache
   License 2.0 allows them. They are not part of this repository and they do
   not change the license of this repository. (owner)
7. A move of the name and the repository to a neutral foundation is a later
   step. The project has not decided it. It needs its own decision. (owner)
8. Dependency rule: prefer a dependency that needs no CLA from its
   contributors, or that a foundation owns. When a choice is open between two
   dependencies, this rule counts in favor of the one that meets it. A
   dependency that needs a CLA, and that one company owns, needs a note in the
   issue that asks for it. (owner)

## Consequences

- The project can never change the license of contributed code on its own.
  This is the purpose of the decision. It also means that the project cannot
  relicense the code later, even if a future maintainer wants to.
- An enterprise that asks for a corporate CLA gets the DCO sign-off instead.
  The project does not sign a corporate CLA.
- `CONTRIBUTING.md` states the rule for contributors in its section
  "License".
- A dependency review includes the question of who owns the dependency and
  whether it needs a CLA. This rule does not forbid a dependency. It sets a
  preference.
- This ADR is not legal advice. It records a project decision. It does not
  claim legal certainty about the effect of any license.
