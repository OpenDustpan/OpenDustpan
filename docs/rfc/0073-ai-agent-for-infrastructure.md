---
rfc: 73
title: An AI agent for infrastructure across both tools
status: draft
created: 2026-10-17
---

# RFC 73. An AI agent for infrastructure across both tools

Status: draft. The question and the open questions only. This RFC proposes no design.

## Problem

Teams ask whether an AI agent may read their infrastructure, explain a plan, propose a change or prepare a fix. Pulumi Neo is a rival feature. It is an AI agent built into Pulumi. OpenDustpan is not affiliated with Pulumi.

RFC 41 covers the interface that teams use to bring their own agent. It proposes no agent of OpenDustpan's own, and it lists an ADR that records "no own AI agent" as a first idea. This RFC asks whether OpenDustpan should ship an agent that works across both the Pulumi CLI and OpenTofu.

Any such agent may act on real infrastructure. It must follow the same access rules, approvals and audit log as a person. The agent may not gain access that a person with the same role does not have.

## Related RFCs

- RFC 41 (an interface for AI agents) is the base. This RFC may change its first idea about "no own AI agent".
- RFC 20 (access tokens) decides how an agent gets a token and which scope it gets.
- RFC 13 (policy and approvals) decides which actions need an approval first.
- RFC 22 (audit log) decides how an agent action appears in the log.

## Decided on 2026-10-17

The owner decided where the agent runs. The built-in agent is Dusty. It runs
in the server as an agent loop on durable operations (ADR 72), not on a
runner. It is off until an administrator connects a model. The other open
questions below stay open.

## Options

Each option was open when this RFC was written.

- **No own agent.** Teams bring an agent through the API and an MCP server (RFC 41).
- **An agent in the backend.** The backend runs the model and calls the API as a principal. This may conflict with RFC 6, which says the backend never runs an infrastructure tool.
- **An agent on a runner.** The agent runs on a runner that the customer hosts (RFC 6). The model calls may still leave the customer's network.

## Open questions

- Where does the model run, and where do its tool calls run? RFC 6 says the backend never runs an infrastructure tool.
- Is the agent a principal with its own token? Which actions does its default scope allow?
- Which actions need an approval before the agent runs them (RFC 13)?
- Does every agent action reach the audit log, with the agent as the actor (RFC 22)?
- Can one set of rules cover both tools, when ADR 12 keeps each tool's model apart?
- Does the agent read state contents (RFC 39)? Which secret values may it see?
- Does the customer choose the model provider? Does the agent send infrastructure data to that provider?
- Does this RFC wait for read-only tokens (RFC 20) and for the MCP server (RFC 41)?
- Does this RFC conflict with RFC 41's first idea about "no own AI agent"? If it does, RFC 41 needs a decision first.

## Outcome

None yet.
