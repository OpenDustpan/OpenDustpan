# 149. Hardening of the operations ADRs

Status: accepted · 2026-10-18 · supersedes part of ADRs 140 to 147 (see "What this ADR replaces")

## Context

ADRs 140 to 147 add business calendars, freeze schedules, break-glass
reviews, on-call schedules, cost and budgets, notifications, chat actions
and incident records. The review of their implementation found gaps in
these areas:

- A request could set the ID of a new object. Anyone can compute the ID
  of a freeze of an occurrence before its plan runs.
- A read by ID did not always take the scope of the route. A route could
  tell that an object exists in another scope.
- An update of an on-call schedule could write or delete on-call
  overrides without the action to override.
- A long dedupe key could become the same key as another key after a cut.
- A secret of a sink could reach the store as plaintext, and a new value
  under an old ref could send to another webhook of the same host.
- A change of a sink could send to a destination that the creator of a
  subscription did not approve.
- A notice to administrators had no recipient that names them.
- A chat link made after an intent could bring back the intent.

ADR 1 does not let us change the text of an accepted ADR. This ADR records
each change, and the ADRs that it changes name it in their status line.

## Decision

### Server-made IDs

1. **The server makes each ID.** The server makes the ID of each calendar
   (ADR 140 decision 1), of each freeze schedule and of each freeze, also
   of a freeze that a person creates (ADR 141 decisions 1 and 6), and of
   each on-call schedule (ADR 143 decision 1). A request cannot set an ID.
   The server never makes the ID of a freeze that a person creates in the
   form of an occurrence ID.
2. **`CreateFreeze` refuses an ID in the form of an occurrence ID.** In addition
   to ADR 141 decision 4, `CreateFreeze` refuses an ID in the form of an
   occurrence ID (`freeze.IsOccurrenceID`), in any namespace, with
   `ErrInvalid`. Anyone can compute the ID of an occurrence before its
   plan runs. Without this rule, a freeze of another namespace could take
   the ID first, and the replace would then skip the freeze of the
   schedule.
3. **The hourly cleanup uses the namespace of the freezes.** This replaces
   the cleanup text of ADR 141 decision 9. `Sink.DeleteScheduled` fails
   with `ErrExists` only while the schedule exists in the namespace of its
   fence. A schedule with the same ID in another namespace does not stop
   it. Each hourly run calls it, with the fence of the namespace of the
   freezes, for each schedule ID that has freezes in a namespace but no
   schedule in that namespace. The run compares the namespace of the
   schedule with the namespace of the freezes. So a request that fails
   between the two writes leaves no future freeze, also when another
   namespace then uses the free ID.

### Reads by ID stay in their scope

4. **404 for an object outside the scope of the route.** A route with an
   object ID answers 404 when the object is not in the scope of the route,
   the same 404 as for an unknown ID. Each read by ID of a store takes the
   scope of the route and returns `ErrNotFound` for an object of another
   scope. So the answer does not tell whether the object exists in another
   scope. This adds to the routes of these ADRs:

   | ADR | Decision | Objects and reads |
   | --- | --- | --- |
   | 140 | 18 | A calendar. Under `/api/namespaces/{ns}`, a calendar of another namespace. Under `/api/calendars`, a calendar of a namespace. `Store.Get` takes the namespace, or `@server` for a group or the installation. |
   | 141 | 14 | A freeze schedule. `freeze.Store.Get` takes the namespace. |
   | 142 | 12 | A break-glass review. Under `/api/namespaces/{ns}`, a review of another namespace. Under `/api/break-glass-reviews`, a review of a namespace. `Store.Get` takes the scope of the route. |
   | 143 | 15 | An on-call schedule, read by `{ns}` and its name, also when another namespace has a schedule of that name. The handoffs route reads the notes with `{ns}` and the schedule ID (`Handoffs.Handoffs`). |
   | 144 | 15 | A budget and its status, also for a person who holds the action in both namespaces. `GetEstimate` takes the namespace of the unit of the job, or the server scope for a blueprint of the installation. `LastStatus` takes the namespace of the budget. |
   | 145 | 27 | A sink, a subscription and a delivery, under `{ns}` or under `/api/server/`. The ports read sinks and subscriptions only by scope, and `Deliveries.History` takes the scope. |
   | 147 | 14 and 16 | An incident and its notes. `Store.Get` and `Store.Notes` take the namespace of the route. A read of an incident or of its notes in another namespace fails with `ErrNotFound`. |

5. **The conformance suite of break-glass reviews.** The suite
   `pkg/breakglass/breakglasstest` of ADR 142 decision 15 also covers a
   read by ID in another scope.
6. **Intents are visible only to their principal.** In addition to ADR 146
   decision 13, the read and the complete route of an intent answer 404
   when the caller is not the principal of the intent, the same 404 as for
   an unknown ID (`chatops.VisibleTo`). `chatops.CheckIntent` checks this
   first. So a caller who is not the principal learns nothing of the
   intent, also not that it has a use or that it expired.

### On-call overrides

7. **`AddOverride` is the only write of on-call overrides.** This replaces
   the first rule of ADR 143 decision 3. A create with on-call overrides is
   `ErrInvalid`. An update keeps the stored on-call overrides. An update
   accepts no on-call overrides or the stored list (`oncall.SameOverrides`),
   and returns `ErrInvalid` for other on-call overrides, so a caller never
   thinks that an update wrote one. An update at a stale version returns
   `ErrConflict` before it checks the on-call overrides, also when its
   on-call overrides are the old list. So:
   - a caller with `namespace:manage-on-call` and not
     `namespace:override-on-call` cannot write an on-call override, its
     creator or its time,
   - a `PUT` with no on-call overrides does not delete the live ones,
   - a `GET`, a change and a `PUT` of the same schedule still works.

   `Store.Get` gives the on-call overrides in the order of their adds, by a
   sequence number that the store gives each add in its transaction. Where
   two on-call overrides cover one time, the last add wins, also when two
   adds have the same time.

### Notifications

8. **A long dedupe key keeps a hash.** In addition to ADR 145 decision 3,
   `Fit` does not cut a dedupe key above its bound of 200 bytes. The key
   becomes its first bytes, `#` and the hex SHA-256 of the full key, in at
   most 200 bytes. A cut alone makes two keys with the same first 200
   bytes one key, and the outbox then drops the second notification with
   no error.
9. **One ref names one value.** In addition to ADR 145 decision 13, a ref
   names one value for good. The API stores each new secret value under a
   new ref, and never writes a new value under an existing ref. A ref is
   an identifier of at most 100 bytes. The URL of a chat webhook is the
   secret, and `Target` holds only its host. So a new value under the same
   ref could send to another webhook of the same host with no change that
   the store sees. The store of sinks keeps the values:
   - `Subscriptions.PutSink` takes the new secret items of the write with
     the sink. In the fenced transaction of the write, it refuses with
     `ErrInvalid` a new item whose ref the scope holds or held, also after
     a delete, and a ref with no value of the sink.
   - `Subscriptions.SinkSecrets` gives the values to the dispatcher.

   So a sink whose kind, target and refs did not change sends to the same
   place. The refs that a scope held stay after a delete of the sink. A SQL
   adapter that keeps them in a table adds that table to the table list of
   backups, and deletes its rows with the namespace.
10. **The API seals each secret before the store.** A secret value never reaches
    the store of sinks as plaintext. The API seals each new value with
    `notify.SinkSealer.Seal` before `PutSink`, with per-scope envelope
    encryption (ADR 23):
    - for a sink of a namespace, `secret.Service` with the secret scope of
      the namespace and the sink ID,
    - for a sink of `@server`, `secret.InstallationSecrets` with the
      installation scope of the sink ID.

    `SinkSealer.Seal` and `SinkSealer.Open` refuse with `ErrInvalid` a sink
    ID that does not start with `notify.SinkIDPrefix`. The installation
    scope of a sink is its ID, so the prefix keeps the installation scopes
    of sinks apart from the other installation scopes. The sealed value
    also holds its ref, so the ref binds the value. The ports carry only
    `notify.SealedSecret`, whose fields are not exported, so a caller
    cannot put a plaintext value in it by mistake. `PutSink` refuses with
    `ErrInvalid` an item sealed for another sink or another scope. The
    dispatcher opens the items of `SinkSecrets` with
    `notify.SinkSealer.Open` just before a send, and keeps the values only
    in memory for that send. A value sealed for another sink, scope or ref
    does not open.
11. **The last approver is the creator.** This replaces the first sentence
    of ADR 145 decision 15. The store sets `CreatedBy` to the subject of
    the audit entry of each create and each change of a subscription, a
    resume included, and ignores the `CreatedBy` of the request. So each
    delivery uses the access of the person who last approved the
    subscription. A person who edits a subscription cannot route
    notifications under the read access of another person.
12. **A sink change suspends and skips.** In addition to ADR 145 decision
    15, a change of the kind, the target or the secrets of a sink makes a
    new destination that the creators of its subscriptions did not approve.
    So in the transaction of that change, `Subscriptions.PutSink` does
    these steps:
    - It suspends each active subscription of the sink, with the reason
      `notify.ReasonSinkChanged`, and increments its version. A put of a
      subscription that a person read before the change then gets
      `ErrConflict`, so it cannot undo the suspend.
    - It appends one notification `subscription.suspended`
      (`notify.SinkChangedNotice`) for each subscription that it suspends.
      The recipients are the administrators of the scope and the earlier
      creator of the subscription, who approved the old destination. Its
      dedupe key is `<subscription ID>/sink-changed/<sink version>`. Its
      object is `notify.NotificationsObject` of the scope
      (`notifications:<scope>`). Only a principal that holds
      `namespace:manage-notifications` on the namespace, or
      `server:manage-notifications` for `@server`, reads a notification of
      this object (`notify.ScopeActions`). So the notice goes only to
      notification managers: the recipients and the subscriptions whose
      creator can manage the notifications of the scope. An earlier creator
      who lost the action does not get it.
    - It sets each pending delivery of each subscription of the sink to
      `skipped` and increments its version. A delivery routed to the old
      place is never sent to the new one. An operation that holds a claim
      of such a delivery gets `ErrConflict` when it records the attempt.

    A person resumes the subscription and becomes its creator. A change of
    only the name of a sink suspends and skips nothing.
13. **A delivery goes out only for its subscription.** In addition to ADR
    145 decision 15, a delivery of a subscription goes out, and
    `Deliveries.Redeliver` sends it again (else `ErrConflict`), only while
    these three conditions are true (`notify.SendsFor`):
    - its subscription is active,
    - its subscription sends to the sink of the delivery,
    - its subscription has the principal of the delivery as its creator.

    So a delivery from before a change of the sink, a change of the
    subscription by another person, or a suspend never goes to a place that
    its principal did not approve.
14. **`BeforeAttempt` reads the subscription.** This replaces the text on
    `notify.BeforeAttempt` in ADR 145 decision 15. Before each attempt, the
    delivery operation calls `notify.BeforeAttempt` with the stored
    subscription of the delivery. It applies `notify.SendsFor` to a
    delivery of a subscription. Then it calls `Reader.CanRead` for the
    principal and, for a delivery to a contact, `Contacts.ContactsOf`. When
    the subscription no longer sends the delivery, the principal can no
    longer read the notification, or the contact is no longer a contact of
    the principal, the operation records the delivery as `skipped` and
    sends nothing. When a port cannot decide, the operation tries again
    later, as for `ErrTemporary`.
15. **Administrators are a recipient.** This replaces the first sentence
    of ADR 145 decision 24. A notice names recipients: principals, groups,
    or the administrators of the scope of the notification (`Admins`).
    `Contacts.Admins` gives each principal that holds the action to manage
    the notifications of the scope.
16. **Only enabled principals receive.** `Contacts.Admins` and
    `Contacts.Members` give enabled principals only. A disabled principal
    with the action to manage the notifications of a scope is not an
    administrator of that scope, and a disabled member of a group is not a
    member that a notice goes to. `ContactsOf` already gives no contact for
    a disabled principal.
17. **The identity package runs `RunContacts`.** This adds to the identity
    consequence of ADR 145. The identity package implements
    `notify.Contacts` and runs the suite `notifytest.RunContacts`. The suite
    covers decision 16 of this ADR.

### Chat links

18. **A link after the intent stops it.** In addition to ADR 146 decision
    9, a link made after the intent (`LinkedAt` after the `CreatedAt` of
    the intent) also stops the intent. So a delete and a new link of the
    same chat user to the same principal does not bring back the intent.

## What this ADR replaces

| ADR | Decision | What changes |
| --- | --- | --- |
| 140 | 1, 18 | The server makes calendar IDs (decision 1). A calendar of another scope is 404 (decision 4). |
| 141 | 1, 4, 6, 9, 14 | The server makes schedule and freeze IDs (decision 1). `IsOccurrenceID` refusal (decision 2). The hourly cleanup uses the fence namespace (decision 3). A schedule of another namespace is 404 (decision 4). |
| 142 | 12, 15 | A review of another scope is 404, and the suite covers it (decisions 4 and 5). |
| 143 | 1, 3, 15 | The server makes schedule IDs (decision 1). The rules of on-call overrides (decision 7). A schedule of another namespace is 404 (decision 4). |
| 144 | 15 | A budget, its status and an estimate of another scope are 404 (decision 4). |
| 145 | 3, 13, 15, 24, 27, Consequences | The dedupe-key hash (decision 8). One ref names one value, sealed before the store (decisions 9 and 10). The `CreatedBy` rule, the sink-change suspend and notice, the skip of pending deliveries and `SendsFor` (decisions 11 to 13). `BeforeAttempt` (decision 14). The `Admins` recipient and enabled principals only (decisions 15 and 16). `RunContacts` (decision 17). A sink, a subscription or a delivery of another scope is 404 (decision 4). |
| 146 | 9, 13 | A link after the intent stops it (decision 18). An intent of another principal is 404 (decision 6). |
| 147 | 14, 16 | An incident and its notes of another namespace are 404 (decision 4). |

## Consequences

- Each changed ADR keeps its text. Its status line names this ADR.
- A client cannot choose the ID of a new calendar, freeze schedule, freeze
  or on-call schedule. The response gives the ID.
- A 404 from a route with an ID does not tell whether the object exists in
  another scope.
- A secret of a sink is never plaintext in the store, in a dump of the
  database or in a backup.
- A change of a sink stops its subscriptions until a person resumes them.
  The notification managers of the scope get a notice.
- A disabled principal gets no notice as an administrator or as a member of
  a group.
