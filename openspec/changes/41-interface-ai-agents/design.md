## Context

An agent program uses the REST API with an access token and tries to list stacks and workspaces, read history, and read state without seeing plain secret values.

This change records the coarse scope of the AI agent interface: read through the REST API with an access token. It defers all behavior to RFC 41 once it is accepted. It changes no server code, no route, and no schema now.

## Decisions

1. **No code now.** Agent reads ride the existing REST API and the existing caller-token routes. No agent route appears before RFC 41 is accepted.
2. **Read only, no write path.** The agent lists units, reads history, and reads masked state. A request for a plain secret value is refused. Read-only tokens (RFC 20) refine on their own path.
3. **Grant check on each call.** The server checks the grant for the requested action on the object. An invalid token answers 401 with `token.invalid`; an ended token answers 401 with `token.ended`; and each check changes nothing.
4. **Secret masking as ADR 41 requires.** State reads replace each secret value with a marker. Reveal stays a separate call with its own grant.
5. **Agent state reads reuse the bounds of 39-reading-state-contents.** No second read shape appears for agents.

## Structure

No new structure now. The bug class is an agent read that bypasses a grant check or sees a plain secret; the existing grant check plus masked reads on the same routes is the structure that removes it. When RFC 41 is accepted, the agent scope is an unexported read-scope value built only through its constructor, which accepts read actions only and refuses any write action.

No custom analyzer and no new tool.

## Risks

- An agent with a read grant reads more than the holder intended. Grants stay per-action and per-object, so the holder narrows the grant.
- An ended token reads during its expiry window. Short token lifetimes plus the ended check bound the window.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| handler | Check the grant for the requested action on the object before any read; answer invalid and ended tokens with 401 and the named code. | Read with an ended token; the answer is 401 with `token.ended` and no data. |
| domain | Replace each secret value with a marker on each state read; refuse plain secret values. | Read state that holds a secret; the answer shows the marker only. |
| service | Resolve the caller from the access token; fail closed when the token is unknown. | Present an unknown token; the server denies without touching state. |
