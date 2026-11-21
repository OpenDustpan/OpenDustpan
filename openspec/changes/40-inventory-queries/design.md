## Context

A developer tries to find stacks and workspaces across tools in one namespace. The inventory has no rich query yet, and the query shape waits until the projection scales.

This change defines what an inventory query covers once the inventory projection (ADR 13) scales. It keeps the inventory as a read projection with no new writers. It changes no server code, no route, and no schema now.

## Decisions

1. **No code now.** Query filters, sort keys, and page shape ship after the projection scales. The current unit list answers as today until then.
2. **Read projection only.** Queries read the projection; no query writes to it. Scale work on the projection itself stays in its own path.
3. **No merged unit model.** The query lists stacks and workspaces as units (tool plus namespace plus the tool's own name) and never merges tool models. No new tool model appears for the query.
4. **Named 400s for bad input.** A bad filter value, cursor, limit, sort key, or time range answers 400 with the field name. An invisible namespace answers 404 with no unit data.
5. **No console views and no generated clients in this change.** They follow the query shape after it is fixed.

## Structure

No new structure now. The bug class is a query that fans out to live stores instead of the projection; one read projection with no new writers is the structure that removes it. When the projection scales, a query is an unexported value built only through its constructor, which checks filter names, sort keys, page limits, and the time range.

No custom analyzer and no new tool.

## Risks

- Developers wait for rich queries until the projection scales. The current list plus the docs page for supported filters cover basic finds until then.
- A query across tools merges two units into one answer. The unit reference (tool plus namespace plus name) keeps each unit distinct.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| handler | Check the namespace grant before any list; answer 404 with no unit data when the caller may not see the namespace. | List units of a hidden namespace; the answer is 404 with no data, not an empty list. |
| domain | Refuse unknown filters, sort keys, and out-of-range pages with 400 and the field name. | Ask with a bad sort key; the answer names the field and changes nothing. |
| store | Read from the projection only; carry no write path for queries. | Trace a query; it touches the projection and no live store write. |
