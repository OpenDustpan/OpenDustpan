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

## First ideas

- Search and filters over the inventory, kept in the URL, with saved and
  shared views.
- A graph of units, profiles, blueprints, sources and runners, with edges from
  facts the backend already has (RFC 5, 12, 14, 15, 6). No state reading
  needed.
- An anchor, traverse, return query shape, with completeness metadata on every
  list: exact or truncated, complete or trimmed by access rules, schema
  version.

## Open questions

- Is the query a new API, or filters on existing list endpoints first?

## Outcome

None yet.
