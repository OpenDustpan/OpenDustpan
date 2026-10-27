---
rfc: 76
title: Hosted multitenancy, the open questions after the namespace decisions
status: draft
created: 2026-10-18
---

# RFC 76. Hosted multitenancy, the open questions after the namespace decisions

Status: draft.

## Problem

ADRs 215 to 221 record the owner decisions of 2026-10-18: OpenDustpan Cloud
is multitenant, the namespace is the tenant, and each namespace can bring
its own key. Those ADRs leave these questions open. Each one has options
and a recommendation. The owner decides. ADR 248 and RFC 88 hold the federation model
and asynchronous geo-replication. RFC 79 is dropped.

## 1. A path prefix for each namespace in the CLI backend URL

ADR 215 decision 14: the Pulumi CLI keeps one token for each backend URL,
and OpenTofu one token for each host. A person in two namespaces on one
host re-logs in, or sets a token for each namespace in the environment
(ADR 215 decision 15).

- **Option A. Only the environment variables of ADR 215 decision 15.** No
  server change.
- **Option B. Also a path prefix for each namespace**, for example
  `pulumi login https://app.opendustpan.com/acme`. The Pulumi CLI would
  keep one token for each URL. It works only if the Pulumi CLI keeps the
  path of the backend URL in each request. OpenTofu finds its API through
  `/.well-known/terraform.json` on the host, so a path prefix does not help
  OpenTofu.

**Recommendation: A now.** Measure B with `make e2e` against each
supported version of the Pulumi CLI. Add it only if every version keeps the
path.

## 2. A move of a principal of the installation into a namespace

ADR 215 decision 11: a change of `identity.principalHome` converts nothing.
A self-hosted installation that changes to `namespace` keeps its people as
principals of the installation.

- **Option A. No move.** People sign in again through the identity provider
  of a namespace and get a new principal there. The administrator makes
  the grants again.
- **Option B. A move operation**, `POST .../principals/{id}/move`, that
  gives the principal a namespace home, if each grant of the principal is
  inside that namespace.

**Recommendation: A** until a self-hosted operator asks for B.

## 3. The role `account-administrator`

`pkg/access/roles.go` has `account-administrator` for "the tenant side of a
hosted installation". With the namespace as the tenant, the namespace role
`admin` does that job (ADR 216 decision 17).

- **Option A. Delete the role.** No tag shipped it (ADR 73), so the server
  keeps no alias. A grant that holds it fails the migration check.
- **Option B. Keep it as an installation role** with the administration
  actions of the installation directory only.

**Recommendation: A.** The word "account" is a console label only (ADR 215
decision 3), so a role name with it misleads.

## 4. The notices of a suspension

ADR 215 decision 16 sends notices at the suspension, 7 days before the
`disabled` state, and 7 days and 1 day before the delete.

- **Question:** are these days right? Who can resume a `disabled`
  namespace: the operator only, or also a namespace administrator after the
  cause ends? Do exports work in the `disabled` state, so that a tenant can
  take its data before the delete? The owner gave the periods of 30, 31 and
  90 days. The notice days are a proposal.

## 5. The open parts of a namespace backup

ADR 218 decision 4 lets a namespace back up and restore in place.

- **Restore as a copy into a new namespace.** Recommendation: allow it, with
  a new namespace ID and no issuer keys (the copy case of ADR 204 decision
  4).
- **Schedules for each namespace.** Recommendation: wait for the backup
  schedule of the installation (ADR 30 defers it), then add a namespace
  scope to it.
- **An export to the tenant.** A download holds no item keys (ADR 218
  decision 4), so it opens only on its installation. A tenant that leaves
  reads its data with the tool CLIs (`pulumi stack export`, `tofu state
  pull`).
  - **Option A. Document the CLI path only.**
  - **Option B. An export with item keys** only with a customer key that
    wraps them.
  - **Option C. A decrypted export of each history item of each unit**,
    with a fresh MFA step (ADR 56) and an audit entry in the scope of the
    namespace.

  Recommendation: document the CLI path now, and add the decrypted export
  of option C before OpenDustpan Cloud opens.

## 6. The client of each KMS adapter

ADR 217 decision 7 gives one port that fits three kinds of client. The
owner asked for a measurement before a choice.

- **Option A. The SDK of each cloud.** Four large dependencies.
  `CLAUDE.md` asks the owner before each new dependency.
- **Option B. Hand-written clients on `net/http`.** ADR 31 made the S3
  adapter with no new dependency, and its request signing can serve AWS
  KMS too. Each KMS needs a few calls only.
- **Option C. Adapters outside the server**, over a small protocol on a
  Unix socket, as Kubernetes KMS v2 plugins are, or over the external
  adapter protocol of ADR 208. The server ships no KMS client. The
  operator runs one adapter process for each KMS.

**The measurement.** For each of AWS KMS, Google Cloud KMS, Azure Key Vault
and Vault Transit, and for each option:

| Measure | How |
| --- | --- |
| Modules that the binary links | `go list -m all` before and after |
| Size of the binary | bytes before and after |
| Known vulnerabilities | `govulncheck` now, and the advisories of the last 12 months for those modules |
| Licenses | `make licenses` and `make trivy` |
| Lines that we keep | lines of the adapter and its tests |
| Latency of one wrap and one unwrap | p50 and p99 of 1,000 calls against the real service, from one region |
| Conformance | the `Wrapper` suite and the error-class suite pass |
| Auth paths | workload identity, static keys, the identity of ADR 217 decision 9 |
| Work for an operator | processes to run and to upgrade (option C) |

**Recommendation:** run the measurement for AWS KMS first, with options B
and C, then decide for all four.

## 7. A new data key for a Pulumi stack after a compromise

ADR 217 decision 20: a full re-encryption cannot change the data key of a
Pulumi stack, because the Pulumi CLI keeps ciphertexts of it in the stack
configuration.

- **Option A. The job lists the stacks**, and the docs give the steps: a
  new stack data key, and `pulumi config` with each secret again.
- **Option B. A second data key for the stack**: new secrets use it, and old
  ciphertexts still open with the old key until the team writes them
  again.

**Recommendation: B.** New Pulumi ciphertexts are tagged with a key
version, so the server knows which data key opens each one. A stays as the
docs for a compromise: the steps that write each secret again. `make e2e`
must show that each supported version of the Pulumi CLI keeps the tagged
ciphertexts unchanged.

## 8. `identity.principalHome: namespace` on SQLite

SQLite has no row-level security (ADR 220 decision 6).

- **Option A. Refuse it.** The server refuses to start with `namespace` on
  SQLite. The error names PostgreSQL.
- **Option B. Allow it**, with the query checks of ADR 220 decision 5
  only.

**Recommendation: A.** A team that needs tenants with their own members
runs PostgreSQL.

## 9. A lock that turns a feature off on OpenDustpan Cloud

ADR 219 lets the server lock a feature off. ADR 59 decision 1 says that the
hosted service never turns a feature off for its customers.

- **Option A. Keep ADR 59 decision 1.** The hosted operator locks limits and
  defaults only. The locks of ADR 215 decision 10 and ADR 216 decision 7
  set where principals live and what an operator grant reaches. They turn
  no feature off.
- **Option B. Allow it for a module that is not a security switch**, for
  example an experimental module before its release, with the notice of
  ADR 219 decision 4.

**Recommendation: A.** It keeps the promise of ADR 59. The owner decides.

## 10. Billing

The owner did not decide billing. ADR 215 decision 18 gives the server no
billing object and no usage counter for billing. This RFC asks nothing about
billing now. A later RFC asks it.

## 11. One cookie jar on one host

ADR 215 decision 4 puts each namespace on one host. A browser keeps one
cookie jar for each host. A person with principals in two namespaces
(ADR 215 decision 12) cannot keep two sessions in one cookie of that host.

- **Option A. A session cookie for each principal home**, with the name of
  the cookie bound to the namespace ID, for example one cookie for each
  namespace ID. The console sends the cookie of the namespace of the path. A browser sends every cookie of the host. The server reads only the cookie of the namespace in the path, and the session cookie of the installation for a route with no namespace.
- **Option B. One active namespace for each browser session.** A change of
  namespace signs the person in again.

**Recommendation: A**, with one cookie for each namespace ID.

## Open questions

1. Which option for each of the sections 1 to 3, 5 to 9 and 11?
2. The notice days and the resume of section 4.

## Outcome

None yet.
