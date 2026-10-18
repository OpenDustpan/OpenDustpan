# 141. Freeze schedules: windows, recurring freezes and calendar freezes

Status: accepted · 2026-10-18

## Context

[ADR 103](0103-job-gates-actions-and-audit.md) decisions 4 to 6 make change
freezes opt-in for each namespace. A freeze has a reason, a start and an
optional end. While a freeze is active, no phase that changes resources,
state or code dispatches. A start during a freeze needs a break-glass with a
reason. The job store checks the freeze in the transaction of the dispatch.

On 17 October 2026 the project owner decided that the job model owns the
freeze gate, and that this design owns the schedule model: windows,
recurring freezes and freezes that follow the business calendar.

A team asks for freezes of these shapes:

- one window, for example the week of a product release,
- every weekend, from Friday 15:00 to Monday 09:00,
- every night, outside working hours,
- every holiday, and the business day before it.

ADR 140 gives the business calendar of a namespace. ADR 72 gives durable
schedules.

## Decision

1. **A schedule makes freezes.** `freeze.Schedule` belongs to one namespace.
   It has an ID with the prefix `fzs-`, a name, a reason and one rule of
   three kinds:
   - `window`: one freeze from a start to an end.
   - `weekly`: on some days of the week, at a time of day in a time zone,
     for a length of at most 6 days. The occurrences of one rule must not
     overlap.
   - `calendar`: periods of the calendar of the namespace. Each flag adds
     periods: days that are not working days, holidays, the time outside
     working hours on business days, and the last 1 to 5 business days
     before each holiday.

   A schedule makes occurrences from `From` and until `Until`, when set. A
   schedule that is not enabled makes none.
2. **The dispatch check reads stored freezes only.** A durable schedule runs
   `Schedule.Plan` for each schedule of each namespace once an hour. It
   writes the occurrences of the next 14 days as freezes of the job model,
   through the port `freeze.Sink`. The dispatch check never computes a
   recurrence or reads a calendar. So the check stays one indexed read in
   the transaction of the dispatch, as ADR 103 decision 4 requires.
3. **Stable IDs.** The freeze of an occurrence has the ID
   `freeze.OccurrenceID(schedule, start)`: `frz-`, the schedule ID with no
   prefix, and the start in Unix milliseconds. `Occurrences` never cuts an
   occurrence at the start of its range. For a calendar rule, it reads back
   over each day that the rule freezes whole. So a second run of `Plan`
   gives the same ID for the same occurrence, and makes no second freeze.
4. **Adjacent periods join.** Periods that touch or overlap become one
   occurrence. A calendar rule with days off and time outside hours gives
   one freeze from Friday 17:00 to Monday 09:00, not three.
5. **Replace, but never rewrite the past.** `Sink.ReplaceScheduled` makes
   the freezes of a schedule that start after now equal to the plan: it
   adds, changes the end, and deletes. It never changes a freeze of the
   schedule that already started. It adds an occurrence that already
   started only when no freeze has its ID. So a freeze that a person ended
   early with `namespace:manage-freezes` never comes back.
6. **A change of a schedule takes effect at once.** The request that
   creates, changes, disables or deletes a schedule also runs `Plan` and
   `ReplaceScheduled`, in the same request. A delete or a disable deletes
   the freezes that did not start. An active occurrence stays until its
   end, or until a person ends it. This keeps one rule: only a person ends
   an active freeze early, and the audit log names that person.
7. **Freezes of a schedule are freezes.** A freeze of a schedule has the
   reason of the schedule and the schedule ID as its origin. Every rule of
   ADR 103 applies to it: plans still run, a running phase runs to its end,
   a queued job waits with the reason `frozen`, a state write of a tool is
   never refused, and a start needs a break-glass.
8. **Notice before a freeze.** The server sends the event
   `freeze.upcoming` at `Notice` before each occurrence, 24 hours by
   default, and `freeze.started` and `freeze.ended` at its start and end
   (ADR 145). The console of a namespace shows the next occurrence.
9. **Calendar changes move freezes.** A change of the calendar of a
   namespace, or of a feed of that calendar, runs `Plan` for each calendar
   schedule of the namespace in the next hourly run. A feed read that
   adds a holiday tomorrow adds its freeze within one hour.
10. **Actions.** A schedule uses the actions of ADR 103. No new action is
    necessary:

    | Route | Action | Object |
    | --- | --- | --- |
    | `GET /api/namespaces/{ns}/freeze-schedules[/{id}]` | `namespace:read` | namespace |
    | `GET /api/namespaces/{ns}/freeze-schedules/{id}/occurrences?from=&to=` | `namespace:read` | namespace |
    | `POST`, `PUT`, `DELETE /api/namespaces/{ns}/freeze-schedules[/{id}]` | `namespace:manage-freezes` | namespace |

    The occurrences route answers a preview for a range of at most 92 days,
    so a person sees the effect of a rule before the save.
11. **Audit entries.** A create, a change and a delete of a schedule write
    one entry each, of the kind `change`, with the action
    `namespace:manage-freezes`, in the scope of the namespace. The entry
    holds the schedule ID and the changed fields. The hourly run writes no
    entry: it is not an action of a person. The end of a scheduled freeze by
    a person writes the entry of ADR 103.
12. **Ports with conformance first.** `freeze.Store` and `freeze.Sink` get
    the suite `pkg/freeze/freezetest`, with a reference adapter in memory.
    The store of the job model implements `freeze.Sink`.

## Requests to other designs

- **Job model (ADR 103).** `job.Freeze` gets a field `Schedule`, the ID of
  the schedule that made it, or empty. The job store implements
  `freeze.Sink` on the freeze table, with the rules of decision 5.
  `job.FreezeStore.ListFreezes` answers the field.

## Consequences

- One hourly run for each installation reads every schedule. With 1,000
  namespaces of 3 schedules each, it writes at most 3,000 small plans an
  hour. Most runs change nothing.
- The 14-day horizon bounds the freezes that one schedule stores. A
  nightly freeze stores 14 rows.
- A schedule in a zone with summer time keeps its local start time. A
  weekly freeze of 66 hours that crosses the change ends one hour later or
  earlier in local time. The preview route shows this.
- A calendar freeze depends on the calendar feed. A feed that fails keeps
  its last good holidays (ADR 140), so a freeze does not vanish when a feed
  is down.

## Open questions

- Does a freeze cover a whole namespace only, or also a collection or a
  unit? ADR 103 keeps this open. A schedule follows the answer.
- Monthly and quarterly rules, for example "the last 3 business days of
  each quarter", are not in this ADR.
- Can a schedule name another calendar than the calendar of its namespace,
  for example the calendar of the team that owns a release?
