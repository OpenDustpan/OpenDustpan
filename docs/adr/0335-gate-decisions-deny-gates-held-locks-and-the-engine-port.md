# 335. Gate decisions: deny gates, held locks, unlocked declares and the engine port

Status: accepted · 2026-11-07. Decided by the project owner on 7 November 2026

Changes:

- Extends ADR 224 decision 13: a deny gate on a cancel or a force-unlock stays. A gate break-glass comes later.
- Changes ADR 224 decision 16: a state version with no lock through the TFE-compatible API meets `state.write` with the operation kind `unlocked-write`.
- Extends ADR 224 decision 9: the gate engine port stays pluggable, and Cedar is the default.
- Extends ADR 222 decision 7: Cedar is the default engine, and another engine can be an adapter.
- Extends ADR 230 decision 4: a save of a gate while locks are held answers 409.
- Extends ADR 44 decision 5: a state version with no lock meets the gates before it is written.

## Context

[ADR 224](0224-gates-at-fixed-decision-points.md) decision 16 puts the writes of a person's own CLI at the point `state.write`. A `deny` gate at that point refuses a Pulumi update cancel and a force-unlock. ADR 224 decision 13 gives a break-glass for a gate in error. It does not pass a `deny`.

The same decision lists the state version with no lock on the OpenTofu HTTP backend as `unlocked-write`. It does not list the same write through the TFE-compatible API. The guide for gates on state writes names that write as unchecked.

ADR 224 decision 16 also says that the server never refuses a state version that holds a lock which passed `state.write`. So a lock that a tool holds when a person saves a new gate was taken under the old gates.

ADR 222 decision 7 and ADR 224 decision 9 name Cedar as the default engine, with one linked adapter and one external adapter. They say that a later engine is a new adapter and a new ADR. They do not say that other engines stay possible.

## Decision

Decided by the project owner on 7 November 2026.

1. **A deny gate on a cancel or a force-unlock stays (owner, 2026-11-07).** A `deny` gate at `state.write` can refuse a Pulumi update cancel or a force-unlock. The gate keeps its effect. No rule passes it. The way out is to edit the gate. The edit needs the actions and the fresh authentication that ADR 224 decision 12 gives.
2. **A break-glass for a gate comes later (owner, 2026-11-07).** A break-glass for a `deny` gate, with a review after use, is a later feature. Until a later ADR accepts it, the break-glass of ADR 224 decision 13 passes the gates in error only.
3. **An unlocked state version on the TFE-compatible API (owner, 2026-11-07).** Such a write meets `state.write` before its transaction. Its operation kind is `unlocked-write`. A refusal refuses the write. This is the rule that ADR 224 decision 16 already gives for the HTTP backend. The refusal can leave resources that no stored state records. The record and the error say so, as they do for the HTTP backend.
4. **A held lock outlives a new gate (owner, 2026-11-07).** A lock that is held when a gate is saved is not changed by that gate. A new gate applies at the next lock.
5. **A save needs a flag when locks are held (owner, 2026-11-07).** Locks can be held on units in the scope of a gate. Then a save of the gate needs an explicit flag in the request. Without the flag, the save answers 409 and changes nothing. The answer lists the held locks: for each lock, the unit, the holder and the time. With the flag, the save goes on. The audit entry names the held locks.
6. **Docs and UI make this very obvious (owner, 2026-11-07).** The user documentation for gates has a section on held locks. The console shows the held locks before a person saves a gate. It also shows that the gate does not apply to them.
7. **The gate engine port stays pluggable (owner, 2026-11-07).** The port `gate.Engine` of ADR 224 decision 9 stays the one place where an engine plugs in. Cedar is the default engine. OPA, Expr or another engine stays possible as an alternative adapter of the same port. An adapter must run the conformance suite `pkg/gate/gatetest`. A new adapter needs its own ADR, as ADR 224 decision 9 says.

## Consequences

- A person who wants to pass a `deny` gate on a cancel or a force-unlock edits or removes the gate. Each such edit writes an audit entry.
- The route test lists the TFE-compatible state version route with no lock as a route at `state.write`. The e2e suite gets a step with the real OpenTofu CLI first. The guide `docs/guides/state-write-gates.md` moves this write from the unchecked list to the checked list.
- The 409 answer needs a new item in the error table of ADR 230 decision 4. The API description names the code and the flag.
- This ADR does not decide if the flag rule covers a gate at a decision point other than `state.write`.
- This ADR does not decide which kinds of lock the 409 answer lists. Tool locks and manual locks on units in the scope of the gate are the two kinds in question.
- This ADR does not decide when a gate break-glass comes, or what its review is.
- This ADR does not name or adopt any engine other than Cedar.
