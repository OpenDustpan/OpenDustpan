# 172. The core resource lifecycle and its port

Status: accepted · 2026-10-18 · part of ADR 170

## Context

A declared HTTP provider, a declared CLI provider and an SDK provider must
behave the same way in OpenTofu and in the Pulumi CLI (ADR 170). The two
tools ask different questions:

- OpenTofu asks the provider to plan a change and then to apply it. It reads
  each object to refresh state, and imports an object by its ID.
- The Pulumi CLI asks the provider to check inputs, to diff them against the
  old ones, and then to create, update or delete. It reads an object to
  refresh it, and imports by ID through a read.

Both reduce to the same five operations on one object, and one decision:
create, update in place, replace or delete. If each adapter made that
decision, the two tools could show different plans for one declaration.

The port and the planner are in `providerkit/lifecycle`.

## Decision

1. **One port, `lifecycle.Provider`.** It returns the name, the version and
   the schemas, and `Configure` returns a `lifecycle.Session`. A session
   holds the resource and data source types bound to one provider
   configuration. Both protocol adapters consume only this port (ADR 176).
2. **The operations of a resource.** `Create`, `Read`, `Update` and
   `Delete`. Each returns the values that the remote system now has: every
   attribute of the schema, with no unknown value.
   - **Write-only values reach the engine (PM, for the project owner).**
     `Create` and `Update` get `Change.Planned`, which holds the write-only
     values of the configuration, so the engine can send them. The adapter
     passes each value set that it gives its tool through
     `lifecycle.ForTool`, which makes every write-only attribute null: the
     planned values of a change, and the values that `Create`, `Read` and
     `Update` return. The tool then never stores a write-only value, also
     when an engine returns one.
3. **Drift comes from Read.** A refresh calls `Read` with the prior values.
   `lifecycle.ErrNotFound` means the object is gone, and the adapter drops
   it from state. Values that differ go into state, and the next plan shows
   the change back to the configuration.
4. **Import is a read by ID.** An import calls `Read` with only the `id`.
   There is no separate import operation. The read of a declared resource
   therefore references only `{id}` and the provider configuration (ADR 171
   decision 11, PM, for the project owner). An import of an `id` that does
   not exist is `ErrNotFound`.
5. **Delete of a missing object is done.** `Delete` returns nil when the
   object is already gone. A destroy that a person runs again then ends.
6. **Data sources bring facts.** `DataSource.Read` returns values for a
   configuration. `ErrNotFound` means that the configuration selects
   nothing. Both tools read a data source during a plan.
7. **One planner, `lifecycle.Plan`.** Both adapters call it. It takes the
   schema, the prior values and the configuration, and returns the action,
   the planned values, the changed attributes and the attributes that force
   a replace. Its rules:
   - A write-only attribute takes the configured value in the planned
     values that the engine gets, and is null in what the tool gets
     (decision 2). It never counts as a change.
   - A computed attribute is unknown on create and on replace. It keeps its
     prior value when nothing changes.
   - On an update in place, a computed attribute is unknown too, because an
     API can change values such as `updated_at`, an etag or a version on
     every write (PM, for the project owner). If the plan kept the prior
     value, OpenTofu would refuse the new value with "Provider produced
     inconsistent result after apply". The `id` and an attribute with the
     flag `stable` keep their prior values (ADR 171 decision 7).
   - An optional computed attribute takes the configured value when it is
     not null. Else it behaves as a computed attribute.
   - An unknown configured value counts as a change.
   - A changed attribute with `replace` makes the action a replace.
8. **The planner checks the configuration.** It returns
   `ErrInvalidConfig` for an attribute that the schema does not have, for a
   computed attribute that the configuration sets, and for a required
   attribute that is null. Data sources and provider configurations use the
   same check, `lifecycle.CheckConfig`.
9. **The errors of the port.** `ErrNotFound`, `ErrUnknownType`,
   `ErrInvalidConfig`, `ErrRejected` (the remote system refused the change)
   and `ErrUnavailable` (no answer after the retries). An engine wraps one
   of them. Each adapter maps them to the diagnostics of its tool.
10. **No call to the remote system during a resource plan.** `Plan` is a pure
    function. Only a data source read and a refresh call the remote system
    before an apply.
11. **Concurrent use.** Both tools apply objects in parallel. The types of a
    session are safe for concurrent use.
12. **A failed create or update keeps the object (PM, for the project
    owner).** When the remote system made the object and a later step
    failed, for example a second call or a read, `Create` returns the values
    that it has, with at least the `id`, together with the error. `Update`
    does the same when it applied a part of a change. `lifecycle.Created`
    reports whether such values hold an `id`. The adapters then:
    - OpenTofu: the adapter returns the values, through `ForTool`, as the
      new state together with the error diagnostic. OpenTofu keeps the
      object of a failed create as tainted, and replaces it in the next
      apply. After a failed update it keeps the returned values.
    - Pulumi: the adapter returns a partial failure, with the `id` and the
      values as properties, together with the reason. The Pulumi CLI keeps
      the object in state and applies the configuration again in the next
      update.

    Without an `id`, the adapter reports the error alone. The object is
    then not in state, so an engine returns an `id` as soon as the remote
    system gives one.
13. **No unknown value after an apply.** When the reply of `create` does not
    give every value that the remote system sets, the engine runs `Read`
    after `Create`. After an update it does the same for each value that the
    plan made unknown (ADR 171 decision 10, ADR 173 decision 6).

## Consequences

- A change to plan rules is one change in one function, with one table test,
  and both tools follow it.
- An engine cannot plan on its own. An API that changes a value on its own
  shows that value as drift in the next plan, after a refresh.
- A plan of an update shows each computed value that is not stable as
  "known after apply". A declaration marks a value `stable` to keep it in
  the plan.
- The conformance suite (ADR 179) drives a provider through this port only,
  so it runs without either tool.

## Open questions

- A composite import ID: an API that finds an object only by two values.
  See ADR 171.

- A custom diff. Some APIs normalize values, for example the case of a
  name. Version 1 shows such a value as drift. A later declaration key or
  an SDK hook can mark two values as equal.
- Timeouts for each operation. The engines have one timeout for each call
  (ADRs 173 and 174). OpenTofu and the Pulumi CLI also let a user set
  timeouts for each resource. The adapters can pass them on later.
