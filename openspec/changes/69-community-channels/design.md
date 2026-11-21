## Context

A developer who adopts OpenDustpan tries to ask a question or report a problem and needs to know where the community meets. No community forum or chat exists yet.

This change records that RFC 69 waits on the owner to set up the channels. It changes no server code, no route, and no schema, now or at the gate.

## Decisions

1. **No code at any step.** Community channels are rooms and forums outside the server, not routes, views, or store changes. No change in this path ever touches the server.
2. **GitHub Issues stays the support path.** Until the owner sets up channels, the support section of the docs names GitHub Issues (`gh issue create`) as the one place to ask and report. No page names a forum or chat link that does not exist.
3. **The owner sets up the channels.** Scope, moderation, and links ship when the owner acts, not through a code change.

## Structure

No new structure exists or is planned. The bug class is a docs page that links to a room that does not exist; one support path with no dead links is the structure that removes it.

No custom analyzer and no new tool.

## Risks

- A developer finds no forum and leaves. The support section answers in one line where to ask, so the path costs one click.
- A page gains a chat link before the room opens. The docs check finds pages that name unsupported steps, and review refuses dead links.
