# 364. Unexported fields and golangci-lint before added checks (owner)

Status: accepted · 2026-11-10. Records two decisions of the project owner of 10 November 2026, in the owner's words

Changes:

- Extends ADR 272 decision 4: the care for the exported surface applies to packages under `internal/` too, not only to the SDK.

## Context

On 8 November 2026 the owner asked the project to stop bug classes that came back after each fix. The owner also asked for a code structure that makes a bug class impossible, in place of more checks and more tools. One example is a state version that only test hooks wrote: an exported setter let a test stand in for the product path. The implementation lead proposed two guards. The first guard is unexported fields, so that only a constructor can make a value, and the constructor checks its invariants. The second guard is the existing golangci-lint configuration, for what visibility cannot express.

## Decision

1. **Visibility first, then golangci-lint (owner, 2026-11-10).** The owner said:

   ```text
   "It's called golangci-lint and private structs/fields"
   ```

   ```text
   "This is a decision and it must also hold going forward. Don't get me
   wrong, I hate Java but we need to be more conscious of our public APIs
   even internally to prevent this kind of mess."
   ```

   The guard against a bug class is Go visibility first. Fields are unexported, so that only a constructor can make a value, and the constructor checks the invariants of the value. Rules that visibility cannot express go into the existing golangci-lint configuration (for example `forbidigo`, `depguard` and `exhaustruct`). This decision adds no custom analyzer and no new tool.

2. **The tactical core of domain-driven design (owner, 2026-11-10).** The implementation lead proposed to take the tactical core of domain-driven design and not the full method. The owner answered:

   ```text
   "Agreed"
   ```

   The proposal that the owner agreed to has these points:
   - A domain type such as a group, a principal or a job is an aggregate with private state. It changes only through its own methods, which enforce its invariants. No code outside its package makes or changes it field by field. This is decision 1 applied to the domain.
   - IDs, names, versions and cursors are value objects: small types that code cannot make in a state that is not valid. The stable IDs of ADR 362 become types, not strings.
   - Domain events stay where they exist today: the audit log and the inventory projection. Each event is written in the same transaction as its write.
   - Event sourcing, CQRS, generic repositories and layers for their own sake are out.
   - Each design names the aggregate that it protects and the invariants of that aggregate.

## Consequences

- Other parts of domain-driven design are in place already. They are the glossary, the separate tool models (ADR 12 and ADR 13), the ports and adapters, and the sentinel errors.
- Each exported name is an API, also between packages under `internal/`. A design names its exported surface and keeps all other names unexported.
- Each OpenSpec design names the unexported type or field that makes its bug class impossible, and the golangci-lint setting for what remains. It also says how a rewrite of many call sites is done: `go fix` with `//go:fix inline`, gopls, `eg` or a script first, then a model for the rest.
- A rule from this ADR that golangci-lint cannot express is a finding for the owner, not a reason to add a tool.
