## Context

Namespace administrators set the default sharing of new stacks and workspaces and write one-step namespace and collection rules. The job-token read path checks the rule and the starter grant at each read.

## Decisions

1. **Default as setting, rules by stable ID.** Store the new-unit default as a namespace setting with effective value visible in the settings read. Store each sharing rule on the producer unit with stable unit ID, collection ID, or namespace ID, never a name.
2. **Check at read time, fail closed.** Check both conditions at each output read on both tool paths and on the tool-neutral outputs route. Deny closed on missing rule, missing grant, unknown object, cross-namespace target, or in-flight change.

## Structure

Unexported outputSharingRule with constructor that checks producer ID, target scope, target stable ID, creator, and time; only the constructor builds a valid rule.

## Risks

- A collection rename or re-create must not widen sharing: rules use stable IDs.
- A rule change during a job changes the next read, which operators must see in the audit log.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| handler | API handler rejects unknown scope, unknown ID, and cross-namespace target with 400 or 403 before any read. | POST a rule that names a unit in another namespace and read across namespaces, expect deny. |
| service | Service resolves stable IDs, checks rule match and starter unit:read-outputs on the producer at each read. | Remove the rule after job start then read again, expect deny; remove the grant then read again, expect deny. |
| store | Store holds rules by stable ID with fence check in the same transaction as unit writes. | Rename a collection then read with the old name, expect the stored ID rule to still match only its members. |
