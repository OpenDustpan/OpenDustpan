# 143. On-call schedules, handoffs and runbooks

Status: accepted · 2026-10-18

## Context

[ADR 160](0160-context-on-every-object.md) gives every object an `onCall`
field: text of at most 500 bytes, for example a pager service or a chat
channel. Owners and on-call resolve along the chain of an object, and a
caller with no read access still sees them (ADR 161 decision 5). ADR 160
also gives every object a `runbook` field (Markdown, 64 KiB) and links of
the kind `runbook`.

On 17 October 2026 the project owner decided:

- On-call schedules (rotations, overrides, handoffs) are first-class
  objects, or a link to an external schedule.
- The on-call field of the context resolves from them.
- Runbooks attach to units and resources.
- The Home page shows "you are on call for @network until Monday 09:00,
  next: <person>".

Many teams already run an on-call tool. Others have none and keep a
rotation in a spreadsheet. OpenDustpan must serve both, with one answer to
"who is on call for this unit now".

## Decision

### Schedules

1. **A schedule is an object of a namespace.** `oncall.Schedule` has an ID
   with the prefix `ocs-`, a name that is unique in its namespace (the form
   of a namespace name), a time zone and a source:
   - `local`: layers and overrides give the shifts,
   - `external`: a schedule of an external on-call tool gives the shifts,
     through the `oncall.Provider` port.
2. **Layers.** A local schedule has 1 to 5 layers. A layer is a rotation:
   1 to 50 members (principal IDs) take turns of a fixed length, in order,
   from a start. A turn of whole days changes at the local time of the
   start, also over a change to or from summer time. A layer can cover some
   hours of some days only, for example the working hours of a team. A
   later layer wins over an earlier one where both cover a time.
3. **Overrides.** An override puts one principal on call from a start to an
   end, over every layer, with a reason. The last override wins over an
   earlier one. Two people can add overrides at one time: `AddOverride`
   does not check the version of the schedule.
4. **The timeline is pure.** `Schedule.Timeline(from, to)` answers the
   shifts of a range of at most 92 days. `Schedule.At(t)` answers the shift
   that covers `t`, with its real start and end. `CurrentAndNext(now)`
   answers the current shift and the next shift of another person. A time
   that nobody covers is `ErrNobodyOnCall`. Table-driven tests cover the
   rules.
5. **External schedules.** An external schedule names a configured provider
   and the key of the schedule in that tool. A durable schedule copies the
   shifts of the next 14 days every 10 minutes, with `Store.ReplaceCopy`.
   Reads use the copy. A copy older than 1 hour shows as stale, with its
   time. When the tool does not answer, the copy stays and the server sends
   the event `on-call.sync-failed` to the owners of the namespace.
6. **People of an external tool.** A provider gives each shift with an
   email address and a name. The `oncall.Directory` port maps a verified
   email address to one principal. A person with no principal shows by the
   name that the tool gives. The provider adapter holds the address and the
   credential of the tool, from the configuration of the installation, and
   never answers the credential.

### Handoffs

7. **A handoff is the end of a shift.** At each change of person, the
   server sends the event `on-call.handoff` to the person who ends and the
   person who starts, with the open incidents of the namespace and the
   jobs that wait or run (ADR 145).
8. **Handoff notes.** The person who ends a shift can leave a note of at
   most 8 KiB for the next person: `oncall.Handoffs.AddHandoff`. The
   timeline shows the notes. A note is a write of a person, with an audit
   entry.

### The on-call field resolves from a schedule

9. **A reference in the field.** An `onCall` value `schedule:<name>` names a
   schedule of the namespace of the object. `schedule:<namespace>/<name>`
   names a schedule of another namespace. `oncall.ParseRef` holds the rule.
   Any other text stays text, as ADR 160 says.
10. **The read API resolves it.** When the resolved `onCall` of an object
    names a schedule, the context read API (ADR 161) adds the person on call
    now, the end of the shift and the next person. Owners and on-call stay
    visible to a caller with no read access to a layer. So the person on
    call is visible too: it is the purpose of the field during an
    incident. A schedule that does not exist gives a warning and the text.
11. **Agents.** Dusty reads the resolved on-call through the same read API
    (ADR 127). It gets the same answer as a person.

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
14. **Runbooks travel with events.** An event about a unit carries a link
    of the kind `runbook` to the runbook of the unit, when it has one (ADR
    145). A chat message, an email and a page show it. An incident shows
    the runbooks of its units (ADR 147).

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
    rotation. The built-in role `writer` gets it.
16. **Audit entries.** A create, a change and a delete of a schedule, an
    override and a handoff note write one entry each, of the kind `change`,
    in the scope of the namespace. The entry holds the schedule ID and the
    changed fields, or the override with its principal, times and reason.
    The copy of an external schedule writes no entry.
17. **Ports with conformance first.** `oncall.Store` and `oncall.Handoffs`
    get the suite `pkg/oncall/oncalltest`, with a reference adapter in
    memory. `oncall.Provider` gets its suite with its first adapter.

## Requests to other designs

- **Context (ADR 160, ADR 161).** `objectcontext` resolves an `onCall` value
  with the prefix `schedule:` through `oncall.ParseRef`, and the read API
  adds the fields of decision 10.

## Consequences

- One question, "who is on call for this unit", has one answer for people,
  chat messages, escalations and Dusty.
- An external on-call tool stays the source for a team that has one. The
  copy every 10 minutes means that a change in the tool shows in
  OpenDustpan after at most 10 minutes.
- The provider adapters for external tools are outside this ADR. Each one
  is a new adapter with its own suite.
- `GLOSSARY.md` needs the terms On-call schedule, Layer, Override, Shift
  and Handoff.

## Open questions

- Which external on-call tools get an adapter first, and are they
  first-party adapters or community adapters on the public port?
- Does OpenDustpan page a person (a phone call or a push) for a critical
  event, or does it hand the page to the external tool only?
- Can a schedule belong to a group (a team) instead of a namespace, so that
  one rotation covers several namespaces? This ADR keeps it in a namespace,
  and the field `schedule:<namespace>/<name>` reaches it from others.
