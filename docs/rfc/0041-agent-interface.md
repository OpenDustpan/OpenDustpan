---
rfc: 41
title: An interface for AI agents
status: draft
created: 2026-10-10
---

# RFC 41. An interface for AI agents

Status: draft. A stub opened from the competitor review in the research doc
"IaC and TACOS landscape". It records the gap and first ideas only.

## Problem

Pulumi Neo, Spacelift Infra Assistant and Intent, env zero Cloud Analyst and
Harness agents are built-in AI agents. The discussed position for OpenDustpan
is: no own agent; agents that teams bring use a self-describing API and an MCP
server. HCP Terraform, env zero, Atmos Pro and Harness ship MCP servers.

## First ideas

- An MCP server with task-shaped tools over the API.
- A self-describing API: a schema primer that an agent fetches (Pulumi's
  `GetGraphQuerySchema`).
- An agent is a principal with a scoped token (RFC 20); its edits go through
  RFC 12 and RFC 13 like anyone's.
- An ADR that records "no own AI agent".

## Open questions

- Is the MCP server part of the binary (a role, RFC 7) or a separate program?

## Outcome

None yet.
