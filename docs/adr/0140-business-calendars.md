# 140. Business calendars for namespaces and teams

Status: accepted · 2026-10-18. Decisions 1 and 18 superseded in part by ADR 149. Route paths and `server:` action names superseded in part by ADR 210

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
for notifications to a person (ADR 145).

A team in Berlin and a team in New York can work on one namespace. A
holiday in one country is a working day in the other. So a calendar must
attach to a team as well as to a namespace.

[ADR 52](0052-groups-roles-grants-and-access-review.md) makes a group of
OpenDustpan the unit of a team. [ADR 72](0072-scale-out-on-one-database.md)
gives durable operations and schedules.

## Decision

### The model

1. **A calendar is a value.** `calendar.Calendar` has an ID with the prefix
   `cal-`, an owner, a name, an IANA time zone, a set of working days,
   working hours (`DayStart` to `DayEnd`) and holidays. A holiday takes out
   a whole day. The functions on a calendar are pure. They read the
   calendar and a time, and never the clock or a store.
2. **One default.** Each installation has the calendar `cal-default`: UTC,
   Monday to Friday, 09:00 to 17:00, with no holidays. An administrator can
   change it. Nobody can delete it.
3. **Owners and assignments.** A calendar belongs to a namespace, to a group
   (a team), or to the installation. The owner does not change after the
   create. An assignment binds one scope to one calendar. A scope can use
   a calendar of its own namespace, of a group, or of the installation. It
   cannot use the calendar of another namespace.
4. **Resolution along a chain.** `Store.Resolve` takes a chain of scopes and
   answers the calendar of the first scope with an assignment, or the
   default. The answer names the scope that gave the calendar, so a reader
   can see why a deadline falls where it does. The chains are:

   | For | Chain |
   | --- | --- |
   | A deadline about an object of a namespace | the namespace, the installation |
   | A notification to a person | the first group of the person with a calendar, the namespace of the notification, the installation |
   | A notification to a group | the group, the namespace of the notification, the installation |
   | A notification in the server scope | the installation |

   The groups of a person have no order. So for a person in two groups with
   two calendars, the server takes the group with the lowest name, and says
   so in the answer. This rule is simple and stable. It can be wrong for a
   person in two teams, and an open question below covers it.
5. **Each write carries the fence of its row.** A calendar of a namespace,
   the holidays of its feeds, and the assignment of a namespace are rows of
   that namespace. Each write of such a row carries the fence of the
   namespace, and the store checks it in the transaction of the write. This
   includes the scheduled read of a feed. A calendar or an assignment of a
   group or of the installation is a row of the server scope (`@server`),
   and its writes carry the zero fence. The store refuses a zero fence for
   a row of a namespace, the fence of another namespace, and a namespace
   fence for a row of the server scope. The port names the error of each
   method.

### Business time

6. **Next business day.** `NextBusinessDay(t)` is the first business day
   after the date of `t` in the zone of the calendar. The date of `t`
   itself never counts, also when it is a business day. A business day is
   a working day that is not a holiday.
7. **The deadline "by the next business day".** `EndOfNextBusinessDay(t)`
   is the end of the working hours of `NextBusinessDay(t)`. A break-glass at
   10:00 on a Tuesday is due on Wednesday at 17:00. A break-glass on a
   Friday night is due on Monday at 17:00, or later after a holiday.
8. **Business hours and business time.** `InBusinessHours(t)` answers
   whether `t` is in the working hours of a business day.
   `NextBusinessMoment(t)` is `t` in business hours, else the next start of
   working hours. `AddBusinessTime(t, d)` counts only time in business hours.
   With 09:00 to 17:00, two hours after Friday 16:00 is Monday 10:00.
9. **Local time decides.** Each computation uses the wall clock of the zone
   of the calendar. A deadline at 17:00 stays at 17:00 over a change to or
   from summer time. Two rules apply on the day of a change:
   - A wall clock time that occurs twice, because the clock goes back,
     gives the first instant. In Berlin, 02:30 on 31 October 2027 is
     02:30 CEST.
   - A wall clock time that the change skips, because the clock goes
     forward, moves forward by the length of the gap. In Berlin, 02:30 on
     28 March 2027 is 03:30 CEST.
10. **A zone is an IANA name.** The zone of a calendar is `UTC` or a name of
    the form Area/Location, for example `Europe/Berlin`. The server refuses
    `Local`, because it is the zone of the host and not of the team, and
    refuses every other name that the time zone database does not hold.
    `calendar.LoadZone` holds this check, and each package that stores a
    zone name uses it, so the server also refuses `localtime`, `posixrules`
    and `/etc/localtime`.
11. **A bound on each search.** A search for a business day stops after 400
    days with `ErrNoBusinessDay`. A calendar whose holidays cover 400 days is
    a mistake. The error makes the mistake visible instead of a loop.

### Holidays from iCal feeds

12. **Feeds.** A calendar has at most 5 iCal feeds (RFC 5545). A durable
    schedule reads each feed once a day, through the `calendar.HolidaySource`
    port. A person can also add up to 1,000 holidays by hand.
13. **What the import reads.** The import window is from 1 year back to 2
    years ahead. The import applies these rules:
    - An event with a `DTSTART` of the type `DATE` is a whole-day event.
      Its last day is the day before `DTEND`, or the day before `DTSTART`
      plus `DURATION`. An event with neither is one day. Each day of the
      event is a holiday.
    - The import skips and counts an event with a time of day, because a
      calendar works with whole days. One exception: an event from 00:00
      to 00:00 of a later day is a whole-day event of those dates. Some
      calendar programs, for example Outlook, write a whole-day event in
      this form. The import reads the time in the zone of its `TZID`, in
      UTC for a time that ends in `Z`, and as a wall clock time for a
      floating time.
    - `RRULE` and `RDATE` add occurrences in the window. `EXDATE` takes
      occurrences out. An event with a `RECURRENCE-ID` replaces the occurrence
      of that date.
    - The import skips and counts an event or an occurrence that its
      `STATUS` property marks as canceled.
    - Two events on one date give one holiday, with the first name in the
      order of the feed.
    - A feed that gives more than 1,000 holidays in the window is a
      failed read of the class `parse`.
14. **A failed read keeps the last good read.** The store has one write for
    each outcome of a read:
    - A good read (`ReplaceFeedHolidays`) replaces the holidays of the feed
      and its whole status.
    - A failed read, and a read that answers "not modified" to a
      conditional request with the `ETag` of the last good read
      (`RecordFeedStatus`), change only the time of the read and the error.
      They keep the holidays, the counts and the `ETag` of the last good
      read. A "not modified" read clears the error.

    Both writes carry the URL of the read. When an update gave
    the feed a new URL during the read, the store refuses the write with
    `ErrConflict`, so the holidays of an old URL never go to a new one.

    The status of a feed holds the time, the error class and the counts.
    The calendar page and the read API show it, so a stale feed is
    visible.
15. **The error of a read is a class.** The status holds one of `refused`,
    `unreachable`, `timeout`, `too_large`, `parse` and `http_status`. It
    never holds the text of a network error, because that text can hold
    an address of the network of the server. The store refuses any other
    value.
16. **The read of a feed is an outbound request.** A principal gives the
    URL, so the read must not reach the network of the server. The read
    uses the shared egress rules, which also apply to the delivery of a
    notification to a webhook (ADR 145):
    - The server checks the URL when a principal creates or changes a
      feed, and when a principal tests a feed. It refuses a URL with a
      scheme other than `https`, with user information, with a fragment,
      with no host, longer than 2,048 bytes, with the host `localhost`, or
      with an IP address that the address rule refuses.
    - The address rule refuses an address that is not public unicast:
      loopback, private, link-local (this includes the metadata address
      169.254.169.254 of cloud providers), unspecified, multicast, shared
      address space, unique local, NAT64, an address with an embedded IPv4
      address, and other reserved ranges. It reads an IPv4-mapped IPv6
      address as its IPv4 address.
    - The adapter applies the address rule to the resolved address of each
      connection, so a DNS answer that changes after the URL check cannot
      pass it.
    - The adapter follows at most 3 redirects, each only to the same host
      and never from `https` to `http`. It uses no proxy from the
      environment and sends no credential.
    - The adapter reads at most 4 MiB of the body after decompression, and
      stops each read after 30 seconds.

    Feeds use `https` only. The URL of a private feed often holds a token,
    and `http` sends that token in clear text.
17. **A feed URL is a credential.** The URL of a private iCal feed gives
    read access to the calendar of its owner. So no read through the API
    returns it. A read returns the scheme and the host, followed by `/…`,
    for example `https://calendar.example.com/…`. Only the store and the
    adapter that reads the feed get the full URL. An update that sends back
    the masked URL of a feed keeps the stored URL. A new URL, or an update
    with no feed of that ID, drops the holidays of that feed. An audit entry holds the masked
    URL only.

### API and actions

18. **Routes.** Each route declares its action and object (ADR 17):

    | Route | Action | Object |
    | --- | --- | --- |
    | `GET /api/namespaces/{ns}/calendars`, `GET .../calendars/{id}` | `namespace:read` | namespace |
    | `POST`, `PUT`, `DELETE /api/namespaces/{ns}/calendars[/{id}]` | `namespace:manage-calendars` | namespace |
    | `POST /api/namespaces/{ns}/calendars/{id}/feeds/{feed}/test` | `namespace:manage-calendars` | namespace |
    | `PUT /api/namespaces/{ns}/calendar` (the assignment) | `namespace:manage-calendars` | namespace |
    | `GET /api/server/groups/{id}/calendar` | `caller:read` | caller |
    | `PUT /api/server/groups/{id}/calendar` (the assignment) | `server:manage-groups` | server |
    | `GET /api/calendars[/{id}]` (calendars of the installation and of groups, and the default) | `caller:read` | caller |
    | `POST`, `PUT`, `DELETE /api/calendars[/{id}]` | `server:manage-calendars` | server |
    | `POST /api/calendars/{id}/feeds/{feed}/test` | `server:manage-calendars` | server |
    | `GET /api/namespaces/{ns}/calendar/next-business-day?at=` | `namespace:read` | namespace |

    Each signed-in person can read the calendars of the installation and
    of the groups, with masked feed URLs (decision 17). An administrator
    creates a calendar of a group through `/api/calendars`, with the group
    as its owner.
    A test of a feed runs one read now, with the egress rules, and answers
    the status of that read. It does not store holidays. The last route
    answers the deadline of a time, with the calendar and the scope that
    gave it. The console uses it to show "due Monday 17:00 Europe/Berlin
    (calendar Berlin office)".
19. **Audit entries.** Each change of a calendar, of its feeds or of an
    assignment writes one entry of the kind `change`, in the scope of its
    owner: the namespace, or `@server` for a group or the installation. The
    entry holds the calendar ID and the changed fields. A scheduled read of
    a feed writes no entry.
20. **Ports with conformance first.** `calendar.Store` gets the suite
    `pkg/calendar/calendartest` with a reference adapter in memory. The
    suite covers the fence rules of decision 5 on each write.
    `calendar.HolidaySource` gets its suite with its first adapter, because
    the cases need an iCal parser and a test server. The suite has these
    cases:
    - A whole-day event of one day, of several days with `DTEND`, and of
      several days with `DURATION`.
    - An Outlook-style event from 00:00 to 00:00, with a `TZID` and as a
      floating time.
    - The source skips and counts a timed event.
    - `RRULE` expands in the window only. `RDATE` adds an occurrence and
      `EXDATE` takes one out. `RECURRENCE-ID` moves an occurrence.
    - `STATUS` canceled on an event and on one occurrence.
    - Two events on one date give one holiday.
    - More than 1,000 holidays in the window is `ErrFeedInvalid`.
    - A body that is not iCal is `ErrFeedInvalid`.
    - A `304` answer to the `ETag` of the last read is `ErrNotModified`.
    - A status other than 200 and 304 is `ErrFeedHTTPStatus`.
    - A body above 4 MiB after decompression, also a small compressed
      body, is `ErrFeedTooLarge`.
    - A read longer than the time bound is `ErrFeedTimeout`.
    - A host that resolves to a loopback, private, link-local or
      IPv4-mapped address is `ErrFeedRefused`.
    - A redirect to another host and a redirect from `https` to `http`
      are `ErrFeedRefused`.
    - The request carries no `Authorization` header and no cookie.

## Consequences

- Five features read one calendar model. A change of a holiday moves every
  deadline, reminder and calendar-aware freeze of that scope.
- The iCal parser is new code or a new dependency. The dependency needs the
  approval of the owner (see "Open questions").
- The server needs the time zone database. Go embeds it with the import of
  `time/tzdata`, at about 450 KB of binary size. Without it, a container
  image with no zone files fails to load every zone but UTC. The binary
  must import `time/tzdata`.
- Two actions join the action list of the `identity` package, and the
  built-in roles need them: `namespace:manage-calendars` (object: a
  namespace) for the role namespace-admin, and `server:manage-calendars`
  (object: the server) for the site administrator. The integrator adds
  them to the list, to the roles and to the route test.
- A feed URL never leaves the server through a read. A person who wants to
  change the token of a feed enters the whole new URL.
- `GLOSSARY.md` needs the terms Business calendar, Business day and
  Business time.

## Open questions

- Which group calendar applies to a person in two teams with two calendars?
  This ADR takes the group with the lowest name. A setting of the person may
  be better.
- iCal parser: our own parser of the subset in decision 13, or a library?
  A library is a new dependency, and needs the approval of the owner.
- Does a namespace need half days, for example a short working day before a
  holiday? This ADR has whole days only.
- Can a calendar have working hours that differ by day, for example a
  Friday that ends at 15:00?
