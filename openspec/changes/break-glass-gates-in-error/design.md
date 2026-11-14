## Context

state.write evaluates gates before each tool write. An engine error in enforce mode stops every write of the scope until a person saves the gate again. The change adds a narrow single-use pass for that error case.

## Decisions

1. **Break-glass as checked value.** Carry break-glass on the change object as a value type built by one constructor that checks action grant, reason length, fresh authentication and pass reuse, so an unchecked pass cannot be built. The holder key is provisional pending the owner answer on subject binding.
2. **Single-use timed pass.** Store one pass row keyed by holder, unit stable ID and exact operation kind with expiry 15 minutes on the store clock and used flag; consume it with a conditional update in the same transaction as the state write and the override audit entry, so only one concurrent caller wins and a crash before commit leaves the pass unused.

## Structure

Unexported type breakGlassPass with constructor newBreakGlassPass that checks grant, reason, fresh auth and single use.

## Risks

- Pass reuse across units or operations if key is too wide.
- Deny bypass if error path also passes deny outcomes.
- Clock skew if expiry uses server clock instead of store clock.
- Pass consumed but write lost on crash before commit.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| handler | Boundary checks reason text, fresh authentication proof and namespace:break-gate-error grant at the API handler. | Send empty reason, overlong reason, control characters, stale auth and no grant directly to the handler; each gives 403 or 422 and no pass row. |
| service | Service consumes the pass only for result error at state.write, only for same holder unit and operation kind, only once within 15 minutes on the store clock. | Replay the same pass for a second write and for another unit; both fail closed with gate.ErrRuleFailed. |
| store | Store guard writes audit entry of kind override with principal, reason and gate IDs in the same transaction, and the next decision record names the pass. | Remove the audit write and the record link; the conformance test fails on missing override entry. |
