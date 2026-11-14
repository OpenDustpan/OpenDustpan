# 342. Gates read state facts for three more writes

Status: accepted · 2026-11-07. Decided by the project owner on 7 November 2026

Changed by:

- ADR 365 changes decision 3: a wording refinement, a client-encrypted
  OpenTofu state is not a read failure.

Changes:

- Changes ADR 224 decision 16: the `state` group has facts for three more writes.
- Extends ADR 226 decision 4: `state.available`, `state.resources` and `state.removed` for the OpenTofu HTTP backend unlocked write, the Pulumi stack import and the Pulumi restore.

## Context

[ADR 224](0224-gates-at-fixed-decision-points.md) decision 16 says that the `state` group has facts only for a tool-neutral operation. There the server holds both the current and the new state. For a write from a person's own CLI, `state.available` is `false`. A gate that reads `state.removed` then never matches.

[RFC 119](../rfc/0119-gates-on-facts-that-an-operation-does-not-fill.md) names three writes where the server holds a new state and the facts stay empty. They are the unlocked write on the OpenTofu HTTP backend, the Pulumi stack import and the Pulumi restore. The restore of a state version through the TFE-compatible API is already filled.

## Decision

Decided by the project owner on 7 November 2026.

1. **Three writes fill the state facts (owner, 2026-11-07).** For these writes, the server sets `state.available` to `true` and fills `state.resources` and `state.removed`:
   - the unlocked write on the OpenTofu HTTP backend (operation kind `unlocked-write`),
   - the Pulumi stack import (operation kind `state-import`),
   - the Pulumi restore of a stack version (operation kind `rollback`).
2. **The server reads the body with limits (owner, 2026-11-07).** The server reads the new state from the body before the gate runs. The read has a size limit, a depth limit and a time limit. The limits are settings. The API shows their effective values. The server refuses a request that is over a limit.
3. **The read fails closed (owner, 2026-11-07).** The server may fail to read the body or to count the resources in it. Then it does not set `state.available` to `true`. It refuses the write, and the error names the reason. It never allows the write with empty facts. A state in the client encryption envelope of OpenTofu is not a read failure. For such a state, the server sets `state.available` to `false`, and the decision record says `state is client-encrypted`. The write goes on under the gates that apply, as for the restore of a state version.
4. **Counts follow the earlier rule (owner, 2026-11-07).** `state.resources` counts resource instances, and `state.removed` names instance addresses, as for the restore of a state version.

## Consequences

- The guide `docs/guides/state-write-gates.md` shows the new rows of its matrix of operations and facts.
- Each path lands with its e2e step against the real CLI first (CLAUDE.md). Each step saves a gate that reads `state.removed` and checks that it matches.
- The server reads the body before the write. This is a new read path of untrusted input, so the defense-in-depth rules apply. The rules need a check at the boundary and limits on size, depth and time. They also need a check of authorization at the point of use and an audit entry for a denial. Each step has its own test.
- This ADR does not decide options B, C and D of RFC 119. A gate that reads an unfilled fact in another operation still evaluates as the zero value. RFC 119 stays open for those options.
- This ADR does not decide the values of the limits.

## Changelog

- 2026-11-13, wording refinement (ADR 365), with the owner's word ("A lgtm"):
  decision 3 now says that a state in the client encryption envelope of
  OpenTofu is not a read failure. ADR 150 decision 9 and ADR 255 already keep
  such a state unread, and the restore of a state version already goes on with
  the facts unavailable.
