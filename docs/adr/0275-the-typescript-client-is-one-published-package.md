# 275. The TypeScript client is one published package

Status: accepted · 2026-10-22. Decisions 1 to 6 and 8 are owner decisions. Decision 7 is a PM decision, taken on the owner's behalf. The package name in decision 2 is a proposal of the author, and the first publish fixes it. Amends decisions O6 and O7 and rule A3 of amendment 1 of RFC 99. Amends ADR 7 in part (the age rule for dependencies). Follows ADR 264

## Context

Amendment 1 of [RFC 99](../rfc/0099-the-api-description-and-the-generated-sdks.md)
puts the TypeScript client third, after Go and Python (decision O6). It also
gives each language its own repository (decision O7), so `client-node` would
be a separate repository with its own output. Rule A3 says that a server
release generates each client and opens a bot pull request in each client
repository. Rule A4 says that a client takes the major and minor version of
the description, and has its own patch version.

The console is the first user of a TypeScript client. The console repository
has a design for a client that the server generates from its API description.
The design and console ADR 5 are in `OpenDustpan/console`. The owner
accepted the design on 22 October 2026 and changed three points of it. The
console must not use a second generator, and must not use a second TypeScript
output. The console must also not depend on server behavior that no one
released.

The description also has two weak points that the console design found:

- Every operation has the key `2XX`. A client cannot see that the operation
  `removeRegistryKey` answers 204, so the console keeps a check by hand in
  each group. The console method `removeIpAllowlist` is an example of such a
  check.
- `info.version` stayed `0.1.0` across three changes of the description. A
  consumer cannot use it to see that the description changed.

## Decision

Owner decisions:

1. **TypeScript is second, after Go and before Python.** This amends
   decision O6. The order of the template sets is Go, TypeScript, Python, and
   then the other languages of O6 in their order.
2. **There is one layered TypeScript output, in one package.** There is no
   `client-node` repository. The one TypeScript package is
   `@opendustpan/client`. This repository builds and publishes it. The console
   and Node programs both use it. The output has the types, the validators,
   the table of operations and a transport interface. A user of the output
   supplies a transport. The output has a default transport with bearer
   tokens, and uses the global `fetch` and `JSON` only. This amends decision
   O7 for TypeScript: there is no separate `client-node` repository and no
   separate `client-node` output, and the console does not generate its own
   client. Decision O7 still holds for Go, Python and the other languages.
   For TypeScript, the reason of O7 (a client change needs a pull request in a
   separate repository) gives way to the server tests of the package, which
   run in this repository against the package build.
3. **This project publishes the package as a generic package to the public
   npm registry, under the `@opendustpan` scope, with npm provenance.** The
   package has no knowledge of the console. The registry needs no token to
   install it. Rule A3 does not apply to TypeScript, because there is no
   client repository for a bot pull request. The release workflow of this
   repository publishes the package in its place, at each server release and,
   before the first tag, as decision 5 says. The check before the merge of
   rule A3 stays: a pull request that changes the description, the generator
   or a template runs the golden tests and the suite against the package
   build. Rule A3 holds for the other languages. The version of the package
   follows rule A4: the major and minor parts come from `info.version`, and
   the patch part is its own. Console ADR 5 makes this version the pin of the
   console, so the rule A4 version is also the pin.
4. **Packages of the `@opendustpan` scope are exempt from the two-week age
   rule of ADR 7.** A consumer still pins such a package to an exact version.
   A package of any other scope keeps the age rule.
5. **"Released" server behavior, before the first server tag, is a published
   package version together with the server commit that the package came
   from.** A consumer such as the console depends only on behavior that is in
   a published package version.
6. **The description gives the exact success status code of each
   operation.** The key of a success answer in `responses` is a status code,
   for example `200`, `201`, `202` or `204`. The description never has the key
   `2XX` or another range.
8. **`info.version` changes with every change of the description.** A test
   enforces this. The change that adds the test is a separate change.

PM decision, taken on the owner's behalf:

7. **A route that creates an object on a first write and replaces it later
   keeps both codes.** `putGitHostApp` and `putGitHostInstallation` answer 201
   for a first write and 200 for a replacement. The API does not change.
   RFC 9110 section 9.3.4 says that a PUT that creates a representation must
   answer 201, so the API keeps 201. The description has the keys `200` and
   `201` for these two operations. The success statuses of an operation are a
   set, with one or more codes. The description reader and the client
   generator accept exactly that set, so that a generated client refuses a 202
   or a 204 from these routes. Decision 6 holds for these two operations: the
   description names each code that they answer.

## Consequences

- The description builder records the set of success statuses of each
  operation. A route label sets it with `WithStatus`, for example
  `WithStatus(200, 201)`. The default is 200 for an answer with a body and 204
  for an answer with no body. The build refuses a code that is not a 2xx code
  and a code that a label gives twice. The reader refuses the key `2XX`, a
  range, and a key that is not in canonical form. The drift check compares the
  statuses of a route with the statuses in the description.
- A test wrapper, `apidesctest.CheckStatus`, runs in the test server of each
  described adapter. It fails a test when a handler answers a 2xx status that
  the description does not give for the route. A second check lists the
  described operations that no test observes with a success answer. It
  compares the list with an allowlist in each package. The allowlist can only
  get shorter: the check also fails when a test observes an operation on the
  list.
- A generated Go client accepts the described success statuses of an operation
  only. It returns an invalid-response error for any other 2xx status.
- The description changes, so `info.version` is `0.2.0`. The major and minor
  parts of the client version follow it, so the Go client is `0.2.x`.
- The TypeScript template set, the program `cmd/clientgen` and the publish
  workflow are later changes. Each one has its own gates. The Python template
  set waits for them.
- RFC 99 is not edited. This ADR is the record of the changes to O6, O7 and
  A3.
- A change of a template is a change in this repository, a package release,
  and then a change of the pin in the consumer.
