# Audit checkpoint layout

The server writes signed audit checkpoints and epoch objects to the blob
store under the prefix `audit-checkpoints/`. A verifier outside the server
can read this layout. The audit log guide tells how the server signs and
checks them.

## Keys

| Object | Key |
| --- | --- |
| Checkpoint copy | `audit-checkpoints/<epoch>/<scope>/<sequence>-<attempt>.json` |
| Epoch copy | `audit-checkpoints/epochs/<epoch>-<attempt>.json` |

- `<epoch>` is the epoch number in decimal, padded with zeros to 8 digits.
  The first epoch object has the number 1. Checkpoints before the first
  epoch object are in epoch 0.
- `<scope>` is the name of the namespace in hexadecimal, or `40736572766572`
  (`@server`) for the installation.
- `<sequence>` is the sequence of the entry in decimal, padded with zeros to
  20 digits. It is 1 or more.
- `<attempt>` is 32 lowercase hexadecimal characters: 16 random bytes that
  each write attempt makes.
- Every key has an attempt part. A key without one is not a copy.

## Copies

- One sequence can have more than one copy, and one epoch can have more than
  one copy. Two server processes that write at the same time each store a
  copy. A writer stores a copy only when it finds no copy of the sequence.
- A check finds the copies only through a list of the directory of the
  scope, never through the database.
- A check reads every copy of each sequence that it checks. These are
  mismatches: a copy with a bad signature, a copy whose body does not match
  its key, and two copies with a valid signature that sign different hashes.
- More than 16 copies of one sequence or of one epoch are a mismatch. The
  check reads none of them.
- A key under the directory of a scope, or under `epochs/`, that is not one
  of the forms above is a mismatch.
- The current epoch is the highest epoch number. A bad copy of any epoch
  stops the read of the epoch, so a bad copy never counts.
- No sweep of the server deletes a key under `audit-checkpoints/`.

## Bodies

A checkpoint copy is a JSON object with the fields `scope`, `sequence`,
`epoch`, `hash`, `at` (RFC 3339 time), `keyID` and `signature` (base64). The
signature is an Ed25519 signature over these lines, joined with a line feed:

```text
opendustpan audit checkpoint v1
<scope name>
<sequence>
<epoch>
<hash>
<time in Unix milliseconds>
<key ID>
```

An epoch copy is a JSON object with the fields `epoch`, `at`, `keyID` and
`signature`. The signature covers these lines, joined with a line feed:

```text
opendustpan audit checkpoint epoch v1
<epoch>
<time in Unix milliseconds>
<key ID>
```

The key ID is the first 16 hexadecimal characters of the SHA-256 hash of the
public key.
