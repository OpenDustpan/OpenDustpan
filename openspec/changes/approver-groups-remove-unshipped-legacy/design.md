## Context

Approval rules in pkg/gate store group names with group IDs. The read path refreshes names through currentNames. Job start checks approvers.

## Decisions

1. **Paired IDs, name-only reads, marked deletions.** Stored rules require paired IDs and names with one constructor check. Legacy no-ID branches are deleted. Reads use a name-only directory lookup keyed by stored group ID. Saves keep member counting. A live renamed group shows the current name. A missing group shows the stored name with the NEW boolean field deleted set true. Job start refuses a deleted group. PUT stays writable on a legacy stored rule so the rule can be rewritten. Job start rechecks each group ID at start time.

## Structure

Unexported approver store shape with constructor that checks len(GroupIDs) equals len(Groups); only the constructor makes a stored value.

## Risks

- A stored rule with the unpaired legacy shape fails the whole gate read with a validation error until rewritten through PUT.

## Diagrams

After this change:

```mermaid
sequenceDiagram
 participant C as Client
 participant H as Handler
 participant S as ConfigService
 participant D as groupDirectory
 C->>H: PUT gate with approver groups
 H->>S: validate paired IDs and check existence
 S->>D: name-only lookup by group ID
 D-->>S: name or not-found
 S-->>H: accept or refuse
 H-->>C: gate read with name and deleted mark
```

Modules that the change touches, and their imports on main:

```mermaid
flowchart LR
P1[pkg/gate]
```

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| none affected | Short low-risk cleanup with no latency or storage target change |  |  |
