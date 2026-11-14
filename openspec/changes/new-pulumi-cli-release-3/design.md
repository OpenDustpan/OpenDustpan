## Context

New stable releases of the Pulumi CLI and OpenTofu need a contract check against the server. The work area is `e2e/contract` and `e2e/doc.go`.

## Decisions

1. **Contract update per release.** Run the e2e suite once per version, then once with contract update enabled, and commit each new file in `e2e/contract/`. Apply the matrix policy in `e2e/doc.go`.

## Structure

No new type. The check reuses the existing e2e contract recorder and the matrix policy in `e2e/doc.go`.

## Risks

- A new CLI read stays unrecorded if the update run is skipped.

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| none affected | e2e-only contract check with no server behavior change |  |  |
