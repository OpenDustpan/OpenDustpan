# Audit log

This guide shows how to read the audit entries of a namespace, check their chain and send a copy of each entry to a sink.
It follows ADR 41 decisions 5 and 13 and ADR 60 decisions 11 to 13.
Retention and the full catalog of events are RFC 22.

## What the log records

Each response that gives a secret value in plain text writes one audit entry.
The reveal call writes one entry with the action `unit:read-secrets`, the
kind `reveal`, the state version in `version` and the value path, before it
answers.
The two Pulumi routes `decrypt/log-decryption` and
`decrypt/log-batch-decryption` (ADR 23 decision 6) write one entry each with
the action `unit:decrypt` and the kind `reveal`.
The Pulumi CLI calls them when a person asks it to show secrets, for example
`pulumi config --show-secrets`.

The routes that answer a secret value to a tool write one entry each with the
kind `tool-operation`, so a reader never confuses a tool that needed secrets
with a person who asked to see one:

- The Pulumi `decrypt` and `batch-decrypt` routes write one entry each with
  the action `unit:decrypt`, the count of values in `valueCount` and an empty
  `version` and value path.
- The OpenTofu state download writes one entry with the action
  `unit:read-secrets`, the state version in `version` and an empty value
  path.
- The OpenTofu route for one output writes one entry with the action
  `unit:read-secrets`, the state version in `version` and the value path
  `/outputs/<name>` when the output is sensitive. A non-sensitive output
  writes no entry.

The server cannot see who runs a CLI command, so a person who runs
`tofu state pull` makes a `tool-operation` entry.
A failed append on a route that answers a secret value answers 500 with no
secret value, so a value without its entry never reaches the client.
The two `log-decryption` routes answer no secret value, so they keep
answering 204 when the append fails.

A restore or a purge of a deleted unit (ADR 47) writes one entry with the
kind `change` (ADR 60), the action `unit:restore` or `unit:purge`, and the
stable ID of the unit in `objectID`. The store appends it in the same
transaction as the change, so a failed append changes nothing.
A purge of the scheduled retention expiry has the subject `@server`. A purge
of an expiry run that a person starts through the API has that person as the
subject.

Each request for an access-review export (ADR 52, decision 13) writes one
entry with the kind `read-access`, the read-access action of the object and
the object `access-review <object> <mode>`. The mode is `snapshot
asOf=<time>`, or `history <from>-<to>`, in Unix milliseconds, followed by
the subject filter and `expand=members` when the request has them. The export of a namespace or
a unit writes its entry in that namespace, and the export of the
installation in `@server`. A failed append answers 500 with no rows.

An entry holds these fields:

- Who: `subject`, `idpSubject` (empty until an identity provider exists),
  `tokenID` (empty until tokens have IDs).
- What: `action`, `object` (a unit reference or a namespace), `version` and
  `valuePath` (for a state read, else empty), `valueCount` (the count of
  secret values of a decrypt call, else omitted).
- When: `atMs` (UTC, Unix milliseconds).
- Where: `sourceIP` (the remote address the server sees), `userAgent`
  (capped at 512 bytes), `channel` (`api` or `console`, as the client
  declares it in the `Dustpan-Channel` header). Unknown values are `api`.
  The server writes two more channels on its own: `startup` and
  `config-file` (see "Settings entries").
- `changes`: the keys of the entry with their values, else omitted. Each
  change holds `key`, `new`, and when they apply, `old`, `layer` and
  `takesEffect`. A `startup` or `config-file` entry holds its settings
  here.
- `reason`: the text that a person gave for the action, else omitted. It has
  at most 500 Unicode code points. A reason that is too long, or that holds
  a control character, a format character or a line or paragraph
  separator, is refused. A force-unlock can carry one.
- Kind: `tool-operation`, `reveal`, `override`, `change` or `read-access`,
  never anything else, so a reader never confuses a tool that needed secrets
  with a person who asked to see one.
- Outcome: `success`, `failure` or `denied`.
- `requestID`: the request ID the server mints. The server returns it in
  the `X-Request-Id` response header and ignores an ID the client sends
  (ADR 32).
- `objectID`: the stable ID of the unit that the entry names (ADR 47). The
  store sets it for each entry on a unit that exists, and for the delete,
  restore and purge of a unit. The list omits it when the entry has none.
- `prevHash` and `hash`: SHA-256 over a fixed, documented encoding of the
  fields above and `prevHash` (see `ComputeHash` in `pkg/audit`).
  The encoding writes each field as its length in bytes (decimal), a
  colon and the bytes of the field, with nothing between the fields. For
  example, the subject `admin` is `5:admin` and an empty field is `0:`.
  After `prevHash`, the encoding always writes five more fields, in this
  fixed order: the decimal value count, the stable ID of the unit, the
  canonical JSON of the changes (sorted by key) as the store holds it, the
  decimal failover version of the namespace (0 in `@server`), and the
  reason. The reason stays last. A later field, for example the principal
  kind and the job ID of a planned change, goes between the failover version
  and the reason: no tag shipped an audit entry with a reason, so this change
  of the order is allowed. No field is optional. An entry with no value count
  writes one `-` in place of the count, so an entry with no count (`-`) and an entry with the count 0
  (`1:0`) have different hashes. Because each field carries its length,
  two different entries never give the same encoding.
  The first entry of a namespace has an empty `prevHash`.

## Force-unlock entries

Each force-unlock of an OpenTofu workspace writes one entry of the kind
`change` with the action `unit:write-forced`. The TFE-compatible API and the
http backend both write it. The server writes it in the transaction that
releases the lock, so a release without its entry does not happen.

- `subject`: the principal that forced the release.
- `version`: the serial of the new state version that the release wrote.
  It is empty when the release wrote no version: a workspace with no state,
  or a state that the client encrypted.
- `changes`: the released `lock-holder` and the time `locked-at` of the lock,
  each in `old`.
- `reason`: the reason that the caller gave, else omitted. Only the
  TFE-compatible API can carry a reason. The lock info of the http backend
  has no field for one.

A force-unlock of a state that the client encrypted also writes an entry of
the kind `override`, as a forced write does.

Maintenance mode also writes this entry. When it starts, it releases each
lock that it finds. Each release writes one `change` entry with the action
`unit:write-forced`, the subject `maintenance` and the reason
`maintenance mode started`.

Reads of the audit log itself are not logged yet (RFC 22), because ADR 41
allows only the two kinds.

## Settings entries

Each server process writes entries about its own settings in the scope
`@server` (ADR 63 decisions 9 and 10). The subject is the instance ID: a new
`ins-` ID at each start, which the start log shows on the line `instance`.
The action is `server:manage-settings`, and the kind is `change`.

- At each start, the server writes one entry with the channel `startup`.
  Its `changes` hold each setting with its `key`, its value in `new` and
  its `layer` (`flag`, `env`, `file` or `default`). A change of the file or of a
  variable while the server is off shows as the difference between two
  start entries.
- When a reload of the configuration file changes a value, the server
  writes one entry with the channel `config-file`. Its `changes` hold each
  changed setting with its `old` and its `new` value, and `takesEffect`.
  No setting applies while the server runs yet, so `takesEffect` is
  `next start`: the entry records a change, and the new value applies at
  the next start.

No route of the API reads the scope `@server` yet (ADR 60 decision 7).
These entries become readable through the API when that route lands.

A sensitive setting (the database URL and each secret) shows only `set` or
`not set`, never the value. When the server cannot write the start entry,
the start stops. When it cannot write the entry of a reload, it keeps the
old configuration and the reload fails.

## Read the entries

`GET /api/audit/entries?organization=<namespace>` lists one page of the
entries of the namespace, newest first, with the total count.
It needs the action `namespace:read-audit` on the namespace.
`page[number]` selects the page (from 1) and `page[size]` selects its size
(up to 200). Both default when missing.

```sh
curl -H "Authorization: token $DUSTPAN_ADMIN_TOKEN" \
  "https://dustpan.example/api/audit/entries?organization=acme&page[size]=50"
```

`GET /api/audit/entries/<sequence>?organization=<namespace>` reads one
entry of the namespace by its sequence, in the same shape as an entry of the
list. It needs the same action. A sequence that the namespace does not hold
answers 404.

```sh
curl -H "Authorization: token $DUSTPAN_ADMIN_TOKEN" \
  "https://dustpan.example/api/audit/entries/7?organization=acme"
```

`GET /api/audit/check?organization=<namespace>` walks the chain of the
namespace oldest first and answers whether it is valid.
It needs the same action.
The check reads the chain in pages of 100 entries, so a large log never
grows the check without a bound.
A valid chain answers `{"valid":true,"entriesChecked":N,"entriesUnchained":U}`.
A broken chain answers `{"valid":false,"badSequence":S,"entriesChecked":N,"entriesUnchained":U}`,
where `S` is the first entry that does not match.

```sh
curl -H "Authorization: token $DUSTPAN_ADMIN_TOKEN" \
  "https://dustpan.example/api/audit/check?organization=acme"
```

The installation writes the entries of an action that is not in a namespace,
such as a namespace create, to the scope `@server`. Read that scope with
`organization=@server`. It needs the action `server:read-audit` on the
installation instead, and the same paging. The read of one entry and the
check work the same way:

```sh
curl -H "Authorization: token $DUSTPAN_ADMIN_TOKEN" \
  "https://dustpan.example/api/audit/entries?organization=@server"
```

A new namespace audit log starts with the grant of its creator.

## Turn the chain on or off

The hash chain is on by default.
`--audit-chain` (`DUSTPAN_AUDIT_CHAIN`, or `audit-chain` in the settings file) takes `on` or `off`.
The server reads the setting once at start.
No API call and no reload of the settings file changes it while the server runs.

The database keeps the state of the chain: `on` or `off`, when it last changed, and who changed it.
An installation without that state counts as `on`.
At start, the server compares its setting with the stored state.
When they agree, the server starts normally.

When they differ, the server enters maintenance mode and changes nothing yet.
A person approves the change:

```sh
opendustpan audit-chain status --server https://dustpan.example
opendustpan audit-chain approve --server https://dustpan.example
```

The commands call `GET` and `POST` `/api/server/audit/chain`, the same routes as the console.
The approval sets the chain to the setting of the server that receives it.

- Turning the chain on needs the action `server:set-audit-chain` (operator family).
- Turning the chain off needs `server:set-audit-chain` (operator family) and `server:disable-audit-chain` (administration family).
  A service principal cannot approve it: a person must approve turning the chain off.
  It also needs a fresh MFA step when the server asks for MFA.

The approval writes an entry in `@server` with the kind `change`, the action `server:set-audit-chain` and the value path `/audit/chain/on` or `/audit/chain/off`.
Then the server migrates the log in the background.
It waits 4 seconds, so every instance refuses requests.
It writes one entry in each scope that holds entries:

- **On:** a start entry with the action `server:start-audit-chain`.
  Its hash has an empty `prevHash`.
  Its changes hold `digest`, a running SHA-256 of every earlier entry of the scope, and `entries`, their count.
  The earlier entries keep their rows.
- **Off:** an end entry with the action `server:end-audit-chain`, which names the person who approved the change.
  Its hash links to the entry before it, so the chain ends with a chained entry.
  Later entries hold an empty `prevHash` and an empty `hash`.

Then the server stores the new state, waits 4 seconds so every instance can see it, and leaves maintenance mode.
`opendustpan audit-chain status --server https://dustpan.example` shows `the migration runs` until then.
A migration that stops before it ends keeps the old state, so the change waits for an approval again.
A restart with the stored setting cancels a waiting change.
The installation then leaves maintenance mode.
Run `opendustpan audit-chain approve --server <url>` to approve a waiting change.

While a change waits or migrates, every instance answers 503 on most routes.
The probes, GET of the metrics route, GET and POST of the audit chain route, the maintenance routes and the console files stay available.
The server refuses a restore while a change waits, and refuses an approval while a restore runs.
An instance whose setting differs from the stored state also answers 503 on most routes, also after another instance approved the change.
The maintenance routes stay available, so an operator can clear stale active work on an instance whose setting differs.
Restart each instance with the stored setting.

The server computes the digest of a start entry over each earlier entry in order of sequence.
For each entry, the next digest is the hex SHA-256 of four parts joined with the unit separator (0x1F).
The parts are the previous digest, the entry hash, the stored `prevHash` and the stored `hash`.
The previous digest of the first entry is the empty string.
The entry hash is the hash of the stored fields of the entry with an empty `prevHash`.

The hash takes no key: any installation can compute it again from the stored fields.
Each entry of a namespace holds `fenceVersion`, the failover version of the namespace when the server appended it.
The hash covers it, so a verifier can see where a forced failover forked the chain of a namespace.
Entries in `@server` hold 0. The hash covers the 0, and the audit API omits the field for them.
A verifier who kept a digest outside the database can compute it again from the old rows.
So the verifier can prove that the old rows did not change after the chain started.

The check of a scope starts at its last start entry, or at sequence 1 when the scope holds none.
From there, each entry holds a hash that matches its fields and links to the entry before it, with no gap.
The chain goes up to now, or up to an end entry.
After an end entry, entries hold no hash.
A start entry also checks its digest against the earlier entries.
With the chain on, a scope must not end after an end entry.
With the chain off, a scope without a chain is valid only when its first entry comes after the time the chain went off.
A row below sequence 1 fails the check.
The store keeps the times of a scope in order.
An entry whose time is earlier than the last entry of its scope takes the time of that entry.
The check answers `entriesUnchained`: the count of entries without a hash.
The check cannot prove that an entry before a start entry, or after an end entry, is unchanged.
Signed checkpoints (see below) and the digest cover that case.

A backup does not carry the state of the chain.
A restore keeps the state of the target installation.
After the swap, the server compares the restored log with that state.
With the chain on, each scope must end with a chained entry that is not an end entry.
With the chain off, each scope must end with an entry without a hash, or with an end entry.
When the log agrees, the server checks the chain of every scope.
When that check fails, the restore still succeeds.
The server then logs an error and adds one to `dustpan_audit_restore_check_failures_total`.
When the log does not agree, the installation stays in maintenance mode until a person approves the change.
The approval runs the same migration on the restored log.
A restore that stops after the swap gets the comparison at the next start.
A restore trusts the audit log in the backup: signed checkpoints (see below) protect a backup.

The server checks the chain of every namespace and of `@server` at start and every hour.
`dustpan_audit_chain_invalid` holds the count of scopes whose chain or checkpoints do not match.
When a database read, a blob read or a checkpoint write fails, the server does not check that scope.
It logs a warning and adds one to `dustpan_audit_chain_check_errors_total`.
Such a check does not lower `dustpan_audit_chain_invalid`.
A failed read during the check of a restored log also adds one to `dustpan_audit_chain_check_errors_total`.

```yaml
      - alert: DustpanAuditChainInvalid
        expr: dustpan_audit_chain_invalid > 0
        labels:
          severity: critical
        annotations:
          summary: An OpenDustpan audit chain does not match
      - alert: DustpanAuditChainCheckErrors
        expr: increase(dustpan_audit_chain_check_errors_total[3h]) >= 3
        labels:
          severity: warning
        annotations:
          summary: The OpenDustpan audit chain check cannot read the log
      - alert: DustpanAuditRestoreCheckFailed
        expr: increase(dustpan_audit_restore_check_failures_total[1h]) > 0
        labels:
          severity: critical
        annotations:
          summary: A restored OpenDustpan audit log does not match its chain
```

## Sign checkpoints

Signed checkpoints are opt-in.
They show a rewrite of the log by a person who can write to the database and compute the chain again.
`--audit-checkpoints` (`DUSTPAN_AUDIT_CHECKPOINTS=true`) turns them on.
They need `--audit-chain=on` and the signing key file that `--audit-signing-key-file` (`DUSTPAN_AUDIT_SIGNING_KEY_FILE`) names.
The server does not start when one of the two is missing.

The key file holds a 32-byte Ed25519 seed in base64.
The file must not be readable by group or others.
The server never makes the key and never derives it from the key-encryption key.
Make a key with this command:

```sh
(umask 077 && openssl rand -base64 32 > audit-signing.key)
```

At each check, the server does these steps for each scope whose chain matches:

1. It checks the checkpoints of the scope in the current epoch.
   It reads every checkpoint that is newer than the last checkpoint that matched, up to 256.
   It also reads a random sample of 8 older checkpoints.
   It always reads the newest checkpoint of the scope.
   When the log ends before that checkpoint, the check fails.
   The next check finds a cut at the end of the log.
   For each checkpoint, it checks the key ID, the signature, the epoch, and the hash of the entry at its sequence.
   The epoch in the checkpoint must match the epoch of its directory.
2. If all of them match, it signs the hash of the last entry of the scope.
   The checkpoint holds the scope, the sequence, the epoch, the hash, the time, the key ID and the signature.
   The key ID is the first 16 hexadecimal characters of the SHA-256 hash of the public key.
3. It writes the checkpoint to the blob store under `audit-checkpoints/<epoch>/<scope in hexadecimal>/<sequence>.json`.
   A key holds one checkpoint and the server never writes it again, so two instances write it once.

A scope with a checkpoint that does not match counts in `dustpan_audit_chain_invalid`, and the server logs the scope and the sequence.
The server never signs a scope whose chain or checkpoints do not match.
Once a checkpoint does not match, later checks fail until a new epoch starts.
When a stored checkpoint holds another hash, the write fails and the scope stays invalid.
While a change of the chain waits or migrates, the server checks the checkpoints but signs none.

The server logs the key ID and the public key in base64 at start.
Keep a copy of the public key outside the server.

Keep the blob store apart from the database: give the two stores different credentials.
If the blob store has object lock, turn it on for the prefix `audit-checkpoints/`.
An entry is safe from a rewrite only after a checkpoint signs it, so the window is at most one hour.
A person who can delete checkpoints can hide a rewrite of the entries that came before the next checkpoint.
The server remembers the newest checkpoint that matched, so a later check finds deleted checkpoints.
The server forgets this at a restart, so without object lock only later checks find a deletion from before a restart.

### Epochs

An epoch is one history of the log.
The check reads only the checkpoints of the current epoch.
The server keeps the current epoch in the blob store under `audit-checkpoints/epochs/`.
Each epoch object holds the epoch number, the time, the key ID and a signature.
The server signs each epoch object with the same key as the checkpoints.
The signed message holds a fixed context line, the epoch, the time in Unix milliseconds and the key ID.
The check reads every epoch object and verifies each one.
It checks the signature, the key ID, and the body number against the key number.
An unsigned or badly signed epoch object makes the check fail and stops new signatures.
A bad epoch object counts the scope in `dustpan_audit_chain_invalid`.
After an investigation, the operator deletes the bad epoch object.
The next check then reads the fixed store.

A restore starts a new epoch, because a restore of an older backup deletes entries that older checkpoints sign.
The restore writes a change entry in the scope `@server` with the value path `/audit/checkpoints/epoch/<number>`.
The checkpoints of older epochs stay in the blob store.
The server does not check them again.

### Change the signing key

A checkpoint or an epoch object that a key signed passes the check only while the server knows that key.
Before you change the signing key, put the old public key in a file and name the file with `--audit-verify-keys-file` (`DUSTPAN_AUDIT_VERIFY_KEYS_FILE`).
The file holds one public key in base64 on each line.
The server ignores empty lines and lines that start with `#`.
The server uses these keys only to check checkpoints and never signs with them.
The server does not start when the file holds a line that is not a 32-byte Ed25519 public key in base64.

### Clear the alarm

A checkpoint that does not match keeps `dustpan_audit_chain_invalid` above zero.
First find the cause: compare the entries of the scope with your audit log sinks and with the backups.
When you know the cause, start a new epoch.
The server then checks only the checkpoints that it signs after this time.

```sh
curl -X POST -H "Authorization: token $DUSTPAN_ADMIN_TOKEN" \
  https://dustpan.example/api/server/audit/checkpoint-epochs
```

The request needs the action `server:manage-audit`.
It answers 201 with the number of the new epoch, and it writes a change entry in the scope `@server`.
It answers 409 when signed checkpoints are off.
Each instance reads the new epoch at its next check.

## What the chain protects against

The chain shows a change or a deletion of an entry.
Each entry holds the hash of its own fields and of the previous entry of
its namespace, so a changed field breaks the hash of that entry and a
deleted entry breaks the link of the next one.
A sink copy is a whole entry: the sink never cuts a field and never sends a changed entry.
Each copy keeps `scope`, `sequence`, `prevHash` and `hash`, so a receiver can follow the links and find gaps and duplicates.
The full entry stays in the server audit log, and the chain check on the server covers it.
A backup carries the `audit_entries` table, so a restore keeps the entries
and a valid chain.

The chain does not stop a person who can write to the database and compute
the chain again.
Such a person can rewrite the entries and their hashes, and the check still
answers valid.
Signed checkpoints in the blob store are the anchor outside the database (see above).
Protect the database and its backups as the guide for backups says.

## Alert on failed logging

A failed append writes a log line with no secret and no value and counts
`dustpan_audit_append_failures_total`.
On the two `log-decryption` routes the call still answers, so the counter is
the only record of the lost entry besides the log line.
On a route that answers a secret value the call answers 500 with no value.
Alert when the counter is above zero.
This is the alert for failed logging (PCI DSS 10.7).

```yaml
groups:
  - name: dustpan
    rules:
      - alert: DustpanAuditAppendFailures
        expr: dustpan_audit_append_failures_total > 0
        labels:
          severity: critical
        annotations:
          summary: OpenDustpan lost audit entries
```

## Send a copy to a sink

The server can send a copy of each audit entry to one or more sinks.
The SQL table stays the record of the audit log.
No sink is the default.

Give `--audit-sink` once for each sink.
In `DUSTPAN_AUDIT_SINK` and in the key `audit-sink` of the configuration file, separate the sinks with spaces or line breaks.
The start log shows only whether `audit-sink` is set, because a webhook URL can hold a token.
A sink item can hold a comma, for example in the query of a webhook URL.
Each item is `[<name>=]<sink>`:

| Sink | Example | What it writes |
| --- | --- | --- |
| A file | `file:/var/log/dustpan/audit.jsonl` | One JSON line for each entry. The server syncs the file after each batch. |
| Syslog | `syslog+tls://logs.example:6514` | One RFC 5424 message for each entry, over `udp`, `tcp` or `tls`. |
| A webhook | `webhook:https://siem.example/hooks/dustpan` | One HTTPS POST for each batch, with a JSON array of entries. |
| Object storage | `blob:audit` | One gzip file of JSON lines for each batch, in the blob store of the server. |

```sh
opendustpan serve \
  --audit-sink 'siem=syslog+tls://logs.example:6514' \
  --audit-sink 'archive=blob:audit'
```

The name keys the cursor of the sink in the database.
Without a name, the kind is the name: `file`, `syslog`, `webhook` or `blob`.
Two sinks of one kind need names.
Keep the name when you change the address of a sink.
A new name starts from the first entry and sends every entry again.

### What a sink receives

Each entry is one JSON object with `time`, `level` (`info`), `msg` (`audit entry`) and the fields of the audit API.
`scope` names the namespace.
`sequence` numbers the entries of the scope.
`valueCount`, `objectID` and `changes` are present only when the entry has them.
`changes` holds the canonical JSON that the hash covers: a list sorted by `key`.
`prevHash` and `hash` hold the chain of the entry.
The server never cuts a field: each copy is a whole entry with `scope`, `sequence`, `prevHash` and `hash`.
A receiver can check the link to the record before it, and can find gaps and duplicates by `scope` and `sequence`.
The full entry stays in the server audit log, and the chain check on the server covers it.

```json
{"time":"2026-10-13T12:00:00.000Z","level":"info","msg":"audit entry","scope":"acme","sequence":7,"atMs":1791892800000,"subject":"admin","action":"unit:read-secrets","object":"opentofu:acme/prod","kind":"reveal","outcome":"success","prevHash":"9f2c...","hash":"41ab..."}
```

The server reads the new entries of each scope in order and writes them to the sink in one batch.
It moves the cursor of the sink only after the sink accepted the batch.
A failed batch stays, and the server sends it again with a longer wait each time, up to 5 minutes.
While its lease holds, one instance ships a sink. After the server loses the lease or a round fails, a sink can receive an entry twice. Delivery is at least once.
Drop duplicates by `scope` and `sequence`.
A batch in one scope that fails does not stop the other scopes.
The server sends a batch every 5 seconds to a file, syslog or a webhook, and every minute to object storage.

### Syslog

Each message has the facility `log audit` (13) and the severity `info`.
Set another facility with `?facility=<0 to 23>`, for example `syslog+udp://logs.example:514?facility=16`.
The message part is the JSON object above.
STRUCTURED-DATA and MSGID are `-`.
Over `tcp` and `tls` each message starts with its length in bytes (octet-counting framing: RFC 6587 section 3.4.1 over TCP, RFC 5425 section 4.3 over TLS).
UDP has no acknowledgment, so a message can get lost without an error.
Use `tls` for an audit log.
`?ca-file=<path>` names a PEM bundle to trust instead of the system store.
`?max-size=<bytes>` sets the largest message: from 480 to 65507 bytes over UDP, and from 480 to 65536 bytes over TCP and TLS.
The default is 8192 over UDP and 65536 (64 KiB) over TCP and TLS, which holds the largest entry the server writes.
Raise the matching limit of the receiver to at least the same size, or the receiver drops the longer messages.
With rsyslog set `$MaxMessageSize`. On a TCP input also set `MaxFrameSize`.
The defaults of both may be below 64 KiB. Check the receiver's limit.
With syslog-ng set `log-msg-size()`. Its default may be below 64 KiB. Check the receiver's limit.
The sink never shortens a field and never sends a changed entry.
The sink refuses an entry above that size and never sends it.
The server counts it in `dustpan_audit_sink_skipped_total` and in `dustpan_audit_sink_failures_total{reason="size"}`, logs one error line with its scope and sequence, and goes on with the next entry.
Read the refused entry by its `scope` and `sequence`, for example `GET /api/audit/entries/7?organization=acme`.
The full entry stays in the server audit log, and the chain check on the server covers it.

### Webhook

The URL must use HTTPS.
The server sends to any host that it can reach, internal addresses and cloud metadata endpoints included.
Only the operator sets the URL.
Check it as you check any outbound address of the server.
If you can, block the metadata endpoints in your network.
Set the signing key in `DUSTPAN_AUDIT_WEBHOOK_SECRET`, or in a file that `DUSTPAN_AUDIT_WEBHOOK_SECRET_FILE` names.
Only the owner of the file can read it.
Each request carries two headers:

- `Dustpan-Timestamp`: the time of the request in Unix seconds.
- `Dustpan-Signature`: `v1=` and the hex HMAC-SHA256 of the timestamp, a `.`, and the body.

The receiver computes the signature with the same key and compares it in constant time.
Refuse a request whose timestamp is more than 5 minutes old.
A 2xx answer means the receiver holds the batch.
The server does not follow a redirect: a 3xx answer is a failed batch, and the server sends the batch again to the same URL.

### Object storage

`blob:<prefix>` writes to the blob store of the server: the directory of `--blob-dir` or the bucket of `--blob-store`.
The prefixes `tofu` and `pulumi` hold state, so the server refuses them.
The prefix `audit-checkpoints` holds signed audit checkpoints, so the server refuses it.
Each object is `<prefix>/<scope>/<yyyy-mm-dd>/<first>-<last>-<random>.jsonl.gz`.
The two times are the first and the last entry of the file in Unix milliseconds.
A key holds the characters `a-z`, `0-9`, `.` and `-` only.
Each other character of the scope, and `_`, becomes `_` and two hexadecimal digits: `acme` stays `acme`, and `@server` becomes `_40server`.
To keep objects that nobody can delete, use a bucket with S3 Object Lock.

### A file and log rotation

The server opens the file again on `SIGHUP`.
A rotation tool moves the file away and sends `SIGHUP` to the server.

### Alert on a sink that falls behind

`dustpan_audit_sink_entries_total{sink="<name>"}` counts the entries that a sink accepted.
`dustpan_audit_sink_failures_total{sink="<name>",reason="<reason>"}` counts the failed deliveries.
The reason is `size` for an entry that the sink refused because it cannot hold it. An example is a syslog record that cannot fit below `max-size`.
The reason is `unreachable` when the sink never reached its receiver.
The reason is `write` for any other failed write.
The reason is `read` for a failed entry, cursor or namespace read.
The reason is `lease` for a failed sink lease take.
A refused entry logs one error line per entry with the sink name, its scope and its sequence.
Any other failure logs one error line per round.
The line has the sink name, the reason, and the sequence of the first entry of the failed batch.
A round that knows no sequence leaves it out.
A round that never read a batch, for example a failed lease take or a failed entry, cursor or namespace read, carries no sequence.
A failed batch loses no entry, but a count that keeps growing means the sink falls behind.
A refused entry never arrives: alert when the `size` series grows.
`dustpan_audit_sink_skipped_total{sink="<name>"}` counts the entries that a sink cannot hold, for example a syslog record that cannot fit below `max-size`.
A skipped entry is not in the sink: alert when that count is above zero.
`dustpan_audit_sink_lag_seconds{sink="<name>"}` holds the age in seconds of the oldest entry the sink has not delivered yet, or 0 when the sink is up to date.
`dustpan_audit_sink_lag_entries{sink="<name>"}` holds the count of entries the sink has not delivered yet, or 0 when the sink is up to date.
Alert when either lag keeps growing: the sink falls behind.
The metrics count 16 sinks at most. With more sinks, the server logs a warning at start.

### After a restore

A restore resets the cursor of every sink.
Each sink then receives the restored log again from its first entry.
The restored log can use a sequence again that a sink already holds for an entry from after the backup.
Keep the copies of the sink from before the restore apart, for example by the time of the restore.

```yaml
groups:
  - name: dustpan
    rules:
      - alert: DustpanAuditSinkFailing
        expr: increase(dustpan_audit_sink_failures_total[15m]) > 0
        labels:
          severity: warning
        annotations:
          summary: An OpenDustpan audit sink refuses entries
      - alert: DustpanAuditSinkRefusedEntry
        expr: increase(dustpan_audit_sink_failures_total{reason="size"}[15m]) > 0
        labels:
          severity: critical
        annotations:
          summary: An OpenDustpan audit sink refused an entry it cannot hold
      - alert: DustpanAuditSinkLagging
        expr: dustpan_audit_sink_lag_entries > 50
        labels:
          severity: warning
        annotations:
          summary: An OpenDustpan audit sink falls behind
```

OpenTelemetry logs are not a sink yet.

## What this guide does not cover

These items are RFC 22.

- Retention.
- The full catalog of events.
- Export through the API in JSON and CEF (ADR 60 decision 9).
