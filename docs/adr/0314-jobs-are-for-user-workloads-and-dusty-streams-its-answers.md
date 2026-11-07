# 314. Jobs are for user workloads, and Dusty streams its answers

Status: accepted · 2026-11-01. Records decisions of the project owner of 18 October 2026

Changed by:

- ADR 357 extends decision 2: what the stream of an answer needs.

Changes:

- Extends ADR 100: what a job is for.
- Extends ADR 120: the stream of an answer of Dusty.

## Context

The project owner made these decisions in the owner decision file 2026-10-17, entries dated 18 October. A check on 1 November 2026 found no ADR that states them. ADR 120 lists the stream of an answer as not decided.

This ADR writes the words of the owner. It adds no decision of its own.

## Decision

1. **Jobs are for user workloads (owner, 2026-10-18).** The owner said: "jobs are for users' IaC and IaC-adjacent workloads and their dependencies, run by users". The owner said also: "They are never for platform logic of ours (backups, HTTP webhooks and similar)".
2. **Dusty streams its answers (owner, 2026-10-18).** The owner said: "Stream Dusty's answers in the console".

## Consequences

- ADR 100 and ADR 120 get a pointer to this ADR.
- Each route of these decisions lands with its e2e step first.
