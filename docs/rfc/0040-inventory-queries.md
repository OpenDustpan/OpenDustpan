---
rfc: 40
title: Inventory search, saved views and graph queries
status: draft
created: 2026-10-10
---

# RFC 40. Inventory search, saved views and graph queries

Status: draft. A stub opened from the competitor review in the research doc
"IaC and TACOS landscape". It records the gap and first ideas only.

## Problem

Every console leads with a list of units that people filter and save:
Spacelift saved views, HCP Terraform Explorer, Pulumi's group-by in the URL,
Upbound Lenses. Pulumi's Context API queries a graph with an anchor, traversal
steps and a return clause, and says how complete each answer is.

The graph query is an owner-wanted feature. It is a rival to the Pulumi
Context API. OpenDustpan is not affiliated with Pulumi.

## First ideas

- Search and filters over the inventory, kept in the URL, with saved and
  shared views. [RFC 100](0100-saved-views.md) holds the saved views. A saved
  view is a server object with an API (ADR 266).
- A graph of both tools' units, with resources, profiles, blueprints, sources
  and runners as nodes. Edges come from facts the backend already has (RFC 5,
  12, 14, 15, 6).
- Resource nodes come from reading state ([RFC 39](0039-reading-state.md),
  ADR 41). ~~No state reading needed.~~ This line is out of date. Resource
  nodes need reading state.
- The same graph may feed the AI agent RFCs ([RFC 41](0041-agent-interface.md)
  and [RFC 73](0073-ai-agent-for-infrastructure.md)) and the "managed
  twice" kind of unmanaged resource in [RFC 47](0047-discovery-and-codification.md).
- Every answer obeys the access rules. Its completeness metadata says when the
  access rules trimmed the answer.
- An anchor, traverse, return query shape, with completeness metadata on every
  list: exact or truncated, complete or trimmed by access rules, schema
  version.

## Open questions

- Is the query a new API, or filters on existing list endpoints first?

## Outcome

None yet.
