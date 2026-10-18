---
rfc: 76
title: Hosted multitenancy, the open questions after the account decisions
status: draft
created: 2026-10-18
---

# RFC 76. Hosted multitenancy, the open questions after the account decisions

Status: draft.

## Problem

ADRs 215 to 221 record the owner decisions of 2026-10-18: OpenDustpan Cloud
is multitenant, the account is the tenant, and each account can bring its
own key. Those ADRs leave these questions open. Each one has options and a
recommendation. The owner decides.

## 1. The host name of an account

ADR 215 decision 9 gives each account a host name that never changes.

- **Option A. A name that the tenant picks at sign-up**, for example
  `acme.opendustpan.com`. Easy to type in `pulumi login` and in a backend
  block. The sign-up shim keeps the names unique. A taken name tells a new
  customer that the name exists, as an organization name does on other
  hosted products.
- **Option B. The account ID**, for example `acc-k3v9q2m7x4t8p1w6z5r0.opendustpan.com`.
  Says nothing about other accounts. Hard to type.
- **Option C. A custom domain of the tenant**, for example
  `iac.acme.example`, with a CNAME to the shard and a certificate from ACME.
  It costs certificate work for each account. A domain that the tenant
  loses can break its cloud trusts (ADR 215 decision 10).

**Recommendation: A first, C later as an extra host name.** The account ID
host of B can stay as a second host name that always works.

## 2. One person in many accounts

ADR 215 decision 6 gives a person a separate principal in each account.

- **Option A. Keep separate principals.** Simple isolation. A person signs
  in to each account host on its own.
- **Option B. One identity with an account picker**, as the Pulumi Cloud and
  HCP Terraform organization models do. One sign-in, then a choice of
  account. It needs an identity object above accounts, which is a read
  across accounts (ADR 220 decision 10).

**Recommendation: A** until customers ask for B with a count.

## 3. The move of an account between shards

ADR 215 decision 2 lets only the operator move an account. ADR 221 allows no
routing, so a move is a copy and a DNS change.

Proposed steps:

1. The source sets the account state `moving`. It refuses new work and
   waits for work in progress, as maintenance mode does (ADR 38).
2. The source exports the account in the portable format of ADR 42, with
   the scope `account`.
3. The target imports it with the same account ID, namespace IDs and unit
   IDs. The key-encryption key of the target wraps the account key again. A
   customer key must allow the identity of the account on the target.
4. The source sets the state `moved`. It refuses each write and signs no
   issuer token. Then the operator changes DNS.

Open: ADR 204 decision 4 says that a restore into another installation
restores no issuer key. A move must keep the keys, or each cloud trust
breaks. **Recommendation:** a move is a third case next to "restore into the
same installation" and "copy". It keeps the keys, and the state `moved` on
the source makes sure that two installations never sign with one key.

## 4. A suspended account

ADR 215 decision 3 has the state `suspended`, for example for an unpaid
invoice.

- **Option A. Read only.** Reads, exports and sign-in work. New work is
  refused. Work in progress ends.
- **Option B. No access**, except for the tenant administrators.

**Recommendation: A**, for 30 days, then the operator can delete the
account after a notice. The number of days is the owner's.

## 5. The open parts of a namespace backup

ADR 218 decision 4 lets a namespace back up and restore in its own account.

- **Restore as a copy into a new namespace.** Recommendation: allow it, with
  a new namespace ID and no issuer keys (the copy case of ADR 204 decision
  4).
- **Schedules for each namespace.** Recommendation: wait for the backup
  schedule of the installation (ADR 30 defers it), then add a namespace
  scope to it.
- **An export to the tenant.** This is not a bucket of the customer for its
  data (ADR 221): the data stays in the shard. Recommendation: a download of
  the backup archive first, a push to a target of the tenant later.

## 6. The client of each KMS adapter

ADR 217 needs adapters for AWS KMS, Google Cloud KMS, Azure Key Vault and
Vault Transit. `CLAUDE.md` asks the owner before each new dependency.

- **Option A. The SDK of each cloud.** Four large dependencies.
- **Option B. Hand-written clients on `net/http`.** ADR 31 made the S3
  adapter with no new dependency, and its request signing serves AWS KMS
  too. Each of the four APIs needs a few calls only.

**Recommendation: B**, with each adapter on the `Wrapper` conformance suite
and a recorded test against the real service in CI.

## 7. The KMS identity of each account

ADR 217 decision 7 needs one identity for each account at its KMS.

| KMS | Option |
| --- | --- |
| AWS KMS | One IAM role of the operator for each account, with the account ID as the external ID. The key policy also requires the encryption context of the account. |
| Google Cloud KMS | One service account of the operator for each account. |
| Azure Key Vault | One application of the operator for each account, with a federated credential. |
| Vault Transit | One JWT auth role for each account, with a token that the server signs for the account. |

**Recommendation:** the table, with the operator making each identity in
the create-account step (ADR 216 decision 4).

## 8. The rotation of a namespace key

ADR 217 decision 14 says a namespace key does not rotate.

- **Option A. No rotation**, as for data keys (ADR 23).
- **Option B. A new version for new keys only.** A new namespace key
  version wraps new data keys and item keys. Old keys stay under the old
  version. A rotation then needs no new write of an item key blob.
- **Option C. A full rewrap** of every item key into a new blob. It breaks
  the rule of ADR 151 that one delete reaches each backup.

**Recommendation: B.**

## 9. More than one account on SQLite

SQLite has no row-level security (ADR 220 decision 4).

- **Option A. Refuse it.** An installation on SQLite holds one account.
- **Option B. Allow it with a static setting**, with the checks of ADR 220
  decision 3 only.

**Recommendation: A.** A team that needs more than one account runs
PostgreSQL.

## 10. A lock that turns a feature off on OpenDustpan Cloud

ADR 219 lets the server lock a feature off. ADR 59 decision 1 says that the
hosted service never turns a feature off for its customers.

- **Option A. Keep ADR 59 decision 1.** The hosted operator locks limits and
  defaults only.
- **Option B. Allow it for a module that is not a security switch**, for
  example an experimental module before its release, with the notice of
  ADR 219 decision 4.

**Recommendation: A.** It keeps the promise of ADR 59. The owner decides.

## 11. Usage counters for billing

ADR 215 decision 3 gives each account a billing reference, and ADR 216
decision 4 lets `installation:read-accounts` answer usage counters.

**Recommendation:** stored bytes and the count of units only. No counter of
requests or of people, so that the server keeps no behavior data (ADR 74).

## Open questions

1. Which option for each of the sections 1 to 11?
2. The number of days of section 4.

## Outcome

None yet.
