# 142. A second-person review of each break-glass by the next business day

Status: accepted · 2026-10-18 · supersedes ADR 70 decision 26 in part (see "Superseded decisions"). Decisions 12 and 15 superseded in part by ADR 149. Route paths and `server:` action names superseded in part by ADR 210

## Context

Two features of OpenDustpan let a person act past a normal control:

- [ADR 103](0103-job-gates-actions-and-audit.md) decision 6: a start of a
  job during an active change freeze, with a break-glass and a reason. The
  audit entry has the kind `override`.
- [ADR 70](0070-act-as-reach-idp-groups-and-break-glass.md) decisions 23 to
  26: a sign-in with a break-glass account. After the session, a second
  administrator reviews the audit entries of the session and records the
  review. ADR 70 sets no deadline for that review.

On 17 October 2026 the project owner decided:

- Each break-glass write gets a mandatory review by a second person by the
  next business day.
- This is an opt-in of the installation.
- Chat reminders and an escalation go out when a review is overdue.
- Reminders and escalations use the business calendar (ADR 140).

An approval before use cannot work in an emergency. A review after use is
the control that remains. ADR 70 already uses this argument for break-glass
accounts.

## Decision

### What needs a review

1. **Two kinds of use.** `breakglass.Use` names one use:
   - `freeze-break`: a job that started through an active freeze. The
     reference is the job ID. The review covers the start and each phase
     of the job.
   - `account-session`: a session of a break-glass account. The reference
     is the session ID. The review covers each action of the session.

   The use holds the scope, the unit of the job, the person, the people
   who cannot review the use, the reason that the person typed, the time
   of the use and, for a session, its time limit. `Use.Validate` refuses
   a use whose scope does not match its kind:
   - a freeze break is in the namespace of its job, never in `@server`,
     has a unit, and has no time limit,
   - a session is in `@server`, has no unit, and has a time limit after
     its sign-in (ADR 70 decision 25).

   The people of a use (`Use.People`) are the person, the named owner of
   the break-glass account (ADR 70 decision 23), and each principal that
   can act as the person (ADR 70), as the server knows them at the time
   of the use. An agent never uses break-glass (ADR 103 decision 7, ADR
   125 decision 15), so a use names no agent. A run of an agent that a
   person starts in a break-glass session acts with the rights of the
   session, so the review of the session covers it.
2. **The opt-in of the installation.** The installation setting
   `breakGlassReview.enabled` is off by default. A site administrator
   changes it with `server:manage-settings`. When it is off:
   - a job through a freeze gets no review,
   - a session of a break-glass account still gets a review, as ADR 70
     requires, with no deadline and no notices.

   When it is on, each use of both kinds gets a review with a deadline and
   notices.
3. **A review opens at the use.** The start of a job with break-glass,
   and the sign-in of a break-glass account, each write the notification
   `break-glass.used` in their transaction (ADR 145). A durable consumer
   of the notification opens the review with `Store.Open`. `Open` is
   idempotent for each use. So each committed use gets one review, also
   after a crash.

   The end of a session writes nothing to the review. A session that ends
   at its time limit, with a sign-out, or with a crash of the server has
   its review from the sign-in. A verdict on a session waits until the
   time limit of the session (`Use.Until`). The server revokes the
   session at that time, so that the review covers each action of the session
   (`ErrInUse`). A sign-out before the time limit does not move it.

### Deadline and notices

4. **The deadline.** `breakglass.Open` sets `DueAt` to the end of the next
   business day after the end of the use, in the calendar of the scope of
   the use (ADR 140 decision 6). The end of a freeze break is the start of
   the job. The end of a session is its time limit. The review records the calendar and the scope that
   gave it.
5. **Notice steps.** The settings have up to 10 steps. Each step has a name,
   a base (the open of the review, the start of the working hours on the due
   day, or the deadline), a delay in business time, and an audience. The
   defaults:

   | Step | When | Audience |
   | --- | --- | --- |
   | `review-needed` | at the open, in business hours | reviewers |
   | `reminder` | at the start of the due day | reviewers, the user |
   | `overdue` | at the deadline | reviewers, the user, the owners of the namespace |
   | `escalation` | 4 business hours after the deadline | administrators, the person on call |

   The last step repeats each business day (8 business hours) until the
   verdict. Every step waits for business hours of the calendar of the
   review, because a review is not an incident. A use at 03:00 on a
   Saturday sends its first notice on Monday at 09:00.
6. **Missed steps join.** `breakglass.Advance` sends one notice for all the
   steps that came due since the last run, with their audiences joined. A
   server that was down for two days sends one escalation, not four
   notices.
7. **Audiences.** The notification model resolves each audience to
   people:
   - reviewers: each principal with the review action on the scope,
     except the people of the use,
   - the user: the person who used break-glass,
   - owners: the owners of the namespace, from its context (ADR 160),
   - on call: the person on call for the namespace, from its context and
     the on-call schedule (ADR 143),
   - administrators: the principals with `server:manage-settings`.

   A notice is the notification `break-glass.review-notice` with these
   recipients (ADR 145). A chat sink shows the button "Open" (ADR 146). No
   chat button records a verdict or stops a notice. Only a verdict stops
   the notices.

### The verdict

8. **States.** A review starts `pending` and ends `accepted` or `disputed`.
   A verdict is final. A pending review after its deadline is overdue. The
   read API answers `overdue: true`, and a list filter selects overdue
   reviews.
9. **Who can review.** `breakglass.CheckReviewer` holds the rule, and a
   table-driven test covers it:
   - the reviewer holds `namespace:review-break-glass` on the namespace of
     a freeze break, or `server:review-break-glass` on `@server` for a
     session,
   - the reviewer has a subject: `ErrInvalid`,
   - no agent acts: `ErrAgentDenied`,
   - the request comes from a console session, never from an access
     token: `ErrNeedsSession`. This is the rule of the other gates that
     need a fresh authentication (ADR 123 decision 10, ADR 125 decision 5).
     So an agent that holds the token of its person cannot record a
     verdict,
   - the reviewer is not one of the people of the use: the person, the
     owner of the break-glass account, or a principal that can act as the
     person (the subjects compare with no case): `ErrSelfReview`,
   - the reviewer did not hold an act-as edge to the person at a time
     from the start to the end of the use (the time limit of a session,
     the verdict for a freeze break): `ErrSelfReview`. The server takes
     these principals from the grant history and gives them in
     `Reviewer.ActAs`, because a principal that gets an edge during the
     use is not in `Use.People`. For example, a break-glass account can
     grant act-as to a colleague during its session,
   - a verdict on a session before its time limit: `ErrInUse`,
   - the request has a fresh authentication, at most 15 minutes old, of the
     kind of ADR 103 decision 2 (MFA step or SSO with a maximum age):
     `ErrAuthStale`.

   The server fills the session, the kind of the authentication and its
   time from the console session of the request, never from the body of
   the request. `Store.Record` checks the verdict again against the stored
   review with `breakglass.CheckVerdict`: the state is a verdict, the
   review names a reviewer who is not one of the people of the stored use
   or of `Verdict.ActAs`, the time of the verdict is not before the use or
   the opening of the review, the authentication was fresh at the time of
   the verdict, and a dispute has a note. The store also refuses a verdict
   whose time is more than one minute after the time of its transaction.
   The store cannot check the console session, the access token or the
   agent of the request, and it cannot check `ActAs` against the grant
   history: `CheckReviewer` checks them in the server before the write.
   `Store.Open` refuses a review that is not new: a state other than
   `pending`, a verdict, a notice step sent, or times that do not agree.

   A review replaces the approval of a manager before use. A fresh
   authentication makes sure that the second person is present.
10. **Accept or dispute.** `breakglass.Accept` records that the reviewer
    found the use justified. A note is optional. `breakglass.Dispute`
    records the opposite, and needs a note. A dispute sends the
    notification `break-glass.reviewed` with the severity `warning` to the administrators
    and the owners. The follow-up of a dispute is outside the review.
11. **Visibility.** The admin access report of ADR 70 lists each pending
    and overdue review. The gauge `dustpan_break_glass_reviews_overdue`
    gives the count of overdue reviews, with no label (ADR 32 decision 5).

### API, actions and audit

12. **Routes.**

    | Route | Action | Object |
    | --- | --- | --- |
    | `GET /api/namespaces/{ns}/break-glass-reviews[/{id}]` | `namespace:read-audit` | namespace |
    | `POST /api/namespaces/{ns}/break-glass-reviews/{id}/verdict` | `namespace:review-break-glass` | namespace |
    | `GET /api/break-glass-reviews[/{id}]` (sessions) | `server:read-audit` | server |
    | `POST /api/break-glass-reviews/{id}/verdict` (sessions) | `server:review-break-glass` | server |
    | `GET`, `PUT /api/settings/break-glass-review` | `server:observe`, `server:manage-settings` | server |

    A review shows the reason of the use and the audit entries that it
    covers, so the read needs the read action of the audit log.
13. **Audit entries.** In the scope of the use:

    | Change | Subject | Action | Kind |
    | --- | --- | --- | --- |
    | Review opened | the server | `namespace:review-break-glass` or `server:review-break-glass` | `change` |
    | Verdict | the reviewer | `namespace:review-break-glass` or `server:review-break-glass` | `change` |
    | Settings changed | the administrator | `server:manage-settings` | `change` |

    The entry of a verdict holds the review ID, the verdict, the note, the
    kind of the fresh authentication and its time. The entry of the open
    names the action of the review, not `namespace:break-freeze`: the
    entry of the start of the job already records the break-glass with the
    kind `override`. The audit log has no kind for an action of the server,
    so the open has the kind `change`. A notice writes no audit entry: it
    is not an action of a person. The delivery history of the notification
    model records it (ADR 145).
14. **Each write carries the fence of its scope.** Each write of
    `breakglass.Store` carries the fence of the namespace of its review,
    and the store checks it in the transaction of the write. A review of
    a freeze break carries the fence of the namespace of the job. A review
    of a session is in `@server` and carries a zero fence. The store
    refuses a zero fence for a namespace, a fence for `@server`, and the
    fence of another namespace: `Open` with `ErrInvalid`, `Record` and
    `Advance` with `ErrNotFound`, so that a write does not tell that a
    review exists in another namespace.
15. **Ports with conformance first.** `breakglass.Store` gets the suite
    `pkg/breakglass/breakglasstest`, with a reference adapter in memory.
    The suite covers a zero fence, the fence of another namespace and a
    stale fence on each write, and a verdict that breaks a rule of
    decision 9.

## Superseded decisions

| ADR | Decision | What changes |
| --- | --- | --- |
| 70 | 26, the review after use | The review is a record of this ADR, with the rules of decision 9. These rules ask for no agent, a console session, a fresh authentication, and a reviewer from outside the people of the use. It opens at the sign-in, not after the session (decision 3). With the opt-in on, it has a deadline and notices. The record replaces the mark "used, not reviewed". Rotation and the regular test of ADR 70 stay. |
| 70 | 13, the sensitive actions | `server:review-break-glass` is a sensitive action too. The identity provider cannot add a member to a group whose reach holds it. |

## Consequences

- Two new actions: `namespace:review-break-glass` and
  `server:review-break-glass`. The built-in role `admin` gets the first.
  The roles `site-administrator` and `account-administrator` get the
  second.
  - `namespace:review-break-glass` is a data action, because a role on a
    namespace holds only data actions.
  - `server:review-break-glass` is an administration action and a
    sensitive action (ADR 70 decision 13).
  - `namespace:break-freeze` and the two review actions do not imply each
    other. No action gives another of them. A principal that holds both
    still cannot review its own use, because decision 9 refuses the people
    of the use.
- A small team with one administrator cannot review a break-glass session
  of that administrator. ADR 70 already asks for two break-glass accounts
  with two people. The review needs a second person too.
- Every review costs one durable notice run for each step. A review that
  gets its verdict before the first step sends nothing.
- The verdict of a session waits for the time limit of the session, also
  after an early sign-out. The time limit of an elevated session is short
  (ADR 70 decision 25), so the wait is short too.
- `GLOSSARY.md` needs the terms Break-glass review, Verdict and Overdue.

## Open questions

- Do the overrides of ADR 44 (forced state write, write with no lock,
  unfenced force-unlock) need a review too? They have the audit kind
  `override`, as a freeze break has. This ADR covers only the two kinds of
  decision 1.
- Should a dispute open an incident (ADR 147) by itself?
- Does a queued job that later gets break-glass (an open question of ADR
  103) open its review at the break-glass or at the dispatch?
