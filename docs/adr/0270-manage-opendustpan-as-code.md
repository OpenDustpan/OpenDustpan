# 270. Manage OpenDustpan as code with a CLI and two providers

Status: accepted · 2026-10-20 · decisions 1 to 3 were decided by the project owner. Decisions 4 to 9 are decided by the product manager under the owner's delegation of 20 October 2026. The open design questions are in RFC 48.

## Context

A platform team wants to configure OpenDustpan in the same way as it
configures the rest of its infrastructure: in files that a reviewer reads, a
pipeline applies and an auditor can trace. Today a person changes namespaces,
collections, gates, approval rules and ownership rules by calls to the REST
API or in the console.

The project owner made decisions on how this works. Issue 967 records them in
its section "Decided". Decisions 1 to 3 below quote them as the issue states
them. The quotes of decisions 2 and 3 carry no date in the issue. The quote of
decision 1 has the date 2026-10-18.

The numbers 48.1, 48.3 and 12.8 are the numbers of the decisions of the owner,
as issue 967 cites them. They are not the numbers of ADRs.

[RFC 48](../rfc/0048-management-as-code.md) holds the two questions that stay
open: the generator (question 2) and the place of the code (question 3). Both
need the owner.

## Decision

Decisions 1 to 3 quote the owner's text from the section "Decided" of issue
967.

1. **A CLI and two providers now, from the published description, with a
   version pin before 1.0.**

   ```text
   48.1 / 48.3 (owner, 2026-10-18): self-management as code: a CLI and the
   OpenTofu and Pulumi providers now, from the published API description,
   with a version pin before 1.0.
   ```

   ADR 264 decision 5 states the rule for generated clients: "each of them
   pins the version of the description that it was generated from". Decision 9
   keeps that pin.
2. **Unit settings are not kept in the repository.**

   ```text
   12.8: unit settings are not kept in the repository; teams configure
   OpenDustpan with our own OpenTofu provider and Pulumi provider.
   ```

3. **Ownership rules are managed as code through the providers.**

   ```text
   Policy model: ownership rules are managed as code through these
   providers.
   ```

Decisions 4 to 9 are decided by the product manager under the owner's
delegation of 20 October 2026.

4. **The order of work is the CLI, then the OpenTofu provider, then the Pulumi
   provider.** The CLI exercises the client with the least code. The providers
   then add only their protocol adapters (ADR 176).
5. **The first objects are the collection, the approval rule and the
   ownership rule.** They come after the server work that RFC 48 lists: a
   collection update route, ownership rule routes, create-only writes and
   access-token writes to gate configuration. Until that work is done, a
   provider release does not claim the object.
6. **A team brings an existing object under management by import with a
   reference now. An `export` command comes later. A create never adopts an
   existing object.** A create of an object that exists is refused and
   changes nothing. The message names the one command that helps: the import
   command of the tool.
7. **The CLI and the providers sign in with an access token. There is no
   exemption for a service principal.** A write that needs a fresh
   authentication fails with the stable error code `mfa.fresh-step-required`
   and changes nothing. A step-up sign-in of a person in the CLI is a later
   RFC. That RFC must keep the rule that an access token never gives a fresh
   authentication (ADR 227 decision 6), or it goes to the owner first.
8. **The tools detect a difference at refresh. Every update and delete sends
   `If-Match`. The server does not reconcile.** A conflict changes nothing.
   The person plans again.
9. **The client pins the version of the description that it was generated
   from. It may also pin a range of server versions.** The server sends both
   versions to any signed-in caller. The client refuses a description version
   outside its pin, with a message that names both versions. For the server
   range, a dev build counts as newer than every release, and a describe build
   counts as its tag, as in `pkg/store/schema`. The dev rule is an exception
   to the range: a dev build is outside every range that has an upper bound,
   and the client still accepts it. The client prints a warning that names
   both versions and continues. The exception does not apply to the pin on
   the description, which a dev build must meet. RFC 48 question 8 holds the
   rules in full.

## Consequences

- The CLI and the two providers are clients of the tool-neutral REST API
  (ADR 17). They have no route, no table and no right that the API does not
  give. A change that a provider makes passes the same authorization and the
  same audit entry as the same change in the console.
- A feature that only a provider can reach is not allowed. A feature that the
  REST API gives is a candidate for the providers, in the order of decision 5.
- Client code is not in `pkg/`, which is the server SDK (ADR 35). A client
  must not pull the `go.mod` of the server (ADR 170 decisions 3 and 4). The
  client code lands in the repository that RFC 48 question 3 chooses. This
  change holds no client code. RFC 48 holds the contract of the client port.
- Each client carries a version pin (decision 9).
- This ADR does not choose how the client is built, where the code lives, or
  which generator the project uses. ADR 264 says that the owner approves a
  generator before the project adds it.
- The pull request that adds the first provider or the CLI adds its page to
  the user documentation (ADR 214). The design pull request that adds this ADR
  adds no page.
