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
- Kind: `tool-operation` or `reveal`, never anything else, so a reader never
  confuses a tool that needed secrets with a person who asked to see one.
- Outcome: `success`, `failure` or `denied`.
- `requestID`: the request ID the server mints. The server returns it in
  the `X-Request-Id` response header and ignores an ID the client sends
  (ADR 32).
- `prevHash` and `hash`: SHA-256 over a fixed, documented encoding of the
  fields above and `prevHash` (see `ComputeHash` in `pkg/audit`).
  When `valueCount` is set, the encoding appends one more field after
  `prevHash`: the text `value_count=` with the decimal count. When it is
  omitted, the encoding is unchanged, so an entry written before the count
  keeps its hash.
  The first entry of a namespace has an empty `prevHash`.

Reads of the audit log itself are not logged yet (RFC 22), because ADR 41
allows only the two kinds.

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
A valid chain answers `{"valid":true,"entriesChecked":N,"entriesUnchained":U}`.
A broken chain answers `{"valid":false,"badSequence":S,"entriesChecked":N,"entriesUnchained":U}`,
where `S` is the first entry that does not match.

```sh
curl -H "Authorization: token $DUSTPAN_ADMIN_TOKEN" \
  "https://dustpan.example/api/audit/check?organization=acme"
```

## Turn the chain on or off

The hash chain is opt-in (ADR 60 decisions 17 and 19, ADR 64).
`--audit-chain` (`DUSTPAN_AUDIT_CHAIN`) takes `auto`, `on` or `off`.
The default is `auto`.

- `auto` keeps the state that the installation recorded last.
  With no record, the chain is on when the log holds chained entries, and off otherwise.
  So a new installation starts with the chain off, and an installation that already holds chained entries keeps the chain on.
- `on` turns the chain on.
- `off` turns the chain off.

With the chain off, a new entry holds an empty `prevHash` and an empty `hash`.
Each change of the chain writes a switch entry in the scope `@server` before the server serves.
A switch entry has the kind `change`, the action `server:set-audit-chain` and the value path `/audit/chain/on` or `/audit/chain/off`.
A switch to off holds a hash, so a chain that goes off ends with a chained entry.
The first start of an installation also writes a switch entry for the state before that start.
So the switch entries are the whole history of the chain.
The server writes the switch entries and the state in one transaction.
Every instance reads the state again at least every 2 seconds, so a change reaches every instance within 2 seconds.
While the log holds chained entries and the chain is off, the server logs a warning at each start.

The installation keeps the state of the chain, and whether its log ever held a chained entry, in a table of its own.
A backup does not carry that table.
A restore keeps the state of the target installation and writes no change entry.
When the restored log holds chained entries and the chain is off, the server logs the same warning after the restore.

The check reads each run of chained entries on its own.
An entry without a hash ends a run, and the next chained entry starts a new run with an empty `prevHash`.
An entry without a hash passes the check only when the switch entries say that the chain was off at its time.
An instance can keep the old state for 2 seconds after a switch to on.
So the check also accepts an entry without a hash up to 2 seconds after that switch.
So the check finds a hash that someone deleted from a chained log.
Keep the clocks of the instances synchronized.
The check answers `entriesUnchained`: the count of entries without a hash.
The check cannot prove that such an entry is unchanged.

While the chain is on, the server checks the chain of every namespace and of `@server` at start and every hour.
While the chain is off, the server does not run this check.
`dustpan_audit_chain_invalid` holds the count of scopes whose check failed.

```yaml
      - alert: DustpanAuditChainInvalid
        expr: dustpan_audit_chain_invalid > 0
        labels:
          severity: critical
        annotations:
          summary: An OpenDustpan audit chain does not match
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
