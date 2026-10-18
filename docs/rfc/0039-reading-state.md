---
rfc: 39
title: Reading state contents
status: accepted
adr: 41
created: 2026-10-10
---

# RFC 39. Reading state contents

Status: accepted (ADR 41). It was a stub opened from the competitor review in
the research doc "IaC and TACOS landscape".

## Problem

The glossary says OpenDustpan stores state but never reads its contents. This
one rule excludes more competitor features than any other: a resource browser
and search across units (Pulumi, Spacelift, Scalr, Terramate), a resource
graph (Pulumi Context API, Upbound), policy on state (Harness), IaC coverage
views (Firefly), state diagrams (Terrakube) and resource-level locks
(Stategraph). It also decides how RFC 15 treats Pulumi secret outputs. All
five parts of the competitor review flagged it.

## First ideas

- Keep the rule. The console stays at the unit level.
- An opt-in indexer per namespace that reads a fixed list of fields: resource
  type, name or address or URN, provider and version, dependencies, parent.
  Never attribute values, because OpenTofu keeps sensitive values in state as
  plain text.
- The indexer as its own role (RFC 7), so that an operator can run it apart
  from the API, or not at all.
- OpenTofu client-side state encryption makes state unreadable to the server;
  those units stay at the unit level.

## Open questions

- Which fields are safe to index for each tool?
- Does the index change what the project promises about state, and how is that
  said to users?
- Accepting this needs an ADR that replaces the glossary rule.

## Outcome

Accepted as [ADR 41](../adr/0041-the-server-reads-state-and-masks-secrets.md)
with a wider model than these first ideas: the server reads state, a read
masks secret values, and a separate reveal call writes an audit entry. The
indexer with a fixed list of fields is not part of that decision.
