# 369. The actor of a job: trust rules, conditions on token claims, and external service principals

Status: accepted · 2026-11-14. Decided by the project owner. Accepts point 4 of RFC 129

Changes:

- Changes ADR 54 decision 6: a trust rule can opt in to the person who started the event. Four kinds of actor.
- Extends ADR 67 decisions 1 and 6: a service principal from an external identity system, keyed by its external ID.
- Extends ADR 302 decision 6: the answer to one principal for each job on the TFE-compatible path.

## Context

ADR 302 decision 6 (owner) lists "One principal for each job on the
TFE-compatible path" as a point of the RFC for concurrent applies.
[RFC 129](../rfc/0129-concurrent-applies-on-one-state.md) point 4 gives the
options. ADR 368 accepts the other points of RFC 129 and leaves point 4
open.

The text that this ADR changes or extends:

- ADR 54 decision 6: "The principal is not a person. A matched workload acts
  as a principal that is not a person, like the owner of a service token
  (ADR 53)." ADR 54 is changed by ADR 69, ADR 70 (decisions 5 and 8), ADR
  204, ADR 222, ADR 223 and ADR 284. None of them changes decision 6.
- ADR 67 decision 1: "Making a service principal of the installation is an
  administration action." Decision 6: "The access to a service principal is
  visible." ADR 67 is changed by ADR 70 and ADR 215.
- ADR 100 decision 20 gives each runner phase a job token with the job-ID
  claim. ADR 144 and ADR 213 change decision 20.

On 14 November 2026 the project owner approved two sets of answers. At about
13:15 UTC the owner approved the credential mechanics of milestone 8: "a
TFE-compatible request names its claim through the job token", and "the
local wrapper gets a launcher token bound to the claim". Later the same day
the owner approved point 4 of RFC 129 with the decisions below.

A check of main on 14 November 2026 found:

- A trust rule matches any string claim of a token exactly, so a rule can
  already match an event-source claim
  (`pkg/trustrule/trustrule.go:143`, `Condition`, and
  `pkg/trustrule/jwt.go:508`, `Rule.Matches`). A rule names one principal
  (`pkg/trustrule/trustrule.go:155`, `Rule.PrincipalID`). No rule maps a
  token to the person who started the event, and no check stops a token of
  an automated source from acting as a person.
- The access limits of a state write run on every state write, also with
  no job (`pkg/gate/statewrite.go:65`, `Service.GuardStateWrite`). Their
  facts hold the kind and the credential of the principal, the age of the
  authentication and the unit (`pkg/gate/facts.go:487`, `AccessFacts`). They
  hold no claim of the token: no ref, no protected flag and no event
  source. The facts of the source group hold a ref and a protected flag for
  a job (`pkg/gate/facts.go:328`, `SourceFacts`), not for a token.
- A principal record has a kind (`person`, `service`, `workload`) and a
  source (`local`, `ldap`, `scim`, `oidc`, `oauth`)
  (`pkg/principal/principal.go:158`, `Principal`). Only the API makes a
  principal of kind service, with the source `local`
  (`pkg/principal/namespace.go:47`). No field holds an external ID apart
  from the subject, no rule marks an external account as a service account,
  and the record has no owner.

## Decision

1. **The identity is always the actual actor (owner, 2026-11-14).** An
   explicit, stored trust rule resolves the actor. A job is not an identity.
   A job acts through a short-lived credential that is bound to the job and
   to its apply claim. The credential carries the verified claims of the
   token that started it. A TFE-compatible request names its apply claim
   through the job token. The local wrapper gets a launcher token that is
   bound to its apply claim.
2. **Three layers (owner, 2026-11-14).**
   - Identity: the trust rules (ADR 54) and the mapping of the directory.
   - Grants: stored grants (ADR 52).
   - Conditions: the access limits and the gates read the verified claims of
     the token: `ref`, `ref_protected` and the event source.

   A branch rule, for example "a plan from any branch, an apply only from
   the protected default branch", is a condition. It is never a second
   identity.
3. **Four kinds of actor (owner, 2026-11-14).**
   1. **Person.** A trust rule can opt in to the person. It applies only to
      an event that a person started: a push, a start in the web interface
      of the Git host, a merge request. The local wrapper always acts as the
      person who signed in.
   2. **Workload.** The default for each automated source: a schedule, an
      API trigger, a pipeline that another pipeline started, and a token
      with no user. A schedule that a person owns never takes the identity
      or the rights of that person. This is a hard check in code. It holds
      also when a trust rule is configured wrong.
   3. **External service principal.** A non-human account of the identity
      system of the organization, for example a service account of GitLab or
      a system account in LDAP.
      - Its kind is `service`. It is keyed by its stable external ID (for
        example the GitLab user ID, or the LDAP `entryUUID` or
        `objectGUID`), never by its name (ADR 362 decision 1).
      - Its life follows the source. Each exchange checks that the account
        is still active in the source.
      - It has stored grants and a named owner (ADR 67).
      - It never gets a console session, MFA or break-glass.
      - An administrator marks an account as a service account with an
        explicit rule: a condition on a claim, an LDAP group or OU, or a
        glob on the external username (decision 4). The rule is stored and
        audited. The owner named the action `server:manage` (see "Not
        decided here").
   4. **The OpenDustpan service principal.** The schedules of OpenDustpan
      (drift checks, run triggers, remediation) act as the namespace service
      principal with its stored grant (ADR 338). The person who configured
      the schedule is recorded as the configurer, not as the actor.
4. **A glob on the external username classifies only (owner, 2026-11-14).**
   An organization with no LDAP group or OU to key on can mark service
   accounts with a glob on the external username, for example `system-*` or
   `bot_*`.
   - A glob only classifies. It never grants. A match makes the principal
     of kind `service`, with no console session, no MFA and no
     break-glass. The principal stays keyed by its stable external ID.
   - The principal record holds the classification and the rule that made
     it. The API and the access review show both.
   - The classification fails toward restriction. A rename that leaves the
     pattern does not make a service principal a person again. Only an administrator does that, with the action of the rules and an
     audit entry. A
     rename into the pattern makes the principal a service principal, with
     an audit entry.
   - A glob rule belongs to the installation or to one namespace, as the
     other classification rules do. It is a stored rule, with an audit entry
     and the action of the rules.
   - The match compares exact bytes: no case folding and no Unicode
     normalization. The docs say so.
5. **An agent, an approval and a retry (owner, 2026-11-14).**
   - An agent acts for a person with the intersection of the rights of both.
   - An approval does not lend an identity.
   - A retry keeps the actor and gets a new credential. It stops when the
     actor was revoked.
   - The audit entry names the actor, the job, the apply claim, the trigger
     and the principal that the actor acts for.

## What main lacks

These are notes from the check of main. Each one is a NEW item of the
changes that need it.

- **Event source in the trust rules.** A rule can match an event-source
  claim, but no rule maps a token to the person who started the event, and
  no hard check keeps an automated source away from a person (decision 3,
  items 1 and 2).
- **Token claims in the access limits.** The access facts of a plain state
  write do not hold `ref`, `ref_protected` or the event source (decision 2).
- **External service principals.** No external ID apart from the subject, no
  classification rule, no record of the rule that classified a principal, and
  no owner on the record (decisions 3 and 4).

## Not decided here

- **The action that creates a trust rule in person mode.** ADR 70 decision 9
  says: "A trust rule names only a principal that its author can act as." A
  rule in person mode names no single principal. The action for it is open.
  It needs a decision of the owner, because ADR 70 records decisions of the
  owner.
- **The read check of a trigger.** ADR 267 decision 14 says that the reads
  of a job that a trigger starts use "the current grants of the trigger's
  last saver". ADR 338 decision 3 (owner) keeps this rule. Decision 3 item 4
  of this ADR records the person who configured a schedule as the
  configurer, not as the actor. This ADR does not change the read check of
  ADR 267 decision 14. The owner decides if that check takes the rights of
  the saver in the sense of decision 3 item 2.
- **The name of the action of the classification rules.** The owner wrote
  `server:manage`. The action list has no action of that name. The change
  proposes `server:manage-principals` for a rule of the installation and
  `namespace:manage-principals` for a rule of a namespace (ADR 67 decisions
  1 and 7).
- **A glob that matches every name.** The changes refuse a glob of `*`
  alone, as a proposal. The owner can choose a warning in place of a
  refusal.

## Consequences

- RFC 129 point 4 is decided. The changes of milestone 8 that waited only on
  point 4 cite this ADR.
- The OpenSpec change `external-service-principal` covers decisions 3 and 4,
  with the gaps of "What main lacks".
- The access facts of the gate engine get the verified claims of the token.
  The gate schema version goes up.
