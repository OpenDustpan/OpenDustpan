---
rfc: 101
title: A private registry for OpenTofu providers and modules
status: discussing
created: 2026-10-20
---

# RFC 101. A private registry for OpenTofu providers and modules

Status: discussing. ADR 269 records the decisions of the project owner: own
and team providers come first, and a team uploads them through the API as the
archive set of ADR 177. ADR 257 decides the protocols. This RFC holds the
rest: the order of the first slices, the routes, the signature check, the
module sources, the authorization actions and the limits. The only code is
the types and the ports in `pkg/registry`, with a conformance suite. There is
no route, no SQL and no new dependency.

## Problem

A team that builds its own OpenTofu providers and modules has no place on
OpenDustpan to put them. It uses a public registry, a git source or a
registry of another product. A regulated team must know every software source
that its plans use, and must keep the record of who published a version and
when.

RFC 10 asks for a registry and for curated upstreams. RFC 93 holds the
curation of public packages. This RFC holds the other part of RFC 10: the
artifacts that a team publishes itself (issue 949, issue 1149).

## Personas

- **Platform engineer at a regulated company.** The design target is DORA
  (Regulation (EU) 2022/2554). The engineer must show a list of the software
  sources of a plan, who published each version, and that a published version
  never changes.
- **Small self-hosting team.** One namespace, one binary, no extra service.
  The team wants to push a provider from CI with one `curl` or one command.
- **Team that moves between tools.** The team uses OpenTofu now and the
  Pulumi CLI for other units. It wants one set of namespaces, identities and
  storage for both. A Pulumi plugin mirror is a later slice (issue 1149).

## Facts

Checked on 2026-10-20 against the pages named.

- **Provider network mirror protocol** (<https://opentofu.org/docs/internals/provider-network-mirror-protocol/>).
  The CLI reads `:hostname/:namespace/:type/index.json` and
  `:hostname/:namespace/:type/:version.json` under a base URL that uses
  `https:`. The first JSON has a `versions` object. The second has an
  `archives` object. The key is a platform such as `linux_amd64`. The value
  has the `url` of the zip archive and optional `hashes`. If the answer has
  no hashes, the CLI installs the archive without a check. The CLI sends the
  configured credentials of the mirror host with the index requests. It does
  not send credentials for an archive. The page does not mention signatures.
  RFC 93 already proposes a signed archive URL for this reason.
- **ADR 257.** OpenDustpan serves the tools' own HTTP protocols and no OCI
  endpoint. The provider registry protocol and the module registry protocol
  need the discovery document, which the TFE-compatible API serves.
- **ADR 177.** The build tool writes `terraform-provider-<name>_<version>_<os>_<arch>.zip`
  for each target, `terraform-provider-<name>_<version>_SHA256SUMS` and the
  detached signature of that file. The open question of signing in ADR 177
  is still open: an OpenPGP library is a new dependency.
- **`go.mod`.** No OpenPGP library is a dependency today.
- **HCP Terraform** (<https://developer.hashicorp.com/terraform/cloud-docs/registry/publish-providers>).
  A private provider is published through its API. The release needs a
  `SHA256SUMS` file and a `SHA256SUMS.sig` file. The owner registers the GPG
  public key first, and the version names the key. Each platform is created
  with its SHA-256 and file name, and then the binary is uploaded.
  This RFC describes what that product does. It copies none of its code.
- **Not checked in this session.** The rules of Google Artifact Registry and
  AWS CodeArtifact for immutable versions, and the numbered controls named in
  the benchmark below, are from the author's reading. The reviewer checks them
  before an ADR quotes them.

## What is decided already

- ADR 269: own and team providers first, uploaded through the API as the
  archive set of ADR 177, with a check of the checksums and the signature at
  upload.
- ADR 257: the tools' own HTTP protocols, no OCI endpoint.
- ADR 12: the wire protocol of OpenTofu lives in `internal/api/tfe/`. The
  shared core stays tool-neutral.
- ADR 17: every feature has a REST API, and every route declares its actions
  and the object of each action.
- ADR 220: a namespace never reads the blob of another namespace.
- ADR 250: every layer of a security check stops the attack alone and has its
  own test.
- ADR 214: a feature that a user can see ships with its docs page.

## Options and recommendations

### A. The order of the slices

Issue 949 (the owner) puts providers first. Issue 1149 and the research of the
product manager put a read-only module registry from git tags first, and a
provider mirror with an allowlist of SHA-256 sums second.

1. **Providers by upload first (recommended).** The owner decided it
   (ADR 269). The slice is: upload route, network mirror routes, a docs
   page, and an e2e step with the real OpenTofu CLI. Modules from git tags
   are the second slice, because they need a forge connection and a decision
   on the module source (section D).
2. **Modules from git tags first.** It needs no upload and no signing. It
   does not follow ADR 269, so the owner must change the order. Do this only
   on a new instruction.
3. **Both in one slice.** Too large to review as one change.

### B. Where the code lives

1. **A domain package `registry` with the model and the ports; the wire
   formats in `internal/api/tfe/` (recommended).** The package holds the
   address, the version, the release record and the store port. It knows no
   file layout of OpenTofu except the names of the archive set that ADR 177
   fixes. The mirror JSON, the registry JSON and the discovery entry live in
   the adapter of the tool (ADR 12). A Pulumi plugin mirror later adds its
   own adapter and reuses the store.
2. **Put it in `workspace`.** The OpenTofu model of units is a different
   thing from a published artifact. This mixes two concepts.
3. **Share the package with RFC 93 (`pkgmirror`).** RFC 93 holds vetted
   copies of public packages. This RFC holds private releases. The two share
   the blob kind and the archive checks, and they differ in the owner of the
   bytes and in the approval. Keep two packages, and let both call one
   shared archive check if the code is the same (question 3).

### C. The address of a private provider

The mirror URL has the host of the provider address. A provider address is
`host/namespace/type`.

1. **The host of the server, the OpenDustpan namespace as the namespace of
   the address (recommended).** A team writes
   `source = "dustpan.example.com/acme/payments"`. The block
   `network_mirror` with `include = ["dustpan.example.com/*/*"]` takes this
   address and no other. No private name can collide with a public one. The
   address is not stored with the host, so a change of the host name of the
   server needs no data change.
2. **The team picks any host.** A team can reuse an old address. A name can
   shadow a public provider, which is the dependency confusion attack. Not
   recommended.

A module address is `namespace/name/system`, as the module registry protocol
asks. The same rule applies.

### D. Where module sources come from

1. **Upload, then git tags later (recommended).** The first slice that
   serves modules takes an uploaded archive in the same upload route. The
   second reads versions from the git tags of a forge connection (issue 1149,
   ADR 213). A tag must be a semantic version. The server reads the archive of
   the tag through the forge connection at the commit, and stores a copy, so a
   moved tag changes nothing (section E).
2. **Git tags only.** It has no upload to defend. A tag can move, so the
   server must store the commit and the archive hash at the first read.
3. **Upload only.** The team needs a CI step for each module.

### E. Storage and immutability

1. **A published version never changes (recommended).** The release record
   and its blobs are written once. A second publish of the same version with
   the same hashes does nothing and succeeds, so a retry after a timeout is
   safe. A second publish with other hashes is refused. A person cannot
   replace a version. A mistake is fixed with a new version. A life cycle
   state such as deprecated or revoked is the work of issue 951 and does not
   change a record.
2. **A version can be replaced by its owner.** This breaks the lock file of
   each user of the version and the record for an audit. Not recommended.

Each archive is one blob in the blob store, addressed by its SHA-256 and
the namespace (ADR 220). Two releases in one namespace that hold the same
bytes share one blob. A purge of a namespace removes its blobs.

### F. Signature and checksum checks at upload

ADR 269 says the checksums and the signature are checked at upload. The
checks, in order, with a test for each (ADR 250):

1. The checksums file has one line for each archive, with a SHA-256 and a
   file name. A line with a name that is not in the upload, a name with a
   path, and a duplicate name are refused.
2. The SHA-256 of each uploaded archive equals the line of its name. The
   refusal names the file.
3. The name of each archive gives the provider name, the version and the
   platform that the address and the request give. A mismatch is refused.
4. The detached signature is valid for the checksums file, made by a key that
   the namespace registered. The refusal names the signature file.
5. The server computes the `h1:` hash of each archive itself, and it does not
   trust a value from the client.

The open point is the OpenPGP library for check 4:

1. **Add `github.com/ProtonMail/go-crypto` after the review of ADR 7
   (recommended).** The license is BSD-3-Clause. It is the standard library
   for the format. The change that adds it comes with the signature check, in
   the second PR, with its own ADR for the dependency. The owner decides (ADR
   177 left this to the owner).
2. **Verify with `gpg` as a process.** The server image needs `gpg`, and the
   process is a new attack surface. Not recommended.
3. **No signature check in the first slice, the checksums only.** This does
   not follow ADR 269.
4. **A different signature scheme, for example Sigstore.** OpenTofu does not
   read it. It can be a later addition.

Until the owner accepts the library, the port `SignatureVerifier` has no
adapter and the suite for it is a skeleton (section I).

The server does not sign again what it serves. The user's CLI checks the
hashes from the mirror answer. A signature that the mirror protocol cannot
carry is kept as provenance in the release record: the fingerprint of the
key and the time. Question 5 asks if the server also signs.

### G. Authorization

Each route declares an action and an object (ADR 17). The actions are
proposals and are not in code. The shared file `pkg/identity` has one owner.

| Action | Object | What it allows |
| --- | --- | --- |
| `registry:read` | the address | list and read a release, and fetch its archives |
| `registry:publish` | the address | upload a release of an address |
| `registry:manage-keys` | the namespace | register and remove a trusted signing key |
| `namespace:read-registry` | the namespace | list the addresses of a namespace |

The object of a read is the address `namespace/kind/name`, so a role can
allow one provider. The mirror routes check `registry:read` at the point of
use (ADR 250, step 4). The signed archive URL is a second layer: it proves a
check at the time it was made, and it expires. A job token of a job in the
namespace holds `registry:read` for its own namespace by default.
`registry:publish` is not in any default role. A person grants it, and a
change freeze does not hold a publish.

Question 4 asks if a namespace can read the registry of another namespace.
The recommendation is no: ADR 220 and the fence say a namespace owns its
data.

### H. Limits

These are proposals. A setting of the installation can raise them.

| Limit | Value | Why |
| --- | --- | --- |
| One archive | 512 MiB | the size of the largest provider binaries, with room |
| One upload | 2 GiB | all archives of the release |
| Unpacked size of a module archive | 1 GiB | a zip bomb limit |
| Files in a module archive | 20,000 | the same |
| Versions of one address | 1,000 | a list that a CLI can read |
| Platforms of one version | 16 | the targets that exist |
| Uploads for each principal | 60 per hour | a runaway CI loop |

A provider archive is not unpacked by the server. The server reads the zip
directory only to check the name of the binary inside it. A module archive is
read with a limit on the sizes above, and a path that leaves the archive is
refused (ADR 250, step 2).

### I. Design only in this PR

The PR holds ADR 269, this RFC, the domain ports in `pkg/registry` and the
suite in `pkg/registry/registrytest` with an in-memory reference. The suite
for `Store` is complete. The suite for `SignatureVerifier` is a skeleton and
skips until an adapter and a key fixture exist. No route, no SQL, no
dependency.

## Benchmark

| Source | What it does | What we take |
| --- | --- | --- |
| HCP Terraform private registry (docs) | A signed release by API, a registered GPG key, a SHA-256 for each platform. Modules from tagged git commits. | The archive set, the key registered first. We refuse a version that is not complete. |
| OpenTofu public registry | A provider is published from a signed GitHub release. | The signature is a proof of origin, not of safety. |
| Google Artifact Registry, AWS CodeArtifact (author's reading, to check) | Each package format over its native protocol. A package version can be made immutable. | Native protocols (ADR 257). A version never changes. |
| PCI DSS v4.0, requirement 6.3.2 (to check) | An inventory of custom software and third-party components. | The list of addresses and versions of a namespace, with the publisher. |
| SOC 2, CC8.1 change management (to check) | Changes are authorized, tested and approved. | A record of who published each version. |
| ISO 27001:2022, A.5.21 ICT supply chain (to check) | Manage the security of the supply chain. | A known list of software sources. |
| DORA, Article 28 (to check) | Management of ICT third-party risk. | The same list, and a log that does not change. |

None of these gives a certificate to the feature. A control is met by the
whole system of a customer, not by one route.

## Open questions

1. **Order.** Providers by upload first (the recommendation, ADR 269), or
   modules from git tags first (issue 1149)? The recommendation follows the
   decision of the owner.
2. **Address host.** The host of the server as the host of every private
   address (section C, recommended), or any host?
3. **Shared code with RFC 93.** One archive check and one blob kind for both
   packages, or two? Recommended: write the check in the first PR that needs
   it and move it when the second needs it.
4. **Cross-namespace reads.** Can a namespace read a release of another
   namespace? Recommended: no.
5. **Re-signing.** Does the server sign what it serves with a key of the
   namespace (RFC 10 asks the same)? Recommended: no in the first slice.
   Reopen when a tool checks a signature on a mirror.
6. **OpenPGP library.** Add `github.com/ProtonMail/go-crypto` (section F,
   recommended)? The owner decides.
7. **Module source.** Upload first and git tags second (recommended), or git
   tags only?
8. **Trusted keys.** Does a namespace register keys (recommended, as HCP
   Terraform does), or does the installation hold one list?
9. **Overlap with RFC 93.** Where does the allowlist of SHA-256 sums land:
   in RFC 93, as its first slice, or here? Recommended: RFC 93, because that
   allowlist admits public packages and this RFC serves private ones.
10. **Life cycle.** Issue 951 adds states to a release. Does a revoked
    version leave the index but stay in the record? Recommended: yes.

## Follow-up issues

- Provider network mirror, the upload route and the docs page (issue 949).
- Module registry from git tags (issue 1149, first slice).
- The OpenPGP dependency and the signature adapter (after question 6).
- A SQL adapter and its migration for the store.
- The registry actions in `pkg/identity` (the shared owner).

## Proposed glossary lines

**Registry release**:
One published version of an OpenTofu provider or module in a namespace, with its archives, their SHA-256 hashes and the record of who published it. It never changes after the publish.
_Avoid_: Package (RFC 93 uses it for a vetted copy of a public package), artifact (that is a plan or a log of a job), upload (that is the act)

**Registry address**:
The name of a provider or module in a namespace: the namespace, the kind and the name, with the system for a module.
_Avoid_: Source address (that is the OpenTofu field), package name

## Outcome

None yet. ADR 269 records the decisions of the owner that this RFC builds on.
