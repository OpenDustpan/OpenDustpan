# 270. Manage OpenDustpan as code with a CLI and two providers

Status: accepted · 2026-10-20 · decided by the project owner. The open design questions are in RFC 48.

## Context

A platform team wants to configure OpenDustpan in the same way as it
configures the rest of its infrastructure: in files that a reviewer reads, a
pipeline applies and an auditor can trace. Today a person changes namespaces,
collections, gates, approval rules and ownership rules by calls to the REST
API or in the console.

The project owner made decisions on how this works. Issue 967 records them in
its section "Decided". This ADR records only those decisions, and quotes each
one as the issue states it. [RFC 48](../rfc/0048-management-as-code.md) holds
every question that stays open: how the client is built, where the providers
live, which objects come first, import, authentication, drift and the format
of the version pin.

The numbers 48.1, 48.3 and 12.8 are the numbers of the decisions of the owner,
as issue 967 cites them. They are not the numbers of ADRs.

## Decision

Each decision below quotes the owner's text from the section "Decided" of
issue 967.

1. **A CLI and two providers now, from the published description, with a
   version pin before 1.0.**

   ```text
   48.1 / 48.3 (owner, 2026-10-18): self-management as code: a CLI and the
   OpenTofu and Pulumi providers now, from the published API description,
   with a version pin before 1.0.
   ```

   ADR 264 decision 5 states the same rule for generated clients.
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

## Consequences

- The CLI and the two providers are clients of the tool-neutral REST API
  (ADR 17). They have no route, no table and no right that the API does not
  give. A change that a provider makes passes the same authorization and the
  same audit entry as the same change in the console.
- A feature that only a provider can reach is not allowed. A feature that the
  REST API gives is a candidate for the providers, in the order that RFC 48
  proposes.
- Each client carries a version pin. A client refuses a server version outside
  its pin. The format of the pin and the rule for a refusal are questions of
  RFC 48.
- This ADR does not choose how the client is built, where the code lives, or
  which generator the project uses. ADR 264 says that the owner approves a
  generator before the project adds it.
- The pull request that adds the first provider or the CLI adds its page to
  the user documentation (ADR 214). The design pull request that adds this ADR
  adds no page.
