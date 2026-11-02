# 264. SDKs and the CLI are generated from the published API description

Status: accepted · 2026-10-18 · decided by the project owner. The open design questions are in RFC 99.

Changed by:

- ADR 322 changes decision 5: until the first release, the description has
  one fixed version.

## Context

Developers call the tool-neutral REST API (ADR 210) from scripts, programs,
the console, a CLI and providers (ADR 17). Today each client writes its own
calls by hand, and a change of the API can break a client with no warning.

On 2026-10-18 the project owner decided how clients get the API (issue 966).
This ADR records only those decisions. [RFC 99](../rfc/0099-the-api-description-and-the-generated-sdks.md)
holds the questions that stay open: the version of OpenAPI, where the
description lives, the generator and where the SDKs live.

## Decision

1. **The server publishes a description of its tool-neutral REST API
   (ADR 210).** It does not cover the Pulumi Cloud-compatible API or the
   TFE-compatible API. Each of those keeps the shapes that its tool expects
   (ADR 17).
2. **The description is an OpenAPI document.** RFC 99 decides the version of
   OpenAPI (3.0 or 3.1).
3. **A CI check fails when the description and the routes differ.** A route
   with no entry, an entry with no route, and an entry that differs from its
   route each fail the check.
4. **The SDKs are generated from the description.** OpenDustpan ships SDKs
   for Go, TypeScript and Python. Each SDK carries a version that is pinned
   to the version of the server. The license of each generator must pass the
   license policy of the project (`make licenses`, `make trivy`).
5. **The CLI and the providers are generated from the published description.**
   Before 1.0, each of them pins the version of the description that it was
   generated from.

## Consequences

- A route that is not in the description has no SDK call, so the description
  and the route table must not drift. RFC 99 proposes the test for decision 3.
- When the description holds an operation, a person does not write the call
  for that operation by hand. A small transport layer for each SDK (for
  example `Idempotency-Key`, `If-Match` and cursors) is not an SDK call, and
  a person can write it.
- ADR 73 lets the API break before 1.0. A pinned version lets a client know
  which API it was built for.
- This ADR does not choose a generator or a place for the SDKs. A later ADR
  records each one after the project owner accepts it.
