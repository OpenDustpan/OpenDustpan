# Security model

This page says what the OpenDustpan server protects, how, and what it does not
protect. It describes the server as it is on `main` today. A planned change
carries the word "planned" and the number of the decision (ADR) or the open
proposal (RFC) that holds it.

OpenDustpan is pre-alpha. Do not keep production state on it.

## Sign-in

- **Administrator tokens are the only sign-in.** The server reads them from
  `DUSTPAN_ADMIN_TOKEN` (comma-separated) or from the file in
  `DUSTPAN_ADMIN_TOKEN_FILE`. No flag takes a token, because flags show in
  process lists. The server deletes `DUSTPAN_ADMIN_TOKEN` from its
  environment after it reads it.
- **Each token is at least 32 bytes.** The server refuses an empty or a
  duplicate token, and a token file that the group or others can read.
- **The server keeps no token in its database.** It holds a SHA-256 hash of
  each token in memory and compares in constant time.
- **Each tool sends the token in its own header:** the Pulumi CLI as
  `Authorization: token <token>`, OpenTofu as `Authorization: Bearer <token>`.
- **Two short-lived capabilities need no token.** A Pulumi update uses an
  update lease token. An OpenTofu state upload uses a random token in its
  URL. Each is 32 random bytes, and the server keeps only its SHA-256 hash.

Planned: users, teams and roles (RFC 19), scoped access tokens (RFC 20), and
single sign-on with directory sync (RFC 21).

## Authorization

- **Every token signs in as an administrator,** and an administrator can do
  every action. No other kind of principal exists today.
- **Every route declares its actions.** Before a handler runs, the server
  signs in the caller, finds the object of each action, and checks each
  action. A test in each API fails when a route has no action and is not on
  the explicit list of public routes.
- **Routes that need no sign-in:** `GET /api/capabilities`,
  `GET /.well-known/terraform.json`, `GET /api/v2/ping`, `GET /health/live`,
  `GET /health/ready`, and the files of the web console when you set
  `--console-dir`.

The actions already split, for example, reading a unit from reading its
secrets. With only administrators, that split has no effect yet.

## Pulumi secrets

The Pulumi CLI sends each secret config value to the server to encrypt and
decrypt it. The server uses envelope encryption:

- **One data key for each stack.** It is 256 random bits, and it encrypts
  the values of that stack with AES-256-GCM, a random 12-byte nonce, and the
  stack as associated data. A ciphertext of one stack does not decrypt as
  another stack.
- **The key-encryption key wraps each data key,** also with AES-256-GCM.
  The database holds only wrapped data keys.
- **The server checks the key at start.** When the database holds wrapped
  keys and the key-encryption key is missing or is a different key, the
  server refuses to start.
- **A stack delete deletes its data key.** After that, no old ciphertext of
  that stack can be decrypted.

The values stay encrypted in your stack configuration and in the checkpoints,
because the Pulumi CLI writes only the ciphertext there.

## OpenTofu state

The server seals each new OpenTofu state version before it stores it:

- **One data key for each workspace.** The key-encryption key wraps it, as
  for a Pulumi stack.
- **The server seals the state blob in chunks of 64 KiB** with AES-256-GCM. The
  associated data of each chunk binds it to its workspace, its position and
  the end of the stream. A changed, reordered or cut-off blob does not open.
- **The server also seals the outputs document in the database.**
- **A download answers the same bytes that the CLI sent.** The MD5 and the
  size of a state version stay those of the plain bytes.

State versions that an older server wrote stay plain text after the
upgrade. Planned: a change that seals the existing versions (ADR 41,
decision 11).

## The key-encryption key

`serve` needs a key-encryption key before it opens the database. It takes
the first of these:

1. `DUSTPAN_KEY_ENCRYPTION_KEY`: the base64 text of 32 random bytes. The
   server deletes the variable from its environment after it reads it.
2. The key file in `--key-encryption-key-file`
   (`DUSTPAN_KEY_ENCRYPTION_KEY_FILE`). The default is `dustpan.key` next to
   the SQLite database, or in the working directory for PostgreSQL. The
   server refuses a key file that the group or others can read.
3. No key and no key file: on a terminal, the server asks for an explicit
   yes and then makes the key file. With
   `--generate-key-encryption-key`, it makes the file with no prompt. Else
   it stops with an error that names each setting.

A key file that the server makes holds 32 random bytes in base64, with mode
0600. The server never writes over a key file that exists. Each start logs
the path of the key file, never the key. No flag takes the key itself.

The database and the backups hold no copy of the key. A lost key means lost
secrets and lost OpenTofu state: nobody can decrypt them. A copy of the data directory that holds
the default key file also holds the key, so keep the backups and the key
apart.

Planned: a command that rotates the key-encryption key (ADR 23), and keys
that a key management service holds (RFC 33).

## What the server encrypts at rest

The server encrypts only the data in the table below. It does not encrypt
the database or the blob store as a whole. Protect both, for example with
disk encryption and with access rules on the S3 bucket.

| Data | Where | Encrypted by the server |
| --- | --- | --- |
| OpenTofu state, with every sensitive value | Blob store, and the outputs in the database | Yes, for each version that this server wrote. Versions from an older server stay plain text |
| Pulumi checkpoints | Blob store, compressed | No. The secret values in them are ciphertext from the Pulumi CLI |
| Pulumi secret config values | Your stack configuration | Yes, as above |
| Wrapped data keys | Database | Yes, by the key-encryption key |
| Audit entries, with no secret value | Database | No |
| Backups | Backup target | Not as a whole. A backup holds the stored bytes: sealed state stays sealed. It holds no key-encryption key |

The server creates the blob directory with access for its owner only.

Encryption of a whole backup is not decided (ADR 30, RFC 8).

## Integrity

- **OpenTofu state: MD5.** The protocol of OpenTofu names each state version
  by its MD5. The server refuses an upload whose size or MD5 differs from
  what the client declared, and checks both again on each copy. MD5 finds
  accidental damage, for example a cut-off upload or a bad disk. It does not
  stop tampering: a person who can write the blob store can make a changed
  state with a matching MD5.
- **Pulumi checkpoints: SHA-256.** The server stores the SHA-256 of each
  checkpoint and checks it on each read.
- **Backups.** A restore checks the manifest, the dialect and the schema
  version, the SHA-256 of the SQL data, the key, and the size and MD5 of each
  blob. It writes nothing before the key check passes, and it compares the
  row counts at the end.

## Transport

- **TLS.** `--tls-cert` and `--tls-key` turn on HTTPS, with TLS 1.2 or later.
  On plain HTTP on an address that is not loopback, the server logs a warning
  and serves anyway. OpenTofu needs HTTPS.
- **Behind a reverse proxy.** The server never reads `X-Forwarded-*`
  headers. Set `--public-url` to the external URL.
- **Browsers.** Only origins in `--allowed-origins` get CORS headers.
- **Response headers.** The server sets no `Content-Security-Policy`,
  `Strict-Transport-Security`, `X-Content-Type-Options` or `X-Frame-Options`
  header. Set them in a reverse proxy if you need them.

## Logs

The server writes one log line for each request: the method, the route
pattern, the status, the duration, the size, a request ID, the API and the
signed-in subject. It does not write the path, the body or any token, so the
upload capability in a path stays out of the log. Tests send known values
through each API and check that the log and the metrics do not hold them.

## Limits

- Each request body has a size limit. The Pulumi CLI checkpoints have a
  limit of 32 MiB and OpenTofu states a limit of 512 MiB by default. The
  [configuration reference](../reference/configuration.md) lists each limit.
- Large Pulumi bodies share 128 MiB at the same time. Over that, the server
  answers 503 with `Retry-After`.
- The server waits at most 10 seconds for the request headers.
- No rate limit exists today. Planned in a new RFC (ADR 32).

## Outbound connections

The server sends no telemetry. It connects only to the PostgreSQL database
and the S3-compatible storage that you configure. The commands
`namespace create`, `backup`, `backup check`, `upgrade check` and `restore`
call only the server in `--server`. `health` calls only the listen address of the local
server.

## Audit log

Each namespace has an append-only audit log. No route changes or deletes an
entry. The [audit log guide](../guides/audit-log.md) shows the fields of an
entry and how to read them.

- **What writes an entry.** The Pulumi CLI calls `decrypt/log-decryption`
  or `decrypt/log-batch-decryption` when a person shows secrets, for example
  with `pulumi config get`, `pulumi stack output --show-secrets` or
  `pulumi stack export --show-secrets`. The server writes one entry of the
  kind `reveal` for each call. `up`, `preview`, `refresh` and `destroy`
  decrypt secrets too, but they do not call these routes, so they write no
  entry. The end-to-end suite checks both.
- **The CLI reports a reveal.** The server records a reveal only when the
  client calls an audit route. A client that does not call the route
  leaves no entry.
- **Who can read the log.** `GET /api/audit/entries` lists the entries and
  `GET /api/audit/check` checks the chain. Both need the action
  `namespace:read-audit` on the namespace.
- **A hash chain for each namespace.** Each entry holds the SHA-256 of its
  own fields and of the previous entry. The check finds a changed or a
  deleted entry. It does not stop a person who can write to the database:
  that person can write the entries and all the hashes again.
- **A failed append does not fail the command.** The route still answers
  204. The server writes a log line with no secret and adds 1 to
  `dustpan_audit_append_failures_total`. Alert when that counter is above
  zero.
- **Backups.** A backup holds the audit log, and a restore keeps a valid
  chain.

Planned: entries of the kind `tool-operation` for each read of plain
secrets that a tool needs for its own work, for example an OpenTofu state
download (ADR 41, decision 9). RFC 22 holds retention, export and an anchor
for the chain outside the database.

## Report a vulnerability

[SECURITY.md](../../SECURITY.md) says how to report a vulnerability in
private.
