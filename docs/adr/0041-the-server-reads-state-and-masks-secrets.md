# 41. The server reads state and masks secrets

Status: accepted · 2026-10-11 · decision 10 superseded in part by ADR 151. Decision 13 superseded in part by ADR 103. Decision 12 superseded in part by ADR 238. Decisions 9 and 13 superseded in part, and decision 5 extended, by ADR 240

Changed by:

- ADR 60 changes decisions 5 and 13: the audit switch, new kinds and an opt-in
  hash chain.
- ADR 103 changes decision 13: the audit entry gets the field Agent.
- ADR 146 extends decision 13: the audit channel gets the value chat.
- ADR 151 changes decision 10: the item key seals OpenTofu state in place of
  the workspace key.
- ADR 238 changes decision 12: no default key file, no prompt, no generate
  flag.
- ADR 240 changes decisions 9 and 13: a state download by a person writes
  reveal.
- ADR 240 extends decision 5: fail closed for the reveal entry of a download.
- ADR 352 extends decision 10: the CI shim encrypts the plan and state files of the team CI at rest.

## Context

[RFC 39](../rfc/0039-reading-state.md) asks if the server may read the
contents of state. The glossary says that OpenDustpan stores state but never
reads its contents. ADR 13 and ADR 28 call state opaque. This rule blocks a
resource view, a state diff, resource search, and an answer for the secret
outputs of RFC 15.

The rule is already not true:

- The `stack` package reads the resource types of each checkpoint to count
  resources for delete protection.
- The TFE adapter keeps the outputs document of each OpenTofu state version.
  It keeps sensitive values in plain text in the `outputs` column of migration
  0010. The list route answers sensitive values as null. The route for one
  output serves the value.
- OpenTofu state blobs are not encrypted at rest. ADR 30 says that a backup
  holds OpenTofu state in plain text.

The OpenTofu cloud backend sends plain text to the server, also when the team
uses client-side state encryption. In
[`internal/cloud/state.go`](https://github.com/opentofu/opentofu/blob/52ade2f770d7b375798023db67db0b8d56653838/internal/cloud/state.go)
at commit `52ade2f`:

- Lines 220 to 228 write the state file with the encryption, then read it
  back with the same encryption. The result is the plain state.
- Lines 230 to 245 render that plain state as `json-state` (when the provider
  schemas are present) and as `json-state-outputs`.
- Lines 288 to 296 (the fallback) and 311 to 322 (the upload) send the state
  file, the `json-state` rendering and the `json-state-outputs` document.

So the server gets plain text in all cases. The real choice is what the
server keeps.

The research section "The case for reading state (RFC 39)" in the product
direction document compares other products. Most of them show resources and
keep attribute values out of the view.

The project owner decided on 2026-10-11, verbatim: "I never said we shouldn't
permit state reads on the backend, a previous agent must have hallucinated it.
I like the Pulumi approach where everything is readable and it's gated by the
permission model, with secret decryption requiring an extra call (the
attribute values are masked in ESC and Pulumi stacks until you hit a radio
button) which also generates an audit log. Stack read and stack read secret
are permissions in Pulumi, but we have fine grained access so we can make it
as granular as we want."

The owner asked an agent session to write the exact rules. The owner can
supersede them with a new ADR.

## Decision

This ADR accepts RFC 39 with the model of the owner. The indexer of the RFC,
with its fixed list of fields, is not part of this decision.

1. **The server may read state.** The server may read the contents of every
   state that it stores, for each tool. Each read is an endpoint with a
   declared action on a unit (ADR 13, ADR 17).
2. **A read masks secret values.** A state read answers the structure and the
   values of a state. It replaces each secret value with a marker. The marker
   holds the value path of the value and never the value. The response also
   lists the value path of each marker. This list names each masked value: a
   real value with the same shape as a marker is not a marker.
3. **Value path.** A value path is a JSON Pointer (RFC 6901) into the state
   document as the tool stores it. For the Pulumi CLI this is the deployment
   of a checkpoint. For OpenTofu this is the state file. An example is
   `/resources/3/instances/0/attributes/password`. The path is valid for one
   state version.
4. **Reveal is a separate call.** To see a secret value, a client calls
   reveal with the unit, the version and one value path. The call answers the
   plain value of that path only. The console shows a masked value until the
   user selects reveal.
5. **Each plain secret writes an audit entry.** Each response that gives a
   secret value in plain text writes one audit entry: a reveal, each state
   read that decrypts secrets, and each plan, apply or update that needs them
   (decisions 9 and 13). The project owner decided this on 2026-10-11. The
   entry has the fields of decision 13, with the kind of access. The server
   writes the entry before it answers. If the write fails, the call fails and
   answers no value. The server then writes a log line and counts the failure
   in a metric. An audit entry is a write in a namespace, so it carries the
   namespace fence.
6. **Actions.** The actions follow the names in `pkg/identity`:
   - `unit:read`: the unit, its history, and the masked state read. It also
     covers the outputs of the unit with secret values masked, and the
     Pulumi `stack export`, which holds secrets only as ciphertext.
   - `unit:read-secrets`: every response that holds a secret value in plain
     text. This is the reveal call. It is also each tool route that must
     answer plain secret values to the CLI (decision 9). Each such response
     writes an audit entry (decision 5). The Pulumi decrypt routes keep
     `unit:decrypt` (ADR 23), and also write an audit entry.
   - `namespace:read-audit`: the list of audit entries of a namespace.
   - `unit:decrypt` and `unit:encrypt` stay as ADR 23 sets them. They are the
     protocol of the Pulumi CLI for any ciphertext of a stack.

   A role (RFC 19) can give `unit:read` without `unit:read-secrets`. RFC 15
   can add an action for outputs only. This ADR adds no such action.
7. **Pulumi CLI.** A secret in a checkpoint is a value with the Pulumi secret
   signature. Under the service secrets provider (ADR 23), it holds ciphertext
   under the data key of the stack. The read masks each secret, with any
   secrets provider. Reveal decrypts the ciphertext with the data key of the
   stack. If the stack uses another secrets provider, reveal answers an error
   that names the provider. The server cannot decrypt it.
8. **OpenTofu.** The read masks:
   - each output with `"sensitive": true`,
   - each attribute path in `sensitive_attributes` of a resource instance,
   - each path that `sensitive_values` marks in the `json-state` rendering of
     that version. This rendering adds the marks of the provider schemas.

   The `sensitive_attributes` list in the state file might not hold the marks
   of the provider schemas. So when the server has no `json-state` marks for a
   version, the read masks all attribute values of each resource instance. A
   state that the client encrypted cannot be read. The read answers its
   outputs only, and says that the client encrypted the state.
9. **What the server keeps from OpenTofu.**
   - The state file: kept, sealed (decision 10). The server answers it to the
     CLI as the CLI sent it.
   - The `json-state-outputs` document: kept, sealed. The CLI reads outputs
     of other workspaces from it.
   - The `json-state` rendering: the server reads the paths that
     `sensitive_values` marks, and keeps only those paths. It discards the
     rest of the rendering. It never keeps a value from it.

   The state download and the route for one sensitive output answer plain
   secret values. So they need `unit:read-secrets`, and each call writes an
   audit entry of the kind `tool-operation`, with an empty value path for a
   download. A user who runs `tofu plan` needs `unit:read-secrets`, as in the
   TFE permission model.
10. **Sealed at rest.** The server seals OpenTofu state blobs and the
    `outputs` column with the data key of the workspace scope. The scope is
    the namespace and the workspace ID (ADR 19, ADR 23). The server seals a
    state blob in chunks, so it never holds a whole state in memory. The MD5
    and the size of a state version stay those of the plain bytes. The data key
    of a workspace goes with the final delete of the workspace (ADR 23,
    decision 5). Plain state then stops reaching the database, the blob store
    and backups.
11. **Existing rows.** A numbered migration adds what the sealed rows need.
    A blob key is immutable (ADR 26), so the server seals an existing state
    blob into a new key and moves the pointer in a fenced transaction. A row
    records if the server sealed it. The server reads both forms until it has
    sealed every row.
12. **The key, with no silent default.** The server needs a key-encryption key
    to seal state. The project owner decided this on 2026-10-11:
    - **Sources.** `DUSTPAN_KEY_ENCRYPTION_KEY` holds the key, as ADR 23
      sets. The key file has a flag now, in the style of `--tls-key`:
      `--key-encryption-key-file` (`DUSTPAN_KEY_ENCRYPTION_KEY_FILE`). The
      flag holds a path, never the key, so ADR 23 decision 1 still holds.
    - **Default path.** The default key file is `dustpan.key` next to the
      SQLite database, as for `--blob-dir`. With PostgreSQL, it is in the
      working directory.
    - **Prompt on a terminal.** When no key and no key file exist, and `serve`
      runs on a terminal (TTY), `serve` asks before it makes a key file. The
      prompt says where the server will write the file. It also says that an
      operator who loses the file loses every secret that the key encrypts,
      and that nobody can recover them. The server makes the file only when
      the operator agrees.
    - **Opt-in without a terminal.** `--generate-key-encryption-key`
      (`DUSTPAN_GENERATE_KEY_ENCRYPTION_KEY=true`) makes the key file with no
      prompt. It is for an operator who chose the single binary with no
      terminal on purpose. It never overwrites a file that exists.
    - **Error.** When no key and no key file exist, no terminal is present
      and the opt-in is not set, `serve` stops before it opens the database.
      The error names each setting that helps: `--generate-key-encryption-key`
      (`DUSTPAN_GENERATE_KEY_ENCRYPTION_KEY`), `--key-encryption-key-file`
      (`DUSTPAN_KEY_ENCRYPTION_KEY_FILE`) and `DUSTPAN_KEY_ENCRYPTION_KEY`.
      The server never makes a key without the prompt or the opt-in.
    - **Made file.** The server writes 32 random bytes in base64 (ADR 23
      decision 2), readable by the owner only. At each start, a log line
      names the path of the key file that the server uses, also when the
      opt-in made it. No log line holds the key.
    - **Containers.** The server does not block the single binary in a
      container. The documents for the reference container deployment
      strongly recommend a mounted key file or the environment variable.
    - **Upgrade check.** The upgrade check (ADR 33) reports an installation
      that holds OpenTofu state and has no key.
13. **The audit store.** RFC 22 stays open for the full audit log. This ADR
    adds the smallest store that decision 5 needs: append-only entries in
    SQL, with a conformance suite and a REST route to list them. No API
    changes or deletes an entry. The two audit routes of ADR 23
    (`decrypt/log-decryption` and `decrypt/log-batch-decryption`) write
    entries of the kind `reveal` to this store. The Pulumi CLI calls them
    when a person asks it to show secrets. The e2e suite checks which
    commands call them. The `decrypt` and `batch-decrypt` routes write
    entries of the kind `tool-operation`. This ADR fixes the entry schema now, so that RFC 22 can
    add events and fields without a change of meaning:
    - **Who:** the principal subject, the subject at the identity provider
      when the principal has one, and the access token ID when the principal
      has one. The schema keeps a column for each, also before the server
      can fill it.
    - **What:** the action and the object (a unit reference or a namespace),
      and for a state read, the version and the value path.
    - **When:** UTC, in Unix milliseconds.
    - **Where:** the source IP address that the server sees, the user agent,
      and the channel (API or console). The client declares the channel, so
      the entry records it as declared. A rule for trusted proxies is for
      RFC 22.
    - **Kind of access:** a field with exactly one of two values, so that a
      reader never confuses them:
      - `tool-operation`: the tool needed the secrets for its own work, for
        example a plan, an apply, an update, or a read of remote state. The
        server gives the whole state or a batch of values to the CLI.
      - `reveal`: a person asked to see a secret value, through the console,
        a CLI or the API. The reveal call of decision 4 and the two audit
        routes of ADR 23 write this kind.

      The server cannot see who runs a CLI command. So a person who runs
      `tofu state pull` makes an entry of the kind `tool-operation`. The
      documents say this.
    - **Outcome:** `success`, `failure` or `denied`.
    - **Request ID:** the `X-Request-Id` of the request (ADR 32).
    - **Hash chain:** each entry holds the SHA-256 of its own fields and of
      the previous entry of its namespace. A check call reads the chain of
      a namespace. The chain shows a change or a deletion of an entry. It
      does not stop a person who can write to the database and compute the
      chain again. An anchor outside the database is for RFC 22.

    The next steps in RFC 22 are retention, export to a SIEM, and the full
    catalog of events.
14. **Order of work.** Each route lands with its e2e step first (ADR 12) and
    with its actions in the route table (ADR 17). The sealing at rest comes
    first, because it changes no API. The route paths and field names are for
    the issues to set. An e2e step on each CLI version shows that no CLI
    rejects them.

## What this ADR replaces

ADR 1 forbids edits to an accepted ADR. So this ADR records what it replaces:

- **GLOSSARY.md, "State":** "OpenDustpan stores state but never reads its
  contents." This change updates the glossary to this ADR.
- **ADR 13:** the words "opaque to the server" and "inside opaque state" in
  the table, and "Components and modules stay inside opaque state". The server
  may read state now. The rest of that decision stays: the server does not
  model components or modules, and the tool models stay separate.
- **ADR 28, decision 3:** "The server never reads or rewrites state." A
  restore still never rewrites state. The server may read state for the
  routes of this ADR. Decision 9 of ADR 28 says that a content diff needs
  RFC 39. A diff now needs only its own issue, with masked values.
- **ADR 30, decision 7:** "A backup holds OpenTofu state in plain text." After
  decisions 10 and 11, a backup holds sealed OpenTofu state.
- **ADR 23, decisions 2 and 3:** "The server never generates the key", and
  "If the database holds no wrapped key and the operator sets no key, the
  server starts." After decision 12, `serve` makes a key file after the
  prompt on a terminal or with the opt-in, and stops with an error when no
  key, no terminal and no opt-in are present.

## Consequences

- Open work, in this order:
  1. Seal OpenTofu state blobs and outputs at rest, with the migration, the
     sealing of existing rows, and backups (decisions 10 to 12).
  2. The new actions in `pkg/identity`, with route table tests (decision 6).
     The OpenTofu download and the route for one output move to
     `unit:read-secrets`.
  3. The audit store and its list route (decisions 5 and 13).
  4. A masked state read for each tool (decisions 2, 3, 7 and 8).
  5. The reveal call with its audit entry (decisions 4 and 5).
- Before roles exist (RFC 19), only administrators pass the new actions. So
  no client loses access now.
- Each `tofu plan` and `tofu apply` writes an audit entry of the kind
  `tool-operation` for the download. So does each Pulumi decrypt call.
  The audit store grows with each run until RFC 22 decides retention.
- A hash chain puts the appends of one namespace in a sequence. Each append
  reads the last entry of its namespace in the same transaction.
- Every installation now needs a key-encryption key, supplied, or made after
  the prompt or with the opt-in. A server with no key, no terminal and no
  opt-in does not start. The e2e
  suite, the container image and its documents must supply a key.
- A lost key means lost OpenTofu state, as it means lost secrets for the
  Pulumi CLI (ADR 23). A key file next to the data does not protect a backup
  of that data. A backup without the key cannot restore state. The documents
  must say this.
- A team that encrypts state on the client still sends plain outputs and a
  plain `json-state` rendering. The server keeps the outputs sealed and keeps
  no value from the rendering. The documents must say this.
- RFC 15 can now choose its first answer for Pulumi secret outputs: the
  server may read the outputs of a checkpoint.
- Resource search and a state diff can build on the masked read. Each needs
  its own issue. A search index that keeps values needs a new ADR.
