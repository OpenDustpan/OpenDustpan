# 264. SDKs and the CLI are generated from the published API description

Status: accepted · 2026-10-18 · decided by the project owner. The open design questions are in RFC 99

## Context

Developers call the tool-neutral REST API (ADR 210) from scripts, programs,
the console, a CLI and providers (ADR 17). Today each client writes its own
calls by hand, and a change of the API can break a client with no warning.

On 2026-10-18 the project owner decided how clients get the API. This ADR
records only those decisions. [RFC 99](../rfc/0099-the-api-description-and-the-generated-sdks.md)
holds the questions that stay open: the format of the description, where it
lives, the generator and where the SDKs live.

## Decision

1. **The server publishes a description of its tool-neutral REST API.** The
   description covers the routes under `/api/dustpan/v1` (ADR 210). It does
   not cover the Pulumi Cloud-compatible API or the TFE-compatible API.
   Each of those keeps the shapes that its tool expects (ADR 17).
2. **The SDKs are generated from the description.** OpenDustpan ships SDKs
   for Go, TypeScript and Python. A person does not write an SDK call by
   hand when the description holds the operation.
3. **The CLI and the providers are generated from the published description.**
   Before 1.0, each of them pins the version of the description that it was
   generated from.

## Consequences

- A route that is not in the description has no SDK call, so the description
  and the route table must not drift. RFC 99 proposes a test for this.
- ADR 73 lets the API break before 1.0. A pinned version lets a client know
  which API it was built for.
- This ADR does not choose a format, a generator or a place for the SDKs. A
  later ADR records each one after the project owner accepts it.
