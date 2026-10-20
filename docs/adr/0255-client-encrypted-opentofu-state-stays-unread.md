# 255. Client-encrypted OpenTofu state stays unread, and the server holds no key for it

Status: accepted · 2026-10-20. Decided by the project manager under the owner's delegation of 20 October 2026. Keeps ADR 41 decision 8 and ADR 163 decision 6 for every feature that reads state. Applies ADR 226 decision 3 to client-encrypted state

## Context

Decided by the project manager under the owner's delegation of 20 October
2026, with the owner's decision rule.

OpenTofu 1.7.0 added the encryption of state and plan files on the client,
with AES-GCM and the key providers `pbkdf2`, `aws_kms`, `gcp_kms` and
`openbao`. 1.10.0 added the key provider `external`, which runs a program
of the team, and 1.11.0 added `azure_vault`
(<https://opentofu.org/docs/language/state/encryption/>, the change logs of
v1.7, v1.10 and v1.11). The encrypted state is a JSON envelope with `meta`,
`encrypted_data` and `encryption_version`. Only `serial` and `lineage` stay
in plain text, so that a backend can order the versions
(`internal/encryption/state.go`, and issue 1407 of `opentofu/opentofu`).

The server already handles this envelope. `workspace.IsClientEncrypted`
finds it, the serial and lineage checks of ADR 25 and ADR 49 use the plain
fields, a force-unlock that must fence the state fails with
`workspace.ErrClientEncrypted`, and a reveal of the contents answers 409.
We read the OpenTofu source for the three backends that can reach the
server:

- **The http backend** sends the envelope and nothing else. ADR 41 records
  this.
- **The `cloud` block** encrypts the state, decrypts it again, and uploads
  the plain `json-state` and the plain `json-state-outputs`, sensitive
  values included (`internal/cloud/state.go`, lines 220 to 247 and 311 to
  321). So the server receives a plain rendering of the state, also when
  the client encrypts.
- **The `remote` backend** uploads the encrypted raw state, but it uploads
  the outputs as plain JSON (`internal/backend/remote/backend_state.go`,
  lines 94 to 117).

So for a unit whose stored state is an envelope, the server cannot read
resources or outputs. The resource index has no entries for it (ADR 163
decision 6). Search, the unit graph from state, unmanaged findings, health
checks and the `state` facts of a gate do not cover it.

Two options:

- **A.** Accept the limit. The console and the API say "contents encrypted
  by the client".
- **B.** OpenDustpan gives the key through a profile, and, opt-in for each
  unit, decrypts on the server.

Option B makes the server a holder of the key. A team encrypts on the
client so that the operator of the backend cannot read the state. With B,
the operator can. The server already seals all state at rest (ADR 41
decision 10), and a namespace can bring its own key (ADR 217). So B gives
what the team has with no client encryption, with more keys in more places.

## Decision

1. **Option A.** The server never decrypts a client-encrypted state. No
   feature of the server asks for, stores or uses a key of state
   encryption.
2. **The unit says so.** The inventory entry, the context read (ADR 161)
   and the console page of such a unit say "contents encrypted by the
   client". Each feature that reads state names this reason when it does
   not cover the unit. It never shows the unit as empty or as healthy.
3. **What still works.** Locks, the fence, the serial and lineage checks,
   state versions, history, download as sent, restore of a version, and
   backups. These need no contents.
4. **Missing contents is a typed value.** A fact that comes from the
   contents of such a state is `available == false`. Built-in gates and
   starter packs treat `available == false` as deny or approval. The save
   check forces each other gate to read `available` (ADR 226 decision 3).
5. **A unit with an envelope keeps its outputs sealed.** For a unit whose
   stored state is an envelope, the server keeps no value from the plain
   `json-state`. It keeps the outputs sealed, as ADR 41 does, and it
   shows no output value in plain text. The user documentation says that
   client encryption keeps the contents from the server only with the http
   backend. With the `cloud` block and the `remote` backend, the outputs
   reach the server in plain text, and with the `cloud` block a rendering
   of the state reaches it too.
6. **A key in a profile is the team's choice.** A profile (ADR 200) can
   hold any value, so a team can put a key there for its runners. The
   server never uses that value to read state. The user documentation says
   that a key in a profile lets the operator of the server read the state,
   and so removes what client encryption gives.
7. **The escape hatch.** A team that wants search, the graph and the state
   facts turns client encryption off for the unit. The server seals the
   state at rest, and the namespace can use its own key (ADR 217).
8. **Plans of runner jobs.** A runner job of such a unit still uploads the
   `plan-display` part of its plan (ADR 102), which the runner makes with the key of the team.
   The user documentation says that this plan reaches the server in plain
   text, masked as each plan is.

Benchmark: AWS S3 and Google Cloud Storage keep data that the client
encrypted as opaque bytes, and the service cannot index it. A service that
must read data uses server-side encryption with a key that the customer
controls (AWS KMS, Google Cloud CMEK). PCI DSS asks that keys be stored in
the fewest possible locations.

## Consequences

- A team that encrypts state on the client with the http backend keeps
  that guarantee against the operator of the server. With the `cloud`
  block and the `remote` backend, the guarantee does not hold for the
  outputs, and for the `cloud` block it does not hold for a rendering of
  the state.
- That team loses the features that read state, for that unit only. The
  console says why.
- An e2e step for the `cloud` block and the `remote` backend with
  encryption on checks what each path sends, and that the server keeps
  no plain value for a unit with an envelope.
