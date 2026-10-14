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
  After `prevHash`, the encoding appends each optional field that the entry
  sets, in this fixed order: `value_count=` with the decimal count, then
  `object_id=` with the stable ID of the unit (ADR 47), then `changes=` with
  the canonical JSON of the changes (sorted by key), as the store holds
  it. An
  optional field that the entry does not set adds nothing, so an entry
  written before the field keeps its hash.
  The first entry of a namespace has an empty `prevHash`.

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

`GET /api/audit/check?organization=<namespace>` walks the chain of the
namespace oldest first and answers whether it is valid.
It needs the same action.
The check reads the chain in pages of 100 entries, so a large log never
grows the check without a bound.
A valid chain answers `{"valid":true,"entriesChecked":N}`.
A broken chain answers `{"valid":false,"badSequence":S,"entriesChecked":N}`,
where `S` is the first entry that does not match.

```sh
curl -H "Authorization: token $DUSTPAN_ADMIN_TOKEN" \
  "https://dustpan.example/api/audit/check?organization=acme"
```

## What the chain protects against

The chain shows a change or a deletion of an entry.
Each entry holds the hash of its own fields and of the previous entry of
its namespace, so a changed field breaks the hash of that entry and a
deleted entry breaks the link of the next one.
A backup carries the `audit_entries` table, so a restore keeps the entries
and a valid chain.

The chain does not stop a person who can write to the database and compute
the chain again.
Such a person can rewrite the entries and their hashes, and the check still
answers valid.
An anchor outside the database is RFC 22.
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

```json
{"time":"2026-10-13T12:00:00.000Z","level":"info","msg":"audit entry","scope":"acme","sequence":7,"atMs":1791892800000,"subject":"admin","action":"unit:read-secrets","object":"opentofu:acme/prod","kind":"reveal","outcome":"success","prevHash":"9f2c...","hash":"41ab..."}
```

The server reads the new entries of each scope in order and writes them to the sink in one batch.
It moves the cursor of the sink only after the sink accepted the batch.
A failed batch stays, and the server sends it again with a longer wait each time, up to 5 minutes.
So a sink can receive an entry twice.
Each instance of the server sends the entries, so with more than one instance a sink receives each entry about once for each instance.
Drop duplicates by `scope` and `sequence`.
A batch in one scope that fails does not stop the other scopes.
The server sends a batch every 5 seconds to a file, syslog or a webhook, and every minute to object storage.

### Syslog

Each message has the facility `log audit` (13) and the severity `info`.
Set another facility with `?facility=<0 to 23>`, for example `syslog+udp://logs.example:514?facility=16`.
The message part is the JSON object above.
STRUCTURED-DATA and MSGID are `-`.
Over `tcp` and `tls` each message starts with its length in bytes (RFC 5425).
UDP has no acknowledgment, so a message can get lost without an error.
Use `tls` for an audit log.
`?ca-file=<path>` names a PEM bundle to trust instead of the system store.
`?max-size=<bytes>` sets the largest message, from 480 to 65507 bytes.
The default is 8192.
The server does not send an entry above that size.
It counts the entry in `dustpan_audit_sink_skipped_total`, logs an error and goes on with the next entry.

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
`dustpan_audit_sink_failures_total{sink="<name>"}` counts the failed batches.
`dustpan_audit_sink_skipped_total{sink="<name>"}` counts the entries that a sink cannot hold, for example a syslog message above `max-size`.
A failed batch loses no entry, but a count that keeps growing means the sink falls behind.
A skipped entry is not in the sink: alert when that count is above zero.
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
```

OpenTelemetry logs are not a sink yet.

## What this guide does not cover

These items are RFC 22.

- Retention.
- The full catalog of events.
- Export through the API in JSON and CEF (ADR 60 decision 9).
