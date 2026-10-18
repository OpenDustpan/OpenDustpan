# 60. The audit log is a port, and logs go to swappable sinks

Status: accepted · 2026-10-13. The optional hash fields of decision 5 superseded by ADR 72. Route paths and `server:` action names superseded in part by ADR 210

## Context

[RFC 22](../rfc/0022-audit-log.md) asks for one audit log for every action
that changes something or reads a secret. ADR 41 decision 13 built the
smallest part of it: entries in SQL for secret reads, a hash chain for each
namespace, a list route and a check route. ADR 44 added the kind `override`.
ADR 48 writes an entry when the server replaces or deletes an instance row.

The project owner answered questions in RFC 22:

- On 2026-10-12, to "Is the audit log a port with adapters": "Yes." To
  "Which events are always on, and which can an operator turn off?": "It's
  either fully on or off until someone decides otherwise."
- On 2026-10-13, for the sinks in milestone 4: "If we're building this
  anyway, the application logs gains this capability too. Structured
  application logs can go to Syslog/Otel/a file as alternative targets to
  stdout. As always, keep the output adapters generic so that users can
  easily swap them out., Syslog (RFC 5424), HTTP webhook, OpenTelemetry
  logs, Object storage".
- On 2026-10-13, after the spike in
  `docs/research/audit-log-tamper-evidence.md`, for tamper evidence: "I see
  the value in building both 1 and 2 as opt-in features but these need to
  be off by default." Option 1 is the hash chain with an external copy.
  Option 2 is signed checkpoints.

This ADR records those answers and the design that follows from them.
Retention is not part of it. It waits for the decision of the owner in
RFC 22.

## Decision

### The audit log

1. **A port with adapters.** `pkg/audit` keeps the audit log port: append,
   list, export and check. The SQL stores are its adapters, and `audittest`
   is its conformance suite. A new port method gets conformance tests
   before an adapter implements it. The SQL table is the record of the
   audit log. A sink (decision 8) receives a copy.
2. **Fully on or fully off.** One switch, `--audit` (`DUSTPAN_AUDIT`), turns
   the whole audit log on or off for the installation. The default is on,
   because ADR 41 already writes an entry for each secret read. No setting
   turns off one event, one kind or one namespace. When the switch is off:
   - the server writes no entry, and the routes of ADR 41 answer without an
     entry,
   - the audit routes answer 404 with an error that names the switch,
   - the server writes a warning at start, and the metric
     `dustpan_audit_enabled` is 0.
3. **What writes an entry.** An entry records each request whose action
   changes something, reads a secret value in plain text, or reads the audit
   log. The route table of ADR 17 declares the action of each route, so one
   middleware writes the entry, not each handler. A denied request and a
   failed request also write an entry. A plain read that answers no secret
   value writes no entry.
4. **Kinds.** The field `kind` keeps the values of ADR 41 and ADR 44, and
   gets two more:
   - `change`: an action that changes something, for example a stack
     update, a state version, a lock, a delete or a setting,
   - `audit-read`: a read or an export of the audit log (PCI DSS 10.2.1.3).

   The middleware sets `change` from the action. A route that already
   writes `tool-operation`, `reveal` or `override` keeps that kind.
5. **Entry fields.** An entry keeps the fields of ADR 41 decision 13, with
   these additions. A new field is optional in the hash encoding, as
   `valueCount` is, so an older entry keeps its hash.
   - **Who:** `subject`, `idpSubject`, `tokenID`. ADR 41 fixed these.
   - **What:** `action` and `kind`.
   - **Object:** `object` (the unit reference or the namespace, as in ADR
     41), and the new field `objectName`: the name of the object that a
     person reads, for example the stack or workspace name, at the time of
     the action. `version` and `valuePath` stay as they are.
   - **When:** `atMs`, UTC, Unix milliseconds.
   - **From where:** `sourceIP`, `userAgent`, `channel`.
   - **Result:** `outcome` (`success`, `failure` or `denied`), the new field
     `status` (the HTTP status of the answer), and `requestID`.
6. **Entries keep their context.** An entry holds every name that a person
   needs to read it, as text, at the time of the action. It holds no
   foreign key to the unit or other object that it names. A delete of a
   unit, a token or a user never deletes or changes an entry. A delete of a
   namespace keeps the entries of that namespace. The migration that lets a
   namespace go deletes the foreign key from `audit_entries` to
   `namespaces`.
7. **Installation entries.** An action that is not in a namespace (an
   instance row of ADR 48, a backup, a restore, a namespace create) writes
   to the scope `@server`, with its own sequence and chain. It needs the
   new action `server:read-audit` on the object `@server`. An administrator
   has it.

### Query and export

8. **Query.** `GET /api/audit/entries` lists entries newest first, as
   today. It gets filters: `subject`, `action`, `kind`, `object`, `outcome`
   and a time range (`from` and `to` in Unix milliseconds). It also gets a
   cursor by sequence, so a reader can page a large log without an offset.
   The page number and size of ADR 41 stay, so no client breaks.
9. **Export.** `GET /api/audit/export` streams the entries of a scope oldest
   first, for a time range or from a sequence. It takes `format=json` or
   `format=cef`:
   - `json`: one JSON object for each line (JSONL), with the same field
     names as the list route, and `sequence`, `prevHash` and `hash`.
   - `cef`: one ArcSight Common Event Format line for each entry, with
     vendor `OpenDustpan` and product `OpenDustpan`. The action is the
     signature ID. The fields map to CEF keys (`suser`, `src`, `act`,
     `outcome`, `rt`, `requestClientApplication`). The other fields go to
     custom string keys with labels. A table in the audit log guide fixes
     the mapping.

   An export streams, so it never holds the whole log in memory. It writes
   one entry of the kind `audit-read`.
10. **Actions.** The list, the export and the check need
    `namespace:read-audit` on a namespace, or `server:read-audit` on
    `@server` (decision 7). Each route declares its action in the route
    table (ADR 17).

### Log sinks

11. **One output port for both logs.** A new package `pkg/logsink` defines
    one port, `Sink`: it takes a batch of structured records and delivers
    them, or answers an error. A record holds a time, a level, a message
    and named attributes. The port knows nothing about audit entries or
    `slog`. Each adapter is in its own package under `pkg/logsink/`, and
    `logsinktest` is the conformance suite. A user can write an adapter
    outside this repository and pass it to `dustpan.NewServer` (ADR 35).
12. **Built-in adapters.**
    - `stdout`: one JSON or text line for each record, as ADR 32 decision 6
      writes today.
    - `file`: appends JSON lines to a file. It opens the file again on
      `SIGHUP`, so an external rotation tool works.
    - `syslog`: RFC 5424 messages over UDP, TCP or TLS (RFC 5425 framing for
      TCP and TLS). The adapter writes the RFC 5424 format itself. The
      standard `log/syslog` package writes the older format, so the adapter
      does not use it.
    - `webhook`: an HTTPS POST of a JSON array of records. Each request
      carries a timestamp header and an HMAC-SHA256 signature over the
      timestamp and the body, so the receiver can check the sender and
      refuse a replayed request.
    - `otlp`: OpenTelemetry logs over OTLP/HTTP with the JSON encoding. The
      adapter uses the standard library and adds no dependency.
    - `objectstore`: rolled JSONL files, compressed with gzip, written to a
      `blob.Store` (a directory or S3, ADR 31). A file rolls at a size or a
      time limit. Each file has an immutable key that holds the time range,
      for example `audit/<scope>/<from>-<to>.jsonl.gz`.
13. **The audit log ships to sinks.** The operator can name zero or more
    audit sinks. The SQL table stays the record. A shipper reads the
    committed entries in order of sequence and delivers each batch to each
    sink. It keeps a cursor for each sink and scope in the database, so a
    restart or another instance (ADR 48) goes on from the cursor. A failed
    delivery retries with a back-off and counts a metric. The shipper never
    drops an entry. A sink can receive an entry twice, so each shipped entry
    carries its scope and `sequence`, and the receiver can drop duplicates.
    When the chain of decision 17 is on, each shipped entry also carries
    `prevHash` and `hash`.
14. **Application logs use the same port.** The structured application log
    (the request log of ADR 32 and every other `log/slog` line) goes
    through a `slog.Handler` that writes to one or more sinks. `stdout` is
    the default. The flag `--log-sink` (`DUSTPAN_LOG_SINK`) names other
    sinks, and it can repeat. An application log sink is best effort: when
    a sink is slow, the handler drops records and counts them in
    `dustpan_log_sink_dropped_total` with the sink name as the only label.
    The request path never waits for a sink.
15. **Configuration.** The operator sets each sink at start, with the flags
    `--audit-sink` and `--log-sink` and their environment variables. Each
    value names the adapter and its options, for example the address of a
    syslog server. A secret of a sink (the HMAC key of a webhook, an OTLP
    header) comes from an environment variable or a file, never from a
    flag, as ADR 23 decision 1 requires. The installation API gets a route
    that lists the configured sinks and their delivery state (the cursor,
    the lag, the last error) with the action `server:observe`. It never
    answers a secret of a sink. A later ADR can make sinks configurable
    through the API at run time.
16. **What a sink never receives.** ADR 32 decision 7 applies to every sink:
    no credential, token, request path or query, body, state, secret, key,
    database URL or driver error text. The canary test of ADR 32 covers
    every built-in adapter.

### Tamper evidence

17. **The chain with an external copy is opt-in.** The switch
    `--audit-chain` (`DUSTPAN_AUDIT_CHAIN`) is off by default. When it is
    on:
    - each append computes the hash chain of its scope, as ADR 41 decision
      13 describes,
    - each entry that a sink ships carries `sequence`, `prevHash` and `hash`
      (decision 13), so the operator's sink holds a copy of the chain
      outside the database,
    - the server runs the chain check of each scope on a schedule. It sets
      the gauge `dustpan_audit_chain_invalid` to the count of scopes whose
      check fails, with no scope label (ADR 32 decision 5), so an operator
      can alert on it.

    When the switch is off, the server writes an entry with an empty
    `prevHash` and an empty `hash`. A change of the switch writes an entry
    of the kind `change` in the scope `@server`. The check of a scope
    starts at the first entry after the chain was last turned on.
18. **Signed checkpoints are opt-in.** The switch `--audit-checkpoints`
    (`DUSTPAN_AUDIT_CHECKPOINTS`) is off by default, and it needs
    `--audit-chain`, because a checkpoint signs the head of a chain. When
    it is on, the server writes a checkpoint for each scope on a schedule.
    The checkpoint holds the scope, a sequence, the hash at that sequence,
    the time, a key ID and an Ed25519 signature. It goes to the blob store
    under an immutable key, so two instances that write the same checkpoint
    change nothing (ADR 48). The signing key comes from the file that
    `DUSTPAN_AUDIT_SIGNING_KEY_FILE` names, with the rules of ADR 23
    decision 1. The server never derives it from the key-encryption key.
    The check also checks each checkpoint with the public key.
19. **This changes ADR 41 decision 13.** ADR 41 decision 13 makes every
    entry hold the hash of its own fields and of the previous entry of its
    namespace, with no switch. That chain is on in every installation today.
    Decision 17 turns it off by default. An installation that holds chained
    entries keeps them, and the check of those entries still works. How
    such an installation moves to the new default is open in RFC 22. The
    server keeps the chain on for an installation that holds chained
    entries until that question has an answer.
20. **The audit switch comes first.** When the switch of decision 2 is off,
    decisions 17 and 18 do nothing.

### Out of scope

21. **Not decided here.** Retention and the deletion of old entries (with a
    retention anchor for the check), a verifier that compares an export
    from a sink with the chain, an entry for each restore, trusted proxies
    for the source IP, and per-namespace sinks stay open in RFC 22.

## What this ADR replaces

ADR 1 forbids edits to an accepted ADR. So this ADR records what it replaces:

- **ADR 32, decision 1:** "The server does not use OpenTelemetry now." The
  `otlp` sink of decision 12 sends logs in the OTLP format. Tracing stays in
  RFC 34.
- **ADR 32, decision 6:** the request log goes to `stdout` only. It now
  goes to the sinks of decision 14, with `stdout` as the default.
- **ADR 41, decision 5:** "If the write fails, the call fails and answers
  no value." This stays true when the audit log is on. When the switch of
  decision 2 is off, the server writes no entry and the call answers.
- **ADR 41, decision 13:** the kind field "with exactly one of two values".
  ADR 44 added `override`. Decision 4 adds `change` and `audit-read`.
- **ADR 41, decision 13:** the hash chain, which is always on. Decision 19
  makes it opt-in and off by default, with the rule for an installation
  that holds chained entries.
- **ADR 23, decision 6:** the audit routes "answer 204 and store nothing
  until the audit log (RFC 22) exists". ADR 41 already changed this. This
  ADR changes nothing more there.

## Consequences

- Open work, in this order. Each route lands with its e2e step first
  (ADR 12) and its action in the route table (ADR 17):
  1. The `--audit` switch and the installation scope `@server`.
  2. The middleware that writes an entry for each action that changes
     something, with the new kinds and fields, and a migration in both
     stores.
  3. Query filters, the cursor, and the export in JSON and CEF.
  4. `pkg/logsink`, its conformance suite and the `stdout` and `file`
     adapters. The application log moves to the sinks with no change of
     output by default.
  5. The shipper with its cursors, then the `syslog`, `webhook`, `otlp` and
     `objectstore` adapters.
  6. The `--audit-chain` switch with the scheduled check and its metric,
     after RFC 22 answers how an existing installation keeps its chain.
  7. The `--audit-checkpoints` switch, the signing key and the checkpoint
     check.
- The audit log guide gets the CEF mapping, the sink settings, an alert
  rule for the delivery lag, an alert rule for
  `dustpan_audit_chain_invalid`, and an example for each sink.
- The README stops saying that the audit log does not exist when step 3 is
  done.
- RFC 22 keeps retention, the verifier, the existing chain of an
  installation, trusted proxies and per-namespace sinks open.
