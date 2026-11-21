## Context

A developer keeps code outside git and wants a run from that source with the same record as a git run. Git is the only accepted source until RFC 54 is accepted.

This change names RFC 54 as a later change that waits on runs and fixes no source kind yet. It changes no server code, no route, and no schema now.

## Decisions

1. **No code now.** The accepted source set ships with the run model. No source kind is fixed before runs exist to record it.
2. **Git stays the only accepted source.** The namespace source filter keeps its allowlist, and a value outside it is refused with 400 and `source.filter-invalid`. The console shows the refusal; nothing is stored.
3. **Forge connections and webhook handling stay out.** They refine on their own path after the source kinds are fixed.
4. **A run from a new source leaves the same record as a git run.** Same actor, same audit entries, same fence checks. The source kind changes where code comes from, not what the record holds.

## Structure

No new structure now. The bug class is a run whose source the record cannot name; the source-filter allowlist plus one run record shape is the structure that removes it. When runs ship, an accepted source is an allowlist value built only through its constructor, which refuses any kind outside the refined set.

No custom analyzer and no new tool.

## Risks

- Developers with code outside git wait until runs land. The docs page says git is the only accepted source and names the refusal.
- A new source kind skips the audit record. The one run record shape carries the source kind, so a run without it is refused.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| handler | Refuse a source-filter value outside the allowlist with 400 and `source.filter-invalid`; change nothing. | Write a source outside the allowlist; the answer is 400 and the filter is unchanged. |
| service | Carry the source kind on each run record with actor, audit entries, and fence checks. | Post a run without a source kind; the server refuses it. |
