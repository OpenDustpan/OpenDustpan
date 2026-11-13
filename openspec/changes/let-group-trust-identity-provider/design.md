## Context

Trust and full sync both feed one membership function.

## Decisions

1. **Trust as checked state.** An unexported groupTrust value is made only by a constructor that checks reach cover and fresh authentication. The write runs in one transaction on the group row, so changes serialize and the last commit wins.
2. **Full sync as setting input.** The membership function reads full sync from the settings resource. Only the API sets it.

## Structure

Unexported groupTrust type with a constructor. Stored references use stable IDs. No new tool. Schema edits go in place in the baseline.

## Risks

- A trusted group widens access.
- Full sync makes each removal final.
- Baseline edits need a dev reset on a dev installation.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| HTTP handler | Checks the action, the fresh step and the input. | Calls with no fresh step, no factor and bad IDs get 403 or 400. |
| service | Checks reach and fresh authentication. | Stale step and uncovered reach are refused. |
| port adapter | Write in one transaction on the group row. | Two parallel writes leave one stored value. |
