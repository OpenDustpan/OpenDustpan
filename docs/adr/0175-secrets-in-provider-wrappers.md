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
   of a file that holds the value. The path goes into a plan, and the
   credential does not.
   - One function, `lifecycle.WithFileForms`, adds the file forms for a
     declared provider and for an SDK provider. `lifecycle.CheckFileForms`
     checks a configuration. A required sensitive attribute becomes
     optional, and the configuration must set exactly one of `token` and
     `token_file`. For an optional one, it can set at most one.
   - `WithFileForms` refuses a name whose file form is longer than 64
     characters, so a sensitive name has at most 59 characters. It also
     refuses an attribute whose name is the file form of another one.
   - `lifecycletest` checks the file forms of each provider, and configures
     it with the file form of each secret of the fixture.
3. **The file form on a runner.** The launcher writes each credential of the
   profile as a file on its memory-backed volume (ADR 134 decisions 4 and
   7), and gives the program of the step only the path, in a variable that
   the profile names. The credential then never passes through OpenTofu or
   the Pulumi CLI at all:
   - OpenTofu: the profile sets a variable of the root module, for example
     `TF_VAR_ipam_token_file`, to the path. The code of the unit passes
     `var.ipam_token_file` to the `token_file` attribute of the provider.
   - Pulumi: the profile sets a variable whose name ends in `_FILE`, for
     example `IPAM_TOKEN_FILE`, to the path. The Pulumi program reads it and
     passes it to the `tokenFile` input of the provider.

   The variable holds a path, not a credential, so this keeps the rule
   that no credential passes through an environment variable.
4. **Ephemeral values for OpenTofu.** The documentation tells OpenTofu users
   to set a sensitive configuration attribute from an ephemeral value, or
   to use the file form. Either keeps the credential out of a saved plan.
5. **Secrets for Pulumi.** The Pulumi adapter marks each sensitive
   attribute, of the configuration and of a resource, as a secret in the
   package schema. The Pulumi CLI then stores it as ciphertext.
6. **Sensitive resource attributes.** A sensitive attribute is masked in
   plans and in the logs of the provider. OpenTofu stores it in state as it
   stores any sensitive value of any provider. The documentation says so
   and points to OpenTofu's state encryption.
7. **Write-only attributes.** The planner keeps the configured value of a
   write-only attribute in the planned values that `Create` and `Update`
   get, so the engine sends it to the API (PM, for the project owner). The
   adapter makes it null, with `lifecycle.ForTool`, in each value set that
   it returns to its tool (ADR 172 decision 2). The OpenTofu adapter also
   declares the attribute as write-only, so OpenTofu never stores it. The
   Pulumi CLI keeps every input in state. The Pulumi adapter marks a
   write-only attribute as a secret, so it is in state as ciphertext only.
   This is the case where "avoidable" ends. `lifecycletest` checks that the
   write-only value of the fixture reached the fake of the remote system.
8. **No secret in a public place.** A sensitive value never goes in a command
   argument, an environment variable, a URL path or a query (ADR 171
   decision 9). The HTTP engine adds a credential after it logs the request,
   never follows a redirect, and needs HTTPS (ADR 173).
9. **Files of the CLI engine.** For each `{file:config.name}`, the engine
   writes the value to a file of mode 0600 in a directory of mode 0700 of
   the session. It writes it once for each session and deletes it when the
   session closes. The SDK writes the files of `Call.SecretFile` (ADR 178)
   in the same directory.
   - When the user gives the file form, the engine passes that path and
     writes nothing. On a runner, this is the way to avoid a write to disk:
     the launcher already wrote the file on its memory-backed volume.
   - `TMPDIR` on a runner is often on disk. The engine makes the session
     directory in a memory-backed directory when one exists and is writable
     by the user, for example `$XDG_RUNTIME_DIR` or `/dev/shm` on Linux.
     Else it uses `TMPDIR`, and the documentation says that the file form
     avoids the write.
   - A process that gets `SIGKILL` cannot delete its files. The name of a
     session directory holds the process ID of the provider. At its next
     start, the engine deletes each session directory of the same user
     whose process no longer runs.
10. **Masking.** The engines replace each sensitive value with `(sensitive)`
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
