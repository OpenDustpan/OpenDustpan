## Context

This part adds module publish checks and module serve routes beside the provider routes of the split. It reuses the publish API shape, signing keys, grants, audit, and version state from the provider part, with module archive layout and module registry protocol mapping.

## Decisions

1. **Module serve adapter reuses registry domain state.** Add a module serve adapter beside the provider serve adapter. Share the stored release record, key check result, and version state through the registry domain port. Serve service discovery with modules.v1 from the same adapter. No shared fetch path with other package mirrors.
2. **Same not-found answer for cross-namespace reads.** Hide cross-namespace existence by mapping a cross-namespace address to the same not-found answer as a missing module in the caller namespace, with an audit entry that keeps the requested address server side only.

## Structure

Registry domain keeps unexported type moduleAddress with constructor parseModuleAddress that checks host, namespace, and registry-safe name; only the constructor builds a value, so malformed or cross-namespace addresses cannot reach storage.

## Risks

- Module address confusion across namespaces causes a wrong download.
- An unpublished or blocked version stays reachable through a cached index answer.
