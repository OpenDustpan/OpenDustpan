# 364. Unexported fields and golangci-lint before added checks (owner)

Status: accepted · 2026-11-10. Records one decision of the project owner of 10 November 2026, in the owner's words

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

## Consequences

- Each exported name is an API, also between packages under `internal/`. A design names its exported surface and keeps all other names unexported.
- Each OpenSpec design names the unexported type or field that makes its bug class impossible, and the golangci-lint setting for what remains. It also says how a rewrite of many call sites is done: `go fix` with `//go:fix inline`, gopls, `eg` or a script first, then a model for the rest.
- A rule from this ADR that golangci-lint cannot express is a finding for the owner, not a reason to add a tool.
