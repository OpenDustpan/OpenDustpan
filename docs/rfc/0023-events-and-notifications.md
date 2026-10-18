---
rfc: 23
title: Events, notifications and webhooks
status: accepted
created: 2026-10-10
adr: 145
---

# RFC 23. Events, notifications and webhooks

Status: accepted (ADR 145). It was a stub opened from the competitor review in
the research doc "IaC and TACOS landscape".

## Problem

Teams expect to hear about failed updates, drift and stuck locks. HCP
Terraform, Pulumi Cloud, Spacelift, Terramate, OTF and Terrakube send webhooks
and chat messages. Pulumi has a webhooks API that the CLI can manage, and TFE
has `notification-configurations`. RFC 13 sends webhooks for approvals only.

## First ideas

- One internal event stream: unit created, update or run started, finished,
  failed; drift found; lock held too long; approval waiting.
- Destinations as adapters: generic HTTP with an HMAC signature, Slack,
  Microsoft Teams, email.
- Serve the Pulumi Cloud webhooks API and TFE `notification-configurations`,
  each in its tool's own shape.
- Run triggers (RFC 24), drift (RFC 25) and approvals (RFC 13) all read the
  same stream.

## Open questions

- Delivery guarantees: at least once with retries, and a delivery history with
  redeliver (Terrakube has one)?
- Can a policy route events (Spacelift notification policies), or are
  subscriptions enough?

## Outcome

Accepted as [ADR 145](../adr/0145-one-event-model-and-sinks.md), one event
model and sinks, on 2026-10-18. A question of this RFC that ADR 145 does not
decide needs a new RFC.
