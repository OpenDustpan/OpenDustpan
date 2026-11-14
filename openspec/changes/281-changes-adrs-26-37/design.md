## Context

Docs-only fix for ADR change links. ADR 281 replaced the write-once blob-key rule with one key per attempt where the database row decides the winner. ADRs 26, 37, 43 and 60 still state the old rule with no Changed-by link.

## Decisions

1. **Backfill links only.** Add one Changed-by line with verb Changes to each of docs/adr/0026-opentofu-state.md decision 2, docs/adr/0037-checkpoints-in-blobs-and-delta-uploads.md decision 2, docs/adr/0043-pulumi-journaling-support.md with no decision number, and docs/adr/0060-audit-log-port-and-log-sinks.md decision 18. Add matching Changes lines to docs/adr/0281-any-s3-compatible-store-the-database-decides-which-write-wins.md mapping decision 11 to ADRs 26, 37 and 43 and decision 12 to ADR 60. Edit no decision text and no status line, then run make adr-index. Confirmed by: the driver's gates.
2. **Fix boundary for other ADRs.** Stay at the four ADRs of the issue plus ADR 281. ADR 41 decision 11 goes to a follow-up issue. ADR 280 and ADR 298 get no link. Confirmed by: the driver's gates.

## Structure

No code structure changes. The guard is make docs-lint, which fails on a link with no reverse item.

## Risks

- A wrong decision number in a link line sends the reader to the wrong rule.
- A hand edit of the generated index diverges from the generator.

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| none affected | Docs-only change with no runtime path |  |  |
