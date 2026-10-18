# 143. On-call schedules, handoffs and runbooks

Status: accepted · 2026-10-18 · decisions 1, 3 and 15 superseded in part by ADR 149. Route paths superseded in part by ADR 200

## Context

[ADR 160](0160-context-on-every-object.md) gives every object an `onCall`
field: text of at most 500 bytes, for example a pager service or a chat
channel. Owners and on-call resolve along the chain of an object, and a
caller with no read access to a layer of the chain still sees them
(ADR 161 decision 5). ADR 160 also gives every object a `runbook` field
(Markdown, 64 KiB) and links of the kind `runbook`.

On 17 October 2026 the project owner decided:

- On-call schedules (rotations, on-call overrides, handoffs) are
  first-class objects, or a link to an external schedule.
- The on-call field of the context resolves from them.
- Runbooks attach to units and resources.
- The Home page shows "you are on call for @network until Monday 09:00,
  next: <person>".

On 18 October 2026 the project manager decided, for the owner, that a
reference to a schedule of another namespace resolves only for a caller
that holds `namespace:read` on that namespace (decision 10).

Many teams already run an on-call tool. Others have none and keep a
rotation in a spreadsheet. OpenDustpan must serve both, with one answer to
"who is on call for this unit now".

## Decision

### Schedules

1. **A schedule is an object of a namespace.** `oncall.Schedule` has an ID
   with the prefix `ocs-`, a name that is unique in its namespace (the form
   of a namespace name), a time zone and a source:
   - `local`: on-call layers and on-call overrides give the shifts,
   - `external`: a schedule of an external on-call tool gives the shifts,
     through the `oncall.Provider` port.

   A schedule never belongs to the server scope. The time zone is a name
   of the IANA time zone database that the server can load, for example
   `Europe/Berlin` or `UTC` (`oncall.ValidZone`), of the form
   Area/Location or `UTC`. The server refuses the names that follow the
   zone of the host, for example `Local`, `localtime`, `posixrules` and
   `/etc/localtime`. The server
   cannot tell a canonical name from an alias of the database, so it
   accepts an alias such as `US/Pacific`.
2. **On-call layers.** A local schedule has 1 to 5 on-call layers. An
   on-call layer is a rotation: 1 to 50 members (principal IDs) take turns
   of a fixed length of at least 1 hour, in order, from a start. Turns
   follow the wall clock of the zone of the schedule: turn k starts at the
   wall time of the start plus k turns. So a turn of 12 hours from 08:00
   changes at 08:00 and 20:00 all year, and a turn of 7 days from Monday
   09:00 changes on Monday at 09:00, also over a change to or from summer
   time. At a change of the clock, the server resolves the wall time
   itself, also in zones west of UTC and in zones that change by 30
   minutes:
   - A turn that starts in the time that a change to summer time skips
     starts at the instant of the change, the end of the skipped time. A
     turn that lies fully in that time is empty, and its member has no
     turn that day.
   - A turn that ends in the time that a change to winter time repeats
     ends at the second occurrence of its end. So the turn before it is
     longer by the repeated time.

   An on-call layer can cover some hours of some days only, for example
   the working hours of a team. A later on-call layer wins over an earlier
   one where both cover a time.
3. **On-call overrides.** An on-call override puts one principal on call
   from a start to an end, over every on-call layer, with a reason, the
   principal that added it and the time of the add. The last on-call
   override wins over an earlier one. Two people can add on-call overrides
   at one time: `AddOverride` does not check the version of the schedule.
   A schedule holds at most 200 on-call overrides. The time of the add is
   the time of the transaction of the store, never a time from the
   request. In the transaction of each add, the store first deletes the
   on-call overrides whose end is not after the time of the add, so the
   cap counts only those that did not end. The audit entry of each add keeps a deleted on-call override.
   The timeline of a past range does not show it.
4. **The timeline is pure.** `Schedule.Timeline(from, to)` answers the
   shifts of a range of at most 92 days. A range that starts before the
   start of an on-call layer gets the turns of that on-call layer from its
   start. `Schedule.At(t)` answers the shift that covers `t`, with its real
   start and end. `CurrentAndNext(now)` answers the current shift and the
   next shift of another person. A time that nobody covers is
   `ErrNobodyOnCall`. Table-driven tests cover the rules, the clock
   changes and a schedule that is younger than 92 days.
5. **External schedules.** An external schedule names a configured provider
   and the key of the schedule in that tool. A durable schedule copies the
   shifts of the next 14 days every 10 minutes, with `Store.ReplaceCopy`.
   Only the copy sets the copied shifts: a create with copied shifts or a
   sync time is `ErrInvalid`, and an update keeps the stored copy. Reads
   use the copy. A copy older than 1 hour shows as stale, with its time.
   When the tool does not answer, the copy stays and the server sends the
   notification `on-call.sync-failed` to the owners of the namespace
   (ADR 145, package `pkg/notify`).
6. **People of an external tool.** A provider gives each shift with an
   email address and a name. The `oncall.Directory` port maps a verified
   email address to one principal. A person with no principal shows by the
   name that the tool gives. The provider adapter holds the address and the
   credential of the tool, from the configuration of the installation, and
   never answers the credential.

### On-call handoffs

7. **An on-call handoff is the end of a shift.** At each change of person,
   the server sends the notification `on-call.handoff` to the person who
   ends and the person who starts, with the open incidents of the
   namespace and the jobs that wait or run (ADR 145).
8. **Handoff notes.** The person who ends a shift can leave a note of at
   most 8 KiB for the next person: `oncall.Handoffs.AddHandoff`. The
   timeline shows the notes. A note is a write of a person, with an audit
   entry.

### The on-call field resolves from a schedule

9. **A reference in the field.** An `onCall` value `schedule:<name>` names a
   schedule of the namespace of the object. `schedule:<namespace>/<name>`
   names a schedule of that namespace. `oncall.ParseRef` holds the rule.
   Any other text stays text, as ADR 160 says.
10. **The read API resolves it, with the access rules of the caller.**
    When the resolved `onCall` of an object names a schedule, the context
    read API (ADR 161) adds the person on call now, the end of the shift
    and the next person. `oncall.Resolver` holds the rule:
    - A schedule of the namespace of the object resolves for every caller
      that gets the `context` section, also through a denied layer of the
      chain. Owners and on-call are not sensitive in the namespace of the
      object, and during an incident a person who reads a unit must get
      the people to call (ADR 161 decision 5).
    - A schedule of another namespace resolves only when the caller holds
      `namespace:read` on that namespace. Else the field stays the text,
      with the status `denied`, and the server does not read the schedule.
      So a reference in namespace A does not show the people of
      namespace B to a reader of A who cannot read B.
    - A schedule that does not exist gives the status `missing`, a
      warning and the text. A schedule with nobody on call now gives the
      status `nobody`.

    A table-driven test covers each status, a caller with and without
    `namespace:read`, and broken access rules.

    `pkg/objectcontext` gets a feature port for this, as ADR 161 decision
    12 has for each source, and a new field of the document:
    - `Sources.OnCall` of the type `OnCallReader`, with one method
      `OnCall(ctx, field, ns, canRead) (OnCallData, error)`. `canRead`
      answers `namespace:read` for one namespace, from `Access.Holds`. An
      adapter over `oncall.Resolver` and `oncall.Store` implements it. A
      nil port leaves the field as text, with no `onCall` data.
    - `ContextData` gets the field `onCall` of the type `OnCallData`:
      `status` (`text`, `resolved`, `denied`, `missing` or `nobody`),
      `schedule` (the namespace and the name, absent for `text`),
      `current` (the principal, or the name that an external tool gives,
      and `until`, the end of the shift), `next` (the principal or that
      name, and `from`), `stale` and `syncedAt` for the old copy of an
      external schedule. The read API gives `current` and `next` for
      `resolved` only.
    - This is a change of the shape of the document, so
      `objectcontext.SchemaVersion` goes from 1 to 2 (ADR 161 decision 11).
11. **Agents.** Dusty reads the resolved on-call through the same read API
    (ADR 127). It gets the same answer as the person that it acts for,
    with the access rules of that person. The agent tool set (ADR 122) has
    no tool that writes a schedule, an on-call override or a note.

### The Home page

12. **One route for the caller.** `GET /api/me/on-call` answers each
    schedule in which the caller is on call now or within 7 days. For each
    one, it gives the namespace, the schedule, the end of the current shift
    and the next person. The console shows "You are on call for @network
    until Monday 09:00, next: Bob". `@network` is the name of the schedule.
    The route reads `Store.ForPrincipal` and drops each schedule of a
    namespace where the caller lacks `namespace:read`.

### Runbooks

13. **A runbook is a field of the context.** The runbook of a unit or a
    resource is the `runbook` field and the links of the kind `runbook` of
    ADR 160. A team writes it in the reserved output of the unit, or sets
    it through the API. This ADR adds no second runbook object.
14. **Runbooks travel with notifications.** A notification about a unit
    carries a link of the kind `runbook` to the runbook of the unit, when
    it has one (ADR 145). A chat message, an email and a page show it. An
    incident shows the runbooks of its units (ADR 147).

### API, actions and audit

15. **Routes.**

    | Route | Action | Object |
    | --- | --- | --- |
    | `GET /api/namespaces/{ns}/on-call[/{name}]` | `namespace:read` | namespace |
    | `GET /api/namespaces/{ns}/on-call/{name}/timeline?from=&to=` | `namespace:read` | namespace |
    | `POST`, `PUT`, `DELETE /api/namespaces/{ns}/on-call[/{name}]` | `namespace:manage-on-call` | namespace |
    | `POST /api/namespaces/{ns}/on-call/{name}/overrides` | `namespace:override-on-call` | namespace |
    | `POST`, `GET /api/namespaces/{ns}/on-call/{name}/handoffs` | `namespace:override-on-call`, `namespace:read` | namespace |
    | `GET /api/me/on-call` | `caller:read` | caller |

    `namespace:override-on-call` is apart from `namespace:manage-on-call`,
    so a member of a rotation can swap a shift with no right to change the
    rotation. The built-in role `writer` gets it, and `maintainer` and
    `admin` get both (ADR 147).
16. **Audit entries.** A create, a change and a delete of a schedule, an
    on-call override and a handoff note write one entry each, of the kind
    `change`, in the scope of the namespace. The entry holds the schedule
    ID and the changed fields, or the on-call override with its principal,
    times and reason. The copy of an external schedule writes no entry.
    The store refuses an entry of another namespace than the row with
    `ErrInvalid`, so a write cannot append to the audit chain of another
    namespace.
17. **Ports with conformance first, and the fence of the row.** Each
    write of `oncall.Store` and `oncall.Handoffs` carries the fence of the namespace of its schedule,
    and the store checks it in the transaction of the write. A zero fence
    or the fence of another namespace does not belong to the row: a create
    is then `ErrInvalid`, and a write that names a stored schedule is
    `ErrNotFound`, so it does not tell whether the schedule exists. A
    fence at an older failover version is `namespace.ErrFenced`, also for
    the copy of an external schedule. An update cannot move a schedule to
    another namespace (`ErrInvalid`). `oncall.Store` and `oncall.Handoffs`
    get the suite `pkg/oncall/oncalltest`, with a reference adapter in
    memory. The suite runs a zero fence, a fence of another namespace and
    a stale fence against each write. `oncall.Provider` gets its suite with
    its first adapter.

## Requests to other designs

- **Context (ADR 160, ADR 161).** `pkg/objectcontext` gets the port
  `OnCallReader` in `Sources`, the field `onCall` of `ContextData`, and
  `SchemaVersion` 2, as decision 10 says. The service gives the port a
  `canRead` that calls `Access.Holds` with `namespace:read` on the
  namespace of the reference.
- **Identity.** `pkg/identity` gets the actions `namespace:manage-on-call`
  and `namespace:override-on-call`, in the namespace family.

## Consequences

- One question, "who is on call for this unit", has one answer for people,
  chat messages, escalations and Dusty, within the access rules of the
  reader.
- A reference to a schedule of another namespace shows a person only to a
  reader of that namespace. Other readers see the text and a `denied`
  mark.
- An external on-call tool stays the source for a team that has one. The
  copy every 10 minutes means that a change in the tool shows in
  OpenDustpan after at most 10 minutes.
- The provider adapters for external tools are outside this ADR. Each one
  is a new adapter with its own suite.
- `GLOSSARY.md` needs the terms On-call schedule, On-call layer, On-call
  override, Shift, On-call handoff and Handoff note. "Layer", "Override"
  and "Handoff" alone already have other meanings there.

## Open questions

- Which external on-call tools get an adapter first, and are they
  first-party adapters or community adapters on the public port?
- Does OpenDustpan page a person (a phone call or a push) for a critical
  notification, or does it hand the page to the external tool only?
- Can a schedule belong to a group (a team) instead of a namespace, so that
  one rotation covers several namespaces? This ADR keeps it in a namespace,
  and the field `schedule:<namespace>/<name>` reaches it from others.
