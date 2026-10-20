# 261. The `pulumi env` commands: supported CLI versions, and the `/api/esc` route prefix only

Status: accepted · 2026-10-20. Decided by the product manager under the owner's delegation of 20 October 2026. Applies to the profile routes of ADR 209

## Context

Decided by the product manager under the owner's delegation of 20 October
2026, with the owner's decision rule.

The `pulumi env` commands read and write environments on a Pulumi
Cloud-compatible backend. OpenDustpan serves them from its profiles (ADR
200 and ADR 209). The wire of these commands changed between versions of
the Pulumi CLI. The facts on 2026-10-20, from the changelogs and the
source of `pulumi/pulumi`:

- `pulumi env` is in the core CLI since v3.88.0.
- Up to v3.131.0, it calls `/api/preview/environments/{org}/{env}`.
- From v3.132.0, it calls `/api/esc/environments/{org}/{project}/{env}`.
- v3.251.0 moved only the source of the commands into `pulumi/pulumi`.
  It made no change on the wire.

The e2e matrix of the Pulumi CLI has three versions: latest, 3.250.0 and
3.200.0. All of them are newer than v3.132.0.

## Decision

1. **Supported versions.** The supported versions of the Pulumi CLI for
   `pulumi env` are the versions in the e2e matrix (latest, 3.250.0 and
   3.200.0). This is the same policy as for the other routes of the Pulumi
   Cloud-compatible API.
2. **One prefix.** The server serves the prefix `/api/esc` only. It serves
   no route under `/api/preview/environments`. A request to that prefix
   gets the status 404 and an error in the shape of the Pulumi
   Cloud-compatible API (`{"code": 404, "message": "..."}`). The refusal
   needs no sign-in and reads no object, so its route is on the public list
   of the route test of the Pulumi adapter. The message names the minimum version,
   3.132.0. The server has no compatibility code for the older shape.
3. **No gate by `User-Agent`.** The server does not accept or refuse a
   request by the `User-Agent` header, because the client controls that
   header and it cannot be trusted. The shapes are the same across the
   supported range. The control is the recorded traffic of each version in
   the matrix, and the check that `make e2e` makes on each new release of the CLI.

Benchmark: cloud SDK and CLI vendors publish a window of supported
versions and test it on each release. A refusal of every older CLI at each
weekly release would break a small team.

## Consequences

- A team that uses `pulumi env` needs the Pulumi CLI 3.132.0 or newer. An
  older CLI gets an error that names the version.
- The server has one set of routes for `pulumi env`.
- `make e2e` checks each new release of the Pulumi CLI before the matrix
  moves.
