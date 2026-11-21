## Context

A developer runs stacks and workspaces and wants to see what a change will cost before apply. The server gives no spend context, so cost review happens outside OpenDustpan or not at all.

This change does the RFC 67 research on spend and cost tools first, with no dependency on RFC 45. It adds no routes, no store, no background work, and no console view. It changes no server code now.

## Decisions

1. **Research first, no integration yet.** The research names the spend sources, the cost tools, and the trust bounds before any route or store exists. Cost estimation (RFC 45) builds on the research result; the research does not depend on the estimation.
2. **No spend route in this change.** A request for spend answers 404 with no spend data until the research-driven design lands. No placeholder route appears that a client could depend on.
3. **No background collection.** No scan, sync, or price fetch runs in this change. Collection scope ships with the research result, so no unattended work starts before its bounds are set.
4. **Drift, existing CI, and the programmatic runs API stay out.** They refine on their own paths; cost research reads their records but changes none of them.

## Structure

No new structure now. The bug class is a spend number without a named source and time; a research record that names each source, its freshness, and its trust bound is the structure that removes it. When the research ends, a spend figure is an unexported value built only through its constructor, which refuses a figure without source and time.

No custom analyzer and no new tool.

## Risks

- Developers keep reviewing cost outside OpenDustpan until the research lands. The CLI path (preview and apply as today) is unchanged, so nothing is lost.
- Research blesses a tool whose prices the server cannot verify. The trust bounds name what the server checks and what it takes as given.
