## Context

A developer manages stacks and workspaces across two tools and tries to ask one agent for status and safe next steps without learning two CLIs.

This change records the Later intent for one AI agent for infrastructure across both tools, with the dependency on the interface for AI agents and access tokens. It defers all behavior, routes, and audit shape to the RFC. It changes no server code, no route, and no schema now.

## Decisions

1. **No code now.** The agent ships after the agent interface (from 41-interface-ai-agents) plus read-only and access tokens (RFC 20) land. No agent behavior lands before its credentials exist.
2. **Read through the tool-neutral API.** The agent calls the same REST API as other callers, with an access token and per-call grant checks. No tool-specific agent path appears.
3. **Tool models stay separate.** The agent reads stacks and workspaces as units (tool plus namespace plus the tool's own name) and never merges the models. Status answers keep each tool's own words.
4. **Each automatic action leaves a record.** The later refinement names the audit shape for agent acts: who or what did it and why, on each act.

## Structure

No new structure now. The bug class is an agent act without a record or with a merged tool model; the shared inventory projection plus a per-act audit record is the structure that removes it. When the gates pass, an agent act is an unexported value built only through its constructor, which checks the token scope, the unit grant, and the audit fields.

No custom analyzer and no new tool.

## Risks

- Developers wait for the agent until the interface and tokens land. The docs page says the agent path is Later and points at the CLI and console until then.
- An agent suggests a risky next step the caller cannot preview. The refinement keeps preview and undo rules from the delight checklist: a risky action is previewed or says plainly that it cannot be undone.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| handler | Check the grant for the requested action on the object before any agent read or act. | Ask the agent path without the grant; the answer is a refusal, not data. |
| domain | Keep tool models separate in each answer; name the tool, the namespace, and the tool's own name. | Ask for status across tools; the answer never merges two units into one. |
| service | Write an audit record for each automatic act with actor and reason. | Run an agent act; the audit log names the act, the actor, and the reason. |
