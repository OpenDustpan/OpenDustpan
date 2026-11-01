# 312. Grant expiry, declared edges, profile objects and operator imports

Status: accepted · 2026-11-01. Records decisions of the project owner of 18 and 19 October 2026

Changes:

- Extends ADR 223: grant conditions.
- Extends ADR 162: declared edges.
- Extends ADR 224 decision 1: the authorization object of a profile.
- Extends ADR 241: Kubernetes operators as import sources.

## Context

The project owner made these decisions in the owner decision file 2026-10-17, entries dated 18 and 19 October. A check on 1 November 2026 found no ADR that states them.

This ADR writes the words of the owner. It adds no decision of its own.

## Decision

1. **Grant conditions (owner, 2026-10-18).** The owner said: "Grant conditions: expiry first". The owner said also: "other conditions go through Cedar access limits".
2. **Declared edges (owner, 2026-10-18).** The owner said: "Declared edges never expire until a person removes them".
3. **The profile authorization object (owner, 2026-10-19).** The owner said: "Profile authorization object (future): keyed by profile ID: profile/<namespace>/<prf-ID>". The owner said also: "folder grants profile-folder/<namespace>/<folder>".
4. **Operators as import sources (owner, 2026-10-18).** The owner said: "Kubernetes operators as import sources: not now".

## Consequences

- ADR 162, ADR 223, ADR 224 and ADR 241 get a pointer to this ADR.
- Each route of these decisions lands with its e2e step first.
