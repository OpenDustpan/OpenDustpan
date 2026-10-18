# 142. A second-person review of each break-glass by the next business day

Status: accepted · 2026-10-18 · supersedes ADR 70 decision 26 in part (see "Superseded decisions")

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

   The use holds the scope (the namespace of the job, or `@server` for a
   session), the unit of the job, the person, the reason that the person
   typed, and the time. An agent never uses break-glass (ADR 103 decision
   7), so a use names no agent.
2. **The opt-in of the installation.** The installation setting
   `breakGlassReview.enabled` is off by default. A site administrator
   changes it with `server:manage-settings`. When it is off:
   - a job through a freeze gets no review,
   - a session of a break-glass account still gets a review, as ADR 70
     requires, with no deadline and no notices.

   When it is on, each use of both kinds gets a review with a deadline and
   notices.
3. **A review always opens.** The start of a job with break-glass writes
   the event `break-glass.used` in its transaction (ADR 145). The end of a
   break-glass session writes the same event. A durable consumer of the
   event opens the review with `Store.Open`. `Open` is idempotent for each
   use. So each committed use gets one review, also after a crash.

### Deadline and notices

4. **The deadline.** `breakglass.Open` sets `DueAt` to the end of the next
   business day after the use, in the calendar of the scope of the use
   (ADR 140 decision 6). The review records the calendar and the scope that
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
7. **Audiences.** The event model resolves each audience to people:
   - reviewers: each principal with the review action on the scope,
     except the user,
   - the user: the person who used break-glass,
   - owners: the owners of the namespace, from its context (ADR 160),
   - on call: the person on call for the namespace, from its context and
     the on-call schedule (ADR 143),
   - administrators: the principals with `server:manage-settings`.

   A notice is the event `break-glass.review-notice` with these recipients
   (ADR 145). A chat sink shows the buttons "Open" and "Acknowledge" (ADR
   146). No chat button records a verdict.

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
   - the reviewer is not the user (the subject compares with no case),
   - no agent acts: `ErrAgentDenied`,
   - the request has a fresh authentication, at most 15 minutes old, of the
     kind of ADR 103 decision 2 (MFA step or SSO with a maximum age):
     `ErrAuthStale`.

   A review replaces the approval of a manager before use. A fresh
   authentication makes sure that the second person is present.
10. **Accept or dispute.** `breakglass.Accept` records that the reviewer
    found the use justified. A note is optional. `breakglass.Dispute`
    records the opposite, and needs a note. A dispute sends the event
    `break-glass.reviewed` with the severity `warning` to the administrators
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

    | Event | Subject | Action | Kind |
    | --- | --- | --- | --- |
    | Review opened | the server | `namespace:break-freeze` or `server:review-break-glass` | `change` |
    | Verdict | the reviewer | `namespace:review-break-glass` or `server:review-break-glass` | `change` |
    | Settings changed | the administrator | `server:manage-settings` | `change` |

    The entry of a verdict holds the review ID, the verdict, the note, the
    kind of the fresh authentication and its time. A notice writes no audit
    entry: it is not an action of a person. The delivery history of the
    event model records it (ADR 145).
14. **Ports with conformance first.** `breakglass.Store` gets the suite
    `pkg/breakglass/breakglasstest`, with a reference adapter in memory.

## Superseded decisions

| ADR | Decision | What changes |
| --- | --- | --- |
| 70 | 26, the review after use | The review is a record of this ADR, with the rules of decision 9: no agent, and a fresh authentication. With the opt-in on, it has a deadline and notices. The record replaces the mark "used, not reviewed". Rotation and the regular test of ADR 70 stay. |

## Consequences

- Two new actions: `namespace:review-break-glass` and
  `server:review-break-glass`. The built-in role `admin` gets the first.
  The roles `site-administrator` and `account-administrator` get the
  second.
- A small team with one administrator cannot review a break-glass session
  of that administrator. ADR 70 already asks for two break-glass accounts
  with two people. The review needs a second person too.
- Every review costs one durable notice run for each step. A review that
  gets its verdict before the first step sends nothing.
- `GLOSSARY.md` needs the terms Break-glass review, Verdict and Overdue.

## Open questions

- Do the overrides of ADR 44 (forced state write, write with no lock,
  unfenced force-unlock) need a review too? They have the audit kind
  `override`, as a freeze break has. This ADR covers only the two kinds of
  decision 1.
- Should a dispute open an incident (ADR 147) by itself?
- Does a queued job that later gets break-glass (an open question of ADR
  103) open its review at the break-glass or at the dispatch?
