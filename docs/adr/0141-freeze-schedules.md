# 141. Freeze schedules: windows, recurring freezes and calendar freezes

Status: accepted · 2026-10-18 · supersedes decision 9 of ADR 103 in part

## Context

[ADR 103](0103-job-gates-actions-and-audit.md) decisions 4 to 6 make change
freezes opt-in for each namespace. A freeze has a reason, a start and an
optional end. While a freeze is active, no phase that changes resources,
state or code dispatches. A start during a freeze needs a break-glass with a
reason. The job store checks the freeze in the transaction of the dispatch.
`job.Freeze` and `job.FreezeStore` (create, end, list) hold the freezes.
ADR 103 decision 9 gives each create and each end of a freeze an audit
entry with the principal as its subject.

On 17 October 2026 the project owner decided that the job model owns the
freeze gate, and that this design owns the schedule model: windows,
recurring freezes and freezes that follow the business calendar.

A team asks for freezes of these shapes:

- one window, for example the week of a product release,
- every weekend, from Friday 15:00 to Monday 09:00,
- every night, outside working hours,
- every holiday, and the business day before it.

ADR 140 gives the business calendar of a namespace. ADR 72 gives durable
operations and their schedules.

## Decision

1. **A schedule makes freezes.** `freeze.Schedule` belongs to one namespace,
   never to the server scope. It has an ID with the prefix `fzs-`, a name,
   a reason and one rule of three kinds:
   - `window`: one freeze from a start to an end.
   - `weekly`: on some days of the week, at a time of day in a time zone,
     for a length of at most 6 days. The occurrences of one rule must not
     overlap.
   - `calendar`: periods of the calendar of the namespace. Each flag adds
     periods: days that are not working days, holidays, the time outside
     working hours on business days, and the last 1 to 5 business days
     before each holiday.

   A schedule makes occurrences from `From` and until `Until`, when set. A
   schedule that is not enabled makes none. A schedule never moves to
   another namespace: an update that names another namespace fails with
   `ErrInvalid`.
2. **Local clock times.** A weekly rule has a zone of the IANA time zone
   database, for example `Europe/Berlin`, or `UTC`. The zone `Local`
   depends on the host of the server, so validation refuses it. The length
   of a weekly rule is a length on the local clock, in whole minutes.
   Friday 15:00 with 66 hours ends Monday 09:00 local time, also over a
   daylight saving change: then 65 or 67 hours elapse. These rules apply to
   each local time of a weekly or a calendar rule:
   - A local time that occurs two times, when the clock goes back, is the
     first of the two instants.
   - A local time that a change skips, when the clock goes forward, moves
     forward by the length of the gap. 02:30 on a day that goes from 02:00
     to 03:00 is 03:30.
3. **The dispatch check reads stored freezes only.** A durable schedule runs
   `Schedule.Plan` for each schedule of each namespace once an hour
   (`freeze.RunInterval`). It writes the occurrences of the next 14 days
   (`freeze.Horizon`) as freezes of the job model, through the port
   `freeze.Sink`. The dispatch check never computes a recurrence or reads a
   calendar. So the check stays one indexed read in the transaction of the
   dispatch, as ADR 103 decision 4 requires.
4. **Stable IDs.** The freeze of an occurrence has the ID
   `freeze.OccurrenceID(schedule, start)`: `frz-`, the schedule ID with no
   prefix, and the start in Unix milliseconds. `Occurrences` never cuts an
   occurrence at the start or the end of its range. For a calendar rule, it
   reads back and forward over each day that the rule freezes whole, for
   at most `calendar.MaxSearchDays` days in each direction. So a second run
   of `Plan` gives the same ID and the same end for the same occurrence,
   and makes no second freeze. Holidays from 24 December to 1 January, the
   weekend after them and the time before the working hours on Monday
   4 January are one freeze, also for a plan whose 14 days end on
   1 January.
5. **Adjacent periods join.** Periods that touch or overlap become one
   occurrence. A calendar rule with days off and time outside hours gives
   one freeze from Friday 17:00 to Monday 09:00, not three.
6. **The freeze port extends the freeze store of the job model.**
   `freeze.Sink` embeds `job.FreezeStore` and adds two writes. The freezes
   of a schedule are rows of the freeze table of the job model, so the
   dispatch check reads one table. A person ends a freeze of a schedule
   with `EndFreeze`, as any other freeze. The freeze of an occurrence is
   `Schedule.FreezeOf(occurrence)`: the ID of decision 4, the namespace and
   the reason of the schedule, and the subject `@schedule:` followed by the
   schedule ID as `CreatedBy`. That subject is not a principal. It marks
   the freeze as a freeze of the schedule (`freeze.ScheduleOf`), so
   `job.Freeze` needs no new field. The freeze passes `job.Freeze.Validate`.
   The schedule store and the freeze store are one database, because a
   replace reads the stored schedule in its transaction.
7. **Replace, but never rewrite the past.** `Sink.ReplaceScheduled` takes
   the schedule and the occurrences of its plan. "Started" means a start at
   or before now on the clock of the store, not on the clock of the caller.
   - A freeze of the schedule that did not start: it adds each missing
     one, sets the end and the reason of each other one, and deletes each
     one that the plan does not have.
   - A freeze of the schedule that started and that no person ended: when
     the occurrence with its ID ends later, for example after a feed adds
     a holiday, the end moves later. The end never moves earlier, and the
     freeze is never deleted.
   - An occurrence that started and has no freeze: the Sink adds it only
     when no freeze of the schedule overlaps it, also a freeze that a person
     ended. A new holiday, a change of the working hours or new time zone
     data can give the active occurrence a new start and so a new ID. The
     rule keeps a freeze that a person ended from coming back with that
     new ID.
   - A freeze that a person ended (`EndedBy` set) is never changed,
     deleted or added again.
8. **Writes check the schedule version.** `ReplaceScheduled` checks the
   fence of the namespace and the version of the schedule in its
   transaction. It fails with `ErrNotFound` for a schedule that no longer
   exists, and with `ErrConflict` for an older version. So an hourly run
   that read the schedule before a change or a delete writes nothing, and
   the next run uses the new version.
9. **A change of a schedule takes effect at once.** The request that
   creates, changes or disables a schedule also runs `Plan` and
   `ReplaceScheduled` with the new version, in the same request. A disable
   deletes the freezes that did not start. The request that deletes a
   schedule then calls `Sink.DeleteScheduled`. It deletes the freezes of
   the schedule that did not start and that no person ended, and fails
   with `ErrExists` while the schedule exists. Each hourly run also calls
   it for each schedule ID that has freezes but no schedule, so a request
   that fails between the two writes leaves no future freeze. An active
   occurrence stays until its end, or until a person ends it. This keeps
   one rule: only a person ends an active freeze early, and the audit log
   names that person.
10. **Each write checks the namespace of its row.** Each write of
    `freeze.Store` and `freeze.Sink` carries the fence of the namespace of
    its row. A zero fence or the fence of another namespace fails: with
    `ErrInvalid` for a create, and with `ErrNotFound` for every write that
    names an existing schedule or freeze. A stale fence fails with
    `namespace.ErrFenced`.
11. **Freezes of a schedule are freezes.** Every rule of ADR 103 applies to
    a freeze of a schedule: plans still run, a running phase runs to its
    end, a queued job waits with the reason `frozen`, a state write of a
    tool is never refused, and a start needs a break-glass.
12. **Notice before a freeze.** The server sends the notification
    `freeze.upcoming` at `Notice` before each occurrence, 24 hours by
    default, and `freeze.started` and `freeze.ended` at its start and end
    (ADR 145, package `pkg/notify`). A notice is at most the horizon less
    one run interval (`freeze.MaxNotice`, 13 days and 23 hours), so a run
    stores each occurrence before its notice is due. The console of a
    namespace shows the next occurrence.
13. **Calendar changes move freezes.** A change of the calendar of a
    namespace, or of a feed of that calendar, runs `Plan` for each calendar
    schedule of the namespace in the next hourly run. A feed read that adds
    a holiday tomorrow adds its freeze within one hour.
14. **Actions.** A schedule uses the actions of ADR 103. No new action is
    necessary:

    | Route | Action | Object |
    | --- | --- | --- |
    | `GET /api/namespaces/{ns}/freeze-schedules[/{id}]` | `namespace:read` | namespace |
    | `GET /api/namespaces/{ns}/freeze-schedules/{id}/occurrences?from=&to=` | `namespace:read` | namespace |
    | `POST`, `PUT`, `DELETE /api/namespaces/{ns}/freeze-schedules[/{id}]` | `namespace:manage-freezes` | namespace |

    The occurrences route answers a preview for a range of at most 92 days,
    so a person sees the effect of a rule before the save.
15. **Audit entries.** A create, a change and a delete of a schedule write
    one entry each, of the kind `change`, with the action
    `namespace:manage-freezes`, in the scope of the namespace. The entry
    holds the schedule ID and the changed fields. The writes of
    `freeze.Sink` that a schedule makes, changes or deletes write no entry:
    the change of the schedule wrote one, and the hourly run is not an
    action of a person. This supersedes decision 9 of ADR 103 in part: the
    row "Freeze created or ended" applies to a freeze that a person creates
    or ends, not to a freeze that a schedule makes. The end of a scheduled
    freeze by a person still writes the entry of ADR 103.
16. **Ports with conformance first.** `freeze.Store` and `freeze.Sink` get
    the suite `pkg/freeze/freezetest`, with a reference adapter in memory.
    The suite of the job model keeps the cases of `job.FreezeStore`. The
    store of the job model implements `freeze.Sink`.

## Requests to other designs

- **Job model (ADR 103).** The store of the job model implements
  `freeze.Sink` on the freeze table, with the rules of decisions 6 to 10,
  and runs `pkg/freeze/freezetest`. An index on the creator and the start
  of a freeze serves the writes of a schedule. `job.Freeze` and
  `job.FreezeStore` do not change.

## Consequences

- One hourly run for each installation reads every schedule. With 1,000
  namespaces of 3 schedules each, it writes at most 3,000 small plans an
  hour. Most runs change nothing.
- The 14-day horizon bounds the freezes that one schedule stores. A
  nightly freeze stores 14 rows. One occurrence of a calendar rule can be
  longer than the horizon, up to the search bound of the calendar.
- A weekly freeze keeps its local start and end times. Over a daylight
  saving change it is one hour longer or shorter in elapsed time. The
  preview route shows this.
- A calendar freeze depends on the calendar feed. A feed that fails keeps
  its last good holidays (ADR 140), so a freeze does not vanish when a feed
  is down.
- The overlap rule of decision 7 compares with the stored times of a freeze
  that a person ended. A change that moves the start of the active
  occurrence to a time after that end adds the occurrence again, and a
  person ends it again.

## Open questions

- Does a freeze cover a whole namespace only, or also a collection or a
  unit? ADR 103 keeps this open. A schedule follows the answer.
- Monthly and quarterly rules, for example "the last 3 business days of
  each quarter", are not in this ADR.
- Can a schedule name another calendar than the calendar of its namespace,
  for example the calendar of the team that owns a release?
