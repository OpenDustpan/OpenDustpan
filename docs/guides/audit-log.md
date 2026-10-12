# Audit log

This guide shows how to read the audit entries of a namespace and check their chain.
It follows ADR 41 decisions 5 and 13.
Retention, export to a SIEM and the full catalog of events are RFC 22.

## What the log records

Each response that gives a secret value in plain text writes one audit entry.
The two Pulumi routes `decrypt/log-decryption` and
`decrypt/log-batch-decryption` (ADR 23 decision 6) write one entry each with
the action `unit:decrypt` and the kind `reveal`.
The Pulumi CLI calls them when a person asks it to show secrets, for example
`pulumi config --show-secrets`.

An entry holds these fields:

- Who: `subject`, `idpSubject` (empty until an identity provider exists),
  `tokenID` (empty until tokens have IDs).
- What: `action`, `object` (a unit reference or a namespace), `version` and
  `valuePath` (for a state read, else empty).
- When: `atMs` (UTC, Unix milliseconds).
- Where: `sourceIP` (the remote address the server sees), `userAgent`
  (capped at 512 bytes), `channel` (`api` or `console`, as the client
  declares it in the `Dustpan-Channel` header; unknown values are `api`).
- Kind: `tool-operation` or `reveal`, never anything else, so a reader never
  confuses a tool that needed secrets with a person who asked to see one.
- Outcome: `success`, `failure` or `denied`.
- `requestID`: the `X-Request-Id` of the request (ADR 32).
- `prevHash` and `hash`: SHA-256 over a fixed, documented encoding of the
  fields above and `prevHash` (see `ComputeHash` in `pkg/audit`).
  The first entry of a namespace has an empty `prevHash`.

Reads of the audit log itself are not logged yet (RFC 22), because ADR 41
allows only the two kinds.

## Read the entries

`GET /api/audit/entries?organization=<namespace>` lists one page of the
entries of the namespace, newest first, with the total count.
It needs the action `namespace:read-audit` on the namespace.
`page[number]` selects the page (from 1) and `page[size]` selects its size
(up to 200); both default when missing.

```sh
curl -H "Authorization: token $DUSTPAN_ADMIN_TOKEN" \
  "https://dustpan.example/api/audit/entries?organization=acme&page[size]=50"
```

`GET /api/audit/check?organization=<namespace>` walks the chain of the
namespace oldest first and answers whether it is valid.
It needs the same action.
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
The routes still answer, so the counter is the only record of the lost
entry besides the log line.
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
