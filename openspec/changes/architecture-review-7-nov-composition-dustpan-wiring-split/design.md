## Context

Explicit-argument areas exist but most wiring stays in pkg/dustpan/dustpan.go, so every feature edits the hot file.

## Decisions

1. **Area-by-area move with ordered calls.** Move one area per pull request from NewServer and newAdapters into its NEW area file as a plain function with explicit arguments. NewServer keeps one ordered call list in unchanged order. No init, no area type, no body edits. Move 2 waits for the architecture review of move 1.
2. **Narrow arguments and same order.** Each area function takes only the services, config values and handles it wires, and returns its adapters and an error. It takes no shared settings object. Startup and shutdown order stay as in NewServer today.

## Structure

Plain functions per area with explicit arguments; no unexported area type; NewServer as the single ordered call list.

## Risks

- An accidental reorder of calls changes startup behavior.
- Widened arguments pass unneeded state to an area.

## Diagrams

Modules that the change touches, and their imports on main:

```mermaid
flowchart LR
P1[internal/servertest]
```

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| none affected | No behavior change; same call order and same routes |  |  |
