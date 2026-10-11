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

## Outcome

The TLS, binary and container image part is accepted as ADR 27 on
2026-10-11. The rest has no outcome yet.
