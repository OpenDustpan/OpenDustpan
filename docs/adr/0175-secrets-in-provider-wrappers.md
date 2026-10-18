# 175. Secrets in provider wrappers

Status: accepted · 2026-10-18 · part of ADR 170

## Context

The project owner decided on 17 October 2026 that secrets pass only through
the provider configuration of the tool, and never go in plans or state in
plain text where that can be avoided (owner).

The two tools keep values in different places:

- OpenTofu does not keep the provider configuration in state. A saved plan
  file can hold the values of the variables that set it. OpenTofu has
  ephemeral values, which it keeps out of plan files and state, and
  write-only attributes, which a resource sends and never stores. It masks
  a sensitive attribute in plan output. It stores a sensitive attribute of
  a resource in state as plain text, unless the team turns on client-side
  state encryption.
- The Pulumi CLI keeps the inputs and outputs of each resource in state,
  also those of the provider resource. It encrypts a value that the schema
  or the program marks as a secret.

ADR 101 and ADR 134 decide that a runner passes credentials as files on a
memory-backed volume, never in environment variables.

## Decision

1. **A credential enters only through the provider configuration (owner).**
   It is a sensitive attribute of the configuration. A declaration holds no
   credential: each credential field of `auth` must be one reference to a
   sensitive configuration attribute (ADR 171). The built provider holds no
   credential either.
2. **A file form for each secret.** For each sensitive configuration
   attribute `token`, a built provider also accepts `token_file`, the path
   of a file that holds the value. Exactly one of the two can be set. The
   path goes into a plan, and the credential does not. On a runner, the
   launcher writes the file on its memory-backed volume (ADR 134), so the
   credential never passes through OpenTofu or the Pulumi CLI at all.
3. **Ephemeral values for OpenTofu.** The documentation tells OpenTofu users
   to set a sensitive configuration attribute from an ephemeral value, or
   to use the file form. Either keeps the credential out of a saved plan.
4. **Secrets for Pulumi.** The Pulumi adapter marks each sensitive
   attribute, of the configuration and of a resource, as a secret in the
   package schema. The Pulumi CLI then stores it as ciphertext.
5. **Sensitive resource attributes.** A sensitive attribute is masked in
   plans and in the logs of the provider. OpenTofu stores it in state as it
   stores any sensitive value of any provider. The documentation says so
   and points to OpenTofu's state encryption.
6. **Write-only attributes.** The OpenTofu adapter declares a write-only
   attribute as write-only, so OpenTofu never stores it. The Pulumi CLI
   keeps every input in state. The Pulumi adapter marks a write-only
   attribute as a secret, so it is in state as ciphertext only. This is
   the case where "avoidable" ends.
7. **No secret in a public place.** A sensitive value never goes in a command
   argument, an environment variable, a URL path or a query (ADR 171
   decision 9). The HTTP engine adds a credential after it logs the request,
   never follows a redirect, and needs HTTPS (ADR 173).
8. **Files of the CLI engine.** For each `{file:config.name}`, the engine
   writes the value to a file of mode 0600 in a directory of mode 0700 in
   its private `TMPDIR`. It writes it once for each session and deletes it
   when the session closes. When the user gives the file form, the engine
   passes that path and writes nothing.
9. **Masking.** The engines replace each sensitive value with `(sensitive)`
   in diagnostics, in logs, and in the parts of reply bodies and standard
   error that they report.

## Consequences

- A team on runners keeps its API tokens in an OpenDustpan profile (RFC 5).
  The launcher writes them as files, and the provider configuration names
  the paths. The token is in no plan, in no state, and in no environment
  variable.
- On a workstation, the file form also keeps a token out of shell history and
  out of `*.tfvars` files.
- A provider cannot protect a sensitive resource attribute in OpenTofu state
  when the team does not encrypt state. The ADR says so, and the generated
  documentation of each such attribute says so.

## Open questions

- Which OpenTofu versions have ephemeral values and write-only attributes,
  and how the adapter behaves with an older version. This needs a check with
  `make e2e` against each supported version.
- Whether the Pulumi CLI has a way to keep a provider input out of state in
  the versions that OpenDustpan supports. If it has, decision 6 changes.
