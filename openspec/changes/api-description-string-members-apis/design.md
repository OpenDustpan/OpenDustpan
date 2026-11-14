## Context

The API description reflector emits an open enum for a named Go string type with a DeclareOpenEnum entry in the route label; plain string stays type string. Runtime decode, stores, and validation keep main behavior and never reject unknown values.

## Decisions

1. **Named open-enum types with unchanged runtime acceptance.** Add a named Go string type with exported constants and a DeclareOpenEnum label entry for each member on the closed member-level convert list; keep every other string member as plain string; keep decode as string-based so unknown and empty values round-trip as main does; add no new rejecting validation and no new error code; regenerate the description with unchanged wire values.

## Structure

Named string types in each domain package with exported constants and string-based decode; the reflector declaration reads the constants while decode and state machines keep main behavior.

## Risks

- Generated client type names change while wire values stay unchanged.

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| none affected | Description-only change; no request path latency or storage target changes |  |  |
