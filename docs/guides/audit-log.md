# Audit log

This guide shows how to read the audit entries of a namespace and check their chain.
It follows ADR 41 decisions 5 and 13.
Retention, export to a SIEM and the full catalog of events are RFC 22.

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
- Kind: `tool-operation` or `reveal`, never anything else, so a reader never
  confuses a tool that needed secrets with a person who asked to see one.
- Outcome: `success`, `failure` or `denied`.
- `requestID`: the request ID the server mints. The server returns it in
  the `X-Request-Id` response header and ignores an ID the client sends
  (ADR 32).
- `prevHash` and `hash`: SHA-256 over a fixed, documented encoding of the
  fields above and `prevHash` (see `ComputeHash` in `pkg/audit`).
  After `prevHash`, the encoding appends each optional field that the entry
  sets, in this fixed order: `value_count=` with the decimal count, then
  `changes=` with the canonical JSON of the changes (sorted by key). An
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

## What this guide does not cover

These items are RFC 22.

- Retention.
- Export to a SIEM.
- The full catalog of events.
