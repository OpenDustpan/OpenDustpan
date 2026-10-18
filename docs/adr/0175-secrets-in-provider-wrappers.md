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

ADR 101 decision 10, ADR 133 decision 13 and ADR 134 decide that the
launcher passes credentials to a tool step as files on a memory-backed
volume, never in environment variables. ADR 133 decision 13 supersedes ADR
101 decision 10 in part. The launcher writes the files of each step in a new
directory of that step, and deletes the directory when the step ends (ADR
134 decision 7). So the path of a credential file differs from step to step:
the plan phase and the apply phase of a job see different paths. The server
stores the plan of a plan phase, and the apply phase applies it (ADR 102).

## Decision

1. **A credential enters only through the provider configuration (owner).**
   It is a sensitive attribute of the configuration. A declaration holds no
   credential: each credential field of `auth` must be one reference to a
   sensitive configuration attribute (ADR 171). The built provider holds no
   credential either.
2. **A file form for each secret, for local use.** For each sensitive
   configuration attribute `token`, a built provider also accepts
   `token_file`, the path of a file that holds the value. The path goes
   into a plan, and the credential does not. A saved plan of OpenTofu, and
   the state of the Pulumi CLI, hold the path, so they depend on it: the
   apply of a saved plan, and the next update of a Pulumi stack, read the
   file at the same path. This fits a workstation, where the path stays.
   It does not fit a runner, where the path changes with each step
   (decision 3).
   - One function, `lifecycle.WithFileForms`, adds the file forms for a
     declared provider and for an SDK provider. `lifecycle.CheckFileForms`
     checks a configuration. A required sensitive attribute becomes
     optional, and the configuration must set exactly one of `token` and
     `token_file`, or neither when the variable of decision 3 names a file.
     For an optional one, it can set at most one.
   - `WithFileForms` refuses a name whose file form is longer than 64
     characters, so a sensitive name has at most 59 characters. It also
     refuses an attribute whose name is the file form of another one.
   - `lifecycletest` checks the file forms of each provider, and configures
     it with the file form of each secret of the fixture.
3. **A fixed variable for each secret, on a runner (PM, for the project
   owner).** On a runner, the path of a credential file is never in a plan
   or in state. The provider reads the path from an environment variable
   with a fixed name, one for each sensitive configuration attribute, not
   from an attribute:
   - The name is the provider name, an underscore, the attribute name and
     `_FILE`, in upper case. For the attribute `token` of the provider
     `ipam`, it is `IPAM_TOKEN_FILE` (`lifecycle.FileEnv`). The generated
     documentation of the provider names the variable of each sensitive
     attribute.
   - The launcher writes each credential of the profile as a file on its
     memory-backed volume (ADR 133 decision 12, ADR 134 decision 4 step 2
     and decision 7). The profile names the variable of the file, as it
     does for a shared credentials file of a cloud. OpenTofu and the
     Pulumi CLI start the provider with the environment of the step, so
     the provider gets the variable. The code of the unit sets neither the
     credential nor its path.
   - The provider uses the attribute `token` when the configuration sets
     it, else `token_file`, else the file that the variable names.
     `lifecycle.CheckFileForms` accepts a required sensitive attribute that
     only the variable gives.
   - The plan and the state hold neither the credential nor the path. So
     the plan of the plan phase applies in the apply phase, with another
     path in the variable.

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
8. **No secret in a public place.** A sensitive or write-only value never goes in a command
   argument, an environment variable, a URL path or a query (ADR 171
   decision 9). The HTTP engine adds a credential after it logs the request,
   never follows a redirect, and needs HTTPS (ADR 173).
9. **Files of the CLI engine.** For each `{file:config.name}`, the engine
   writes the value to a file of mode 0600 in the provider session
   directory, of mode 0700. A provider session is what `Configure` returns
   (`lifecycle.Session`, ADR 172). The engine writes the file once for each
   provider session and deletes it when the provider session closes. The
   SDK writes the files of `Call.SecretFile` (ADR 178) in the same
   directory, and the operation deletes each one when its program ends.
   - When the user gives the file form, or the variable of decision 3
     names a file, the engine passes that path and writes nothing. On a
     runner, this is the way to avoid a write to disk: the launcher already
     wrote the file on its memory-backed volume.
   - On a runner, `TMPDIR` is in the work directory on disk and
     `XDG_RUNTIME_DIR` is not set (ADR 134 decision 6). The engine makes
     the provider session directory in a memory-backed directory when one
     exists and the user can write to it, for example `$XDG_RUNTIME_DIR` on
     a workstation or `/dev/shm` on Linux. Else it uses `TMPDIR`, and the
     documentation says that the variable of decision 3 avoids the write.
   - A process that gets `SIGKILL` cannot delete its files. The name of a
     provider session directory holds the process ID of the provider. At its
     next start, the engine deletes each provider session directory of the
     same user whose process no longer runs.
10. **Masking.** The engines replace each sensitive value and each
    write-only value with `(sensitive)` in diagnostics, in logs, and in the
    parts of reply bodies and standard error that they report. A write-only
    attribute needs no `sensitive` flag for this.

## Consequences

- A team on runners keeps its API tokens in an OpenDustpan profile (RFC 5).
  The launcher writes them as files, and the fixed variables name the
  paths. The token and its path are in no plan and in no state, and the
  token is in no environment variable.
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
