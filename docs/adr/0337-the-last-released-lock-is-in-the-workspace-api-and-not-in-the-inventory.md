# 337. The last released lock is in the workspace API and not in the inventory

Status: accepted · 2026-11-07. Decided by the project owner on 7 November 2026

Changes:

- Extends ADR 13: applies the rule of the shared core to the field `lastRelease` of the inventory entry.
- Changes ADR 298 decision 9: the workspace API shows the record of the last released lock. The inventory does not.

## Context

ADR 13 keeps the tool models apart and makes the inventory a tool-neutral read model. CLAUDE.md says the same: no field in a shared package may exist only because one tool needs it.

ADR 298 decision 9 keeps a record of the last released lock on the workspace row. A repeated unlock after a lost answer then succeeds. The inventory entry of a unit has a field `lastRelease` with this record. The field is only for an OpenTofu workspace. It is never there for a Pulumi stack. So it is a tool-specific field in a tool-neutral entry.

On 7 November 2026 the project owner decided this point.

## Decision

Decided by the project owner on 7 November 2026.

1. **`lastRelease` moves to the workspace API (owner, 2026-11-07).** The API of the workspace shows the record of the last released lock. The record holds the lock ID, the holder, the releaser, the time and the kind, `normal` or `forced`. The inventory entry of a unit does not have the field.
2. **The rule of ADR 298 does not change (owner, 2026-11-07).** The record is still kept on the workspace row. It still matches for 24 hours after the release. A repeated unlock still uses it. Only the API that shows it changes.

## Consequences

- The tool-neutral inventory API loses the field `lastRelease`. The generated clients lose it too. The console reads the record from the workspace API.
- The inventory projection no longer carries the record. A tool package publishes to the inventory only fields that every tool has.
- The inventory reference page, `docs/reference/inventory-api.md`, has a row for `lastRelease`. The code change removes that row and adds the field to the page for the workspace API.
- The tool-neutral API can break before the first tag. The change needs no alias and no compatibility code.
