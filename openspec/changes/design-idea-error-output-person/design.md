## Context

Error answers already carry stable codes and arguments. The 1am-readable summary, named upstream detail, and request link are a backlog idea for tool-neutral shapes only, decided by a later RFC.

## Decisions

1. **Backlog idea with no implementation.** Record the idea only. No wire change, no constructor, no verbose flag, and no console view in this change.

## Structure

No new structure in this change. A later RFC names the unexported error type and its constructor.

## Risks

- Upstream detail leaks a secret if placed in the summary.

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| none affected | Backlog design idea with no behavior change in this change |  |  |
