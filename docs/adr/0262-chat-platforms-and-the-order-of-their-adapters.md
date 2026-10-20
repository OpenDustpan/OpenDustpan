# 262. Chat platforms and the order of their adapters

Status: accepted · 2026-10-18 · decided by the project owner. Answers the open question on the first chat tools in ADR 145 and ADR 146

## Context

OpenDustpan gets notifications in chat (ADR 145, ADR 146). Each chat service
needs its own adapter. [RFC 98](../rfc/0098-chat-sinks-one-sender-a-renderer-for-each-platform.md)
holds the design questions of those adapters. They are still open.

## Decision

1. **The platforms and the order.** OpenDustpan gets chat adapters for these
   chat platforms, in this order: Slack, Microsoft Teams, Mattermost, Matrix.
2. **One pull request for each platform.** Each adapter ships in its own
   pull request.

## Consequences

- The design of the sender, the renderers and the secrets stays in RFC 98
  until the project owner accepts it.
- A later ADR records each decision that the owner accepts from RFC 98.
