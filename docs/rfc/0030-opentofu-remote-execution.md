---
rfc: 30
title: CLI-driven remote execution for OpenTofu
status: draft
created: 2026-10-10
---

# RFC 30. CLI-driven remote execution for OpenTofu

Status: draft. A stub opened from the competitor review in the research doc
"IaC and TACOS landscape". It records the gap and first ideas only.

## Problem

RFC 1 serves OpenTofu with `operations: false`: every plan runs on the
workstation. HCP Terraform, OTF, Terrakube and OpenTaco (beta) let `tofu plan`
on a laptop run remotely and stream the log back. RFC 6 notes this as a
follow-up to RFC 1.

## First ideas

- Serve the TFE run, plan, apply and configuration-version endpoints that the
  CLI uses for remote operations.
- A runner (RFC 6) claims the run; the backend only queues it and streams
  logs.
- Decide whether to serve the TFE agent-pool API or keep RFC 6's own runner
  protocol.

## Open questions

- Which subset of the TFE run API does the OpenTofu CLI need? This needs a
  check with `make e2e`.
- How do runner labels map to TFE agent pools?

## Outcome

Accepted in part as [ADR 254](../adr/0254-speculative-remote-plans-for-opentofu-first.md)
on 2026-10-20: speculative plans on runners come first. The apply, the
saved cloud plan, the `remote` backend and the map of runner labels to
agent pools stay open (ADR 254 decision 10).
