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

One per-action step-up configuration that covers break-glass and the reset
of a factor together. The owner said on 15 November 2026: "If we build this
for break-glass we might as well fold it in for factor reset too." The
default for both is `required` (on). The master switch `freshAuthEnabled`
stays and gives the default of the other dangerous actions (option 2 or
option 3 above). The choice between option 2 and option 3 is open.

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

## Owner position of 15 November 2026

One per-action step-up configuration covers break-glass, the reset of a
factor and the issue of an enrollment code, default on for each. The issue of
an enrollment code is the administrator side of the reset of a factor.

An administrator reset of the factors of a person also deletes the recovery
codes of the person, removes the passkeys and ends the sessions of the
person. The person gets new recovery codes when the person enrolls again.

## What main has (15 November 2026)

- Recovery codes and enrollment codes are separate. The recovery codes of a
  person are a set of `RecoveryCodeCount` (10) digests in `RecoveryCodeStore`
  (`pkg/mfa/mfa.go:241-254`). The set changes only when the person makes a new
  set (`ReplaceRecoveryCodes`) or uses one code (`UseRecoveryCode`). The rule
  "at most one open code" belongs to the enrollment code (ADR 301 decision
  14). No code of main issues an enrollment code today. The OpenSpec change
  `app-level-second-factor-totp` has the requirement `issue-enrollment-code`
  (`openspec/changes/app-level-second-factor-totp/specs/second-factor/spec.md`).
  It does not say that an enrollment code leaves the recovery codes alone.
- The administrator removal of all factors has a store port and no route.
  `ResetStore.ResetFactors` (`internal/stepup/stepup.go:619-630`) deletes the
  step-up keys and the active and pending TOTP factor, revokes the step-up
  credentials, voids open challenges and appends one audit entry, in one
  transaction. No service, route or action calls it outside tests. The reset
  of the password (`internal/api/server/localusers.go:14`, ADR 285 decision
  17) leaves factors and recovery codes in place. The text of ADR 46 decision
  9 and ADR 372 decision 6 names the reset, and no code or route gives it.
- A person with no factor can be asked to enroll. `ErrEnrollmentRequired`
  (`pkg/mfa/mfa.go:102-105`) and the enroll-only session
  (`pkg/session/cookie.go:136`, `pkg/mfa/mfa.go:283`) exist, with a grace
  setting (`EnrollmentGraceDays`, `pkg/mfa/mfa.go:350-353`).

- The MFA policy has one level on main: the installation. `Policy`
  (`pkg/mfa/mfa.go:339-358`) has `RequireForAll` and `EnrollmentGraceDays`.
  No namespace level exists in code. Whether a namespace may also require MFA
  is open in RFC 68 (MFA freshness: "installation, a namespace, or both").
  The enroll-only session after a factor reset follows the installation
  policy only. If a namespace level is added, the reset must follow the
  stronger of the two.

## Smallest spec change for the owner to decide

1. In the change `app-level-second-factor-totp`, add to `issue-enrollment-code`:
   issuing or using an enrollment code does not delete, replace or count
   against the recovery codes of the person. Add a test for it.
2. Add a requirement for the administrator reset of the factors of a person:
   a REST route under the principal, a stored grant (one action, named by the
   ADR), a reason, a reach check, one audit entry, the end of the sessions of
   the person, and the removal of every kind of factor (keys, TOTP, passkeys
   and the recovery codes, as the owner decided). At the next sign-in the person gets an enroll-only
   session when the installation requires MFA. This uses `ResetFactors` and
   the enroll-only session that exist. The owner decided that the
   recovery codes go with the reset.

## Checklist

- [ ] Tolerates asynchronous replication between installations: each namespace writes only its own half, under its own fence, and no write waits for another installation (ADR 248 decisions 3, 6 and 9).

## Outcome

Empty until the RFC is accepted (link the ADR) or dropped (say why).
