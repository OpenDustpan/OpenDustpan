---
rfc: 77
title: Open points of the gate model
status: discussing
created: 2026-10-18
---

# RFC 77. Open points of the gate model

Status: discussing.

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

## 7. Approval assignment

Decided: the bipartite matching of ADR 192 decision 6 holds for every
approval (ADR 225 decision 6). This point is closed.

## Outcome

Empty until the owner decides each point.
