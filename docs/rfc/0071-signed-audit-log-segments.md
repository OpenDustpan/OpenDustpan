---
rfc: 71
title: Export signed audit log segments to the blob store for offline verification
status: draft
created: 2026-10-17
---

# RFC 71. Export signed audit log segments to the blob store for offline verification

Status: draft. Answers [issue 802](https://github.com/OpenDustpan/OpenDustpan/issues/802).
This RFC decides nothing. An ADR records a decision (ADR 15).

## Problem

The audit log in the database is the record of truth (ADR 60). Each entry
holds a hash that links it to the previous entry of its scope (ADR 41
decision 13, ADR 64). Signed checkpoints (ADR 60 decision 18) sign the hash
at one sequence of a scope. Log sinks hold live copies.

An auditor who must check the log without trust in the running server has
these gaps today:

- A checkpoint proves one hash at one sequence. It does not carry the
  entries between two checkpoints. The auditor must read the database, and
  the database is the thing that the auditor does not trust.
- A sink copy is a live stream. It has no boundary, no signature and no proof
  that the stream is complete.
- A person with write access to the database and a valid chain algorithm can
  write a new chain. The hash takes no key (ADR 64 decision 10). Only a signed
  object outside the database stops this (ADR 64 "Consequences").
- ADR 60 decision 21 and ADR 68 decision 2 leave open a verifier that
  compares an export with the chain, and a retention anchor.

The owner approved this direction on 2026-10-17: the database is the record,
sinks are live copies, and signed segments in object storage are the
independent proof. This RFC works out the details.

## Facts about the current code

- The chain is per scope (a namespace or `@server`). An entry has a
  sequence, a hash and a previous hash. A start entry holds a digest of
  earlier entries. An end entry stops the chain (ADR 64).
- `pkg/audit/checkpoint.go` signs checkpoints with an Ed25519 key. The key
  comes from `DUSTPAN_AUDIT_SIGNING_KEY_FILE`, never from the key-encryption
  key (ADR 60 decision 18). A checkpoint holds the epoch, scope, sequence,
  hash, time and key ID. The key ID is the first 16 hexadecimal characters of
  the SHA-256 hash of the public key. A signed epoch object names a new
  history of the log after a restore.
- Blob keys are immutable. `Put` on an existing key returns `ErrExists` and
  leaves the first object unchanged (`pkg/blob`).
- `pkg/durable` gives operations with a start key, a claim with an expiry on
  the database clock, and a fencing token that a store checks in the
  transaction of each write (ADR 72 decisions 1 to 3). A schedule is a row
  with `next_run_at`.
- `pkg/secret` uses AES-256-GCM under a key-encryption key (ADR 23). It does
  not sign. The only signing key in the installation is the audit signing
  key.

## Research: how comparable systems do this

All sources are public documentation. None needed contact with the project.

**Cloud audit log file integrity validation.** AWS CloudTrail hashes each
log file with SHA-256. Each hour it writes a *digest file* that lists the log
files of that hour with their hashes. It signs the digest file with
SHA256withRSA, with one key pair for each Region. The digest file holds the
hash and signature of the previous digest file, so a deleted digest shows as
a break in the chain. Digest files go to a separate folder of the same bucket.
A digest is written even for an hour with no events, so an auditor can assert
that no log file exists for that hour. The first digest after a restart has
no previous digest, and CloudTrail writes no digest while validation is off.
The CLI validates in place. The documentation says that an auditor who moves
the files writes a custom validator.
Sources: [digest file structure](https://docs.aws.amazon.com/awscloudtrail/latest/userguide/cloudtrail-log-file-validation-digest-file-structure.html),
[how validation works](https://docs.aws.amazon.com/awscloudtrail/latest/userguide/cloudtrail-log-file-validation-intro.html).

What we take: a signed manifest per period, a link to the previous
manifest, a signed manifest for an empty period, a separate key prefix, and a
public rule for a custom verifier. What we change: Ed25519 instead of RSA
(see question 7), and a link by sequence and hash, not by time only.

**Append-only transparency logs.** RFC 9162 (Certificate Transparency 2.0)
defines a Merkle tree over the entries. A log signs a *tree head* (STH): log
ID, timestamp, tree size and root hash. A *consistency proof* shows that a
newer tree extends an older tree without change. A *monitor* reads every
new entry and compares successive tree heads. The RFC states a known limit:
a log can show two different views to two clients (a *split view*), so
auditors must compare heads that they got from different places.
Source: [RFC 9162](https://www.rfc-editor.org/rfc/rfc9162.html).

What we take: the signed head (our manifest holds the last sequence and last
hash), and the rule that a verifier compares heads from two places (the
auditor pins a head outside the blob store). What we leave: the Merkle tree.
The audit log is a linear chain per scope, so a consistency proof is a
check that the chain continues, which the previous-hash link already
gives. A Merkle root per segment is option B of question 9.

**Vault audit devices.** Vault sends every request and response to all
enabled audit devices (file, syslog, socket). It refuses the request if it
cannot write to at least one device. It writes an HMAC-SHA256 of most string
values, so the log holds no secret in plain text. The page describes no
signing, chaining or integrity check of the log.
Source: [Vault audit devices](https://developer.hashicorp.com/vault/docs/audit).

What we take: two independent copies, and a keyed or hashed form for secret
values (OpenDustpan already keeps secrets out of entries, ADR 32 decision 7).
What we add: Vault gives no tamper evidence, and this RFC gives it.

## Proposal in outline

This section is the author's preferred shape. Each part has open questions
in the next section. Words like "segment" are new terms for `GLOSSARY.md`
if the RFC is accepted.

### 1. What a segment is

A *segment* is a closed, contiguous range of entries of one scope in one
epoch. It is two blob objects:

- `audit-segments/<epoch>/<scope-hex>/<segment-number>.entries.ndjson.gz`.
  One line per entry, with every stored field that the entry hash covers,
  plus the stored `hash` and `previousHash`. This is the form that
  `ComputeStoredHash` reads, so a verifier recomputes each hash.
- `audit-segments/<epoch>/<scope-hex>/<segment-number>.manifest.json`. A
  small signed JSON object (the *manifest*).

The segment number has fixed width, as the checkpoint sequence has, so a list
returns segments in order. The scope name is in hexadecimal for the same
reason as checkpoints: a scope name can hold characters that a blob key
cannot.

The manifest holds:

| Field | Meaning |
| --- | --- |
| `version` | Format version, starts at 1. |
| `installationID` | Stable ID of the installation, so a segment from another installation does not pass. |
| `epoch`, `scope`, `segment` | Where the segment sits. |
| `firstSequence`, `lastSequence`, `count` | The range. `count = lastSequence - firstSequence + 1`. |
| `firstPreviousHash`, `lastHash` | The hash that links in, and the head hash. |
| `entriesSHA256` | SHA-256 of the uncompressed entries file. |
| `firstAt`, `lastAt`, `closedAt` | Times of the first and last entry, and the time of the close. |
| `reason` | `count`, `age`, `end` (an end entry closed the chain), `epoch` or `empty`. |
| `previousManifestSHA256` | SHA-256 of the previous manifest of this scope and epoch. Empty for the first. |
| `keyID`, `signature` | The key and the Ed25519 signature over a fixed context line and the other fields, as for a checkpoint. |

The signed message starts with a context line, for example
`opendustpan audit segment v1`, so a segment signature is never valid for a
checkpoint or an epoch object (the same rule as the checkpoint code).

A segment also holds the start entry or end entry of ADR 64 when one falls
in its range. A verifier then applies the check of ADR 64 decision 9 inside
the segment.

### 2. Boundaries

A segment closes when the first of these happens:

- The open range reaches *N* entries.
- The oldest open entry is *T* old and the range has at least one entry.
- The chain ends with an end entry, or a restore starts a new epoch (the
  *reason* field says which).

Defaults for discussion: *N* = 10 000 entries, *T* = 1 hour. An operator can
set both. A busy scope closes by count. A quiet scope closes by age, so the
proof for an event is never late by more than *T* plus the time to write.

A segment with zero entries is the *empty* kind (question 2).

### 3. How a segment links to the previous one

Two links, so that a verifier detects each kind of change:

1. **By chain.** `firstSequence = previous.lastSequence + 1` and
   `firstPreviousHash = previous.lastHash`. This repeats the entry chain at
   the segment level. It proves that no entry is missing between segments.
2. **By manifest.** `previousManifestSHA256` binds the exact previous
   manifest, including its signature. This proves that a previous manifest
   was not replaced by another validly signed manifest for the same range.

A verifier detects:

| Change | How it shows |
| --- | --- |
| A segment deleted in the middle | The segment number or sequence has a gap, and the next manifest names a hash that no stored manifest has. |
| An entry changed in a segment | The entry hash does not match, or `entriesSHA256` does not match. |
| A manifest replaced | The next manifest names another `previousManifestSHA256`, or the signature fails. |
| An entry added or removed in the middle | The count or the hash chain breaks. |
| The newest segments deleted (tail cut) | **Not detected by the segments alone.** The verifier needs a head from another place (question 3). |
| A fork after a forced failover | The `fence_version` of ADR 64 decision 10 shows in the entries. The verifier reports it and does not fail. |

The tail cut is the same limit as CloudTrail has. The chain of digests
proves history up to the newest digest that the auditor holds.

### 4. What is signed, and with which key

A manifest is signed with the *audit signing key* (Ed25519). That is the
only signing key of the installation today. The key already has these
rules: it comes from a file with mode rules, and the server never derives
it from the key-encryption key.

The key hierarchy that exists is: the key-encryption key wraps the data keys
of secrets (ADR 23), and a separate audit signing key signs checkpoints. The
signing key has no parent. This RFC does not put a segment key under the
key-encryption key. A leak of the key-encryption key must not let a person
forge a proof.

Rotation: see question 4.

### 5. Verification

A new offline command, for example
`opendustpan audit verify-segments --source <dir-or-blob-url> --public-key <file> [--scope <name>] [--head <manifest-hash>]`.
It reads only the exported objects and the public key. It needs no database
and no running server. The `--source` can be a directory that the auditor
copied, so the check also works on a disk that the auditor holds.

For each scope and epoch, it checks, in order:

1. Each manifest has a valid signature from a key in the key list (question 4).
2. The entries file matches `entriesSHA256` and the count.
3. Each entry hash matches its fields and its previous hash, with the
   algorithm of ADR 41 decision 13, ADR 60 and ADR 64 decision 10.
4. The segment links to the previous one by chain and by manifest hash.
5. If `--head` is given, the newest segment is that head or a descendant.
6. Start and end entries follow the rules of ADR 64 decision 9.

Output, for people and for machines:

```
scope acme  epoch 3  segments 1-42  entries 1-418 211  OK
scope @server  epoch 3  segments 1-6  entries 1-9 004  FAIL
  segment 5: previous manifest hash does not match segment 4
  segment 6: first sequence 8 101 follows 7 998 (gap of 102)
checked 2 scopes, 48 segments, 427 215 entries; 1 scope failed
```

`--format json` prints one object per scope with `status`, `firstSequence`,
`lastSequence`, `head` (the last manifest hash, for the auditor to pin) and
a list of `findings`. The exit code is 0 for OK, 1 for a finding, 2 for a
usage or read error. The same logic exists as a Go function in `pkg/audit`,
so a third party can verify with the SDK. The format is documented
byte for byte in `docs/reference/`, so an auditor can write a verifier
without our code, as AWS documents for CloudTrail.

The server also needs the live read of the current check, so the console can
show "last segment closed at". That is a status field in the REST API
(ADR 17). It adds no new trust.

### 6. Retention, backups and restores

- Segments live in the blob store under the prefix `audit-segments/`. The
  backup (RFC 8) holds the database and its blobs as it does today. Whether
  it also copies `audit-segments/` is question 6.
- A retention rule on the database (ADR 68 decision 2) may delete entries
  that a signed segment already holds. The first retained segment
  then is the anchor: its `firstPreviousHash` and `previousManifestSHA256`
  are the trusted start. This is a candidate for the "retention anchor" that
  ADR 64 leaves open. The server must not delete an entry that no segment
  holds yet.
- A retention rule on the blob store removes the oldest segments. The
  verifier accepts a start at any segment, and reports "verified from
  segment *k*". An operator who needs 12 months (ADR 68) keeps segments at
  least that long. Object lock on the bucket is the control that stops a
  person with write access from deleting segments. The guide recommends it.
- A restore replaces the log and starts a new epoch (the checkpoint code
  does this already). The old epoch keeps its segments. The new epoch starts
  with the first segment of the restored log. The verifier reports the epoch
  change and the range of sequence numbers that the old epoch signed and the
  new epoch does not hold. That range is the evidence of what the restore
  lost.

### 7. Several instances on one database

Rule: a segment is a deterministic function of the entries and of one
recorded close decision, and the blob store never overwrites.

- A *segment close* is a durable operation (ADR 72). The start key is
  `audit-segment/<epoch>/<scope>/<segment>`. Any instance can start it. The
  store keeps one operation for one key, so two instances start the same
  operation.
- One instance claims it. It carries the fencing token (ADR 72 decision 2).
  The steps are: (1) fix the range and the close time in one conditional
  database write, (2) write the entries file, (3) write the manifest, (4)
  mark the segment as written. Each step checks the token in its transaction.
- A schedule row (ADR 72 decision 3) creates the close operations: one
  per scope with open entries at *T*, and one when the count passes *N*.
- If the claim holder stops after step 3 and before step 4, the next holder
  repeats steps 2 and 3. The bytes are the same because the range, the close
  time and the Ed25519 signature are the same (Ed25519 is deterministic).
  `Put` returns `ErrExists`, and the holder treats this as success after it
  reads the object and compares it. A different object under the same key
  is an alert, not a success.
- Two instances never write two different segments with the same number,
  because the range of segment *n* is recorded once, in step 1, by a
  conditional write.

The close time is a database value from step 1, never a wall-clock read at
signing time, so a repeat gives the same bytes.

### 8. FIPS

Hash: SHA-256, approved. Signature: Ed25519, approved in FIPS 186-5. The
question is whether the validated module covers it. Go ships a FIPS 140-3
module. A build selects it with `GOFIPS140`, and a run turns on FIPS mode with
`GODEBUG=fips140=on`. Version v1.0.0 has CMVP certificate 5247
([Go FIPS 140-3 documentation](https://go.dev/doc/security/fips140)). The page
does not list the algorithms, so this RFC does not claim that Ed25519 is
inside the validated boundary. A spike must read the certificate and the
module's algorithm list before an ADR decides (question 7).

Segments use no encryption in this proposal. If question 8 chooses encrypted
segments, AES-256-GCM is the algorithm that `pkg/secret` already uses.

## Open questions

Each question has options and the author's recommendation. The owner decides.

### Question 1. What closes a segment?

- **A. Count only.** Simple and deterministic. A quiet scope never closes, so
  its last entries have no proof for a long time.
- **B. Time only.** Bounded delay. A busy scope makes huge segments.
- **C. Count or age, whichever comes first.** Two settings. Bounded size and
  bounded delay.

Recommendation: C, with defaults of 10 000 entries and 1 hour, as settings.

### Question 2. Does a quiet period get a segment?

- **A. No empty segments.** Less storage. An auditor cannot tell "no events"
  from "the server did not write".
- **B. An empty signed segment for each period T, for each scope that has a
  chain.** CloudTrail does this. The count of objects is scopes times 24
  each day. A scope with one entry a month costs 720 objects of about 1 KiB.
- **C. One heartbeat manifest per installation per period, with the head of
  every scope in it.** Fewer objects. It is a new object type and the manifest
  size grows with the scope count.

Recommendation: C if the scope count is large, otherwise B. This needs a
number: ask for the largest known count of scopes.

### Question 3. How does a verifier detect a cut at the tail?

- **A. Do nothing.** The limit is documented, as in CloudTrail.
- **B. The auditor pins the head** (`--head`) from a source outside the blob
  store: a ticket, an email, a sink or a printed hash. The console shows the
  head after each close.
- **C. The server also publishes the head to a second place on a schedule,**
  for example a different bucket or a log sink. A cut then needs a write in two
  places.

Recommendation: B now, and C as an option for hosted. It costs no code in the
server beyond showing the head.

### Question 4. Which key signs, and how does it rotate?

- **A. The checkpoint key signs segments too.** One key and one file. A leak
  breaks both proofs. Rotation of that key already needs a verify-key list
  (the signer has `AddVerifyKey`).
- **B. A separate segment key.** A leak of one does not forge the other. Two
  files to protect and rotate.
- **C. Key list object.** A signed object `audit-segments/keys.json` lists
  every public key with a validity range. Each new key entry is signed by the
  previous key, so the auditor pins only the first public key. Rotation adds
  an entry. A lost key with no overlap ends the chain: the operator then
  starts a new epoch (like a restore).

Recommendation: A for the key, with C for the list. Rotate by adding the new
key to the list, signed by the old key, and then switching the signer. The
old key stays in the list for verification forever. A compromise of a key is
a new epoch with an alert, and the verifier reports that segments of the old
epoch after the compromise time are not trusted.

### Question 5. Which scopes get segments?

- **A. All scopes with a chain.** Simple. Many small namespaces make many
  small objects.
- **B. `@server` and namespaces that opt in.** Less storage. A silent gap
  for a namespace that did not opt in.
- **C. All scopes, with several quiet scopes packed into one manifest.**
  Fewer objects. A more complex format.

Recommendation: A first. Measure object count. Move to C only if the count is
a real cost.

### Question 6. Are segments part of a backup?

- **A. Not in the backup.** They are independent proof. A restore to an
  older point leaves segments that are newer than the log, and the verifier
  shows the lost range.
- **B. In the backup, as a copy.** A new installation can verify old
  history. The copy sits under the control of the same operator.
- **C. Both:** the live bucket is never restored over, and the backup holds a
  copy for portability.

Recommendation: A, and say so in the backup guide. The point of the proof is
that a restore cannot rewrite it.

### Question 7. Which signature algorithm in FIPS builds?

- **A. Ed25519 in all builds.** One format. It may sit outside the validated
  module in some versions.
- **B. ECDSA P-256 with SHA-256 in all builds.** Inside the module. The
  signature is not deterministic, so the repeat rule in part 7 needs the
  step that stores the signature in the database before the blob write.
- **C. Ed25519 by default, ECDSA P-256 as a setting for FIPS builds.** The
  `keyID` and an `alg` field in the manifest say which. The verifier supports
  both.

Recommendation: C, after a spike confirms whether the validated module
covers Ed25519. If it does, drop to A.

### Question 8. Are the entries files encrypted?

Entries hold personal data (names, email addresses, IP addresses; ADR 68
decision 4). Signed and immutable objects conflict with an erasure request.

- **A. Plain, in the operator's bucket.** Simple. The operator controls access.
- **B. Encrypted with a data key under the key-encryption key.** Access
  control is stronger. An auditor needs the key. Offline verification gets
  harder.
- **C. Plain, with personal fields replaced by a keyed hash (as Vault does
  for string values).** The chain would need to hash the replaced form, which
  changes ADR 41 and ADR 60.

Recommendation: A, with a guide that names the erasure limit and the object
lock trade-off. Raise C as its own RFC, because it changes the entry hash.

### Question 9. A Merkle root in each segment?

- **A. No.** A linear chain per scope already proves order and continuity.
- **B. Yes.** A Merkle root over the entries gives proof of inclusion for one
  entry without the whole file, which helps a person who must show one event
  to a third party.

Recommendation: A. Revisit if people ask for single-entry proofs.

### Question 10. Does the server need a segment to exist before it deletes an entry?

- **A. Yes.** Retention never deletes an entry that no segment holds. If the
  segment writer is down, retention waits.
- **B. No.** Retention is independent. A window exists where an entry is
  gone and has no proof.

Recommendation: A.

### Question 11. Is the feature on by default?

Checkpoints are opt-in and need the chain (ADR 60 decision 18). The chain is
on by default (ADR 64).

- **A. Opt-in with `--audit-segments`**, like checkpoints. Needs a signing
  key.
- **B. On by default when a signing key and a blob store exist.** An
  operator without a key gets a warning.

Recommendation: A first, because the feature writes to object storage and
needs a key that the server must not create. The server never generates the
key (the rule of ADR 23 decision 2).

### Question 12. Does one scope with a failed writer stop writes?

Vault stops the request if no device can write. The audit log here is in the
database, so the segment writer is not on the request path.

- **A. No.** The writer retries with backoff. A metric and a status field show
  the lag. Requests continue.
- **B. Yes, after a limit.** A lag above a limit puts the server in a
  degraded mode. This hurts availability for a copy of data that the database
  already holds.

Recommendation: A, with an alert on lag.

## Risks

- The format becomes a public contract for auditors. A change after release
  needs a `version` bump and a verifier that reads both versions.
- Object count grows with scopes and time (question 2 and 5). The sizing guide
  must state the numbers.
- A signing key on the server can sign a false segment if an attacker holds
  the server. Segments prove the log as of the last close, to a person who
  holds an earlier head. They do not prove that the server was honest.

## Outcome

None yet.
