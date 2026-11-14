## Context

Triggers are stored per unit and start plan jobs through the job service. Push intake, the schedule tick and the follows chain live in their own changes and call the plan-only start of this change.

## Decisions

1. **Service checks before write.** Store trigger rows per unit with kind, schedule fields, source unit stable ID, paths list and last saver. Validate shape and same-namespace rule in the service before write.
2. **Plan-only start port.** Start path calls only the plan-only port of the job service. It passes source kind schedule, forge or trigger and the namespace service principal. It never passes an apply operation.

## Structure

Unexported trigger.Service built only by NewService with a plan-only Starter interface and a fenced Store; no public setter alters kind or operation after construction.

## Risks

- A caller bypasses the plan-only port and starts an apply.
- A trigger row names a source in another namespace.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| port adapter | Service validates kind, cron shape, interval, paths and source unit reference before write. | Save a trigger with a bad kind, a short cron interval and a cross-namespace source; each save is refused. |
| service | Job start uses only the plan operation, pool from unit settings, and namespace service principal with stored plan-start grant. | Request an apply operation through the trigger start port and a start with a caller principal; the port refuses both. |
| HTTP handler | Route requires sign-in with unit manage action on the unit for create, replace and delete, unit read action for list and read, and records an audit entry with actor and trigger id. | Call create and fire without the action and with a forged namespace; the route returns 403 and writes a denial entry. |
