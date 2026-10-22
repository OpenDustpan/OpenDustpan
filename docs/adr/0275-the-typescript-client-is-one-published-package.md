# 275. The TypeScript client is one published package

Status: accepted · 2026-10-22. Decisions 1 to 7 are owner decisions. Amends decisions O6 and O7 of amendment 1 of RFC 99. Amends ADR 7 in part (the age rule for dependencies). Follows ADR 264

## Context

Amendment 1 of [RFC 99](../rfc/0099-the-api-description-and-the-generated-sdks.md)
puts the TypeScript client third, after Go and Python (decision O6). It also
gives each language its own repository (decision O7), so `client-node` would
be a separate repository with its own output.

The console is the first user of a TypeScript client. The console repository
has a design for a client that the server generates from its API description.
The design and console ADR 5 are in `OpenDustpan/console`. The owner
accepted the design on 22 October 2026 and changed three points of it. The
console must not use a second generator, and must not use a second TypeScript
output. The console must also not depend on server behavior that no one
released.

The description also has two weak points that the console design found:

- Every operation has the key `2XX`. A client cannot see that
  `removeIpAllowlist` answers 204, so the console keeps a check by hand in
  each group.
- `info.version` stayed `0.1.0` across three changes of the description. A
  consumer cannot use it to see that the description changed.

## Decision

1. **TypeScript is second, after Go and before Python.** This amends
   decision O6. The order of the template sets is Go, TypeScript, Python, and
   then the other languages of O6 in their order.
2. **There is one layered TypeScript output.** `client-node` and the console
   both use it. The output has the types, the validators, the table of
   operations and a transport interface. A user of the output supplies a
   transport. The output has a default transport with bearer tokens, and
   uses the global `fetch` and `JSON` only. This amends decision O7 for
   TypeScript: there is no separate `client-node` output, and the console does
   not generate its own client. The build and the publish workflow of the
   package stay in this repository, where the server tests them.
3. **This project publishes the package as a generic package to the public
   npm registry, under the `@opendustpan` scope, with npm provenance.** The
   package has no knowledge of the console. The registry needs no token to
   install it.
4. **Packages of the `@opendustpan` scope are exempt from the two-week age
   rule of ADR 7.** A consumer still pins such a package to an exact version.
   A package of any other scope keeps the age rule.
5. **"Released" server behavior, before the first server tag, is a published
   package version together with the server commit that the package came
   from.** A consumer such as the console depends only on behavior that is in
   a published package version.
6. **The description gives the exact success code of each operation.** An
   operation with one success status has the key of that status in
   `responses`, for example `200`, `201`, `202` or `204`. An operation that
   can answer with two success statuses has the key `2XX`. Today these are
   the writes that create an object at version 1 and replace it later
   (`putGitHostApp` and `putGitHostInstallation`). The API does not change.
   Only the description changes.
7. **`info.version` changes with every change of the description.** A test
   enforces this. The change that adds the test is a separate change.

## Consequences

- The description builder records the success status of each operation. A
  route label sets it with `WithStatus`, or with `WithAnyStatus` for two
  statuses. The default is 200 for an answer with a body and 204 for an answer
  with no body. The drift check compares the status of a route with the
  status in the description.
- The description changes, so `info.version` is `0.2.0`. The major and minor
  parts of the client version follow it, so the Go client is `0.2.x`.
- The TypeScript template set, the program `cmd/clientgen` and the publish
  workflow are later changes. Each one has its own gates. The Python template
  set waits for them.
- RFC 99 is not edited. This ADR is the record of the changes to O6 and O7.
- A change of a template is a change in this repository, a package release,
  and then a change of the pin in the consumer.
