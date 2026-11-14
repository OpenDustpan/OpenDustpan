## Context

The clientgen conformance process helper keeps one portable wait loop with no build tag so the package has no other-platform file and no per-OS copy.

## Decisions

1. **One portable wait loop with no build tag.** Delete internal/clientgen/conformance/process_group_other.go. Move endGroupAfterExit to the NEW file internal/clientgen/conformance/process_group.go with no build tag and a portable implementation that uses only the standard library and no waitid constants. Remove the per-OS copies and the unused waitid constants, and delete process_linux.go and process_darwin.go when they hold no other code after the removal.

## Structure

One unexported function endGroupAfterExit in the NEW file internal/clientgen/conformance/process_group.go with no build tag; it is the only definition in the package and it references no waitid constant.

## Risks

- The portable wait must match the Linux and macOS behavior on exit and on stop.
- A second copy of the loop can return without notice.

## Diagrams

Modules that the change touches, and their imports on main:

```mermaid
flowchart LR
P1[internal/clientgen/conformance]
```

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| none affected | Internal refactor with no user-visible behavior or budget change |  |  |
