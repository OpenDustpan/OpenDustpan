# 217. Customer-managed keys for each account: the key hierarchy (owner)

Status: accepted · 2026-10-18 · accepts RFC 33 · supersedes in part ADR 23 decision 8 and ADR 151 decision 2

## Context

The project owner decided on 2026-10-18, as the project manager relayed:
customer-managed keys (bring your own key) for each account (ADR 215), built
now. The customer's key in its key management service (KMS) wraps the
account key. The account key wraps the namespace keys and the item keys.

What exists:

- ADR 23: a data key for each scope, wrapped by the `Wrapper` port with the
  built-in key-encryption key. Data keys never rotate, because the Pulumi
  CLI keeps ciphertexts of the stack data key. Decision 8 sends
  customer-managed keys to RFC 33.
- ADR 41: OpenTofu state and outputs sealed at rest. Decision 12: the key
  file and the start-up rules of the key-encryption key.
- ADR 151: one random item key for each history item. The `Wrapper` wraps
  it. The server writes the wrapped item key one time, as the blob
  `item-keys/<item key ID>`. A shred deletes that blob and marks the record.
- ADR 204 decision 4: the private keys of the issuer of a namespace are
  wrapped by a data key of the namespace scope `issuer`.
- RFC 33 asks: one key for each namespace, or for each unit?

## Decision

### The hierarchy

1. **Five levels.** From the top:

   | Level | One for each | Wraps | Where it is kept |
   | --- | --- | --- | --- |
   | Key-encryption key | installation | each account key, and the data keys of `@server` | outside the server: the key file of ADR 41, or a KMS key of the operator |
   | Customer key (optional) | account | the account key | in the customer's KMS. The server never holds it. |
   | Account key | account | the namespace keys of the account, and the data keys of the account scope | SQL, in the account key record |
   | Namespace key | namespace | the data keys of the namespace (ADR 23, ADR 41, ADR 204) and its item keys (ADR 151) | SQL, in the namespace key record |
   | Data key, item key | scope, history item | content | SQL for data keys, the blob store for item keys (ADR 151 decision 2) |

   - The account key and the namespace key are random 256-bit keys. The
     server never derives one from another key. An account key ID has the
     prefix `ak-`, and a namespace key ID has the prefix `nk-`.
   - The data keys of the installation scope (`@server`) stay wrapped by
     the key-encryption key directly. The key-encryption key keeps its
     meaning in `GLOSSARY.md`: the key outside the database at the top of
     the hierarchy.
   - RFC 33 asks for one key for each namespace or for each unit. The
     answer: the customer key is one for each account. The namespace key
     gives each namespace its own key inside the account. The data keys and
     item keys of ADR 23 and ADR 151 stay as they are.
2. **The wrap of the account key.** With no customer key, the
   key-encryption key wraps the account key. With a customer key, the
   customer key wraps the account key, and the key-encryption key wraps the
   result. So the server needs both keys to open an account. The customer
   can revoke access alone, and a key policy of the customer that is too
   wide does not open an account without the key-encryption key of the
   operator.
3. **One construction for each KMS.** The four KMS adapters do not all bind
   associated data. So each adapter wraps a fresh random 256-bit key with
   the KMS, and the server seals the account key with AES-256-GCM under
   that key. The associated data is the account ID, the account key ID and
   the purpose `account-key`. A KMS that takes a context also gets the same
   values as its context:
   - **AWS KMS**: `Encrypt` and `Decrypt` with an encryption context.
   - **Google Cloud KMS**: `encrypt` and `decrypt` with
     `additionalAuthenticatedData`.
   - **Azure Key Vault**: `wrapKey` and `unwrapKey` with `RSA-OAEP-256`.
     The key operation takes no context, so only the server binds the
     values.
   - **HashiCorp Vault Transit**: `encrypt` and `decrypt`. A key with
     derivation takes the account ID as its `context`.

   Each adapter implements the `Wrapper` port and runs its conformance
   suite. The key-encryption key can use the same four adapters, set in the
   static layer.
4. **A wrapper ID for each level.** Each wrapped key records the ID of each
   wrapper above it. A start check (ADR 23 decision 3) refuses to start
   when the key-encryption key does not match the records.
5. **The namespace key replaces the key-encryption key below the account.**
   ADR 151 decision 2 says that the `Wrapper` wraps an item key. The
   namespace key now wraps it, with the same associated data. ADR 23 data
   keys of a namespace are wrapped by the namespace key too. The rest of
   ADR 151 does not change: the wrapped item key is written one time, and a
   shred deletes its blob. This ADR decides what ADR 23 decision 8 left open.

### Setting the customer key

6. **A tenant administrator sets it.** `account:manage-key` (ADR 216) sets,
   changes and clears the customer key. Each of these needs a fresh MFA
   step (ADR 56) and writes an entry in the audit scope of the account. The
   request names the key: an AWS KMS key ARN, a Google Cloud KMS key
   resource name, an Azure Key Vault key URL, or a Vault Transit mount and
   key name.
7. **Checks before the change.** The server changes nothing until each
   check passes:
   - It wraps and opens a test value with the key.
   - **One identity for each account.** The server calls the KMS of an
     account with an identity of that account only, so a key policy that
     names it cannot serve another account. On OpenDustpan Cloud, the
     operator makes that identity when it creates the account. RFC 76 gives
     its form for each KMS.
   - **A confused deputy check.** On AWS KMS, the server tries to encrypt
     with a context that names another account ID. When the KMS accepts
     it, the key policy does not require the context of this account, and
     the server refuses the key. The error names the condition key
     `kms:EncryptionContext:opendustpan:account`.
   - Then, in one transaction, it writes the new wrap of the account key.
     The old wrap stays until the new one commits.

### Use, caching and failure

8. **The cache.** Each instance keeps the open account key of an account in
   memory for a time, the setting `keys.accountKeyCacheSeconds`: 300 by
   default, from 60 to 3600. The open namespace keys and data keys of the
   account leave memory with it. While the account has requests, the
   instance opens the account key again in the background at half the time.
   So a KMS outage that is shorter than half the time has no effect. A
   tenant can see one KMS call for each instance at each refresh, not one
   for each request.
9. **The KMS cannot answer.** A timeout, a network error, a 5xx answer or a
   rate limit of the KMS:
   - Requests that need content of the account answer 503 with
     `Retry-After` and the error code `account.key-unavailable` (ADR 182).
     The message names the customer key and the KMS. Content is state,
     checkpoints, secrets, history items, plans, job logs, profile secrets
     and issuer signatures.
   - Requests that need no key still work: sign-in, lists of namespaces and
     units, the inventory, the audit log and the account settings.
   - The server refuses the start of new work (ADR 215, the rule of RFC 68
     "A limit never stops work in progress"). Work in progress (a Pulumi
     update with a lease, a held OpenTofu lock, a running job phase) keeps
     the keys that it has open for up to `keys.inFlightGraceSeconds` after
     the cache time ends: 3600 by default, 0 turns it off. A state write
     after a real change to infrastructure then has a chance to land.
10. **The customer revokes the key.** The KMS answers that the key is
    disabled, scheduled for deletion, or that the identity has no access.
    Each adapter maps its own errors to "revoked", for example AWS
    `DisabledException` and `AccessDeniedException`, Google Cloud
    `PERMISSION_DENIED`, Azure 403, and Vault 403. An error that the
    adapter does not know counts as "cannot answer" (decision 9), never as
    "revoked".
    - At a "revoked" answer, every instance drops each open key of the
      account at once, also the keys of work in progress. So a revoke takes
      effect within the cache time of decision 8, 5 minutes by default.
    - The account gets the key state `revoked`. Requests that need content
      answer 403 with the error code `account.key-revoked`. The message
      names the customer key and says that a tenant administrator can
      enable it again. Requests that need no key still work, so the tenant
      can read its audit log and its key state.
    - While the key state is `revoked`, the server tries the key again each
      minute. The first success sets the state back to `active` and writes
      an audit entry.
    - The server deletes no data because of a revoked key. When the
      customer deletes the key in its KMS, the content of the account,
      in the database, the blob store and every backup, cannot be opened by
      anyone. That is a crypto-shred of the account.
11. **Errors in the tool-compatible APIs.** The Pulumi Cloud-compatible API
    and the TFE-compatible API answer the same statuses, in the error shape
    of each API. `make e2e` shows what each CLI prints for 503 and 403
    before this ships.

### Rotation

12. **The customer key.** A rotation inside the KMS (a new key version with
    the same key ID) needs nothing from the server. The schedule
    `account-key.rewrap` wraps the account key again with the current
    version every 24 hours, so the customer can disable old versions after
    one day. A change to another key uses decision 7.
13. **The account key.** `POST /api/dustpan/v1/account/key/rotate` and a
    schedule (365 days by default) make a new account key version. The
    server wraps each namespace key of the account again, in one
    transaction for each namespace. A version that wraps no namespace key
    and that no backup record names is deleted.
14. **The namespace key does not rotate.** A rotation would write each item
    key blob again, and ADR 151 writes each blob one time so that one
    delete reaches each backup. Data keys do not rotate either (ADR 23
    decision 4). RFC 76 keeps the rotation of a namespace key open.
15. **The key-encryption key.** The rewrap command of ADR 23 decision 4 wraps each
    account key again. It does not touch customer keys.

### Backup and restore

16. **A backup holds wrapped keys only.** A backup holds the account key
    records, the namespace key records, the data key rows and the item key
    blobs, each wrapped. It never holds the key-encryption key, a customer key or an
    open key.
17. **A restore of the installation** needs the key-encryption key. The check
    before apply (ADR 38 decision 3) opens each account key. An account
    whose customer key fails does not stop the restore. The check lists
    the account, and the account restores with the key state `revoked`.
18. **A restore of a namespace** (ADR 218) needs the account key version
    that wrapped its namespace key, so a version stays while a backup
    record names it (decision 13). A namespace backup restores only into
    its own account.

### Audit

19. **Entries in the account scope.** The server writes an entry for: a
    set, change or clear of the customer key, each failed check of
    decision 7, an account key rotation, each change of the key state
    (`active`, `unavailable`, `revoked`), and each rewrap. It writes one
    entry for each change of state, not one for each failed request.
20. **Entries in the customer's KMS.** Each call names the account ID, the
    account key ID and the purpose in the context of decision 3. So the
    KMS log of the customer (AWS CloudTrail, Google Cloud Audit Logs, the
    Vault audit device) names the account and the purpose of each call.
    Azure Key Vault logs take no context, so the account audit scope is the
    record there.
21. **Metrics.** `dustpan_kms_requests_total{adapter,result}` counts KMS
    calls, with no account label (ADR 32 decision 5).

### Self-hosted installations

22. **The same feature.** The default account can set a customer key, as
    each account on OpenDustpan Cloud can (ADR 59 decision 2). An operator
    that sets no customer key and keeps the key file of ADR 41 sees no
    change in behavior. The first start after the upgrade makes the account
    key and the namespace keys, and wraps each existing data key and item
    key again under them. It is a durable operation (ADR 72) that resumes
    after a crash.

## Benchmarks

Designed against these, as a comparison only, never a compliance claim:

- ISO/IEC 27001:2022 Annex A 8.24 (use of cryptography) and NIST SP
  800-57 Part 1 (key hierarchy and cryptoperiods): decisions 1, 12 and 13.
- SOC 2 CC6.1 (encryption that protects information assets): decisions
  2 and 10.
- AWS KMS encryption context and key policy conditions, and the AWS
  guidance on the confused deputy problem: decisions 3 and 7.
- The Pulumi Cloud and HCP Terraform customer-managed key features, as a
  comparison of what a buyer expects (RFC 33).

## Not decided here

- The SDK or hand-written client of each KMS adapter. Each SDK is a new
  dependency, which needs the owner (`CLAUDE.md`). ADR 31 made the S3
  adapter with no new dependency. RFC 76 asks.
- The rotation of a namespace key (decision 14).

## Consequences

- `pkg/secret` gets the account key and namespace key records, their
  ports and their conformance cases, and the cache of decision 8. Each KMS
  adapter runs the `Wrapper` conformance suite and an error-class suite
  for decision 10.
- The error codes `account.key-unavailable` and `account.key-revoked` are
  added to the list of ADR 182.
- `make e2e` gets steps for a KMS that cannot answer and for a revoked key,
  with a fake KMS adapter, for both CLIs.
- The backup manifest (ADR 42) names the account key version of each
  namespace key.
- RFC 33 is accepted.
