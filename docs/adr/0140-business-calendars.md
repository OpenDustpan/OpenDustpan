# 140. Business calendars for namespaces and teams

Status: accepted · 2026-10-18

## Context

On 17 October 2026 the project owner decided these points for operations:

- A break-glass write needs a review by a second person by the next
  business day (ADR 142).
- Reminders and escalations use a business calendar.
- A business calendar exists for each namespace or team. It has a time
  zone, working days, and holidays from an iCal feed.
- Change freezes can follow the calendar (ADR 141).

Before this ADR, the server knows only UTC and fixed durations. "Next
business day" has no meaning for it. Five features need one shared answer:
review deadlines, reminders, escalations, freeze schedules and quiet hours
for personal notices (ADR 145).

A team in Berlin and a team in New York can work on one namespace. A
holiday in one country is a working day in the other. So a calendar must
attach to a team as well as to a namespace.

[ADR 52](0052-groups-roles-grants-and-access-review.md) makes a group of
OpenDustpan the unit of a team. [ADR 72](0072-scale-out-on-one-database.md)
gives durable operations and schedules.

## Decision

### The model

1. **A calendar is a value.** `calendar.Calendar` has an ID with the prefix
   `cal-`, a name, an IANA time zone, a set of working days, working hours
   (`DayStart` to `DayEnd`) and holidays. A holiday takes out a whole day. The
   functions on a calendar are pure. They read the calendar and a time, and
   never the clock or a store.
2. **One default.** Each installation has the calendar `cal-default`: UTC,
   Monday to Friday, 09:00 to 17:00, with no holidays. An administrator can
   change it. Nobody can delete it.
3. **Owners and assignments.** A calendar belongs to a namespace, to a group
   (a team), or to the installation. An assignment binds one scope to one
   calendar. A namespace can use a calendar of its own or of the
   installation. It cannot use the calendar of another namespace.
4. **Resolution along a chain.** `Store.Resolve` takes a chain of scopes and
   answers the calendar of the first scope with an assignment, or the
   default. The answer names the scope that gave the calendar, so a reader
   can see why a deadline falls where it does. The chains are:

   | For | Chain |
   | --- | --- |
   | A deadline about an object of a namespace | the namespace, the installation |
   | A notice to a person | the first group of the person with a calendar, the namespace of the notice, the installation |
   | A notice to a group | the group, the namespace of the notice, the installation |
   | A notice in the server scope | the installation |

   The groups of a person have no order. So for a person in two groups with
   two calendars, the server takes the group with the lowest name, and says
   so in the answer. This rule is simple and stable. It can be wrong for a
   person in two teams, and an open question below covers it.

### Business time

5. **Next business day.** `NextBusinessDay(t)` is the first business day
   after the date of `t` in the zone of the calendar. The date of `t`
   itself never counts, also when it is a business day. A business day is
   a working day that is not a holiday.
6. **The deadline "by the next business day".** `EndOfNextBusinessDay(t)`
   is the end of the working hours of `NextBusinessDay(t)`. A break-glass at
   10:00 on a Tuesday is due on Wednesday at 17:00. A break-glass on a
   Friday night is due on Monday at 17:00, or later after a holiday.
7. **Business hours and business time.** `InBusinessHours(t)` answers
   whether `t` is in the working hours of a business day.
   `NextBusinessMoment(t)` is `t` in business hours, else the next start of
   working hours. `AddBusinessTime(t, d)` counts only time in business hours.
   With 09:00 to 17:00, two hours after Friday 16:00 is Monday 10:00.
8. **Local time decides.** Each computation uses the wall clock of the zone
   of the calendar. A deadline at 17:00 stays at 17:00 over a change to or
   from summer time. A time that the change skips moves forward, as Go's
   `time.Date` does.
9. **A bound on each search.** A search for a business day stops after 400
   days with `ErrNoBusinessDay`. A calendar whose holidays cover 400 days is
   a mistake. The error makes the mistake visible instead of a loop.

### Holidays from iCal feeds

10. **Feeds.** A calendar has at most 5 iCal feeds (RFC 5545). A durable
    schedule reads each feed once a day, through the `calendar.HolidaySource`
    port. A person can also add up to 1,000 holidays by hand.
11. **What the import reads.** An event with a `DTSTART` of the type `DATE`
    is a whole-day event, and `DTEND` is the first day after it. Each day of
    such an event is a holiday. The import skips and counts an event with a time
    of day, because a calendar works with whole days. The import window is
    from 1 year back to 2 years ahead. The import expands recurring events in
    the window. A feed gives at most 1,000 holidays.
12. **A failed read keeps the last good read.** A feed that fails keeps the
    holidays of its last good read. The status of the feed records the time,
    the error and the count of skipped events. The calendar page and the
    read API show the status, so a stale feed is visible. A conditional read
    with the `ETag` of the last good read saves work when nothing changed.
13. **The read of a feed is an outbound request.** The adapter reads only
    `https` URLs, through the egress rules of the installation. It refuses
    a private, loopback or link-local address, a redirect to another host,
    and a body above 1 MiB. It sends no credential. A scheduled read writes
    no audit entry. A change of the feed list writes one.

### API and actions

14. **Routes.** Each route declares its action and object (ADR 17):

    | Route | Action | Object |
    | --- | --- | --- |
    | `GET /api/namespaces/{ns}/calendars`, `GET .../calendars/{id}` | `namespace:read` | namespace |
    | `POST`, `PUT`, `DELETE /api/namespaces/{ns}/calendars[/{id}]` | `namespace:manage-calendars` | namespace |
    | `PUT /api/namespaces/{ns}/calendar` (the assignment) | `namespace:manage-calendars` | namespace |
    | `GET /api/groups/{group}/calendar` | `caller:read` | caller |
    | `PUT /api/groups/{group}/calendar` | `server:manage-groups` | server |
    | `GET /api/calendars[/{id}]` (installation calendars and the default) | `caller:read` | caller |
    | `POST`, `PUT`, `DELETE /api/calendars[/{id}]` | `server:manage-calendars` | server |
    | `GET /api/namespaces/{ns}/calendar/next-business-day?at=` | `namespace:read` | namespace |

    A calendar holds no secret, so each signed-in person can read the
    calendars of the installation and of the groups. The last route answers the deadline of a time, with the calendar and the
    scope that gave it. The console uses it to show "due Monday 17:00
    Europe/Berlin (calendar Berlin office)".
15. **Audit entries.** Each change of a calendar, of its feeds or of an
    assignment writes one entry of the kind `change`, in the scope of its
    owner: the namespace, or `@server` for a group or the installation. The
    entry holds the calendar ID and the changed fields. A scheduled read of
    a feed writes no entry.
16. **Ports with conformance first.** `calendar.Store` gets the suite
    `pkg/calendar/calendartest` with a reference adapter in memory.
    `calendar.HolidaySource` gets its suite with its first adapter, because
    the cases need an iCal parser and a test server.

## Consequences

- Five features read one calendar model. A change of a holiday moves every
  deadline, reminder and calendar-aware freeze of that scope.
- The iCal parser is new code or a new dependency. The dependency needs the
  approval of the owner (see "Open questions").
- The server needs the time zone database. Go embeds it with the import of
  `time/tzdata`, at about 450 KB of binary size. Without it, a container
  image with no zone files fails to load every zone but UTC. The binary
  must import `time/tzdata`.
- `GLOSSARY.md` needs the terms Business calendar, Business day and
  Business time.

## Open questions

- Which group calendar applies to a person in two teams with two calendars?
  This ADR takes the group with the lowest name. A setting of the person may
  be better.
- iCal parser: our own parser of the subset in decision 11, or a library?
  A library is a new dependency, and needs the approval of the owner.
- Does a namespace need half days, for example a short working day before a
  holiday? This ADR has whole days only.
- Can a calendar have working hours that differ by day, for example a
  Friday that ends at 15:00?
