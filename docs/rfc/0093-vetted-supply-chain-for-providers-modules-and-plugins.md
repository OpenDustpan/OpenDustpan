---
rfc: 93
title: A vetted supply chain for OpenTofu providers and modules and Pulumi resource plugins
status: draft
created: 2026-10-20
---

# RFC 93. A vetted supply chain for OpenTofu providers and modules and Pulumi resource plugins

Status: draft. Nothing in this RFC is decided. No code exists for it. The
owner answers the questions at the end (ADR 15).

## Problem

An OpenTofu provider and a Pulumi resource plugin are executables from a
third party. They run in the plan and the apply, with the cloud credentials
of the job. A module runs in the same plan. A security team in a regulated
company cannot vet each of them, so it refuses community providers and
modules, or it vets them by hand: committed lock files, modules pinned to a
commit, a `provider_installation` block in each CLI configuration, a private
mirror, a fork that it builds itself.

One trap is common. A lock file holds the `h1:` hash only for the platforms
that someone ran `tofu init` on. CI on another platform finds no matching
hash, and depending on the source, it can trust the provider again on first
use (see the facts below).

The goal of the theme: a security team allows a provider, a module or a
Pulumi resource plugin once, through an approval in OpenDustpan. From then
on every job uses exactly that artifact, on every platform, and nothing
else. Four layers do this, and each layer must stop the attack alone
(ADR 250): a curated mirror, an allowlist with approvals, verification, and
enforcement at run time.

**Relation to RFC 10.** RFC 10 (draft) asks for an artifact registry with
curated upstreams, private publishing and scanning. This RFC takes the
curation part of RFC 10 and makes it concrete: the mirror, the approval, the
hashes and the enforcement. Private publishing (a team uploads its own
provider) and the version lifecycle stay in RFC 10 (question 12).

**Names (proposal, question 1).** _Provider_ alone is on the _Avoid_ list of
the glossary (it is a provider block of a profile). This RFC says:

- **Vetted artifact**: one version of an OpenTofu provider, an OpenTofu
  module or a Pulumi resource plugin that a namespace admitted, with its
  archives and hashes for each platform.
- **Artifact admission**: the change that adds a vetted artifact to the
  allowlist of a namespace. It meets the gates and approval rules.
- **Artifact mirror**: the routes that serve vetted artifacts to the tools,
  through the protocols that the tools already speak.

## Facts from primary sources

Checked on 2026-10-20 against the docs, and against the source at OpenTofu
`main` 42f15c9b and Pulumi `master` 6c60436a where the docs say less.

**OpenTofu provider network mirror protocol**
(<https://opentofu.org/docs/internals/provider-network-mirror-protocol/>)

- `GET :hostname/:namespace/:type/index.json` gives `{"versions":
  {"1.0.0": {}}}`. A 404 means that the mirror does not have the provider.
- `GET :hostname/:namespace/:type/:version.json` gives `{"archives":
  {"<os>_<arch>": {"url": ..., "hashes": [...]}}}`. `url` can be relative
  to the document. `hashes` is optional, and with no hashes the CLI
  installs the package with no check.
- No service discovery: the URL comes from `network_mirror { url }`, and it
  must be `https:` (`internal/getproviders/http_mirror_source.go`).
- The CLI sends the credentials of the mirror host to the two JSON
  requests only, not to the archive downloads. The docs say to give
  signed URLs that expire for the archives.

**OpenTofu provider registry protocol**
(<https://opentofu.org/docs/internals/provider-registry-protocol/>)

- Discovery maps `providers.v1` in `/.well-known/terraform.json`.
- `GET :namespace/:type/:version/download/:os/:arch` gives `download_url`,
  `shasums_url`, `shasums_signature_url`, `shasum` and
  `signing_keys.gpg_public_keys`.
- The CLI checks the SHA-256 of the zip against `shasum` and the checksums
  file, then the detached GPG signature of that file
  (`internal/getproviders/package_authentication.go`).

**Signing state of OpenTofu** (<https://opentofu.org/docs/cli/plugins/signing/>)

- For `registry.opentofu.org` only, when the registry gives no key, the CLI
  skips the GPG check and logs a warning. Every other registry host needs a
  valid key. `OPENTOFU_ENFORCE_GPG_VALIDATION=true` turns the skip off. The
  docs call the skip temporary.
- "OpenTofu only authenticates provider plugins fetched from a registry."
  From a mirror, the hashes are the only check.
- We found no build attestation or other provenance for providers in the
  docs or in <https://github.com/opentofu/registry>.

**OpenTofu module registry protocol**
(<https://opentofu.org/docs/internals/module-registry-protocol/>)

- Discovery key `modules.v1`. `GET :namespace/:name/:system/versions`, and
  `GET .../:version/download`, which gives a JSON `location` or an
  `X-Terraform-Get` header.
- The location can be any module source, for example `git::https://...`,
  except another registry address.

**CLI configuration** (<https://opentofu.org/docs/cli/config/config-file/>)

- `provider_installation` holds `direct`, `filesystem_mirror`,
  `network_mirror` (`url`, `include`, `exclude`, `trust_all_hashes`) and
  `oci_mirror`. With no `direct` method, a provider that no method
  matches fails with "was not found in any of the search locations".
- `TF_CLI_CONFIG_FILE` names the file. We found no `TOFU_CLI_CONFIG_FILE`.

**Dependency lock file**
(<https://opentofu.org/docs/language/files/dependency-lock/>,
<https://opentofu.org/docs/cli/commands/providers/lock/>)

- `zh:` is the SHA-256 of the official zip. `h1:` is a hash of the content
  of the package, so it also matches an unpacked copy.
- From a registry, the CLI records each checksum that the registry gives,
  which usually covers every platform. **From a mirror, it records only the
  current platform.** This is the multi-platform trap. With
  `trust_all_hashes = true` on the `network_mirror`, it records the mirror's
  hashes for all platforms.
- `tofu providers lock -platform=linux_amd64 -platform=darwin_arm64` records
  the hashes of each named platform. `-net-mirror=<url>` takes them from a
  mirror instead.
- Modules are not in the lock file.

**Pulumi CLI plugins** (`sdk/go/common/workspace/plugins.go`,
<https://www.pulumi.com/docs/iac/cli/environment-variables/>,
<https://www.pulumi.com/docs/iac/concepts/projects/project-file/>)

- `pluginDownloadURL` accepts `https://`, `http://`, `github://`,
  `gitlab://` and `git://`. With none, the CLI tries GitHub and then
  `get.pulumi.com`.
- An HTTP source downloads
  `<url>/pulumi-<kind>-<name>-v<version>-<os>-<arch>.tar.gz`.
- `PULUMI_PLUGIN_DOWNLOAD_URL_OVERRIDES` takes `regexp=URL` pairs and
  replaces the URL of each plugin that matches.
- `PluginSpec.Checksums` maps `<os>-<arch>` to a SHA-256. **When no entry
  exists for the current platform, the CLI skips the check with no
  warning.** `pulumi plugin install --checksum` sets one platform only.
  The `packages` entries of `Pulumi.yaml` accept `checksums`.
- The CLI checks no signature of a plugin.
- `PULUMI_IGNORE_AMBIENT_PLUGINS` stops the search of `$PATH`, and
  `PULUMI_DISABLE_AUTOMATIC_PLUGIN_ACQUISITION` stops downloads.
- We did not find the doc page of the `pluginDownloadURL` resource option.
  The behavior above is from the source.

## What is decided already

- ADR 12: each tool's wire protocol lives in its own adapter under
  `internal/api/<tool>/`. Shared packages stay tool-neutral.
- ADR 13: cross-tool features do not merge tool models. Shared code names a
  unit by tool, namespace and the tool's own name.
- ADR 17: every feature has a REST API, and every route declares its
  actions and objects.
- ADR 224 decision 2: six fixed decision points. A new decision point is a
  new ADR. Decision 16: `state.write` runs before each state write that no
  job of OpenDustpan makes.
- ADR 225: approval rules are data, with approvers, a count and
  `authorMayApprove`.
- ADR 134 decision 7: the launcher writes the OpenTofu CLI configuration of
  each tool step (`TF_CLI_CONFIG_FILE`) and the Pulumi home (`PULUMI_HOME`).
  The credentials helper answers the job token for the host of the server
  only.
- ADR 137 decision 6: a pool can limit the egress of a sandbox to the server
  and to the hosts that the tools need, through the egress proxy of RFC 16.
- ADR 250: every layer stops the attack alone, and has its own test.

## Options and recommendations

### A. Where the code lives (ADR 12)

1. **One shared core, one adapter per tool (recommended).** A new domain
   package `pkg/artifact` holds the tool-neutral parts: the vetted artifact
   record (kind, address, version, platforms, a SHA-256 for each archive,
   the admission record), the allowlist queries, and the store and blob
   ports. The kind is one of `opentofu-provider`, `opentofu-module` and
   `pulumi-plugin`. Each tool's address grammar, its upstream protocol and
   its hash scheme (`h1:` is an OpenTofu scheme) live in a port
   `artifact.Upstream` with one adapter for each kind. The wire routes live
   in `internal/api/tfe/` (provider network mirror, module registry) and in
   `internal/api/pulumi/` (plugin downloads).
2. **One model in each tool package.** `workspace` holds OpenTofu providers
   and modules, `stack` holds Pulumi resource plugins. No shared allowlist,
   so the console needs two views, and the approval and storage code is
   written twice.
3. **A separate registry service** (RFC 10, option 1, the upstream proxy).
   More moving parts for the first slice. It can come later as a role of the
   binary (RFC 7) on the same port.

The address and the version stay as the tool writes them. The core compares
them as opaque strings after the adapter checks their grammar (section F).

### B. The mirror, through the tools' own protocols

- **OpenTofu providers: the provider network mirror protocol
  (recommended).** Routes under
  `/tofu/mirror/v1/{namespace}/{hostname}/{ns}/{type}/index.json` and
  `.../{version}.json`. The index lists only the admitted versions. Each
  version document lists only the admitted platforms, with an archive URL on
  the server and the `h1:` hash and `zh:` hash of each archive (section D).
  A network mirror needs no service discovery, so the URL can carry the
  namespace. The CLI sends the credentials of the mirror host to the two
  JSON routes, so the credentials helper of the launcher already gives the
  job token there. The CLI sends no credentials for an archive, so each
  archive URL in the version document is a signed URL of the server that
  names one blob and expires after 10 minutes (proposal). The server checks
  the read action when it signs the URL, and the signature and the expiry
  when the CLI downloads.
- **OpenTofu providers: the provider registry protocol.** A registry
  serves the providers whose source address names its own host. So each
  `required_providers` block must change from `registry.opentofu.org/...` to
  the server host, and that changes the provider address in each state and
  lock file. Not recommended for mirrored providers. It fits the private
  providers of RFC 10.
- **OpenTofu modules: the module registry protocol (recommended).** Add
  `modules.v1` to the discovery document that the TFE-compatible API already
  serves. The module address is `<server host>/<namespace>/<name>/<system>`,
  so the registry namespace is the OpenDustpan namespace. The download
  answer names a signed archive URL on the server, never the upstream
  location, which can be a git URL (see the facts).
  A team changes its `source` lines once, to the server host.
- **Pulumi resource plugins: the plugin download URL (recommended).** Routes
  under `/pulumi/plugins/v1/{namespace}/` that serve the archive names that
  the Pulumi CLI asks for. The launcher points the CLI at them (section E).
- **Storage.** Each archive is one blob of a new kind `artifact`, addressed
  by its SHA-256 in the namespace, through the `blob` port (fs or S3). Two
  admissions of the same bytes share one blob. A namespace never reads a
  blob of another namespace (ADR 220).
- **Size limits (proposal).** 512 MiB for one archive, 2 GiB unpacked, 20,000
  entries in one archive, 16 platforms for one version. An installation
  setting can raise them. Large cloud providers are above 100 MiB for one
  platform, so the limit is on one archive, not on the sum.
- **Retention (proposal).** A vetted artifact stays while it is admitted. A
  revoked artifact keeps its record for ever, and its blobs while a job
  record or a state version in retention names it. Then the retention
  expiry deletes the blobs, and the record says so.

### C. The allowlist and the approval

An artifact admission is a change of the namespace, not of a unit. The
fixed decision points of ADR 224 do not cover it.

1. **A new decision point `artifact.admit` (recommended).** It runs before
   the server stores an admission. Its fact schema `artifact.admit/1.0`
   holds the kind, the upstream host, the address, the version, the age of
   the version upstream, the signature result (`verified`, `unsigned`,
   `unknown-key`, `not-checked` or `failed`), the number of platforms, and a `scan` group
   with an `available` flag for later (section H). Gates and approval rules
   work as for every other decision point: a gate with the outcome
   `approval:supply-chain` makes the admission wait for the approvers of
   that rule. A built-in starter gate asks for one approval on each
   admission, with `final: true` when the installation sets it. This needs
   a new ADR (ADR 224 decision 2).
2. **A fixed approval on the allowlist.** Each admission needs one approval
   by a holder of an action, with no gates. Simple, but no facts, no
   `evaluate` mode, no exemptions, and a second approval system, which
   ADR 222 replaced with one model.
3. **No approval, an action only.** A holder of the admit action adds an
   entry. This is the minimum for a small team, and it is a mode of option
   1: no gate at `artifact.admit` gives no requirement.

**Scope (proposal).** The allowlist belongs to a namespace. The installation
can hold admissions that each namespace inherits, as gates do. A namespace
can revoke for itself an artifact that the installation admits, and cannot
admit one that the installation revoked (question 3).

**Actions (proposal, ADR 17):**

| Action | Object | Allows |
| --- | --- | --- |
| `namespace:read-artifacts` | namespace | read the allowlist, the records and the hashes |
| `namespace:request-artifact` | namespace | ask for an admission (the gates of `artifact.admit` decide) |
| `namespace:revoke-artifact` | namespace | revoke a vetted artifact, with a fresh authentication and a reason |
| `namespace:download-artifacts` | namespace | the mirror routes (a job token of the namespace holds it) |
| `installation:manage-artifacts` | installation | the admissions and revocations of the installation |

A revocation needs no approval: it only takes away. It is in the audit log
with the reason.

### D. Verification, and the lock file gap

At admission, the server fetches **every platform** that the upstream
lists, or the set that the request names, not only the platform of the
caller. For each archive it:

1. checks the SHA-256 of the archive against the upstream checksums file,
   and the signature of that file against the upstream key where one
   exists (OpenTofu providers).
2. computes the `h1:` hash of the content (OpenTofu providers), in memory,
   from the archive, never from files on disk.
3. stores the archive, the checksums file, the signature, the key ID and
   the upstream URLs with the record.

The record also holds who asked, the decision record of the gates, each
signer of each approval, and the time.

**How the mirror fills the lock file gap.** The version document of the
mirror carries the `h1:` and `zh:` hashes of each admitted platform. A
mirror alone is not enough: the CLI records only the current platform from a
mirror (see the facts). So the launcher sets `trust_all_hashes = true` on the
`network_mirror` that it writes, and the documentation tells teams to do the
same for the mirror of their namespace and of no other host. Then one
`tofu init` on one platform writes a lock file that is valid on all admitted
platforms. A second route gives the lock file entries of a set of providers
directly (`GET .../namespaces/{namespace}/artifacts/lock-file`), so a team
can commit them with no `tofu init`. The documentation also names
`tofu providers lock -net-mirror=<url> -platform=...` for each platform.

**Signatures.** The standard library has no OpenPGP. A check of the
signature needs a dependency, which needs the owner's approval (question 4).
Without it, the record says `not-checked` and a gate can read it. An
upstream that gives no key (the CLI allows this for `registry.opentofu.org`
only, see the facts) gives `unsigned`.
The SHA-256 check and the approval still stand alone.

**Upstream provenance.** Where an upstream gives more (for example a build
attestation of a release), the record keeps it as an opaque blob, and a
later fact can read it. No check depends on it in the first slice.

**Pulumi resource plugins.** The Pulumi CLI checks no signature, and it
checks a SHA-256 only when the plugin spec has an entry for the current
platform (see the facts). So, as for OpenTofu, the server fetches the
archive of each platform at admission, keeps its SHA-256, and gives the
`checksums` map for each `packages` entry of `Pulumi.yaml` for every
admitted platform (`GET .../artifacts/pulumi-checksums`). A team commits it,
and the CLI then checks the bytes on each platform. The launcher does not
rely on it (layer 4).

### E. Enforcement at run time: five independent layers

Each layer stops a job that uses an artifact that is not vetted, without
help from the others (ADR 250).

1. **The CLI configuration.** For OpenTofu, the launcher writes a
   `provider_installation` block with one `network_mirror` to the mirror of
   the namespace and no `direct` method. For Pulumi, it sets
   `PULUMI_PLUGIN_DOWNLOAD_URL_OVERRIDES` with `.*` to the mirror routes,
   and `PULUMI_IGNORE_AMBIENT_PLUGINS=true`. It stops a fetch by the CLI
   from any other source. It cannot stop a binary that the image or a
   plugin cache already holds.
2. **The mirror.** It serves only admitted versions and platforms, with
   their hashes. It stops a version or a platform that nobody admitted. It
   cannot stop an artifact that the image brings.
3. **Egress.** The sandbox can reach only the server (ADR 137 decision 6,
   RFC 16). This stops a module `source` with a git URL, a download in a
   `local-exec`, and a provider that fetches its own plugin. It cannot stop
   a fetch from the server host itself.
4. **The job record.** After `tofu init`, or before `pulumi up`, the
   launcher reads the lock file, `.terraform/modules/modules.json` and the
   plugin directory. It reports each address, version and hash. The server
   compares them with the allowlist, and on a mismatch it fails the job
   before the plan. This stops a binary from the image or a cache, and a
   hash that is not the admitted one. It cannot stop a binary that the tool
   loads after the check.
5. **`state.write` (option).** The server refuses a state write whose
   providers are not on the allowlist. This stops a write from a team's own
   CI that used other providers, with the limits below.

The job record ties the exact addresses, versions and hashes to the plan,
to the apply, and to the state version or the Pulumi update that the job
writes (ADR 102). The console shows them on the state version.

**What the server can see in state (layer 5).** An OpenTofu state names
the provider address of each resource, for example
`provider["registry.opentofu.org/hashicorp/aws"]`, and, as far as we know,
no version and no hash. A Pulumi checkpoint holds a provider resource for
each provider, whose inputs are likely to hold the `version` and the
`pluginDownloadURL`. The e2e suite must check both with real state before
the ADR. So layer 5 can refuse an OpenTofu provider address that is not on
the allowlist, and probably a Pulumi plugin version that is not on it. It
cannot check a hash, or a module, because no state holds them.

**What a team's own CI can and cannot do.** The team can point its CLI at
the mirror (layer 1 and 2 in its own configuration) and receive the same hashes.
The server cannot see the binary that ran, the CLI configuration, or the
network of that CI. So for a write from a team's own CI, layers 1 to 4 are
voluntary, and only layer 5 is enforced, with the limits above. A security
team that needs all five layers runs its writes on runners of OpenDustpan,
and a gate at `state.write` can refuse writes that do not come from a job
(ADR 224 decision 16 already gives the facts for that).

### F. Untrusted input (ADR 250)

Archives, checksums files, signatures, keys and every upstream document are
untrusted input. So are the lock file, `modules.json` and the plugin list
that a job reports. The ADR that accepts this RFC adds them to the list of
ADR 250.

1. **Check at the boundary.**
   - An allowlist of upstream hosts. `https` only. No user info in a URL.
     No redirect to a host that is not on the list.
   - A version grammar (SemVer for providers and plugins). `os` and `arch`
     from a fixed list.
   - The server refuses a module `source` with a `git::` prefix, an `ssh`
     scheme, a user info part, or a query other than a 40-character commit.
   - The server refuses a JSON document over 1 MiB.
   - Test: one hostile value for each rule, with the fetch skipped.
2. **Encode or contain at use.**
   - The server reads an archive in memory, entry by entry, and never
     unpacks it to disk.
   - An entry name that is absolute, holds `..`, a backslash or a NUL, or
     that is a link, refuses the archive. A duplicate name refuses it too.
   - The server builds the archive URLs of the mirror from the stored
     record, never from the upstream text.
   - SQL placeholders only. The server deletes CR and LF from log values.
   - Test: a zip with `../x`, `/etc/x`, a link entry and a duplicate name,
     with the boundary check skipped.
3. **Limit the effect.**
   - `http.MaxBytesReader` and a timeout on each fetch.
   - Limits on archive size, unpacked size, entry count, and the ratio of
     unpacked to packed size (archive bombs).
   - The server checks the resolved IP of each connection against the
     always-denied ranges. A fetch uses no credential of the installation.
   - Test: a zip bomb, a 600 MiB archive and a slow upstream, each at its
     limit plus one.
4. **Authorize at use.**
   - The store checks the namespace of the admission and the action on it,
     in the same transaction as the write.
   - The mirror route checks `namespace:download-artifacts` for the
     namespace in the path and the namespace of the token. The archive
     route checks the signature and the expiry of the URL, and that the
     blob belongs to that namespace.
   - Test: a job token of another namespace on the mirror, and a signed URL
     of another namespace, with the route check skipped.
5. **Fail closed and record.**
   - A checksum mismatch, a bad signature, an unknown key, a parse error, a
     size limit or an upstream error refuses the admission.
   - A missing entry in the job record fails the job.
   - Each refusal writes an audit entry with the upstream host, the address
     and the reason, never the body.
   - Test: each failure gives an audit entry and no stored row.

A module needs one more rule: the server fetches a module archive only from
an upstream module registry, and never runs `git` for it in the first slice.
A module whose upstream answer points to a git URL needs a later fetcher in
a runner sandbox (question 7).

### G. Who fetches from upstream

1. **The server, with an upstream allowlist (recommended for the first
   slice).** Small, and the checks of section F hold. The server needs an
   outbound path, like the egress allowlist of model calls (ADR 129
   decision 25).
2. **A runner job of a new kind `artifact-fetch`.** The fetch and the
   parse run in a sandbox, and the server checks the SHA-256 of the uploaded
   bytes against a checksums file that the server fetches itself. Safer for
   a server with no outbound access, and more work. A later step.
3. **An upload by a person.** For an air gap: the person uploads the
   archives and the checksums file, and the server checks them as in
   option 1. It can come with either option.

### H. Later: scanning hooks

A scanner (for example `govulncheck -mode=binary` for Go providers, as
RFC 10 says, or a license scanner) reads a vetted artifact and gives a
report. The report uses the model of RFC 90: a scanner report blob with
scanner findings, bound to the vetted artifact instead of a plan. The
`scan` group of `artifact.admit` then reads its counts, so a gate can deny
an admission with a finding at or above a severity. A new scan of an
admitted artifact can flag it, and a person revokes it. No scan revokes by
itself in this RFC (question 9).

### I. The first slice

**Persona.** The security team of a bank runs OpenDustpan. Its platform
team asks for one community provider for OpenTofu in the namespace
`payments`. The security team approves that one version once. After that
every job in `payments` uses exactly those bytes on `linux_amd64` runners
and `darwin_arm64` laptops, and nothing else.

**Scope.**

- OpenTofu providers only. No modules, no Pulumi resource plugins.
- `pkg/artifact` with the record, the store port and its conformance suite
  in `pkg/artifact/artifacttest`, for SQLite and PostgreSQL.
- The decision point `artifact.admit` (after its ADR) and one approval rule.
- The server fetch from an allowlist of upstream hosts, with the checks of
  section F, the SHA-256 against the checksums file, and the `h1:` hash for
  each platform.
- The network mirror routes in `internal/api/tfe/`.
- Layer 1 in the launcher: the `provider_installation` block, with
  `trust_all_hashes = true`.
- The admission, list and revoke routes, and a user documentation page.

**The e2e step (real OpenTofu CLI).** A test upstream in the e2e suite
serves one provider that the suite builds, with a checksums file, for
`linux_amd64` and `darwin_arm64`. The step:

1. asks for the admission of version 1.0.0 and sees it wait for approval.
2. approves it as a second user, and sees the record with two platforms.
3. runs `tofu init` with a CLI configuration that names only the mirror,
   and checks that the lock file holds the `h1:` hash of both platforms.
4. changes the constraint to 1.1.0 (not admitted) and checks that
   `tofu init` fails.
5. serves a changed archive for 1.0.0 upstream, asks again, and checks that
   the admission fails on the checksum.
6. revokes 1.0.0 and checks that `tofu init` with an empty cache fails.

## Open questions

1. Are the names right: vetted artifact, artifact admission, artifact
   mirror? Or "approved provider" and a word for each tool?
2. Does `artifact.admit` become a seventh decision point (option C.1), or
   do admissions use a fixed approval (option C.2)?
3. Does the installation hold admissions that each namespace inherits, and
   can a namespace narrow them, as gates do?
4. Does the owner accept an OpenPGP dependency to check provider
   signatures? Which one, after its review (ADR 7)?
5. Does the server fetch from upstream (option G.1), or only a runner job
   (G.2), or both with an upload for an air gap (G.3)?
6. Is the job record (layer 4) a failure before the plan, or a warning in
   the first slice?
7. Modules: only from an upstream module registry, or also from git at a
   commit, fetched in a runner sandbox?
8. Is layer 5 at `state.write` worth its cost, given what state shows (see
   section E)?
9. Can a scanner report revoke an artifact by itself, or does a person
   always revoke?
10. A minimum age for a new upstream version before admission, as ADR 7
    does for our own dependencies: a fact for a gate, or a fixed rule?
11. Does the mirror sign what it serves again with a key of the namespace
    (RFC 10 asks the same)?
12. Does RFC 10 keep private publishing and the version lifecycle, and drop
    its curation part in favor of this RFC?

## Proposed glossary lines

**Vetted artifact**:
One version of an OpenTofu provider, an OpenTofu module or a Pulumi resource
plugin that a namespace admitted, with an archive and its hashes for each
platform, and the record of its admission.
_Avoid_: Approved provider, package, dependency

**Artifact admission**:
The change that adds a vetted artifact to the allowlist of a namespace. It
meets the gates of `artifact.admit`.
_Avoid_: Import, approval (alone)

**Artifact mirror**:
The routes that serve the vetted artifacts of a namespace through the
protocols of each tool: the provider network mirror and the module registry
for OpenTofu, and the plugin download for the Pulumi CLI.
_Avoid_: Registry (alone), proxy, cache

## Outcome

None yet.
