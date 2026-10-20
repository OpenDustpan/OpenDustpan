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
ADR 41 found that the `cloud` block at commit `52ade2f` sends plain state
and plain outputs also when the client encrypts. The http backend sends the
envelope. Whether each path of the TFE-compatible API sends plain state is
UNCONFIRMED: another TFE-compatible server reported encrypted state from
the `remote` backend.

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
4. **Gates fail closed.** A fact that comes from the contents of such a
   state is `available == false`. A gate that reads it meets the rule of
   ADR 226 decision 3: `available == false` matches its `deny` or approval
   outcome. A missing contents never passes a gate.
5. **A key in a profile is the team's choice.** A profile (ADR 200) can
   hold any value, so a team can put a key there for its runners. The
   server never uses that value to read state. The user documentation says
   that a key in a profile lets the operator of the server read the state,
   and so removes what client encryption gives.
6. **The escape hatch.** A team that wants search, the graph and the state
   facts turns client encryption off for the unit. The server seals the
   state at rest, and the namespace can use its own key (ADR 217).
7. **Plans of runner jobs.** A runner job of such a unit still uploads the
   `plan-display` part of its plan (ADR 102), which the runner makes with the key of the team.
   The user documentation says that this plan reaches the server in plain
   text, masked as each plan is.

Benchmark: AWS S3 and Google Cloud Storage keep data that the client
encrypted as opaque bytes, and the service cannot index it. A service that
must read data uses server-side encryption with a key that the customer
controls (AWS KMS, Google Cloud CMEK). PCI DSS asks that keys be stored in
the fewest possible locations.

## Consequences

- A team that encrypts state on the client keeps that guarantee against
  the operator of the server.
- That team loses the features that read state, for that unit only. The
  console says why.
- The check of what each path of the TFE-compatible API sends (plain or
  envelope) needs an e2e step for the `cloud` block and the `remote`
  backend with encryption on.
