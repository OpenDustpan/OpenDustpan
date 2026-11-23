## Context

The change keep-two-working-break-glass owns the count, the guard and the add route. The first-setup gate needs a setup-finish flow, and main has none. This change waits on that flow and changes no code now.

## Decisions

1. **No code now.** The gate ships with the setup-finish flow. This placeholder only holds the requirement and its test that moved out of keep-two-working-break-glass.
2. **One count.** The gate calls the one count function of the change keep-two-working-break-glass and does not count a second way.
3. **Fail closed.** A finish with fewer than two working accounts answers 409 with break-glass.setup-incomplete and writes nothing.

## Structure

No new structure now. The refinement names the finish step and the lock order that it shares with the restore swap.

## Risks

- Two finishes run together or the process stops during finish. The refinement must keep setup unfinished until the count is two.
- Other e2e steps fail at first setup. The shared e2e fixture makes two accounts.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| service | The finish step counts working accounts in its own transaction. | Finish with one account. The service refuses. |
| store adapter | The store counts again in the transaction that marks the setup finished. | Call the store directly to mark finished with one account. The store refuses. |
| HTTP handler | The handler maps the refusal to 409 with the missing count. | Send a finish with one account. The answer is 409. |
