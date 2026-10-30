---
rfc: 35
title: Packaging and installation
status: accepted
created: 2026-10-10
adr: 283
---

# RFC 35. Packaging and installation

Status: accepted. The TLS, binary and container image part is accepted as
ADR 27. The Helm chart is accepted as ADR 246. The scope for `v0.1.0` is
accepted as [ADR 283](../adr/0283-packaging-for-v0-1-0.md).

## Problem

The first experience of a self-hoster is the install. Competitors ship Helm
charts, container images and quickstarts. RFC 1 also needs TLS, because
OpenTofu refuses plain HTTP, and no decision says where TLS ends.

## First ideas

- A container image, a Helm chart and a single-binary quickstart.
- TLS: built-in certificates (including ACME), or always a reverse proxy,
  documented for both tools.
- Default settings that are safe for a first install, with warnings for unsafe
  ones (RFC 9's posture).

## Open questions

- Is built-in TLS worth its code, given that most installs sit behind a proxy?

## Owner direction (2026-10-12)

The project owner answered questions in this RFC on 2026-10-12. Words in
quotation marks are the words of the owner. A question that the owner sent to
research or to a spike stays in "Open questions", with a mark. This section
decides nothing. An ADR records a decision (ADR 15).

- Comment on the question "Is built-in TLS worth its code": "Built-in TLS is fine
  but including ACME seems like a stretch." The question stays in "Open
  questions".

## Outcome

The TLS, binary and container image part is accepted as ADR 27 on
2026-10-11. The Helm chart is accepted as ADR 246 on 2026-10-19, after the
owner decided on 2026-10-17 to start it with an external PostgreSQL.

Accepted on 2026-10-30 as
[ADR 283](../adr/0283-packaging-for-v0-1-0.md), a decision of the project
owner. For `v0.1.0`: TLS from files and a documented reverse proxy, no ACME,
no OS packages, signing and an SBOM later, and multi-arch images with
`docker/setup-buildx-action` and `docker/setup-qemu-action` pinned by SHA.
