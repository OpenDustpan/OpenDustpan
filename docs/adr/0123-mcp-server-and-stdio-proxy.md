# 123. An MCP endpoint and a stdio proxy offer the same tools

Status: accepted · 2026-10-17. The decisions of the project owner and the
project manager below are accepted. The other decisions take effect when the
project lead accepts the pull request of this ADR.

## Context

The project owner decided that the task-shaped tools of the built-in agent
(ADR 122) are also offered over an MCP server and a CLI stdio proxy. Outside
agents get the same tools, the same act-as and the same audit log as Dusty.
[RFC 41](../rfc/0041-agent-interface.md) asks whether the MCP server is part
of the binary or a separate program.

The Model Context Protocol (MCP) has two transports: stdio, for a server
that the client starts as a child process, and Streamable HTTP, for a
server at an address. Many agent clients support only stdio.

ADR 72 makes the instances stateless. Any instance must answer any request.

The project manager decided on 2026-10-17, for the owner: the MCP endpoint is
off by default and an administrator turns it on, and the official MCP Go SDK
is approved, pinned to a release that is at least two weeks old.

## Decision

1. **The MCP endpoint is part of the binary.** The server serves MCP over
   the Streamable HTTP transport at `/api/agent/mcp`. It follows the MCP
   specification revision 2025-06-18 or a later revision. It needs no new
   process and no new role.
2. **Tools only.** The endpoint offers the `tools` capability: `tools/list`
   answers the catalog of ADR 122, and `tools/call` runs a tool through the
   one `Toolset` (ADR 122 decision 13). It offers no resources, no prompts
   and no sampling. Resources can come later, for example for docs.
3. **The same credentials as the API.** A client sends an access token in
   the `Authorization` header: a personal token or a service token (ADR
   53). The server authenticates it as it authenticates an API request. The
   actor of each call is the principal of the token, with the channel
   `mcp`. The authorization flow of the MCP specification (OAuth) can come
   later, with the sign-in methods of ADR 55.
4. **No state in an instance.** The endpoint answers each request on its
   own. It makes an `Mcp-Session-Id` at `initialize` and keeps no session
   state in memory. The audit log records the ID (ADR 126). The endpoint
   answers each `tools/call` with one JSON response, not a stream, so any
   instance can answer it.
5. **A key for each call with an effect.** MCP has no idempotency key. The
   server makes the key of a call with an effect from the session ID and
   the JSON-RPC request ID. A client that sends a call again with one ID
   gets one change (ADR 120 decision 11).
6. **Limits.** A call through MCP uses no model of the installation, so
   the outside agent pays for its own tokens. Each call has the byte limit
   of a tool result (ADR 120 decision 16). Each token has a rate limit of
   60 tool calls in each minute by default, which an administrator can set.
   A call above it answers the JSON-RPC error of a rate limit.
7. **Off by default. An administrator turns it on.** The endpoint and the
   stdio proxy are off until an administrator with `server:manage-agent`
   sets `mcpEnabled: true` with `PUT /api/agent/settings`
   (`agent.InstallationSettings.MCPEnabled`). The change writes an audit
   entry (ADR 126 decision 7). While it is off, the endpoint answers 404,
   and the proxy writes to stderr that the endpoint is off and which
   setting turns it on. The status route (ADR 120 decision 7) answers
   `mcp: "on"` or `mcp: "off"`. The endpoint does not need a model
   connection: an outside agent can do only what its token can do through
   the API. With the audit log off, the endpoint answers 404 whatever the
   setting (ADR 120 decision 5).
8. **The stdio proxy is a client command.** `opendustpan agent mcp` reads
   JSON-RPC messages on stdin and writes the answers on stdout. It forwards
   each message to `/api/agent/mcp` of the server that `--server` names,
   with the token of the other client commands. It never opens the
   database, the blob store or the key-encryption key (ADR 39). It never
   writes the token to stdout or to a log. It sends the header
   `Dustpan-Channel: mcp-proxy`, so the audit entry has the channel
   `mcp-proxy`. The channel is what the client declares, as for the other
   channels of the audit log. On the MCP endpoint the server accepts only
   `mcp` and `mcp-proxy`, never `agent`.
9. **The proxy forwards and adds nothing.** The proxy has no tool of its
   own and no cache. A new tool on the server reaches each proxy at once.
   The proxy writes errors of the transport to stderr only, because stdout
   belongs to the protocol.
10. **Per-use consent for secret values.** An outside agent that asks for
    secret values passes the secret gate (ADR 124) and also needs a
    consent for that one use. The person gives it in the console, never
    through MCP:
    - The call answers an error result with the URL of a consent page and
      the ID of the consent. The proxy also writes the URL to stderr, so
      the person sees it in the terminal.
    - The person opens the URL in the console. The page shows the tool,
      the unit and the client. The approval route accepts a console session
      with a fresh MFA step (ADR 56, ADR 71) and refuses an access token.
      So an agent that holds the token of its person cannot approve its
      own consent.
    - The approval holds for one call of that tool on that unit with the
      same token, in the same MCP session, for 5 minutes. The agent sends
      the call again, and the server uses the consent once
      (`agent.ConsentStore.Use`).
    - A session that used a consent cannot propose a change for the rest of
      its life (ADR 124 decision 12).

    The route `POST /api/agent/secret-consents/{id}/approve` checks
    `unit:read-secrets` on the unit and needs a console session.

## Consequences

- The MCP endpoint and Dusty share one `Toolset`, one authorization path
  and one audit path. A fix in a tool reaches both.
- The endpoint is a new route prefix with its actions in the route table
  (ADR 17). Each `tools/call` declares the action of its tool. The route
  test needs a case for the MCP route, because one route answers many
  actions.
- **The dependency is approved, and not added here.** The endpoint uses
  the official MCP Go SDK, `github.com/modelcontextprotocol/go-sdk`. The
  implementation pins it to a release that is at least two weeks old (as
  ADR 7 asks for skills), and adds it in its own pull request. Its license
  must pass `make trivy` and `make licenses`. This pull request adds no
  dependency.
- The e2e suite gets a step that drives the proxy with a scripted MCP
  client.

## Not decided here

- The OAuth authorization flow of the MCP specification.
- MCP resources and prompts.
- A token scope that allows only the tools of agents.
