# Research: does the audit log need tamper evidence, and how would it work?

Question (RFC 22, open question "spike"): The project owner wrote on
2026-10-12: "Spike it if it's required. I don't know if it is, and how this
would work." This note answers two questions:

1. Do the common audit frameworks require tamper evidence for an audit log?
2. If OpenDustpan gives tamper evidence, which design fits the server, and
   what does it cost?

Date: 2026-10-13

Outcome: on 2026-10-13 the owner chose to build both the chain with an
external copy and signed checkpoints, as opt-in features that are off by
default. ADR 60 records the decision.

This note gives facts, measurements and one recommendation. It decides
nothing. The owner decides in RFC 22 (ADR 15). It is not legal or compliance
advice. An assessor decides what meets a requirement for one organization.

## Summary

- **Required?** No framework in scope requires cryptographic tamper evidence
  for every system. PCI DSS v4.0.1 requires protection against change, a
  prompt copy to a central server or to media that is difficult to change,
  and change detection with alerts. SOC 2 has no log-integrity criterion.
  ISO/IEC 27001:2022 A.8.15 requires that logs are "protected". Only the NIST
  SP 800-53 Rev. 5 HIGH baseline requires cryptographic protection (AU-9(3))
  and non-repudiation (AU-10).
- **What exists.** The server already keeps a hash chain over the audit
  entries of each namespace (ADR 41, decision 13). It shows a change or a
  deletion of an entry by a person who does not compute the chain again.
- **Cost of the chain, measured.** On SQLite the chain adds 30 to 70 µs to an
  append and no new serialization. On PostgreSQL the chain limits one
  namespace to about 1,200 appends per second, whatever the concurrency.
  Without the chain the same path reaches 4,000 to 4,900. Appends to
  different namespaces do not wait for each other.
- **Recommendation.** Option C, an external anchor, built on the chain that
  exists. Each entry that a sink ships (ADR 60) carries its sequence, its
  previous hash and its hash. The operator's append-only sink then holds a
  copy that the server cannot rewrite. A verifier compares an export from the
  sink with the chain of the server. Signed checkpoints (option B) stay
  available for installations that need NIST HIGH.

## Part 1. What the frameworks require

Method: each requirement was read on 2026-10-13. The table says where only a
secondary source was available.

### PCI DSS v4.0.1 (June 2024)

Source: the PCI DSS v4.0.1 "Requirements and Testing Procedures" PDF, pages
243 to 251. The PCI SSC document library needs a license click-through, so
this note read a copy of the official PDF from a university site. The PDF
metadata names PCI SSC as the author.
(<https://www.middlebury.edu/sites/default/files/2025-01/PCI-DSS-v4_0_1.pdf>,
SHA-256 `5e6b9093b84007b973097d20126a3768ea2f0a1d4200255c849b0fb3bf04ebc7`.)

| Requirement | Text | Tamper evidence? |
| --- | --- | --- |
| 10.3 | "Audit logs are protected from destruction and unauthorized modifications." | Heading only. |
| 10.3.1 | "Read access to audit logs files is limited to those with a job-related need." | No. Access control. |
| 10.3.2 | "Audit log files are protected to prevent modifications by individuals." The testing procedure checks protection "via access control mechanisms, physical segregation, and/or network segregation." | No. Protection against change. |
| 10.3.3 | "Audit log files, including those for external-facing technologies, are promptly backed up to a secure, central, internal log server(s) or other media that is difficult to modify." | No. A copy outside the system. |
| 10.3.4 | "File integrity monitoring or change-detection mechanisms is used on audit logs to ensure that existing log data cannot be changed without generating alerts." Good practice: "new log data being added to an audit log should not generate an alert." | Change detection with an alert. A hash chain is one way to detect a change. It is not named. |
| 10.5.1 | "Retain audit log history for at least 12 months, with at least the most recent three months immediately available for analysis." | Context for retention. |

The words "tamper", "hash", "digital signature" and "WORM" do not occur in
the logging requirements.

### SOC 2 (AICPA 2017 Trust Services Criteria)

Source: AICPA "TSP Section 100, 2017 Trust Services Criteria", March 2020
update, from the Internet Archive
(<https://web.archive.org/web/2021id_/https://us.aicpa.org/content/dam/aicpa/interestareas/frc/assuranceadvisoryservices/downloadabledocuments/trust-services-criteria.pdf>).
The 2022 edition with revised points of focus needs an AICPA account. The
AICPA states that the 2022 revision does not change the criteria, so the
criteria text below is current. This note did not check the 2022 wording of
the points of focus.

| Criterion | Text | Tamper evidence? |
| --- | --- | --- |
| CC7.2 | "The entity monitors system components and the operation of those components for anomalies that are indicative of malicious acts, natural disasters, and errors affecting the entity's ability to meet its objectives; anomalies are analyzed to determine whether they represent security events." A point of focus lists "logging of unusual system activities." | No. |
| CC4.1 | "COSO Principle 16: The entity selects, develops, and performs ongoing and/or separate evaluations to ascertain whether the components of internal control are present and functioning." | No. No point of focus names logs. |

Paragraph .04 of the criteria says: "Use of the trust services criteria does
not require an assessment of whether each point of focus is addressed."

### ISO/IEC 27001:2022 Annex A 8.15 and ISO/IEC 27002:2022 8.15

Source: secondary only. Both standards are behind a paywall, and the ISO
Online Browsing Platform refused the request.

| Control | Text (secondary) | Tamper evidence? |
| --- | --- | --- |
| 27001:2022 A.8.15 | "Logs that record activities, exceptions, faults and other relevant events shall be produced, stored, protected and analysed." Several sites give this text. | No. "Protected". |
| 27002:2022 8.15 (guidance) | Logs "should be complete, accurate and protected against any unauthorised changes or operational problems". ISMS.online lists "Cryptographic hashing, Append-only recording, Read-only recording, Use of public transparency files" as protection methods (<https://www.isms.online/iso-27002/control-8-15-logging/>). | Recommended technique, not mandatory. 27002 is guidance ("should"). |

This note did not check whether 27002 asks for all four methods or for any
one of them. An organization can exclude an Annex A control in its Statement
of Applicability, with a reason.

### NIST SP 800-53 Rev. 5 and SP 800-53B

Source: the NIST OSCAL catalog and baseline profiles for SP 800-53 Rev. 5
(<https://github.com/usnistgov/oscal-content/tree/main/nist.gov/SP800-53/rev5/json>).
The current release is 5.2.0 (2025-08-27). Its list of changes does not touch
AU-9 or AU-10, so the text is the same as in 5.1.1.

| Control | Text | Baseline | Tamper evidence? |
| --- | --- | --- | --- |
| AU-9 | "a. Protect audit information and audit logging tools from unauthorized access, modification, and deletion; and b. Alert [Assignment: personnel or roles] upon detection of unauthorized access, modification, or deletion of audit information." | LOW, MODERATE, HIGH | Protection and an alert. |
| AU-9(2) | "Store audit records [Assignment: frequency] in a repository that is part of a physically different system or system component than the system or component being audited." | HIGH | A copy outside the system. |
| AU-9(3) | "Implement cryptographic mechanisms to protect the integrity of audit information and audit tools." The discussion names "signed hash functions using asymmetric cryptography." | HIGH | **Yes.** |
| AU-9(4) | "Authorize access to management of audit logging functionality to only [Assignment: subset of privileged users or roles]." | MODERATE, HIGH | No. |
| AU-9(6) | "Authorize read-only access to audit information to [Assignment: subset of privileged users or roles]." | none | No. |
| AU-10 | "Provide irrefutable evidence that an individual (or process acting on behalf of an individual) has performed [Assignment: actions]." The discussion names "digital signatures and digital message receipts." | HIGH | **Yes**, for the actions that the organization selects. |

### What the frameworks ask of OpenDustpan

Four needs come back in more than one framework:

1. **Restricted access** to read and to manage the log: PCI 10.3.1, AU-9(4).
   The action `namespace:read-audit` (ADR 41) covers the read.
2. **No change through the product**: PCI 10.3.2, AU-9. No API changes or
   deletes an entry (ADR 41, decision 13).
3. **A copy outside the system, promptly**: PCI 10.3.3, AU-9(2). The sinks
   (ADR 60) give this.
4. **Detection of a change, with an alert**: PCI 10.3.4, AU-9 b. The chain
   detects a change when something runs the check. Nothing runs the check on
   a schedule today, so nothing alerts.

Cryptographic protection is mandatory only for NIST HIGH (AU-9(3), AU-10).

## Part 2. How it would work here

### What exists today

- `pkg/audit` defines the entry and the hash: SHA-256 over a fixed encoding
  of the fields and the hash of the previous entry of the namespace
  (`ComputeHash`).
- Each store appends in one transaction with the fence check. It reads the
  last entry of the namespace, sets the next sequence and the hash, and
  inserts the row. On PostgreSQL the transaction holds the namespace row
  `FOR UPDATE`, so two appends cannot read the same last entry. Every other
  fenced write holds the same row `FOR SHARE`.
- `GET /api/audit/check` walks the chain of a namespace in pages of 100.
- The guide (`docs/guides/audit-log.md`) says that a person who can write to
  the database can compute the chain again, and that an anchor outside the
  database is RFC 22.

### What the chain proves today, and what it does not prove

The chain proves one thing: when a person calls the check, it finds an entry
that someone changed or deleted in the middle of the log without computing
the chain again. The check walks from sequence 1 and stops at the first entry
whose sequence, previous hash or own hash does not match.

It does not prove these things:

- **A rewrite by a database writer.** A person who can write to the database
  can change entries and compute every hash again. The check then answers
  valid. The guide says this.
- **A cut of the end of the log.** A delete of the last entries of a
  namespace leaves a valid chain. Nothing outside the database records the
  last sequence.
- **Restore.** A backup carries `audit_entries` (ADR 30, ADR 42), so a
  restore gives back a valid chain. But a restore rewinds the log to the
  backup point. The entries written after the backup are gone, and the check
  answers valid. The restore writes no audit entry today, so the log does not
  show that a restore occurred. A restore from an old or changed backup also
  checks as valid, if the backup is consistent.
- **Retention.** No code deletes entries today. A delete of the oldest
  entries, for example by hand in SQL, breaks the check at the first kept
  entry, because the check expects sequence 1 and an empty previous hash. So
  any retention rule needs a change to the check first.
- **More than one instance (ADR 48).** The chain itself works. Each append
  reads the last entry and inserts the next one in one database transaction,
  so all instances build one sequence for each namespace. On PostgreSQL the
  lock on the namespace row orders them. On SQLite only one node writes. But
  an entry does not record which instance wrote it, and the chain proves
  only the order of the commits, not the time. `atMs` comes from the clock of
  the instance that wrote the entry.
- **An alert.** Nothing runs the check on a schedule. A change stays unseen
  until a person calls `GET /api/audit/check`.

### Method of the measurement

A throwaway Go program in a scratch directory outside the repository
imported the real stores (`pkg/store/sqlite`, `pkg/store/postgres`) through
a `replace` directive. It was deleted after the spike. It compared, on the
same database:

- **no chain**: one transaction with the fence read (`FOR SHARE` on
  PostgreSQL) and an insert of a row with the same columns and a hash, but no
  read of the last entry.
- **raw chain**: the same transaction, plus a read of the last entry by
  `(namespace, sequence)`. On PostgreSQL the transaction holds the
  namespace row `FOR UPDATE`, as the store does.
- **store chain**: `Store.Append` of the real store.

Each row of the tables is 4,000 appends, spread over `c` goroutines and over
one or eight namespaces. Machine: a container with 4 vCPUs (Intel Xeon at
2.80 GHz), 15 GB of memory and a virtual disk. SQLite as the store opens it
(WAL, `synchronous(normal)`, `_txlock=immediate`). PostgreSQL 16.15 on the
same machine, on a Unix socket, with default settings (`fsync` on). Each
scenario ran at least twice. The numbers below are one run. The other runs
were within about 20 percent.

These numbers come from one small machine. Treat them as ratios, not as a
sizing guide.

### Option A: a hash chain for each namespace (what exists)

**SQLite**, appends per second and latency:

| c | namespaces | no chain | raw chain | store chain |
| --- | --- | --- | --- | --- |
| 1 | 1 | 15,826/s, p50 43 µs, p99 167 µs | 9,152/s, p50 70 µs, p99 379 µs | 5,793/s, p50 117 µs, p99 559 µs |
| 8 | 1 | 11,012/s, p99 0.5 ms | 7,801/s, p99 1.5 ms | 4,800/s, p99 3.9 ms |
| 8 | 8 | 9,958/s, p99 0.5 ms | 6,616/s, p99 3.5 ms | 4,532/s, p99 3.1 ms |
| 32 | 1 | 5,872/s, p99 54 ms | 5,732/s, p99 89 ms | 3,378/s, p99 105 ms |
| 32 | 8 | 5,879/s, p99 51 ms | 3,800/s, p99 72 ms | 3,595/s, p99 80 ms |

- The read of the last entry costs about 30 µs at c=1 (no chain to raw
  chain). The rest of the gap to the store chain is the store code: the
  validation, a secondary index on `(namespace, at_ms, sequence)` and the
  sqlc layer.
- SQLite has one writer for the whole file. Every write already waits for
  every other write, so the chain adds no new serialization. The number of
  namespaces does not change the result.
- The long tail at c=32 (p99 above 50 ms, a maximum near 1 s) is the
  `busy_timeout` back-off of SQLite. It occurs with and without the chain.
- `Check` read 4,000 entries in 36 to 44 ms.

**PostgreSQL**, appends per second and latency:

| c | namespaces | no chain | raw chain | store chain |
| --- | --- | --- | --- | --- |
| 1 | 1 | 1,389/s, p50 0.7 ms | 1,176/s, p50 0.8 ms | 1,017/s, p50 0.9 ms |
| 8 | 1 | 4,059/s, p50 1.9 ms | 1,229/s, p50 6.2 ms | 1,271/s, p50 6.2 ms |
| 8 | 8 | 4,439/s, p50 1.7 ms | 3,825/s, p50 2.0 ms | 3,512/s, p50 2.2 ms |
| 32 | 1 | 4,874/s, p50 6.4 ms | 1,266/s, p50 24.6 ms | 1,194/s, p50 26.2 ms |
| 32 | 8 | 3,961/s, p50 7.5 ms | 3,713/s, p50 8.4 ms | 3,568/s, p50 8.8 ms |

- **The chain serializes the appends of one namespace.** One namespace stays
  near 1,200 appends per second at c=1, 8 and 32. The latency grows with the
  number of waiting appends (p50 26 ms at c=32). Without the chain the same
  load reaches 4,000 to 4,900 per second.
- **Namespaces stay independent.** With eight namespaces the chain costs
  about 10 to 20 percent of throughput, and no namespace waits for another.
- `Check` read 11,992 entries in 80 to 98 ms.

**The lock on the namespace row.** The store holds the namespace row
`FOR UPDATE` during an append. Every other fenced write in the namespace (a
stack update, a state version) takes the same row `FOR SHARE`, so it waits
for each append. The spike timed an empty fenced transaction
(`FOR SHARE` and commit) in one namespace, with eight goroutines that
appended to the same namespace in a loop:

| Load in the namespace | Fenced write p50 | p99 |
| --- | --- | --- |
| none | 0.5 ms | 1.0 ms |
| 8 appenders, no chain | 1.8 ms | 4.0 ms |
| 8 appenders, store chain (`FOR UPDATE` on the namespace row) | 3.6 ms | 6.1 ms |
| 8 appenders, chain with a separate head row (`FOR SHARE` on the namespace row, `FOR UPDATE` on one row for each namespace in a new table) | 2.5 ms | 4.5 ms |

Most of the slowdown is load on the machine (the no-chain row). The lock on
the namespace row adds about 1.8 ms at p50. A separate head row takes away about
60 percent of that, and keeps the same append throughput (1,347 per second
at c=8, 1,194 at c=32). This change is a proposal only. The spike changed no
production code.

**Is the limit a problem?** Today only secret reads and overrides write an
entry. The full catalog of RFC 22 adds one entry for each action that changes
something. An update of a Pulumi stack or an OpenTofu apply is one action,
not one entry for each checkpoint. A namespace that makes 1,200 auditable
actions per second is far beyond the load that this note expects, but this
note has no measurement of real traffic.

**What option A gives, and what it does not give:**

- It shows a change or a deletion of one entry, when the check runs.
- It does not stop a person who can write to the database: that person can
  compute the chain again.
- It does not show a cut of the end of the log. A person who deletes the
  last entries leaves a valid chain.

### Option B: signed checkpoints in the object store

A job writes a checkpoint for each namespace from time to time. The
checkpoint holds the namespace, a sequence, the hash at that sequence, the
time and an Ed25519 signature. It goes to the blob store under an immutable
key, for example `audit/checkpoints/<namespace>/<sequence>.json`. With the
chain, the checkpoint needs to sign only the hash of the last entry: that
hash commits to every entry before it. A Merkle tree is needed only for
proofs that one entry is in the log without the whole log.

Measured cost, on one core:

| Work | Time |
| --- | --- |
| Ed25519 signature of a root | 41 to 56 µs |
| Merkle root (RFC 6962 style) over 10,000 entry hashes | 15 ms |
| Merkle root over 100,000 entry hashes | 123 ms |
| Merkle root over 1,000,000 entry hashes | 1.23 s |

The job runs outside the write path, so it adds nothing to an append. It
reads the last entry of each namespace, or every entry since the last
checkpoint for a Merkle tree.

- **Gives:** a person who can write to the database but cannot use the
  signing key cannot rewrite the log without detection. A cut of the end of
  the log shows, because a checkpoint names a sequence that the database no
  longer holds. With an object-lock (WORM) bucket, a person who can write to
  the bucket cannot delete a checkpoint before its retention ends.
- **Does not give:** protection against a person who holds the signing key,
  for example an operator of the server. Entries after the last checkpoint
  have no protection.
- **Needs:** a new key and its storage, rotation and publication (see "Key
  management" below). A WORM bucket needs S3 Object Lock, which ADR 31 does
  not cover. A local directory has no object lock.

### Option C: an external anchor (ship to a sink that the operator controls)

The sinks of ADR 60 ship each entry to syslog, a webhook, OTLP logs, rolled
JSONL in object storage or a file. Each shipped entry carries `sequence`,
`prevHash` and `hash`. The operator's sink (a SIEM, a syslog server, an
object-lock bucket) is append-only for the server. The server cannot change
or delete a shipped entry.

- **Cost on the write path:** none for tamper evidence. The cost is that of
  the sink adapter, which ADR 60 needs anyway. The adapter ships after the
  commit, from the table, with a cursor for each sink. A failed delivery
  retries from the cursor and counts a metric.
- **Gives:** an independent copy (PCI 10.3.3, AU-9(2)). A verifier detects a
  rewrite, a deletion and a cut of the end of the log: it finds an entry in
  the sink that the database does not hold, or one with another hash.
- **Does not give:** protection for entries that the server did not ship yet.
  The window is the delivery delay. It gives no cryptographic proof against
  a person who controls both the server and the sink.
- **Needs:** nothing new beyond ADR 60, if each shipped entry carries the
  chain fields.

### How each option interacts with the rest of the server

| Topic | A: chain only | B: signed checkpoints | C: external anchor |
| --- | --- | --- | --- |
| **Backup and restore** (ADR 30, ADR 42) | A backup carries `audit_entries`. A restore rewinds the log to the backup point. The chain stays valid, so the check cannot see the rewind. | A checkpoint after the backup point names a sequence that the restored database does not hold. The verifier reports a rewind. Restore must then go on from the restored head, and a checkpoint must mark the restore. | The sink still holds every entry that the server shipped after the backup point. After a restore the server appends from a lower sequence again, so the sink holds two entries with one sequence. The verifier must tell a restore apart from a rewrite: an entry for the restore (ADR 38 apply) helps. |
| **Retention and deletion** | A delete of old entries breaks a check that starts at sequence 1. A fix: the delete keeps a retention anchor (the first kept sequence and its previous hash), and the check starts there. The anchor is in the same database, so it protects nothing against a database writer. | A checkpoint at or after the first kept sequence still verifies. The checkpoints themselves need a retention rule (bucket lifecycle). | The sink has its own retention, set by the operator. The server can delete old entries without loss of evidence if the sink keeps them (PCI 10.5.1: 12 months). |
| **More than one instance** (ADR 48) | The database orders the appends, so every instance builds one chain. | Two instances can write the same checkpoint. An immutable key with `ErrExists` makes the write safe: the second write fails and changes nothing. A lease (ADR 48) can elect one writer, to save work. | Each instance can ship. Two instances can ship the same entry. The cursor must live in the database, and a sink must accept a duplicate (same namespace and sequence). |
| **Key management** (ADR 23) | No key. | A new Ed25519 key. If the server derives it from the key-encryption key, anyone with that key can sign, so it protects only against a database writer. A separate key file, in the style of ADR 23 decision 1, keeps the roles apart. Rotation needs a key ID in each checkpoint and a list of public keys for the verifier. | No key in the server. A signed webhook (ADR 60) uses a shared secret for delivery, not for tamper evidence. |
| **Verifier** (ADR 17, ADR 39) | `GET /api/audit/check` walks the chain. | The check also reads the checkpoints from the blob store and checks each signature with the public key. It compares each signed hash with the entry at that sequence. A command `opendustpan audit verify` calls the route and needs no database access (ADR 39). An independent verifier can read the bucket and an export with only the public key. | A route or a command reads an export from the sink (JSONL). It compares each entry with the chain of the server through the API. It reports the first sequence that differs, the entries that the server lost, and the entries that the sink never received. The command reads a local file and the API only (ADR 39). |

### Change detection with an alert

None of the options alerts by itself. PCI 10.3.4 and AU-9 b ask for an
alert. The server can run the chain check on a schedule and count the result
in a metric, for example `dustpan_audit_chain_invalid` for each failed
namespace, with no namespace label (ADR 32 decision 5). The guide then gives
an alert rule, as it does for `dustpan_audit_append_failures_total`. The
same applies to options B and C, with a check of the checkpoints or of the
sink delivery delay.

## Recommendation

**Option C: an external anchor, built on the chain that exists.**

1. Keep the per-namespace chain (option A). It is already built and tested,
   it adds no serialization on SQLite, and on PostgreSQL it limits only one
   namespace to about 1,200 appends per second.
2. Each entry that a sink of ADR 60 ships carries `sequence`, `prevHash` and
   `hash`. The operator's sink is the anchor outside the database.
3. A verifier route and command compare an export from a sink with the chain
   of the server (ADR 17, ADR 39).
4. A scheduled chain check sets a metric for the alert of PCI 10.3.4.
5. A restore (ADR 38) writes an entry that names the backup, so the verifier
   can tell a restore apart from a rewrite.

Why C:

- It meets the frameworks that most users name (PCI DSS 10.3.3 and 10.3.4,
  ISO 27001 A.8.15, SOC 2) with features that milestone 4 builds anyway.
- It is the only option that still holds the entries that a restore rewinds.
- It needs no new key and no new storage feature.
- It does not change the write path.

Trade-offs that the owner accepts with C:

- No cryptographic proof against a person who controls the server and the
  sink. An installation that needs NIST HIGH (AU-9(3), AU-10) needs option B
  as well. Option B can come later, because the chain is already in place
  and a checkpoint signs only its head.
- Entries that the server did not ship yet have only the chain. The window is
  the delivery delay.
- The verifier depends on an export from each kind of sink. The JSONL file
  and the object-storage sink are the simplest to read back.

Follow-ups that this note proposes, for the owner to decide:

- Move the PostgreSQL append lock from the namespace row to a head row for
  each namespace. Measured: fenced writes in a busy namespace wait about
  1.1 ms less at p50 (3.6 ms to 2.5 ms), with the same append throughput.
- Retention needs a retention anchor (the first kept sequence and its
  previous hash) for the check. It waits for the owner's decision on
  retention.

## Sources

- PCI DSS v4.0.1, Requirements and Testing Procedures, June 2024, copy of the
  official PDF:
  <https://www.middlebury.edu/sites/default/files/2025-01/PCI-DSS-v4_0_1.pdf>
- AICPA, TSP Section 100, 2017 Trust Services Criteria (March 2020 update):
  <https://web.archive.org/web/2021id_/https://us.aicpa.org/content/dam/aicpa/interestareas/frc/assuranceadvisoryservices/downloadabledocuments/trust-services-criteria.pdf>
- ISMS.online, ISO 27002:2022 control 8.15 (secondary):
  <https://www.isms.online/iso-27002/control-8-15-logging/>
- NIST SP 800-53 Rev. 5, OSCAL catalog and SP 800-53B baselines:
  <https://github.com/usnistgov/oscal-content/tree/main/nist.gov/SP800-53/rev5/json>
- NIST SP 800-53 Rev. 5 release 5.2.0:
  <https://csrc.nist.gov/pubs/sp/800/53/r5/upd1/final>
- RFC 6962, Certificate Transparency, section 2.1 (Merkle tree hash):
  <https://www.rfc-editor.org/rfc/rfc6962#section-2.1>
- In this repository: ADR 23, ADR 30, ADR 32, ADR 38, ADR 39, ADR 41
  (decision 13), ADR 42, ADR 48, `pkg/audit`, `pkg/store/*/audit.go`,
  `docs/guides/audit-log.md`.
