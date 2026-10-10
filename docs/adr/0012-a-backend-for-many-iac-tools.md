# 12. A backend for many infrastructure-as-code tools

Status: accepted · 2026-10-10

## Context

OpenDustpan started as a self-hosted backend for the Pulumi CLI (ADRs 1 to 8).
ADR 9 proposed OpenTofu support through a TFE-compatible API. Teams rarely use
a single infrastructure-as-code tool; they want one place for state, history,
locks, secrets, access control and audit across all of them. The project owner
decided OpenDustpan should be that place: a backend for infrastructure-as-code
tools in general, with the Pulumi CLI as the first one supported.

## Decision

- OpenDustpan is positioned and documented as a backend for
  infrastructure-as-code tools. No tool is primary in the project's framing;
  each is listed with its own support status.
- A tool connects through the remote-backend protocol its CLI already speaks.
  OpenDustpan ships no client plugins or wrappers.
- The core is tool-neutral and shared by every tool: namespaces (what each
  tool calls an organization, ADR 10), home regions and fencing (ADR 4),
  identity (ADR 6), blob storage, secrets encryption and storage adapters
  (ADR 2, ADR 3). Nothing enters the core because only one tool needs it.
- Each tool has its own domain package for its model of deployable units and
  state (`stack` for the Pulumi CLI, `workspace` for OpenTofu) and its own HTTP
  adapter under `api/<tool>/` with its own route prefix. Tools share concepts
  through the core, never types through each other.
- A tool is supported, and listed as such, only when the e2e suite drives its
  real CLI across a version matrix and the server passes those steps.
- A new tool is taken on when its users need remote state, its backend
  protocol can be implemented from observed behavior and public client
  sources without copying code, and its license lets the e2e suite run its
  CLI. Each one gets an ADR covering protocol, model and scope, as ADR 9 does
  for OpenTofu.
- Naming: "works with <tool>" and "<vendor>-compatible API". Other projects'
  names, logos and colors are never ours, and the README disclaims
  affiliation with every vendor whose tool is listed.

## Consequences

- OpenTofu support is in scope. ADR 9's protocol design still stands on its
  own merits and needs acceptance separately.
- Shared packages need review for tool-specific leakage when a second tool's
  adapter lands; the first such review is due with the TFE-compatible API.
- Every tool adds an API surface that is a contract (CLAUDE.md), an e2e matrix
  and maintenance. Breadth must not come at the cost of depth: a tool is
  listed as supported only when its everyday workflow works end to end.
- The console must present namespaces and access control tool-neutrally, and
  each tool's units (stacks, workspaces) in its own views.
