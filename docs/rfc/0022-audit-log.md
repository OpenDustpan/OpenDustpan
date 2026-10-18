---
rfc: 22
title: Audit log
status: discussing
created: 2026-10-10
---

# RFC 22. Audit log

Status: discussing. Opened from the competitor review in the research doc
"IaC and TACOS landscape". [ADR 60](../adr/0060-audit-log-port-and-log-sinks.md)
accepts the port, the entry fields, query and export, the log sinks, and
tamper evidence as two opt-in features that are off by default. Retention,
the existing chain of an installation, the verifier, trusted proxies and
per-namespace sinks stay open.

## Problem

Every competitor with a console has an audit log, usually in a paid tier:
Spacelift (Enterprise), HCP Terraform (Standard), Pulumi Cloud (Pro, export on
Enterprise), Terramate (Enterprise), Stategraph (paid). OpenDustpan's RFCs
record some events (profile opens in RFC 5, policy decisions in RFC 13, egress
requests in RFC 16) but have no common log.

## First ideas

- One append-only log for every action that changes something or reads a
  secret: who, what, which unit or object, when, from where, and the result.
- Query and filter in the API and the console, export (JSON, CEF), streaming
  to an external sink, retention.
- Entries keep their context when the object is deleted.
- RFCs 5, 13, 16 and others write into it instead of keeping their own logs.

## Open questions

- The existing chain of an installation. ADR 41 decision 13 keeps the
  chain on in every installation today, and ADR 60 decision 17 turns it off
  by default. How does an existing installation move to the new default?
  Lead proposal: an installation that already holds chained entries keeps
  the chain on until an administrator turns it off. The server writes an
  entry of the kind `change` when the administrator does so. A new
  installation starts with the chain off. ADR 60 decision 19 keeps the
  chain on for such an installation until the owner answers.
- The verifier: a route and a command that compare an export from a sink
  with the chain of the server, and report the first sequence that differs
  (spike proposal). The command uses the API only (ADR 39).
- An entry for each restore that names the backup, so that a verifier can
  tell a restore apart from a rewrite (spike proposal).
- The PostgreSQL append lock: move it from the namespace row to a head row
  for each scope (spike proposal; measured: other fenced writes in a busy
  namespace wait 1.1 ms less at p50).
- Retention: how long the server keeps entries, and how a delete of old
  entries keeps the chain check valid, for example with a retention anchor
  (the first kept sequence and its previous hash). PCI DSS 10.5.1 asks for
  12 months, with the last three months at hand. This waits for the owner.
- Trusted proxies: which `X-Forwarded-For` values the server believes for
  `sourceIP`.
- Per-namespace sinks: can a namespace send its entries to its own sink?

## Owner direction (2026-10-12)

The project owner answered questions in this RFC on 2026-10-12. Words in
quotation marks are the words of the owner. A question that the owner sent to
research or to a spike stays in "Open questions", with a mark. This section
decides nothing. An ADR records a decision (ADR 15).

- Question "Is the audit log a port with adapters": "Yes."
- Question "Which events are always on, and which can an operator turn off?":
  "It's either fully on or off until someone decides otherwise."
- Question "How does the log stay tamper-evident": "Spike it if it's required. I
  don't know if it is, and how this would work." The question stays in "Open
  questions".

## Owner direction (2026-10-13)

The project owner answered on 2026-10-13. Words in quotation marks are the
words of the owner. ADR 60 records the decision.

- The two answers of 2026-10-12, repeated here for one place: to "Is the
  audit log a port with adapters": "Yes." To "Which events are always on,
  and which can an operator turn off?": "It's either fully on or off until
  someone decides otherwise."
- Question "Which sinks are in milestone 4?": "If we're building this
  anyway, the application logs gains this capability too. Structured
  application logs can go to Syslog/Otel/a file as alternative targets to
  stdout. As always, keep the output adapters generic so that users can
  easily swap them out., Syslog (RFC 5424), HTTP webhook, OpenTelemetry
  logs, Object storage"
- Question "How does the log stay tamper-evident", after the proposal
  below: "I see the value in building both 1 and 2 as opt-in features but
  these need to be off by default." Option 1 is the chain with the external
  copy: each entry that a sink ships carries `sequence`, `prevHash` and
  `hash`, and a periodic check sets a metric for an alert. Option 2 is
  signed checkpoints. ADR 60 decisions 17 to 20 record this.

## Proposal: tamper evidence

Answered by the owner on 2026-10-13 (see above) and recorded in ADR 60.
The text below is the proposal as the owner read it. The spike is in
[`docs/research/audit-log-tamper-evidence.md`](../research/audit-log-tamper-evidence.md).

**Is it required?** Not by PCI DSS v4.0.1, SOC 2 or ISO/IEC 27001:2022.
PCI DSS asks for protection against change (10.3.2), a prompt copy to a
central server or to media that is difficult to change (10.3.3), and change
detection with an alert (10.3.4). Only the NIST SP 800-53 Rev. 5 HIGH
baseline asks for cryptographic protection (AU-9(3)) and non-repudiation
(AU-10).

**Measured cost of the chain that exists (ADR 41).** On SQLite the chain
adds 30 to 70 µs to an append and no new serialization. On PostgreSQL it
limits one namespace to about 1,200 appends per second (4,000 to 4,900
without the chain). Namespaces do not wait for each other.

**Proposal: option C, an external anchor on top of the chain.**

1. Keep the chain of each namespace.
2. Each entry that a sink of ADR 60 ships carries `sequence`, `prevHash`
   and `hash`. The operator's append-only sink is the anchor outside the
   database.
3. A verifier route and command compare an export from a sink with the
   chain of the server. The command uses the API only (ADR 39).
4. A scheduled chain check sets a metric, so an operator can alert (PCI DSS
   10.3.4).
5. A restore writes an entry that names the backup, so the verifier can tell
   a restore apart from a rewrite.

Trade-offs: no cryptographic proof against a person who controls both the
server and the sink, and no protection for entries that the server did not
ship yet. An installation that needs NIST HIGH also needs signed checkpoints
(option B). They can come later, because a checkpoint signs only the head of
the chain.

Follow-ups for the owner: move the PostgreSQL append lock from the namespace
row to a head row for each namespace (measured: other fenced writes in a busy
namespace wait 1.1 ms less at p50), and a retention anchor for the check.

## Outcome

Partly accepted: ADR 60 (the port, the entry fields, query and export, the
log sinks, and opt-in tamper evidence that is off by default). Retention
waits for the owner.

The existing chain of an installation: on 2026-10-14 the lead accepted the
lead proposal under the delegation of the owner. An installation with
chained entries keeps the chain on until an administrator turns it off on
purpose. That change is an audit entry of the kind `change` in `@server`,
and the server warns at each start while chained entries exist and the
chain is off.
A backup does not carry the chain state: a restore keeps the state of
the target installation, writes no change entry, and warns when the
restored log holds chained entries and the chain is off.

Project manager decisions on 2026-10-18, under the owner's decision rule
([ADR 212](../adr/0212-planner-role-smtp-settings-fresh-mfa-proxies-restore-audit-and-branding.md)):

- Decision 4 records the trusted-proxy rule that the code has: named
  proxies, one named header, and a walk from the right. It supersedes in
  part ADR 27 decision 2.
- Decision 5: a restore writes one audit entry in `@server` with the backup
  ID and the SHA-256 of the manifest.
