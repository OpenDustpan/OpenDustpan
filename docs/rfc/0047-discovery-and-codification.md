---
rfc: 47
title: Discovery and codification of unmanaged resources
status: draft
created: 2026-10-10
---

# RFC 47. Discovery and codification of unmanaged resources

Status: draft. A stub opened from the competitor review in the research doc
"IaC and TACOS landscape". It records the gap and first ideas only.

## Problem

Firefly, Pulumi Insights and HCP Terraform Search find cloud resources that no
IaC manages and generate code with import blocks. ControlMonkey did the same.

The umbrella term is **unmanaged resource**: a cloud resource that does not
have exactly one unit that manages it. The owner chose the term and its three
kinds on 2026-10-17. The kinds differ in what finds them.

- **Not in any unit.** A cloud resource that no unit's state has. Finding
  it needs a cloud scan, so it needs a runner job (RFC 6).
- **Left behind.** A resource in the last state of a unit that was deleted,
  soft-deleted or failed to destroy. The backend may find it from state history
  alone, without a cloud scan.
- **Managed twice.** The same cloud resource ID in the state of two units,
  possibly across both tools. The inventory projection (ADR 13) and reading
  state (ADR 41) may find it without a cloud scan.

As a first idea, not a decision, "left behind" and "managed twice" may need
no runner.
This RFC does not decide which cases are in scope.

## First ideas

- A runner job (RFC 6) that scans an account with credentials from a profile
  (RFC 5) and writes code as a review request (RFC 12).
- The backend never scans clouds itself.

## Open questions

- Is this in scope for a backend, or a separate tool that uses OpenDustpan's
  API?

## Outcome

None yet.
