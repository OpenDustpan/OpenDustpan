## Context

Test harness only in internal/clientgen/conformance/fixtures.go: the per-URL fallback seed state caches a failed write error for the life of the process and is never cleared, so a reused test port skips seeding.

## Decisions

1. **Key by server instance, do not cache failures, clear on close.** Key the fallback entry by the server instance handle plus its address. Never store a failed write result, so the next ensureProfile call retries. Keep the success entry once per live instance. Delete the entry when that server closes through its existing test cleanup. A generation counter stops a late success after close from storing an entry again. A seed conflict is re-read and then counts as success.

## Structure

Unexported fallback seed cache in package internal/clientgen/conformance with a constructor that creates one entry per live server instance and an accessor that returns only a live entry. A failed write leaves no entry, and server close deletes the entry.

## Risks

- A second server on a reused port inherits the first seed done flag and never posts its seed profile.
- A failed seed write poisons later calls for the same URL.

## Diagrams

Modules that the change touches, and their imports on main:

```mermaid
flowchart LR
P1[internal/clientgen/conformance]
```

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| none affected | Test harness only with no user-visible behavior change |  |  |
