# 23. Secrets and the built-in key-encryption key

Status: accepted · 2026-10-10. Decision 8 superseded in part by ADR 217, which also makes the namespace key the wrapper of the data keys of a namespace. Decision 4: the rewrap command wraps namespace key versions (ADR 217 decision 21). Decision 2 superseded in part by ADR 237

Changed by:

- ADR 41 changes decisions 2, 3 and 6: key generation, start without a key and
  the audit routes.
- ADR 41 extends decision 1: a flag for the path of the key file.
- ADR 45 changes decision 6: the batch limit of the secrets routes.
- ADR 47 extends decision 5: the final delete happens when retention ends.
- ADR 151 extends decision 3: item key wrapper IDs count in the start-up
  check.
- ADR 217 changes decisions 4 and 8: the namespace key wraps data keys, the
  rewrap command.
- ADR 237 changes decision 2: dev makes its own key in the data directory.

## Context

The Pulumi CLI sends every secret config value to the server to be encrypted
and decrypted (the "service" secrets provider). RFC 3 proposed the rules for
this, and part 2 of RFC 3 covers only secrets. The `secret` package already
implements them: `Service`, the `Wrapper` port and the `KeyStore` port. No
built-in `Wrapper`, no `KeyStore` adapter, no route and no setting for the
key-encryption key exist yet
([issue 34](https://github.com/OpenDustpan/OpenDustpan/issues/34)).

ADR 19 gives each stack a stable ID. The scope of a data key uses this ID.
ADR 6 reads the admin token from `DUSTPAN_ADMIN_TOKEN` only, never from a flag,
so that it does not show in process lists.

The project owner delegated this decision to an agent session on 2026-10-10.
The owner allowed the agent's reviewed recommendation to become the decision.
The owner can supersede it with a new ADR.

## Decision

The "Secrets" section of
[RFC 3](../rfc/0003-domain-modules-for-updates-secrets-and-state.md), and its
`secret` module item, this ADR accepts as written. The scope uses the stack ID
(ADR 19). A handler resolves the stack name in the route to the ID. Part 3
(OpenTofu state) of RFC 3 stays open.

1. **Source of the built-in key-encryption key.** The key comes from the
   environment variable `DUSTPAN_KEY_ENCRYPTION_KEY`, or from the file that
   `DUSTPAN_KEY_ENCRYPTION_KEY_FILE` names. The file must not be readable by
   group or others. The server never reads the key from a command-line flag,
   because flags show in process lists. This is the style of
   `DUSTPAN_ADMIN_TOKEN`.
2. **Format.** The value is 32 random bytes, encoded in base64. The server
   never generates the key. A generated key would sit next to the database
   that it protects.
3. **Start-up.** If the database holds wrapped keys, and the key is missing or
   does not match their wrapper ID, the server refuses to start. The error
   names the variable. If the database holds no wrapped key and the operator
   sets no key, the server starts. Encrypt and decrypt then answer with an
   error that names the variable.
4. **Wrapper ID.** The wrapper ID of the built-in key is `builtin-` and the
   first 16 hexadecimal characters of HMAC-SHA256, keyed with the
   key-encryption key, over the fixed text `opendustpan wrapper id v1`. A
   one-way function makes it, so it does not contain the key. This ADR fixes
   the form now, so that a later rewrap command can rotate the key-encryption
   key with no format change. The rotation itself is a follow-up. Data keys
   never rotate, because their ciphertexts live in the stack config of the
   users.
5. **Delete.** The store deletes the data key of a stack in the same
   transaction as the final delete of the stack. Soft delete (RFC 17) decides
   when the final delete happens. A conformance test covers this. Old
   ciphertexts of that stack then cannot be read.
6. **Limits and routes.** The limits are those of RFC 3. The server advertises
   the `batchEncryption` capability. It caps the decompressed size of the
   request body. It always serves `batch-decrypt`, because the CLI has no
   fallback. The audit routes `decrypt/log-decryption` and
   `decrypt/log-batch-decryption` answer 204 and store nothing until the audit
   log (RFC 22) exists.
7. **Authorization.** Two new tool-neutral actions on a unit (ADR 13, ADR 17),
   in the style of `namespace:read`: `unit:encrypt` and `unit:decrypt`. They do
   not reuse `stack:read` and `stack:write`. A viewer who may read a stack does
   not get its plaintext secrets by this right alone.
8. **Customer-managed keys.** RFC 33 builds on the `Wrapper` port as its only
   seam. A namespace may choose its wrapper later. This ADR does not decide
   that.

## Consequences

- Open work for the milestone 1 exit test, from the
  [brief for issue 34](https://github.com/OpenDustpan/OpenDustpan/issues/34#issuecomment-6103294917):
  1. A built-in `Wrapper` adapter and its configuration (decisions 1, 2 and 4).
  2. A `KeyStore` conformance suite, SQLite and PostgreSQL adapters, and
     numbered migrations. The suite includes the delete rule (decision 5).
  3. Wiring in `dustpan.NewServer`, an option, and the settings and start-up
     checks of `cmd/opendustpan` (decisions 1 to 3).
  4. Routes for encrypt, decrypt, batch-encrypt, batch-decrypt and the two
     audit routes. Each route declares its action (ADR 17) and has an entry in
     the route test. The `batchEncryption` capability and the body limit
     belong here. So do the error mappings of `ErrDecrypt` and `ErrTooLarge`.
  5. E2E steps: `config set --secret`, `config get`, `preview`, `up`,
     `stack export` (the secret stays encrypted) and a rename that keeps the
     secrets.
- A lost key-encryption key means lost secrets. The documents must say this.
  Backup rules belong to RFC 8.
- Follow-up work: a rewrap command to rotate the key-encryption key, RFC 33
  (customer-managed keys), and RFC 22 (the audit log for the two audit routes).
- A server without a key can start only while the database holds no wrapped
  key. This keeps a server for other uses possible, and it still protects
  existing secrets.
