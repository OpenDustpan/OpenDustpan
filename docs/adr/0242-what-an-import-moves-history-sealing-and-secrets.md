# 242. What an import moves: history, sealing and secrets

Status: accepted · 2026-10-19. Part of ADR 241. Supersedes in part ADR 29 decision 5. Extends ADR 150 decision 3. Accepts RFC 81 questions 3 and 5

## Context

ADR 241 makes an import a resource that the server runs. The owner asked
for an import of state and history (ADR 241 decision 1). The word "history"
means a different thing for each tool (ADR 13):

- For the Pulumi CLI, the history of a stack is its list of updates. Each
  update has a kind (`update`, `preview`, `refresh`, `destroy` or
  `import`), a result, a start and an end time, a message, the environment
  of the CLI, the config of the run, a count of resource changes and engine
  events. Each update that changed state has a checkpoint version.
- For OpenTofu, the history of a workspace is its list of state versions.
  Each version has a serial, a lineage, an MD5, a time and the run or user
  that made it. A state backend that is not a TFE-compatible API keeps the
  current state only, unless its storage keeps object versions.

Facts:

- ADR 41 decision 10 seals OpenTofu state blobs and outputs with the data
  key of the workspace scope. ADR 151 gives each history item its own item
  key. ADR 217 puts the namespace key above the data keys.
- ADR 37 decision 4 keeps the deployment of a Pulumi checkpoint as the exact
  bytes that the CLI sent.
- Under the service secrets provider of a Pulumi Cloud-compatible API, a
  secret in a checkpoint is ciphertext that only the source can decrypt,
  through its `decrypt` and `batch-decrypt` routes.
- Under the passphrase provider or a cloud KMS provider, the CLI encrypts
  each secret itself. The server holds no passphrase and no KMS access for
  such a stack.
- OpenTofu state that a client encrypted (client-side state encryption)
  cannot be read by the server. The OpenTofu cloud backend still sends a
  plain `json-state-outputs` document (ADR 41).

## Decision

### The three classes

1. **Verbatim, mapped and lost.** The plan of an import (ADR 241 decision 8)
   puts each part of a source unit in one class, and the docs page lists
   them:
   - **Verbatim:** the import keeps the bytes or the value as the source
     answers it.
   - **Mapped:** the import keeps the meaning in an OpenDustpan field with
     another shape, and the plan names the field.
   - **Lost:** the import does not move it. The plan lists it as "not
     moved" with the reason.

### History items

2. **Each source version becomes one history item (product manager).** An
   import moves the whole history of each source unit by default. The
   options `historyLimit` (the count of the newest versions) and `since` (a
   time) make it shorter. The current state always moves. Each imported
   item is a full state item. The server stores no delta and no journal for
   it.

   Benchmark: GitLab imports the full history of a repository by default.
   ADR 29 decision 5 left the count to the team, because a CLI replay costs
   one command for each version. A server-side read costs no work of a
   person.
3. **Two times and two actors.** An imported history item records:
   - `at`: the time that the source gives the version. The view of a unit
     at a time (ADR 150 decision 12) and blame use it. It is a claim of the
     source. Each API answer and each console view of an imported item
     labels it "time in the source", next to `importedAt`.
   - `importedAt`: the time of the commit of the import.
   - the actor of ADR 150 decision 3: the principal that ran the import,
     its token ID, and an empty job ID.
   - `sourceActor`: the name of the user or the run that made the version
     in the source, as text. The import never maps it to a principal of
     OpenDustpan. It is a claim of the source. The import removes each
     control character and cuts it to at most 256 bytes of valid UTF-8
     (`unitimport.SanitizeSourceActor`). Each reader renders it as text
     only.
   - `importID` and `sourceVersion`: the import and the ID of the version
     in the source.

   For the upload source, `at`, `sourceActor` and the tool metadata come
   from the client, and the item labels them "reported by the client" (ADR
   241 decision 21).

   Imported items are in the order of the source (the order of `Versions`
   of ADR 243 decision 2), not in the order of `at`. The times of S3 object
   versions and of the files of a Pulumi DIY backend can go backward.

   So a history item never claims that a principal of OpenDustpan made a
   change in another product, and the audit log never states a time or an
   actor of the source as a fact of OpenDustpan. This extends ADR 150 decision 3 with
   `sourceActor`, `importID` and `sourceVersion`, as optional fields.
4. **Blame runs as for new items.** After each imported state item commits,
   the tool computes blame for it (ADR 150 decision 4), oldest first. A
   client-encrypted OpenTofu item is `opaque` (ADR 150 decision 9).

### Pulumi CLI

5. **What moves from a Pulumi Cloud-compatible API.**

   | Part | Class | Where |
   | --- | --- | --- |
   | The deployment of each checkpoint version | verbatim, apart from secret ciphertext (decision 8) | the checkpoint of the imported update |
   | The update kind, result, message, start and end time | verbatim | the imported update |
   | The environment of the CLI (for example the git commit) and the plain config values of the run | verbatim | the imported update |
   | The secret config values of the run | lost. Only the keys move, each marked "value not moved" | the imported update |
   | The count of resource changes | verbatim | the imported update |
   | The update user | mapped | `sourceActor` (decision 3) |
   | The stack tags | mapped | the stack tags |
   | The project name and the stack name | verbatim | the target names of the import equal them (below) |
   | The engine events of each update | lost (decision 16) | listed in the plan |
   | Deployments, schedules, webhooks, teams, access and policy packs of the source | lost | listed in the plan |
   | The stack config file `Pulumi.<stack>.yaml` | not on the server | the config rewrite of decision 9 |

   Benchmark: `pulumi stack export --version` reads each checkpoint
   version from a service backend, and `pulumi stack history` reads the
   update list. The import reads the same data through the same API.

   **The names stay (product manager).** The URNs in a checkpoint hold the
   stack name and the project name (`urn:pulumi:<stack>::<project>::...`).
   So for the Pulumi CLI, the target project name and stack name of an
   import unit must equal the source names. Only the namespace changes.
   Else the dry run fails that unit with `import.rename-unsupported`. A
   person renames a stack after the release with `pulumi stack rename`.

   Benchmark: `pulumi stack import` keeps the URNs of the deployment as
   they are, and `pulumi stack rename` rewrites them after the import.

   **Secret config of an update (product manager).** The config of an
   update record holds each secret value as ciphertext under the key of
   the source. The import moves only the key of each secret config value,
   marked "value not moved", and never the ciphertext. So the import never
   moves ciphertext that the target cannot decrypt (decision 8). The
   current secret config values of a stack move with the config rewrite of
   decision 9.

   Benchmark: the GitLab project import does not move the values of CI/CD
   variables.
6. **Pulumi DIY backends.** The S3 adapter (ADR 243 decision 7) also reads
   the bucket layout of a Pulumi DIY backend: the current checkpoint of
   each stack and the checkpoints of its history directory. Each history
   checkpoint becomes one history item with the time of its file. The kind
   of each item is `update`, because the DIY layout keeps no other kind in
   a form that the server can rely on. The e2e step of ADR 244 decision 11
   decides if the layout gives more. A file backend imports through the
   upload source (ADR 241 decision 3).
7. **Exact bytes, apart from secrets.** ADR 37 decision 4 keeps the bytes
   that the CLI sent. An imported checkpoint keeps the bytes that the source
   answered, with two changes, both under the service secrets provider of
   decision 8: the new ciphertext of each secret value, and the new
   `secrets_providers.state` that names the target stack. The ledger row
   (ADR 241 decision 7) keeps the SHA-256 of the source bytes and of the
   stored bytes.

### Sealing and secrets

8. **Secrets under the service provider are re-encrypted (owner).** For a
   stack under the service secrets provider of the source, the import
   creates the target stack with its own data key (ADR 23) under the
   namespace key (ADR 217). The import reads the secrets provider of each
   checkpoint version, because a stack can change its provider during its
   history. For each checkpoint version under the service provider, oldest
   first, it:
   1. finds each value with the Pulumi secret signature,
   2. sends the ciphertexts to the `batch-decrypt` route of the source, in
      batches,
   3. encrypts each plain value with the data key of the target stack,
   4. writes the new ciphertext in the same place of the deployment,
   5. drops each plain value from memory before the next batch,
   6. writes a new `secrets_providers.state` that names the target stack
      in place of the source URL, organization, project and stack. The
      CLI reads this state to decrypt, so without the change a decrypt on
      the target fails after `pulumi logout` from the source.

   The plain values exist only in the memory of the instance, for one
   batch. No plain value reaches SQL, a blob, a log or an audit entry. The
   audit entry of the import unit holds the count of re-encrypted values
   (ADR 241 decision 17). The dry run sends one sample decrypt for each
   unit (ADR 241 decision 8). A source that refuses `batch-decrypt` fails
   the import unit with the error code `import.source-decrypt-refused`. The
   import never moves service-provider ciphertext that the target cannot
   decrypt.
9. **The config rewrite (product manager).** The secret config values of a
   stack live in `Pulumi.<stack>.yaml`, as ciphertext of the source. The
   create of an import takes, for each stack, an optional list of these
   ciphertexts. The import decrypts them through the source and encrypts
   them with the data key of the target stack, as in decision 8. The import
   unit answers the new ciphertexts, in the same order. The
   `opendustpan import run` command reads each `secure:` value from the
   stack file, sends it, and writes a new stack file next to the old one.
   It never writes a plain value to disk.

   Benchmark: `pulumi stack change-secrets-provider` rewrites the
   `secure:` values of the stack file in place. ADR 29 decision 3 moves
   each value with `pulumi config --show-secrets --json`, which shows the
   plain value to the person.
10. **Other secrets providers stay as they are (product manager, RFC 81
    question 5).** For a
    stack under the passphrase provider or a cloud KMS provider, the import
    moves the ciphertext verbatim. The stack keeps its provider. The plan
    and the move check (ADR 244 decision 4) report the provider with a
    warning, and give the command that changes it after the move:
    `pulumi stack change-secrets-provider default`. The server never asks
    for a passphrase or a KMS credential of a stack.

    Benchmark: ADR 29 decision 3. The Pulumi CLI keeps the provider of a
    stack at `stack import`, and the person changes it with one command.
11. **The server seals every imported state at rest (owner).** An imported
    state item goes through the same store path as a state item that the
    CLI writes. So the server seals each imported OpenTofu state blob and
    outputs document with the data key of the workspace scope (ADR 41
    decision 10), and each imported Pulumi checkpoint with its item key
    (ADR 151). Each item key is under the namespace key (ADR 217). An import adds
    no path that writes plain state. A backup of an imported unit is the
    same as a backup of any other unit.

### OpenTofu

12. **What moves from a TFE-compatible API.**

    | Part | Class | Where |
    | --- | --- | --- |
    | The state file of each state version | verbatim | the state version |
    | The serial, the lineage and the MD5 | verbatim | the state version |
    | The time of the version | verbatim | `at` (decision 3) |
    | The `json-state-outputs` document, where the source gives it | verbatim | the outputs document of the version |
    | The `sensitive_values` marks of the `json-state` rendering, where the source gives it | mapped | the kept marks of ADR 41 decision 9 |
    | The run or the user that made the version | mapped | `sourceActor` |
    | The workspace name, description and tags | mapped | the workspace |
    | Runs, plans, logs, policy checks, run tasks, variables, variable sets, team access, run triggers, notifications and agent pools | lost | listed in the plan |

    When the source gives no outputs document, the server reads the outputs
    from the state file. When it gives no `json-state` rendering, the read
    masks each attribute value (ADR 41 decision 8).
13. **What moves from other OpenTofu backends.**
    - **S3 and S3-compatible storage.** With bucket versioning on, each
      object version of a state key is one state version, oldest first, with
      the time of the object version. With versioning off, the current
      state only. The adapter finds the workspaces of a key from the
      workspace key prefix of the backend (`env:` by default).
    - **The OpenTofu `http` backend.** The current state only.
    - **The upload source.** Each version that the client sends.

    For each of them, the serial, the lineage and the bytes are verbatim,
    and the MD5 is the MD5 of the bytes.
14. **Client-encrypted state moves as it is.** The import keeps the bytes of
    a state that a client encrypted. It cannot read the serial, the lineage
    or the resources. It marks the item `opaque` (ADR 150 decision 9), and
    the move check relies on the plan of decision 4 of ADR 244. The plan of
    the import says that the team must keep its `encryption` block and its
    key.
15. **State from Terraform later than 1.5.** When the `terraform_version`
    of a state is later than 1.5, the plan gives a warning that the state
    may not load in OpenTofu. The import still moves it.

### What does not move now

16. **No engine events, run logs or plans now (product manager, RFC 81
    question 3).** An import moves state and the metadata of each version.
    It does not move the engine events of a Pulumi update, or the runs,
    plans and logs of a TFE-compatible workspace. The plan lists them as
    "not moved". An option for the engine events of a Pulumi
    Cloud-compatible source can come later, with its own e2e step.

    Benchmark: `tfmigrate` moves state only. The GitLab project import does
    not move the logs of CI jobs.

## What this ADR replaces

ADR 1 forbids edits to an accepted ADR. So this ADR records what it
replaces:

- **ADR 29, decision 5:** "P2 and T1 move the current state only." An import
  through the API moves the history directory of a Pulumi DIY bucket and
  each object version of a versioned S3 bucket. The guide path still moves
  the current state only.
- **ADR 150, decision 3:** the actor has three fields. An imported item
  adds `sourceActor`, `importID` and `sourceVersion` (decision 3).

## Consequences

- `pkg/stack` and `pkg/workspace` each get a port method that writes one
  imported state item with its times and its source fields. Each method gets
  its conformance tests before an adapter implements it.
- A long history costs one full stored item for each version. The plan of
  the import gives the total bytes before the run, so the team can set
  `historyLimit`.
- An import of a Pulumi stack under the service provider sends one
  `batch-decrypt` request to the source for each batch of secrets. The
  source can see and log these calls. The guide says so.
- Imported engine events, run logs and plans are not part of this design
  (decision 16).
- The masked digest of the move check (ADR 244 decision 3) leaves out
  `secrets_providers.state`, because the import rewrites it.
