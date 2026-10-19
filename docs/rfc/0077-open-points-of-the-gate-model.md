---
rfc: 77
title: Open points of the gate model
status: accepted
created: 2026-10-18
---

# RFC 77. Open points of the gate model

Status: accepted. The owner decided each point on 2026-10-18. ADRs 222 to
230 record the decisions.

## Problem

ADRs 222 to 230 record one model for access, gates, approval rules and
facts. The spike on `spike/policy-model` left points that the ADRs do not
decide. Each point below has options and a recommendation. Version 1 of the
ADRs takes the fail-closed choice where one exists.

## 1. Does an ownership rule add a requirement with no gate?

In version 1, an ownership rule applies only when a gate with the outcome
`approval:owners` matches (ADR 225 decision 9). A rule with no such gate
does nothing, and nothing tells the person who saved it.

- **A. Keep version 1.** One way in for each requirement. A forgotten gate
  silently turns all ownership rules off.
- **B. An ownership rule is its own source of requirements.** A rule applies
  when its `paths` and its `when` match. A gate can still name
  `approval:owners`, with no other effect. Two sources (gates and ownership
  rules) plus approval defaults.
- **C. The save of the first ownership rule makes the gate.** Hidden state.

**Recommendation: B.** The research found two sources of requirements in
each product it read (rules on the change and owners of the code). A rule
that does nothing when saved is a trap. The `when` of the rule already
gives the condition, so the gate adds nothing.

## 2. Who may approve a plain approval default?

An approval default is one requirement named `default` (ADR 225 decision
4). The spike let any person other than the author meet it.

- **A. Any holder of the approve action of the object, not the author.** As
  `MinApprovals` did (ADR 192).
- **B. The owners of the unit in its context (ADR 161), else A.**
- **C. A default names its approvers.** The default becomes an approval rule
  with a count, and A is the value when it names none.

**Recommendation: C, with A as the value when no approvers are named.** It
uses the same data as every approval rule, and keeps the default of today
for a team that sets only a count.

## 3. What does `final` mean?

ADR 224 decision 12 decides that a narrower scope never changes a wider
gate, and that `final` stops a gate with the same ID at a narrower scope.
Gate IDs are unique in the installation, so the second part has no effect
today.

- **A. Keep it.** `final` is a guard for a later change of the ID rules.
- **B. `final` stops a switch to `evaluate` mode at the gate's own scope.** A
  final gate in `enforce` mode can only be deleted, with a fresh
  authentication and an audit entry.
- **C. A change of a final gate needs an approval under an approval rule of
  the scope above.**
- **D. Drop the flag.**

**Recommendation: no change to "a narrower scope never sets a wider gate to
`evaluate`", and B for `final`.** A switch to `evaluate` turns off a control
with no trace in the change that it lets pass. A delete is visible.

## 4. A break-glass when a wide gate errors

A gate at the installation scope that errors stops each change below it
(ADR 224 decision 13). The fix is a save by an administrator of that
scope.

- **A. No break-glass.** The administrator switches the gate to `evaluate`
  or fixes it. A namespace waits for that person.
- **B. A break-glass that passes only gates whose result is `error`.** It
  needs a reason, a person who is not an agent, a fresh authentication and a
  new action `namespace:break-gate-error`. The audit entry has the kind
  `override`, and the break-glass review of ADR 142 holds for it. It never
  passes a `deny` or a requirement.
- **C. A break-glass that passes every gate.**

**Recommendation: B.** A freeze has a break-glass (ADR 103 decision 6). An
outage of the gate store or a bad gate needs the same path, with the same
review. C turns a gate into a suggestion.

## 5. Gates on calls of the tool-compatible APIs

A CLI-run update of the Pulumi CLI and a state write of OpenTofu meet no
gate in version 1 (ADR 224 decision 16). A team that runs `pulumi up` on a
laptop passes every gate.

- **A. Keep version 1.** Gates cover the work that OpenDustpan starts.
- **B. `change.propose` at the create of a Pulumi update.** The Pulumi CLI
  shows the error message. An approval outcome refuses the update and names
  the command that opens a change order.
- **C. An access limit for direct updates.** A limit can already forbid
  the update action for `principal.credential == "session"` in a scope.

**Recommendation: C now, B after the Pulumi slice.** C needs no new code.
B needs an e2e step against the real CLI first.

## 6. Path facts for gates

Cedar has no quantifier, so a gate cannot say "every changed path is under
`modules/`". Ownership rules match paths in Go.

- **A. Ownership rules only.**
- **B. A computed fact `change.pathPrefixes`**, the set of the first two
  segments of each changed path.
- **C. Named path sets on a scope**, with a fact `change.pathSets` that
  names each set that holds a changed path.

**Recommendation: C.** It keeps globs in Go and gives Cedar a set of names.

## 7. One fresh-authentication window

ADR 227 gives one window, `freshAuthSeconds`, for each fresh step.

- **A. One window, 60 to 900 seconds, or 0 to ask every time.**
- **B. One window for each kind of step.**

**Recommendation: A.**

## 8. The word and the wire value of the approval wait

The job state value is `awaiting-approval`. The glossary word is "approval
wait".

- **A. Keep the wire value, and use the word "approval wait" in text.**
- **B. Rename the wire value to `approval-wait`.**

**Recommendation: A.** A rename breaks clients for no change of meaning.

## 9. The names of the fixed agent controls

The confirmation gate and the secret gate are fixed controls, but their
names hold "gate", which now names the stored rule.

- **A. Keep the names.**
- **B. Rename them to "confirmation step" and "secret consent".** Old ADRs
  keep their text (ADR 1).

**Recommendation: B.**

## 10. Quota counts

A quota reservation is a fixed control (ADR 226 decision 11).

- **A. Quota counts stay fixed controls.** A gate reads the `quota` fact.
- **B. Quota counts become gates.**

**Recommendation: A.** A reservation needs a transaction that a gate does
not have.

## 11. Built-in gates

`builtin/plan-readable` and `builtin/mandatory-resource-policy` keep the
checks of ADR 194, which held for change orders only.

- **A. Enforce by default, for each change with a plan phase.**
- **B. Evaluate by default.**

**Recommendation: A.** It keeps the behavior of ADR 194 and gives it to
each job with a plan phase.

## 12. Signatures and a new plan

ADR 191 decision 6 voids each signature on a new revision, also when the
plan digest inputs are the same. The first version of ADR 225 had a field
`staleOnNewPlan` on each approval rule.

- **A. Always void.**
- **B. `staleOnNewPlan` on each approval rule.**
- **C. An opt-in setting on a namespace, a collection or a unit** that lets
  a signature carry over to a revision with identical plan digest inputs.

**Recommendation: C.**

## 13. Approval assignment

Decided before this RFC: the bipartite matching of ADR 192 decision 6 holds
for every approval (ADR 225 decision 6).

## Outcome

The owner decided each point on 2026-10-18.

1. **B.** An ownership rule adds an approval requirement by itself, with no
   gate. An ownership rule has a `mode` (`enforce` or `evaluate`), and a new
   rule starts in `evaluate` mode (ADR 225 decisions 2 and 9).
2. **A.** A default that names no approvers can be met by any holder of the
   approve action who is not the author. A default can also name its
   approvers, and is then its own requirement (ADR 225 decision 4).
3. **Not B.** A narrower scope can never weaken a final gate. The owner of
   the gate's own scope can switch it to `evaluate`, as a change-controlled
   edit with a reason, a fresh authentication and an audit entry. `final`
   stops a narrower scope from saving a gate with its ID or an approval rule
   with the ID that it names, and from a break-glass of it.
   `gate.ErrFinal` has four exact triggers (ADR 224 decision 12).
4. **B, with `onError`.** An erroring gate fails closed by default. The gate
   author can set `onError: skip` in advance. A break-glass passes only gates
   in error, also `gate.ErrUnavailable`, never a deny or a requirement, with
   a reason, a fresh authentication, an `override` audit entry and the review
   of ADR 142. The change still meets `change.apply` (ADR 224 decision 13).
5. **B, wider.** Version 1 has the decision point `state.write` for Pulumi
   updates from a person's own CLI, for OpenTofu writes from their own CLI
   or CI, and for the tool-neutral state operations (ADR 224 decision 16).
   The credential kinds are `session`, `access-token`, `job-token` and
   `trust-token` (ADR 226 decision 4).
6. **C.** Named path sets, with the facts `paths.touches["<set>"]` and
   `paths.only["<set>"]` (ADR 226 decision 13).
7. **A** (ADR 227 decision 3).
8. **A** (ADR 222 decision 8, ADR 225 decision 8).
9. **B** (ADR 222 decision 8, `GLOSSARY.md`).
10. **A** (ADR 222 decision 4).
11. **A.** The scope widens from change orders to each job with a plan
    phase (ADR 226 decision 7).
12. **C.** Off by default, and a parent level can forbid it. A changed
    policy, a changed approver set, an expired approval or a revoked signer
    never carries over. This amends ADR 191 decision 6 (ADR 225 decision
    12).
13. Closed before this RFC.
