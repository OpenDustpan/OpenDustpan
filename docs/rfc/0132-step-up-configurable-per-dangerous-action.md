---
rfc: 132
title: Step-up configurable per dangerous action
status: draft
created: 2026-11-15
---

# RFC 132. Step-up configurable per dangerous action

Status: draft. The project owner asked for this question on 15 November 2026
while the owner accepted [ADR 385](../adr/0385-a-fresh-step-is-opt-in.md). It
changes no decision. ADR 385 keeps the proof of a break-glass pass and the
reset of a factor as they are until the owner decides.

## Problem

ADR 385 gives one installation setting, `freshAuthEnabled`. When it is off, the
server asks a person for no fresh authentication at the steps of ADR 227
decision 2. When it is on, each of those steps asks for one. The setting is
all or nothing.

The owner sees two gaps:

1. Break-glass may be a normal dangerous action. The proof of a break-glass
   pass (ADR 373 decision 1, ADR 142) has its own rule today and does not
   depend on the setting. The owner thinks it may belong with the other
   dangerous actions.
2. The reset of a factor (ADR 301 decision 8, ADR 285 decision 17) stays
   required whatever the setting says. Many tools let a signed-in person change
   the MFA settings of their own account. GitLab is one example. A later
   decision may make the rule configurable, as break-glass may become.

An operator who wants a step for a `destroy` but none for a token creation has
no way to say so.

## Options

1. **Do nothing.** One setting, as in ADR 385. The proof of a break-glass pass
   and the reset of a factor keep their own rules. Cost: no per-action choice.
2. **A setting for each dangerous action.** Each action in the list of ADR 227
   decision 2, the proof of a break-glass pass and the reset of a factor get a
   setting. A setting has the values `required` and `not required`. The master
   switch `freshAuthEnabled` stays and gives the default of each. Cost: more
   settings to show, to audit and to test. Gain: an operator picks the
   actions.
3. **A few groups.** The actions form groups, for example: approvals, token and
   secret changes, settings changes, sign-in system. One setting for each
   group. Cost: the groups are a new thing to learn. Gain: fewer settings than
   option 2.

## Proposal

No preference yet. The default of each setting stays the safe value. The
default for break-glass and for the reset of a factor is `required` (on),
as the owner said.

## Open questions

- Is the proof of a break-glass pass a normal dangerous action, or does it keep
  its own rule because it protects the sign-in system?
- May an operator turn off the step for the reset of a factor? What does the
  server do when the person has lost every factor? ADR 372 decision 6 gives the
  recovery path.
- Does a weaker setting for one action need a fresh step while the action is
  required (ADR 385 decision 4)?
- Where does the settings API show the effective value of each action, and
  where does it come from (ADR 212 decision 9)?
- Which actions never become configurable? The first-administrator code (ADR
  339 decision 6) is not a step-up (ADR 385 decision 5).

## Checklist

- [ ] Tolerates asynchronous replication between installations: each namespace writes only its own half, under its own fence, and no write waits for another installation (ADR 248 decisions 3, 6 and 9).

## Outcome

Empty until the RFC is accepted (link the ADR) or dropped (say why).
