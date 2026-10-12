---
rfc: 35
title: Packaging and installation
status: draft
created: 2026-10-10
---

# RFC 35. Packaging and installation

Status: draft. A stub opened from the competitor review in the research doc
"IaC and TACOS landscape". It records the gap and first ideas only.

The TLS, binary and container image part is accepted as ADR 27. The rest
stays a draft, including the Helm chart.

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
2026-10-11. The rest has no outcome yet.
