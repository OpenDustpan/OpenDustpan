---
rfc: 34
title: Observing the OpenDustpan server
status: draft
created: 2026-10-10
---

# RFC 34. Observing the OpenDustpan server

Status: draft. A stub opened from the competitor review in the research doc
"IaC and TACOS landscape". It records the gap and first ideas only.

## Problem

Anyone who self-hosts must watch the server. Terraform Enterprise exposes
metrics and log forwarding; the Pulumi Kubernetes Operator ships Prometheus
metrics; Terrakube has OpenTelemetry tracing. OpenDustpan has `log/slog` only.

## First ideas

- Prometheus metrics: request rates and latency, store and blob errors, lock
  and lease counts, job queue length.
- Health and readiness endpoints.
- OpenTelemetry tracing through the ports.
- Rate limits per token and per namespace.

## Open questions

- Which of these need new dependencies, and which can the standard library do?

## Outcome

None yet.
