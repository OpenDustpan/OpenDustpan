# 217. Customer-managed keys for each namespace: the key hierarchy and rotation (owner)

Status: accepted · 2026-10-18 · accepts RFC 33 · supersedes in part ADR 23 (decision 8) and ADR 151 (decision 2)

## Context

The project owner decided on 2026-10-18, as the project manager relayed:

1. Customer-managed keys (bring your own key) for each namespace, built
   now. The customer key wraps the namespace key. The namespace key wraps
   the data keys and the item keys.
2. Rotation is envelope rotation. A new key version encrypts new data. Old
   versions only decrypt. The data keys are wrapped again at each rotation
   of the key above them. A full re-encryption runs only after a compromise
   or on request, as a background job with progress and audit entries.
3. The client of each key management service (KMS) is not decided. A
   measurement compares SDKs, hand-written clients and adapters outside the
   server. The port must fit all three.

ADR 215 makes the namespace the tenant. An earlier draft of this ADR had an
account key between the customer key and the namespace key. The owner did
not take the account, so this ADR has no account level.

What exists:

- ADR 23: a data key for each scope, wrapped by the `Wrapper` port with the
  built-in key-encryption key. Data keys never rotate, because the Pulumi
  CLI keeps ciphertexts of the stack data key. Decision 8 sends
  customer-managed keys to RFC 33.
- ADR 41: OpenTofu state and outputs sealed at rest. Decision 12: the key
  file and the start-up rules of the key-encryption key.
- ADR 151: one random item key for each history item, wrapped by the
  `Wrapper` and written as the blob `item-keys/<item key ID>`. A backup
  target holds one copy of each key blob for all of its backups, so one
  delete reaches each of them.
- ADR 204 decision 4: the private keys of the issuer of a namespace are
  wrapped by a data key of the namespace scope `issuer`.

## Decision

### The hierarchy

1. **Four levels (owner).** From the top:

   | Level | One for each | Wraps | Where it is kept |
   | --- | --- | --- | --- |
   | Key-encryption key | installation | each namespace key version, the data keys of `@server`, and the sign-in secrets of each namespace (decision 6) | outside the server: the key file of ADR 41, or a KMS key of the operator |
   | Customer key (optional) | namespace | each namespace key version of its namespace | in the customer's KMS. The server never holds it. |
   | Namespace key | namespace, with versions | the data keys (ADR 23, ADR 41, ADR 204) and the item keys (ADR 151) of the namespace | SQL, in the namespace key record |
   | Data key, item key | scope, history item | content | SQL for data keys, the blob store for item keys (ADR 151 decision 2) |

   - A namespace key version is a random 256-bit key. The server never
     derives one key from another key. A namespace key ID has the prefix
     `nk-`, and each version has a number that starts at 1.
   - The data keys of `@server` stay wrapped by the key-encryption key.
   - RFC 33 asks for one key for each namespace or for each unit. The
     answer: one customer key and one namespace key for each namespace. The
     data keys and item keys of ADR 23 and ADR 151 stay as they are.
2. **The wrap of a namespace key version.** With no customer key, the
   key-encryption key wraps it. With a customer key, the customer key wraps
   it, and the key-encryption key wraps the result. So the server needs both
   keys to open a namespace. The customer can revoke access alone. A key
   policy of the customer that is too wide does not open a namespace
   without the key-encryption key of the operator.
3. **One construction for each KMS.** The KMS services do not all bind
   associated data. So each adapter wraps a fresh random 256-bit key with
   the KMS, and the server seals the namespace key version with AES-256-GCM
   under that key. The associated data is the namespace ID, the namespace
   key ID, the version and the purpose `namespace-key`. A KMS that takes a
   context also gets these values as its context:
   - **AWS KMS**: `Encrypt` and `Decrypt` with an encryption context.
   - **Google Cloud KMS**: `encrypt` and `decrypt` with
     `additionalAuthenticatedData`.
   - **Azure Key Vault**: `wrapKey` and `unwrapKey` with `RSA-OAEP-256`.
     The key operation takes no context, so only the server binds the
     values.
   - **HashiCorp Vault Transit**: `encrypt` and `decrypt`. A key with
     derivation takes the namespace ID as its `context`.
4. **A wrapper ID for each level.** Each wrapped key records the ID of each
   wrapper above it and the KMS key version that the KMS answered. A start
   check (ADR 23 decision 3) refuses to start when the key-encryption key
   does not match the records.
5. **The namespace key replaces the key-encryption key below the
   namespace.** ADR 151 decision 2 says that the `Wrapper` wraps an item
   key. The namespace key now wraps it, with the same associated data. The
   data keys of a namespace (ADR 23) are wrapped by the namespace key too.
   This decides what ADR 23 decision 8 left open.
6. **The sign-in secrets of a namespace never depend on the customer key.**
   The client secret and the signing keys of the identity provider of a
   namespace (ADR 216) are wrapped by the key-encryption key directly. The
   associated data holds the namespace ID and the purpose `sign-in`. So a
   revoked customer key never locks the namespace administrator out: the
   administrator can still sign in, read the key state and enable the key
   again.

### The port

7. **One port fits each kind of client.** The owner did not decide the
   client of each KMS (RFC 76). The `Wrapper` port gets this shape, so that
   an SDK client, a hand-written client on `net/http`, and an adapter
   outside the server can each implement it:
   - `Wrap(ctx, key, plaintext, associatedData)` answers the wrapped bytes
     and the KMS key version.
   - `Unwrap(ctx, key, wrapped, associatedData)` answers the plaintext.
   - `Rewrap(ctx, key, wrapped, associatedData)` wraps again with the
     current KMS key version. An adapter with no rewrap call in its KMS
     implements it as `Unwrap` then `Wrap`. AWS KMS has `ReEncrypt`, and
     Vault Transit has `rewrap`.
   - `Status(ctx, key)` answers whether the key can wrap now.
   - Each method answers one of three error classes: `ErrUnavailable`
     (decision 13), `ErrRevoked` (decision 14), or `ErrInvalid` for a
     request that the KMS refuses for another reason.

   `key` is an opaque reference string that the adapter parses, for
   example an AWS KMS key ARN. No type of a cloud SDK goes through the port.
   An adapter outside the server talks a small protocol over a Unix socket
   with the same four calls, as the Kubernetes KMS v2 plugin does with
   `Status`, `Encrypt` and `Decrypt`. ADR 208 already gives an external
   adapter protocol for profile providers, which such a protocol can
   follow. RFC 76 decides whether that protocol exists. Each adapter of each kind runs the `Wrapper` conformance suite
   and an error-class suite.

### Setting the customer key

8. **A namespace administrator sets it.** New actions, in the
   administration family, held by the namespace role `admin`:
   - `namespace:manage-key` sets, changes and clears the customer key and
     rotates the namespace key.
   - `namespace:re-encrypt` starts a full re-encryption.
   - `namespace:read-key` reads the key state, the KMS identity and the
     progress of a rotation or re-encryption.

   The operator
   action `installation:re-encrypt-namespace` (operator family) starts a
   re-encryption after a compromise of a key of the installation, and ADR
   216 decision 4 lists it. Each of these actions, except
   `namespace:read-key`, needs a fresh MFA step (ADR 56) and writes an
   entry in the audit scope of the namespace. The request names the key: an AWS KMS key ARN, a
   Google Cloud KMS key resource name, an Azure Key Vault key URL, or a
   Vault Transit mount and key name.
9. **One identity for each namespace at the KMS.** The server calls the KMS
   of a namespace with an identity of that namespace only, so a key policy
   that names it cannot serve another namespace. The operator makes the
   identity. The answer of the set-key flow names it, so that the customer
   can put it in its key policy:

   | KMS | Identity of one namespace | What the key policy names |
   | --- | --- | --- |
   | AWS KMS | one IAM role for each namespace, in the account of the operator. The server assumes it. | the role, and the condition `kms:EncryptionContext:opendustpan:namespace` equal to the namespace ID |
   | Google Cloud KMS | one service account for each namespace. The server impersonates it. | the service account, with the role that allows encrypt and decrypt on the key |
   | Azure Key Vault | one application for each namespace, with a federated credential | the application, with the role that allows wrap and unwrap on the key |
   | Vault Transit | one JWT auth role for each namespace, bound to the namespace ID as the subject. The server signs the JWT. | a policy that allows `encrypt`, `decrypt` and `rewrap` on the key for that role |

   The role is in the account of the operator, and only the server assumes
   it, so the role has no external ID. The key policy of the customer
   names the role and the encryption context instead.

   The JWT for Vault and the token for the Azure federated credential are
   signed by an issuer of the installation. Its keys are wrapped by the
   key-encryption key directly, as decision 6 does for sign-in secrets,
   never by a key of the namespace. A namespace key never depends on a key
   that it wraps.

10. **Checks before the change.** The server changes nothing until each
    check passes:
    - It wraps and opens a test value with the key and the identity of
      the namespace.
    - **A confused deputy probe.** Where the KMS policy can test the
      context (AWS KMS), the server tries to encrypt with a context that
      holds a new random namespace ID. When the KMS accepts it, the key
      policy does not require the context of this namespace, and the server
      refuses the key. The error names the condition key
      `kms:EncryptionContext:opendustpan:namespace`. A random ID never
      names a real namespace, so the probe never encrypts for another
      tenant.
    - Then, in one transaction, it writes the new wrap of each namespace
      key version. The old wraps stay until the new ones commit.
    - The first set of a customer key, and each change to another key, also
      rotates the namespace key (decision 18). So the version that wraps new data keys and item keys under the new customer key was never wrapped without it. Older versions stay readable from an earlier copy until the server destroys them (decision 15).
11. **The probe runs again at each rewrap.** The daily rewrap of decision
    17 runs the probe of decision 10 again. When the probe now passes for
    the random ID, the server keeps the key in use, writes an audit entry
    in the scope of the namespace, sends a notification to its
    administrators, and lists the namespace in the admin access report
    (ADR 70 decision 29).

### Use, caching and failure

12. **The cache.** Each instance keeps the open namespace key versions of a
    namespace in memory for `keys.namespaceKeyCacheSeconds`: 300 by
    default, from 60 to 3600. The open data keys and item keys of the
    namespace leave memory with them. While the namespace has requests, the
    instance opens the namespace key again in the background at half the
    time. So a KMS outage that is shorter than half the time has no effect.
    A customer sees one KMS call for each instance at each refresh, not
    one for each request.
13. **The KMS cannot answer.** A timeout, a network error, a 5xx answer or
    a rate limit of the KMS gives `ErrUnavailable`:
    - Requests that need content of the namespace answer 503 with
      `Retry-After` and the error code `namespace.key-unavailable` (ADR
      182). The message names the customer key and the KMS. Content is
      state, checkpoints, secrets, history items, plans, job logs, profile
      secrets and issuer signatures.
    - Requests that need no key still work: sign-in, the lists of units,
      the inventory, the audit log and the settings of the namespace.
    - The server refuses the start of new work. Work in progress (a Pulumi
      update with a lease, a held OpenTofu lock, a running job phase) keeps
      the keys of its own unit that it has open, for at most
      `keys.inFlightGraceSeconds` after the cache time ends. The grace is
      900 seconds by default. The installation sets its maximum, and a
      namespace administrator can set a lower value for the namespace. 0
      turns it off. A state write after a real change to infrastructure
      then has a chance to land.
14. **The customer revokes the key.** The KMS answers that the key is
    disabled, scheduled for deletion, or that the identity has no access.
    Each adapter maps its own errors to `ErrRevoked`, for example AWS
    `DisabledException` and `AccessDeniedException`, Google Cloud
    `PERMISSION_DENIED`, Azure 403, and Vault 403. An error that the adapter
    does not know is `ErrUnavailable`, never `ErrRevoked`.
    - The instance that gets `ErrRevoked` writes the key state `revoked` to
      the namespace key record in SQL. Each open of a data key or an item
      key reads the key state in the same read as the key record. So every
      instance stops each new open at once, also with a namespace key in
      its cache.
    - Work in progress keeps only the keys of its own unit that it already
      holds open, for at most the grace of decision 13. Then each instance
      drops them.
    - **The worst case.** The time from the revoke in the KMS to the last
      use of a key is at most the cache time plus the grace: 300 + 900 =
      1,200 seconds with the defaults. With a grace of 0, it is at most the
      cache time. The docs and the console of the key state show this
      number for the current settings.
    - Requests that need content answer 403 with the error code
      `namespace.key-revoked`. The message names the customer key and says
      that a namespace administrator can enable it again. Requests that
      need no key still work, so the namespace administrator can read the
      audit log and the key state.
    - While the key state is `revoked`, the server tries the key again each
      minute. The first success sets the state to `active` and writes an
      audit entry.
15. **What a revoke and a delete of the customer key reach.** The server
    deletes no data because of a revoked key. When the customer deletes
    the key in its KMS, no one can open the sealed content of the
    namespace in the database, the blob store or any backup. Sealed content
    is the content of decision 13, and each secret value of the namespace.
    This is a crypto-shred of the sealed content only. These stay readable
    to the server and the operator, because the server needs them with no
    key:
    - the IDs and names of the namespace, its collections and units, and
      their tags and settings,
    - the inventory projection and the resource index, which hold resource
      types and cloud IDs from state,
    - the records of history items, with their times, sizes, authors and
      kinds, but not their content,
    - the audit entries, the job records and the dependency edges,
    - the principals, groups, roles and grants of the namespace.

    The crypto-shred does not reach: a copy of the database that the
    operator made before the customer key was set or changed (a dump, a
    replica, a write-ahead log archive), because it holds a namespace key
    version and data keys under the key-encryption key only, or under the
    earlier customer key. It also does not reach a backup that holds such a
    version, until the server destroys that version (decision 18). A data key of a Pulumi stack
    never changes, so such a copy opens its secret values. The docs of the customer key list these cases, so that no customer reads "crypto-shred" as a delete of all data.
16. **Errors in the tool-compatible APIs.** The Pulumi Cloud-compatible API
    and the TFE-compatible API answer the same statuses, in the error shape
    of each API. `make e2e` shows what each CLI prints for 503 and 403
    before this ships.

### Rotation

17. **The customer key (owner).** A rotation in the KMS makes a new key
    version with the same key reference. The server needs nothing for it.
    The schedule `namespace-key.rewrap` calls `Rewrap` for each namespace
    key version every 24 hours, so that the current customer key version
    wraps each one. After one day, the customer can disable the old KMS
    key version. A change to another key uses decision 10.
18. **The namespace key (owner).** A rotation makes a new namespace key
    version. A schedule rotates it every 365 days by default, from 90 to
    2,560 days. A namespace administrator can rotate it with
    `POST /api/dustpan/v1/namespaces/{namespace}/key/rotate`.
    - The new version wraps each new data key and item key. The old
      versions only unwrap.
    - A durable operation (ADR 72) then wraps each data key and each item
      key of the namespace again under the new version. The keys and the
      content do not change. Only their wrap changes.
    - The rewrap writes each item key as a new blob,
      `item-keys/<item key ID>/v<namespace key version>`, and the key record
      names the current blob. The server never writes one blob name two
      times, so "written one time" in ADR 151 decision 2 stays. Before the
      write, the rewrap reads the key record and stops if the record has a
      shred time. After the write, it reads the record again. If a shred
      committed in between, it deletes the new blob. The next backup to each
      target copies the new blob. The server deletes the old blob from the live store when the key record names the new blob. It deletes the old blob from a target when no backup in that target names its namespace key version in its manifest (ADR 42). A
      shred deletes every blob under `item-keys/<item key ID>/` in the live
      store and in each target.
    - The server destroys an old version when no wrapped key in the live store uses it. It also waits until no backup that the `backups` table holds names it in its manifest. Until then the version only unwraps.
    - The operation records its progress (keys done, keys left). The API
      answers it, and the audit scope of the namespace gets an entry at the
      start and at the end.
19. **Data keys and item keys do not change at a rotation.** A rotation
    above them changes their wrap only. A data key of a Pulumi stack never
    changes, because the Pulumi CLI keeps ciphertexts under it (ADR 23
    decision 4).
20. **A full re-encryption runs only after a compromise or on request
    (owner).** A namespace administrator starts it with
    `POST /api/dustpan/v1/namespaces/{namespace}/key/re-encrypt`, with a
    fresh MFA step. The operator can start it after a compromise of a key of
    the installation. It is a durable operation (ADR 72) that runs in the
    background:
    - It makes a new item key for each history item, seals the content
      again under it, and then shreds the old item key (ADR 151).
    - It makes a new data key for each scope where no client holds a
      ciphertext, for example OpenTofu state (ADR 41) and the issuer scope.
      It lists each Pulumi stack whose data key it cannot change, and RFC 76
      asks how such a stack gets a new data key.
    - It records its progress: items done, items left, bytes done. The API
      answers the progress. The audit scope of the namespace gets an entry
      at the start, at each 10 percent, and at the end.
    - It lists each backup that still holds content under an old item key.
21. **The key-encryption key.** The rewrap command of ADR 23 decision 4
    wraps each namespace key version and each sign-in secret again. It does
    not touch customer keys. A restore of a backup made before a
    rotation of the key-encryption key needs the old key-encryption key,
    because the backup holds the wraps under it.

### Backup and restore

22. **A backup holds wrapped keys only.** A backup holds the namespace key
    records, the data key rows and the item key blobs, each wrapped. It
    never holds the key-encryption key, a customer key or an open key. A
    backup writes item key blobs only to a backup target that the `backups`
    table names (ADR 218 decision 4).
23. **A restore of the installation** needs the key-encryption key. The
    check before apply (ADR 38 decision 3) opens each namespace key
    version. A namespace whose customer key fails does not stop the
    restore. The check lists the namespace, and the namespace restores with
    the key state `revoked`.
24. **A restore of a namespace** (ADR 218) needs each namespace key version
    that wraps a key in its backup. So a version stays while a backup
    record names it (decision 18).

### Audit and metrics

25. **Entries in the audit scope of the namespace.** The server writes an
    entry for: a set, change or clear of the customer key, each failed check
    of decision 10, each probe of decision 11 that passes for the random ID,
    each rotation, each re-encryption, and each change of the key state
    (`active`, `unavailable`, `revoked`). It writes one entry for each
    change of state, not one for each failed request.
26. **Entries in the customer's KMS.** Each call names the namespace ID,
    the namespace key ID and the purpose in the context of decision 3. So
    the KMS log of the customer (AWS CloudTrail, Google Cloud Audit Logs,
    the Vault audit device) names the namespace and the purpose of each
    call. Azure Key Vault logs take no context, so the audit scope of the
    namespace is the record there.
27. **Metrics.** `dustpan_kms_requests_total{adapter,result}` counts KMS
    calls, with no namespace label (ADR 32 decision 5).

### Self-hosted installations

28. **The same feature.** A namespace on a self-hosted installation can set
    a customer key, as each namespace on OpenDustpan Cloud can (ADR 59
    decision 2). With no customer key, the key-encryption key wraps each
    namespace key version, and the operator sees no change in behavior.
29. **No conversion.** No tag shipped a wrapped data key or an item key
    blob, so the server has no code that converts the old wrap (ADR 73).
    The migration collapse before the next tag makes the server refuse a
    database of an untagged build with `ErrSchemaDiverged`.

## Benchmarks

Designed against these, as a comparison only, never a compliance claim:

- AWS KMS key rotation: automatic rotation every 365 days by default, a
  period from 90 to 2,560 days, and old key material kept to decrypt.
  Decisions 17 and 18 use the same period range.
- Google Cloud KMS key rotation: a new primary version encrypts, the old
  versions decrypt until they are disabled, and data is encrypted again
  only when the customer asks. Decisions 17, 18 and 20.
- NIST SP 800-57 Part 1 Rev. 5: the key hierarchy, and cryptoperiods with
  an originator usage period (wrap) shorter than the recipient usage period
  (unwrap). Decisions 1 and 18.
- PCI DSS v4.0 requirement 3.7: key management procedures, key changes at
  the end of a cryptoperiod (3.7.4), and the replacement of a key that is
  weakened or suspected to be compromised (3.7.5). Decisions 18 and 20.
- CSA Cloud Controls Matrix v4, the domain Cryptography, Encryption and Key
  Management (CEK), with its controls on key rotation, revocation and
  destruction. Decisions 14, 15 and 18.
- AWS KMS encryption context, key policy conditions and the AWS guidance on
  the confused deputy problem: decisions 3, 9, 10 and 11.
- The Kubernetes KMS v2 plugin API, as a comparison of a key adapter
  outside the server: decision 7.

## Not decided here

- The client of each KMS adapter, and whether the protocol for an adapter
  outside the server exists (RFC 76).
- How a Pulumi stack gets a new data key after a compromise (RFC 76).

## Consequences

- `pkg/secret` gets the namespace key records with versions, the port of
  decision 7 with its conformance and error-class suites, and the cache of
  decision 12.
- The error codes `namespace.key-unavailable` and `namespace.key-revoked`
  are added to the list of ADR 182.
- `make e2e` gets steps for a KMS that cannot answer and for a revoked key,
  with a fake KMS adapter, for both CLIs.
- The backup manifest (ADR 42) names the namespace key versions of the
  backup.
- ADR 23 and ADR 151 get status lines. RFC 33 is accepted.
