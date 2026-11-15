---
rfc: 93
title: A vetted supply chain for OpenTofu providers and modules and Pulumi resource plugins
status: discussing
created: 2026-10-20
revised: 2026-11-07
---

# RFC 93. A vetted supply chain for OpenTofu providers and modules and Pulumi resource plugins

Status: discussing. No code exists for it. The owner answers the questions at
the end (ADR 15).

**Revision of 2026-11-07.** On 7 November 2026 the owner decided that
OpenDustpan does not decide what provenance means (ADR 351). This revision
follows that decision. It keeps the facts from primary sources, the mirror,
the storage and the run-time layers. It cuts every built-in verdict about
trust: the installation gate that could not be lowered, and the rule that a
signature, an age or a scan result makes a package fit or unfit. In their
place the RFC defines the provenance signals as facts, the policy that an
administrator writes over them with the gates of ADR 222, and the guidelines
and example policies for each kind of organization. The sections "Owner
decision of 2026-11-07" to "One-way doors" are new. Section C, section D (the
parts about signatures, age and provenance), section H, the first slice, and
the questions changed. The section "Prior art: artifact firewalls" is new in this revision: it records how JFrog, Sonatype, Cloudsmith, GitHub, Socket and others, and the Go module ecosystem, set signals, policies and waivers, and the example policies follow from it. The earlier proposal of 2026-10-30 is kept as history
where it still holds.

## Owner decision of 2026-11-07: signals, not verdicts

The owner's words:

> We don't decide. We can provide guidelines, but at the end of the day admins
> need to decide what provenance means to their organization (which
> vendors/github/gitlab orgs, registries or individual provider (versions)
> they trust, if GitHub stars and repository age and last commit time, means
> anything to them, what govulncheck means to them, and if they want any
> additional external scanners). These are just examples, not an exhaustive
> list. Provenance means one thing to an advertising startup, than it does to
> a medical company which has an ISO regulated SOUP process.

The design follows from it:

1. **OpenDustpan computes and shows signals.** A signal is a fact about a
   package: who published it, from which registry, how it is signed, how old
   the version and the repository are, what a scan found. The server shows
   each signal with its origin and the time of its read.
2. **The administrator writes the policy.** The policy is a gate over those
   facts (ADR 222 to 230), at the installation, the namespace or the unit.
   OpenDustpan has no built-in answer to "is this package trusted".
3. **OpenDustpan ships guidelines, not a verdict.** The documentation and the
   example policies for three kinds of organization show what a policy can
   read. They are data that an administrator copies and edits.
4. **No magic.** Each admission and each refusal names the policy that
   decided and the facts that it read. A signal from outside the installation
   is an unverified fact, and the documentation says which signals an
   attacker can fake.

What stays in code is **integrity**, not provenance. The server serves the
bytes that it admitted and no other bytes. It checks each hash, each size
limit and each signed URL, and it checks a signature when an administrator
asks for it. Integrity answers "are these the bytes that a person admitted".
Provenance answers "should a person admit them". Only the first is code.

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
on every job on an OpenDustpan runner uses exactly that artifact, on every
platform, and nothing else.

**What this RFC does not promise.** Five layers work toward the goal (section
E). Each layer is built to stop the attack alone (ADR 250). All five hold
together only for a job on an OpenDustpan runner whose pool has egress
turned on (egress is an option of a pool, ADR 137 decision 6). On a laptop
or in a team's own CI, the server enforces nothing at run time. The
artifacts that this RFC does not cover are:

- the language SDK packages of a Pulumi program (npm, PyPI, Go modules,
  NuGet, Maven),
- the `tofu` and `pulumi` binaries, and the runner image,
- provisioners (`local-exec`, `remote-exec`) and the `external` data source,
  which run any program,
- Pulumi policy packs.

**Relation to RFC 10.** RFC 10 (draft) asks for an "artifact registry" with
curated upstreams, private publishing and scanning. This RFC takes the
curation part of RFC 10 and makes it concrete: the mirror, the approval, the
hashes and the enforcement. Private publishing (a team uploads its own
provider) and the version lifecycle stay in RFC 10 (question 12).

**Names (proposal, question 1).** The glossary has _Provider_ on the _Avoid_
list (it is a provider block or a profile provider), and its entry for
_Profile provider_ says that a provider alone can mean a plugin of OpenTofu
or the Pulumi CLI. So this RFC writes "OpenTofu provider" in full, and
"Pulumi resource plugin". "Artifact" also names a plan or a log of a job
(ADR 152, "job artifacts"), and RFC 10 uses "artifact registry". To avoid a
clash, the proposal uses a different word:

- **Vetted package**: one version of an OpenTofu provider, an OpenTofu
  module or a Pulumi resource plugin that a namespace admitted, with its
  archives and hashes for each platform.
- **Package admission**: the change that adds a vetted package to the
  allowlist of a namespace. It meets the gates and approval rules.
- **Package mirror**: the routes that serve vetted packages to the tools,
  through the protocols that the tools already speak.

The rest of this RFC uses these names. The identifiers of the sketch use
`package` (for example the decision point `package.admit`). Question 1 asks
if the owner wants other names. RFC 10 would then need the same word.

## Facts from primary sources

Checked on 2026-10-20 against the docs, and against the source at OpenTofu
`main` 42f15c9b and Pulumi `master` 6c60436a where the docs say less. The
file names and the line numbers are from that check. The lines move, so the
ADR checks them again when it is written.

Open item: the line numbers of this section are not checked again at the
pinned upstream commits. The check needs a read of the upstream repositories.
The owner decides which access the check needs. Until the check is
done, a line number in this section is a claim from the earlier review.

**OpenTofu provider network mirror protocol**
(<https://opentofu.org/docs/internals/provider-network-mirror-protocol/>,
`internal/getproviders/http_mirror_source.go`)

- `GET :hostname/:namespace/:type/index.json` gives `{"versions":
  {"1.0.0": {}}}`. A 404 means that the mirror does not have the provider.
- `GET :hostname/:namespace/:type/:version.json` gives `{"archives":
  {"<os>_<arch>": {"url": ..., "hashes": [...]}}}`. `url` can be relative
  to the document. `hashes` is optional, and with no hashes the CLI
  installs the package with no check. The hashes can use the `zh:` and the
  `h1:` scheme.
- No service discovery: the URL comes from `network_mirror { url }`, and it
  must be `https:`.
- The CLI sends the credentials of the mirror host on the two JSON requests
  (`index.json` and the version document), and it forwards them on a
  redirect (`http_mirror_source.go:306-331`). It sends no credentials for an
  archive download. The docs advise a user-specific URL that expires for the
  archives.
- New and not yet released in 1.14: an archive entry can carry
  `"use_mirror_credentials": true`, and then the CLI sends the credentials of
  the mirror host for that archive too (`http_mirror_source.go:179` and
  `:224`). Until a release has it, an archive URL must carry its own proof.

**OpenTofu provider registry protocol**
(<https://opentofu.org/docs/internals/provider-registry-protocol/>)

- Discovery maps `providers.v1` in `/.well-known/terraform.json`.
- `GET :namespace/:type/:version/download/:os/:arch` gives `download_url`,
  `shasums_url`, `shasums_signature_url`, `shasum` and
  `signing_keys.gpg_public_keys`.
- The CLI checks the SHA-256 of the zip against `shasum` and the checksums
  file, then the detached GPG signature of that file
  (`internal/getproviders/package_authentication.go`).
- For the upstream fetch of the server, `download_url` and `shasums_url`
  often point to `github.com` and then to a release-asset CDN host, so the
  fetch crosses several hosts (section G).

**Signing state of OpenTofu** (<https://opentofu.org/docs/cli/plugins/signing/>)

- For `registry.opentofu.org` only, when the registry gives no key, the CLI
  skips the GPG check and logs a WARN. Every other registry host needs a
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
  except another registry address. Most public modules use
  `git::https://github.com/...?ref=...` as their source.

**CLI configuration** (<https://opentofu.org/docs/cli/config/config-file/>)

- `provider_installation` holds `direct`, `filesystem_mirror`,
  `network_mirror` (`url`, `include`, `exclude`, `trust_all_hashes`) and
  `oci_mirror`. With no `direct` method, a provider that no method
  matches fails with "was not found in any of the search locations".
- `TF_CLI_CONFIG_FILE` names the file. We found no `TOFU_CLI_CONFIG_FILE`.
- `plugin_cache_dir` shares provider packages between runs.
  `plugin_cache_may_break_dependency_lock_file` lets the cache skip the lock
  file check.

**Dependency lock file**
(<https://opentofu.org/docs/language/files/dependency-lock/>,
<https://opentofu.org/docs/cli/commands/providers/lock/>)

- `zh:` is the SHA-256 of the official zip. `h1:` is a hash of the content
  of the package, so it also matches an unpacked copy.
- From a registry, the CLI records each checksum that the registry gives,
  which usually covers every platform. **From a mirror, it records only the
  current platform.** This is the multi-platform trap.
- `trust_all_hashes = true` (from 1.12) records the hashes of all platforms
  of the mirror, but only after one hash matches the package that the CLI
  installed, and only when the source does not need a signature. The source
  needs one for `registry.opentofu.org` when
  `OPENTOFU_ENFORCE_GPG_VALIDATION` is set, and for every other registry.
  So the flag helps in a plain mirror setup and is not a guarantee.
- `tofu providers lock -platform=linux_amd64 -platform=darwin_arm64` records
  the hashes of each named platform. `-net-mirror=<url>` takes them from a
  mirror instead.
- Modules are not in the lock file. The state holds no provider version and
  no provider hash.

**Pulumi CLI plugins** (`sdk/go/common/workspace/plugins.go`,
<https://www.pulumi.com/docs/iac/cli/environment-variables/>,
<https://www.pulumi.com/docs/iac/concepts/projects/project-file/>)

- `pluginDownloadURL` accepts `https://`, `http://`, `github://`,
  `gitlab://` and `git://`. With none, the CLI tries GitHub and then
  `get.pulumi.com`.
- An HTTP source downloads
  `<url>/pulumi-<kind>-<name>-v<version>-<os>-<arch>.tar.gz`. An HTTPS
  download sends a `User-Agent` and no other header. It has no credentials
  (`plugins.go:763-781` builds the URL of the HTTP source, `:1669-1686`
  sends the request). With `-v=1`, the CLI logs the full override URL
  (`plugins.go:771`), and the text of a download error holds the URL
  (`plugins.go:1745`).
- `PULUMI_PLUGIN_DOWNLOAD_URL_OVERRIDES` is a list of `regexp=URL` pairs.
  The CLI splits the list on `,` and each pair on `=`, so a URL with an `=`
  or a `,` breaks the pair (`plugins.go:131-150`). A bad pair panics at
  `:126`. An override also applies to a
  `pluginDownloadURL` and to the `get.pulumi.com` fallback.
- `PluginSpec.Checksums` maps `<goos>-<goarch>` to a SHA-256. **When no entry
  exists for the current platform, the CLI skips the check with no
  warning** (`plugins.go:934-941`). `pulumi plugin install --checksum` sets one platform only.
  The `packages` entries of `Pulumi.yaml` accept `checksums`.
- The CLI checks no signature of a plugin.
- `PULUMI_IGNORE_AMBIENT_PLUGINS` stops the search of `$PATH`, and
  `PULUMI_DISABLE_AUTOMATIC_PLUGIN_ACQUISITION` stops downloads.
- A provider resource in a Pulumi checkpoint stores the `version`, the
  `pluginDownloadURL` and the `pluginChecksums` in its inputs. We did not
  find the doc page of the `pluginDownloadURL` resource option. The behavior
  above is from the source.

## What is decided already

- ADR 12: each tool's wire protocol lives in its own adapter under
  `internal/api/<tool>/`. Shared packages stay tool-neutral. A format that
  only one tool reads (a lock file, a checksums map) is built in the adapter
  of that tool.
- ADR 13: cross-tool features do not merge tool models. Shared code names a
  unit by tool, namespace and the tool's own name.
- ADR 17: every feature has a REST API, and every route declares its
  actions and objects. The project rules in `CLAUDE.md` add that a route
  without an action fails the route test, and that a route with no sign-in
  goes on the explicit public list of that test.
- ADR 210: the tool-neutral REST routes sit under `/api/dustpan/v1/`
  (decision 1). A protocol route that a tool stores stays outside that
  prefix (decision 2). No path holds the word `server` (decision 3). An
  action name has the form `<area>:<verb>` (decision 7).
- ADR 220: a namespace never reads a blob of another namespace.
- ADR 224 decision 2: six fixed decision points. A new decision point is a
  new ADR. Decision 16: `state.write` runs at the lock or at the create of
  an update, before the state is written, for a write that no job of
  OpenDustpan makes.
- ADR 225: approval rules are data, with approvers, a count and
  `authorMayApprove`. Decision 7 lists the approve actions.
- ADR 226 decision 3: a fact that a producer can fail to give is a typed
  value with an `available` flag.
- ADR 134 decision 7: the launcher writes the OpenTofu CLI configuration of
  each tool step (`TF_CLI_CONFIG_FILE`) and the Pulumi home (`PULUMI_HOME`).
  The CLI configuration names the credentials helper and nothing else. The
  credentials helper answers the job token for the host of the server only.
- ADR 129 decision 25 and `pkg/egress`: an egress allowlist and an HTTP
  client that checks the resolved IP address of each connection.
- ADR 137 decision 6: a pool can limit the egress of a sandbox to the server
  and to the hosts that the tools need, through the egress proxy of RFC 16.
  Egress is an option of the pool.
- ADR 152: "job artifacts" are plans and logs. They are a different thing.
- ADR 250: every layer stops the attack alone, and has its own test.

## Options and recommendations

### A. Where the code lives (ADR 12)

1. **One shared core, one adapter per tool (recommended).** A new domain
   package `pkg/pkgmirror` holds the tool-neutral parts: the record of a
   vetted package (kind, upstream host, address, version, platforms, a
   SHA-256 for each archive, the admission record), the allowlist queries,
   and the store and blob ports. The kind is one of `opentofu-provider`,
   `opentofu-module` and `pulumi-plugin`. Each tool's address grammar, its
   upstream protocol and its hash scheme (`h1:` is an OpenTofu scheme) live
   in a port `pkgmirror.Upstream` with one adapter for each kind. The wire
   routes and the tool formats live in the adapter of the tool:
   `internal/api/tfe/` for the provider network mirror, the module registry
   and the lock file output, and `internal/api/pulumi/` for the plugin
   downloads and the `checksums` output. The shared core never builds a
   lock file or a `checksums` map.
2. **One model in each tool package.** `workspace` holds OpenTofu providers
   and modules, `stack` holds Pulumi resource plugins. No shared allowlist,
   so the console needs two views, and the approval and storage code is
   written twice.
3. **A separate registry service** (RFC 10, option 1, the upstream proxy).
   More moving parts for the first slice. It can come later as a role of the
   binary (RFC 7) on the same port.

The address and the version stay as the tool writes them. The core compares
them as opaque strings after the adapter checks their grammar (section F).
The key of a record is the kind, the upstream host, the address and the
version. A request for a key that has a record with different hashes is
refused (section D).

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
  archive URL in the version document is a signed URL (see below). A later
  release of the CLI can send the credentials for an archive with
  `use_mirror_credentials`. The server can then drop the signed URL for a
  CLI that has it. Until then the signed URL is the proof. The CLI forwards
  credentials on a redirect, so the mirror routes never redirect.
- **OpenTofu providers: the provider registry protocol.** A registry
  serves the providers whose source address names its own host. So each
  `required_providers` block must change from `registry.opentofu.org/...` to
  the server host, and that changes the provider address in each state and
  lock file. Not recommended for mirrored providers. It fits the private
  providers of RFC 10.
- **OpenTofu modules: the module registry protocol (recommended for
  registry modules).** Add `modules.v1` to the discovery document that the
  TFE-compatible API already serves. See "Module address" below. The
  download answer names a signed archive URL on the server, never the
  upstream location. Modules wait on the decision of question 7 (section F).
- **Pulumi resource plugins: the plugin download URL (recommended).** Routes
  under `/pulumi/plugins/v1/` that serve the archive names that the Pulumi
  CLI asks for. The launcher points the CLI at them (section E). See "Pulumi
  mirror proof" below.
- **Storage.** Each archive is one blob of a new kind `package`, addressed
  by its SHA-256, through the `blob` port (fs or S3). Two admissions of the
  same bytes in one namespace share one blob. Each namespace has its own
  copy, sealed under its own key. See "Who owns a blob".
- **Size limits (proposal).** 512 MiB for one archive, 2 GiB unpacked, 20,000
  entries in one archive, 16 platforms for one version. An installation
  setting can raise them. Large cloud providers are above 100 MiB for one
  platform, so the limit is on one archive, not on the sum. The server never
  holds an archive in memory. See section F, step 3.
- **Retention (proposal).** A vetted package stays while it is admitted. A
  revoked package keeps its record for ever. Its blobs stay while a job
  record or a state version in retention names it, and then the retention
  expiry deletes the blobs, and the record says so. A kept blob of a revoked
  package is never served by any route. Only the audit and forensic read
  (`namespace:read-packages`) opens it, and that read gives the bytes with no
  signed URL.
- **Pending storage (proposal).** The fetch runs before the approval
  (section D). So a holder of `namespace:request-package` could fill the
  storage before a person approves anything. One request can store up to 16
  platforms of 512 MiB each. Each namespace therefore has a quota for the
  bytes of its pending blobs. The server refuses a request that would pass
  the quota and writes an audit entry. The quota value is question 13.
- **Retention of refused and expired requests (proposal).** The server
  deletes the blobs of a refused request when it records the refusal. The
  server deletes the blobs of an expired request at its expiry, and the
  record says so. The request expiry period is question 13.

**Routes that ADR 210 needs (note for the future ADR).** The two protocol
routes sit outside `/api/dustpan/v1`, because the tools store them (ADR 210
decision 2). The ADR that accepts this RFC amends ADR 210 decision 2 with
two rows:

- `/tofu/mirror/v1/{namespace}/...`, stored in the OpenTofu CLI
  configuration as the `network_mirror` URL.
- `/pulumi/plugins/v1/cap/{capability}`, stored in the environment that the
  launcher gives the Pulumi CLI (`PULUMI_PLUGIN_DOWNLOAD_URL_OVERRIDES`).

The rotation route, the lock-file route, the Pulumi checksums route and the
admission, list and revoke routes are under `/api/dustpan/v1`. They do not
change decision 2 (see sections B, C and D).

**The package signing key.** Signed archive URLs and the Pulumi capability
(see "Pulumi mirror proof") use one MAC key. The bullets below describe it:

- One random 32-byte value, sealed in the scope `@server` (ADR 23). It is not
  a secret item, and no route lists or answers it. The server makes it at
  start, before it serves a route: an insert with an on-conflict clause that
  does nothing, and then a read of the row, so two instances that start at
  the same time make one key. The key is in the table list of a backup
  (ADR 30), sealed.
- It is a separate row with its own random value, not a value derived from
  the conversation HMAC key (recommended): a leak or a rotation of one key
  never touches the other. The alternative is one master value and a key
  for each label (`pkgmirror/url/v1`, `pkgmirror/cap/v1`, the conversation
  label), derived with HMAC-SHA256. That has less storage and links the
  lifetime of the three uses. Each use also puts its label in the MAC input,
  so a signed URL is never valid as a capability.
- Each key has a **key ID** (one byte, in the table). Each capability and
  each signed URL holds the key ID. The server picks the key by that ID. An
  unknown ID gives 404 and an audit entry.
- **Rotation.** `POST /api/dustpan/v1/package-keys/rotate` (action
  `installation:manage-packages`, a fresh authentication, a reason, an audit
  entry) makes a new key with the next ID. New values use the new key. The
  server accepts the old key for the longest lifetime that a value can have
  (the longest phase deadline, see below, or 10 minutes for a URL), and then
  deletes it. A job that runs during a rotation keeps its capability, which
  stays valid until its phase deadline, and receives a capability with the
  new key at its next phase. A rotation after a compromise can ask to delete the
  old key at once. Every value with that key then fails (404), a running
  phase fails closed at its next download, and the job retries with a new
  value at its next phase.
- **Restore.** A restore brings back the keys of the backup. A value that
  the server signed with a key that is not in the backup fails (404), so the
  job step that holds it fails closed and the next phase receives a value that
  the restored key signs. No route rotates the key in the first slice
  except this one.
  A deleted key is a risk here. A restore of an older backup brings the
  deleted key back, and its values could become valid again. So the
  proposal keeps a denylist of the deleted key IDs and of the fingerprints
  of the deleted keys. The denylist is not in the table list of a backup
  (ADR 30), so a restore does not replace it. A value with a denied key
  fails (404) and writes an audit entry. A key ID is one byte. A new key
  could receive the ID of a denied key, so an ID alone is not safe. The
  fingerprint does not have that problem. A test restores a backup that
  holds a deleted key and checks the 404. The alternative is to rotate the
  key at each restore, with the delete-at-once rule for the key of the
  backup.

**Signed archive URLs.** A signed URL binds the key ID, the namespace, the
ID of the record, the platform, the SHA-256 of the archive and the expiry.
It expires after 10 minutes (proposal). The archive route:

1. refuses a URL that the server did not build. It recomputes the MAC over
   the fields with the key of the key ID, and it reads the record by ID **in
   the effective allowlist of the namespace of the URL** (its own records and
   the inherited records that the namespace did not revoke, see "Scope" in
   section C). The namespace, the platform and the SHA-256 of the URL must
   equal the record. A URL with other fields, an extra query key or a bad MAC
   gives 404 and an audit entry.
2. checks, on each download, that the record is still admitted and not
   revoked. So a revocation acts at once, also for a URL that has not yet
   expired.
3. serves the blob that the record names, and no other.

The server deletes the query of a signed URL from its access log and from
its traces (the route pattern is logged, not the URL). The URL is not a
secret that outlives its expiry, but it is a bearer token until then.

**Who owns a blob (ADR 220 decisions 7 to 9 and 20, ADR 217).** ADR 220 puts
each blob of a namespace under `namespaces/<namespace ID>/`, gives a domain
service of a namespace a view that refuses each other key, and seals each
blob under a key of its namespace. An installation admission has no
namespace, and a namespace must not read a blob of another. Two options:

1. **A copy for each namespace (recommended).** The installation admission
   fetches the archives into a staging prefix of the installation. Only the
   admission service reads that prefix, through an installation view. No view
   of a namespace reaches it. When a namespace first uses the record, the
   server copies each archive into the prefix of that namespace
   (`namespaces/<namespace ID>/packages/<SHA-256>`), seals it under the key of
   that namespace (ADR 217), and checks the SHA-256 of the copy against the
   record while it streams. The copy starts at the first request of the
   version document of the record (a route that has already checked
   `namespace:download-packages`), which answers 503 with `Retry-After` until
   the platforms that it lists are copied. So the archive route only reads
   and the route never takes a blob address from the request. A namespace
   admission fetches directly into the prefix of its namespace. Cost: one
   copy for each namespace that uses the package. This needs no change of
   ADR 220 or ADR 217. The ADR checks that the installation staging prefix
   fits ADR 220 decision 7 (it holds only data that no namespace view
   reads), and it keeps a test that a view of a namespace refuses the staging
   key (decision 20). A revocation of the installation record stops service
   at once, because the effective allowlist no longer holds it. The copies
   stay until the retention of section B deletes them.
2. **One shared blob under the installation prefix.** The blob stays under
   an installation prefix, sealed under an installation key. A namespace
   reads it through a new read-only installation view that refuses writes and
   every key but `installation/packages/<SHA-256>`. This needs one copy only,
   and it amends ADR 220 decisions 7 to 9: a second, read-only view and a
   blob that no namespace key seals. ADR 217 would name the key. The
   `blobtest` suite adds a case for the read-only view (it refuses a write, a
   list and a key outside the prefix), and decision 20 adds a test that a
   namespace reads only a key of the effective allowlist. The route finds the
   record in the effective allowlist of the namespace of the proof, then
   reads the blob from the installation.

Option 1 is the recommendation: it keeps the isolation rule of ADR 220
whole, at the price of storage.

**Module address.** The module registry protocol has an address with three
parts, `<namespace>/<name>/<system>`. The upstream address of a module is
also `<namespace>/<name>/<system>`, for example
`terraform-aws-modules/vpc/aws`. If the first part were the OpenDustpan
namespace, an upstream module and a module of another upstream namespace
could end up with the same name. So the proposal keeps the upstream address
whole: the module address is `<server host>/<upstream namespace>/<name>/
<system>`, and the OpenDustpan namespace comes from the token of the job or
the person (the same as for the provider routes of a job). Then
`terraform-aws-modules/vpc/aws` and `other/vpc/aws` stay two modules. A
module with the same three parts on two upstream hosts is refused at the
second admission (a conflict), because the address has no room for the host.
The alternative is a module address that holds the OpenDustpan namespace and
a joined name, and the grammar of a module name makes a safe join hard.

**Pulumi mirror proof.** The Pulumi CLI sends no credentials to a plugin
URL, and the override list splits on `=` and on `,`, so a token in the query
(`?token=...`) breaks the pair. Two options:

1. **A capability in a path segment (recommended).** The launcher sets
   `PULUMI_PLUGIN_DOWNLOAD_URL_OVERRIDES=.*=https://<server>/pulumi/plugins/v1/cap/<cap>`.
   The `<cap>` is a MAC-signed value in base64url, with no `=` and no `,`.
   It holds the key ID (see "The package signing key"), one job, one phase
   or tool step, one namespace and an expiry. The launcher asks for a new
   capability at **each phase or tool step**, and the expiry is the deadline
   of that phase, not the end of the job. At use the server checks the MAC,
   the expiry, that the job is still running, that the namespace of the job
   record equals the namespace of the capability, and then the record of each
   package in the effective allowlist of that namespace, as for a signed URL.
   A copy of the value gives the same read as that namespace has until the
   phase deadline, and no more. If an actor replays it in another job of the
   same namespace, the reader receives that namespace's allowlist and nothing
   else, so a replay across jobs of one namespace gives no extra right.
   A replay needs a running job: the server refuses a capability when its
   job is no longer running.
2. **Public routes keyed by hash.** The routes have no proof, and the path
   holds the SHA-256 of the archive. The CLI asks for a file name, not a
   hash, so the file name route needs the version and the platform, and the
   route cannot tell which namespace asks. It would also serve one
   namespace's admitted package to anyone who knows the name. Not
   recommended.

**Where the capability can leak, and what the proposal does.** The segment
is part of a URL that the CLI treats as public, so it can appear in more
places than a header would:

- *The CLI logs.* With `-v=1` the CLI logs the full override URL
  (`plugins.go:771`), and the text of a download error holds the URL
  (`plugins.go:1745`). The `RedactURL` function of the CLI does not delete a
  path segment. The launcher adds the capability to the secrets that the log
  of the job redacts, before it uploads a line. The capability stops working
  at the phase deadline and when the job ends.
- *The update events.* The CLI sends its diagnostics, with that text, to
  the update events of the server (the Pulumi Cloud-compatible API). So the
  capability could come back to the server in an event, and then into the
  stored events that a console reader sees. The server **redacts the
  segment from incoming update events** before it stores them (recommended):
  any text that matches `/pulumi/plugins/v1/cap/<base64url>`, and the exact
  capability of the running job. The alternative is to accept the stored
  capability, which is expired when a reader sees it, and to keep only the
  test below. Redaction is cheap, so the proposal prefers it.
- *Log files of the CLI in the sandbox.* The CLI can write a log file in its
  temporary directory. The launcher sets a temporary directory for each tool
  step, deletes it at the end of the step, and never collects it as a job
  artifact.
- *OpenTofu signed URLs in `TF_LOG`.* An OpenTofu archive URL can appear in
  the debug log of the CLI. The proposal accepts this: the URL lives 10
  minutes, the route checks the revocation on each download, and the URL
  names one record in one namespace. The launcher redacts it from the job
  log as well.

Test (e2e, with the real Pulumi CLI at a high verbosity and a forced
download error): the capability value is **absent** from the stored
checkpoint, from the plan, from the job log and from the stored update
events. Other tests: a capability of a finished job, of an expired phase, of
a job of another namespace (the namespace of the job record differs from the
capability), with a key ID that was deleted by a rotation, and with a bad
MAC, each with the other checks skipped.

With option 1 the `cap` routes, and the signed archive routes of the OpenTofu
mirror, carry their own proof and no bearer header. They go on the explicit
public list of the route test (ADR 17), each with a comment that names its
proof. A new test asserts that the list holds these routes only, and that
each one rejects a request with no valid proof.

**Slice 1 does not include a Pulumi plugin mirror.** This section is the
design for the later slice.

### C. The allowlist and the approval

A package admission is a change of the namespace (or of the installation),
not of a unit. The six fixed decision points of ADR 224 do not cover it.

1. **A new decision point `package.admit` (recommended).** It runs before
   the server stores an admission. Gates and approval rules work as for
   every other decision point: a gate with the outcome
   `approval:<rule id>` makes the admission wait for the approvers of that
   rule. The fact schema `package.admit/1.0` holds the groups of section
   "The provenance signals", each with an `available` flag as ADR 226
   decision 3 asks. The administrator writes the gates (section "Policy").
   The RFC does not write a gate for the administrator.

   **Amendments that option C.1 makes.** The ADR that accepts C.1 changes:
   - ADR 224 decision 2: a seventh decision point, `package.admit`.
   - ADR 224 decision 4: the Cedar request has the namespace, or the
     installation, as its resource. It has no unit. One admission gives one
     request and one decision record.
   - ADR 224 decision 5: the gates that apply are the gates of the
     installation and of the namespace. A gate for `package.admit` at the
     scope of a collection or a unit is refused at save (`gate.ErrInvalid`),
     because no collection or unit is in the request. A gate at the scope of
     a unit decides the **use** of a package, see "Policy at a unit".
   - ADR 224 decisions 13 and 14 apply with the namespace or the installation
     as the one resource: the combining runs once for it, and an error of a
     gate stops the admission (`gate.ErrRuleFailed`).
   - ADR 229 decision 1: the record "for the change" is a record for the
     admission. It names the admission request in place of a job or change
     order, and it holds one resource entry (the namespace or the
     installation) in place of one entry for each unit. It holds the whole
     fact document, so the record is the evidence of an approved item.
   - ADR 225 decision 1: a new `approvers` entry, `holders`: the persons who
     hold the approve action of the resource of the request. The reserved
     list of rule IDs adds `package-admission`.
   - ADR 225 decision 7: two approve actions, `namespace:approve-package`
     and `installation:approve-package`, with the same rules for who may
     approve.
   - ADR 226: the new schema `package.admit/1.0`, with its own version line.
   - ADR 230 decision 2: **no new row**. The rows
     `installation:manage-gates` and `namespace:manage-gates` already cover
     the gates and approval rules of every decision point at their scope, so
     they cover the gates and the rules of `package.admit`.

   **No built-in verdict.** The earlier proposal had a built-in gate with
   `final: true` that no narrower scope could lower. The owner's decision
   removes it, because a final gate is a built-in answer to "what is
   acceptable". The server keeps only the four rules below. They say who
   decides when no policy decides, and they are not a view on provenance.
   Each rule has its own test (proposal names):
   - **No policy does not mean allowed.** An admission that finds no gate
     that decides it does not admit. It waits for a person who holds the
     approve action, and the decision record says "no policy matched". The
     code never reads "no gate" as "allowed" (ADR 250). Test:
     `TestPackageAdmitNoGateWaitsForAPerson`.
   - **Managing gates is not approving.** A requester who holds
     `namespace:manage-gates` can save a gate for the namespace. The
     requester cannot approve the own admission by default, because the
     approve action is separate, and a job token, a service principal, an
     access token or an agent never approves (ADR 225 decision 7). The
     installation setting `packageAdmission.authorMayApprove` has the
     default `false`, and the API shows its effective value. A small team may
     set it to `true`. That change is a sensitive setting: it needs
     `installation:manage-gates`, a fresh authentication (ADR 227), a reason
     and an audit entry, and the console warns (ADR 347). Test:
     `TestPackageAdmitRequesterWithManageGatesCannotApprove`.
   - **No self-admit.** The route for an admission has no way to set the
     state to admitted. Only the engine, after the decision record, writes
     it. Test: `TestPackageAdmitRouteCannotSetAdmitted`.
   - **A namespace grant does not reach the installation.** The approvers of
     a rule resolve the approve action at the scope of the rule. A grant of
     `namespace:approve-package` serves the rules of that namespace and no
     rule of the installation. Test:
     `TestPackageAdmitNamespaceGrantDoesNotApproveInstallationRule`.

   **Approvers: the choice.** `holders` is a new entry kind, and a rule
   with it needs the amendment of ADR 225 decision 1 above (recommended). It
   names no group that an installation must create, so a new installation
   has approvers as soon as one person holds the approve action. The
   alternative is a default group `package-approvers` that the installation
   creates, empty. It needs no new entry kind. But with an empty group no
   admission can pass, so a new installation is stuck until an owner finds
   the group and fills it. Not recommended.
2. **A fixed approval on the allowlist.** Each admission needs one approval
   by a holder of an action, with no gates. Simple, but no facts, no
   `evaluate` mode, no exemptions, and a second approval system, which
   ADR 222 replaced with one model.
3. **No approval, an action only.** A holder of the admit action adds an
   entry. This is the mode of a small team. It is not a mode of option 1
   with a hidden default, because option 1 never reads "no gate" as
   "allowed". The administrator picks it with a gate that returns `allow`
   for the requesters that the administrator names. That is a policy
   written by a person, with a record. It needs `namespace:manage-gates` or
   `installation:manage-gates`, and the starter pack "Startup" holds it as an
   example (section "Policy").

**Scope (proposal).** The allowlist belongs to a namespace. The installation
can hold admissions that each namespace inherits, as gates do. A namespace
can revoke for itself a package that the installation admits, and cannot
admit one that the installation revoked (question 3).

**Actions (proposal, ADR 17):**

| Action | Object | Allows |
| --- | --- | --- |
| `namespace:read-packages` | namespace | read the allowlist, the records and the hashes |
| `namespace:request-package` | namespace | ask for an admission (the gates of `package.admit` decide) |
| `namespace:approve-package` | namespace | approve an admission of the namespace as an eligible approver (ADR 225 decision 7) |
| `installation:approve-package` | installation | approve an admission of the installation as an eligible approver (ADR 225 decision 7) |
| `namespace:revoke-package` | namespace | revoke a vetted package, with a fresh authentication and a reason |
| `namespace:download-packages` | namespace | the mirror routes, the lock-file route and the Pulumi checksums route. A job token of the namespace holds it. The token of a person (a laptop, a team CI) needs it as well |
| `installation:manage-packages` | installation | request and revoke the admissions of the installation, and rotate the package signing key. It does not change gates or rules (that needs `installation:manage-gates`), so a person who manages admissions cannot change the policy that governs them |

A revocation needs no approval: it only takes away. It is in the audit log
with the reason.

**Routes (proposal, ADR 210).** The admission, list and revoke routes sit
under `/api/dustpan/v1`:

- `POST /api/dustpan/v1/namespaces/{namespace}/package-admissions` asks for an
  admission (`namespace:request-package`).
- `GET /api/dustpan/v1/namespaces/{namespace}/packages` lists the vetted
  packages and their records (`namespace:read-packages`).
- `POST /api/dustpan/v1/namespaces/{namespace}/packages/{id}/revoke` revokes a
  vetted package (`namespace:revoke-package`).
- `POST /api/dustpan/v1/package-admissions` asks for an admission of the
  installation (`installation:manage-packages`).

This RFC does not name the route that approves an admission.

### D. Verification, and the lock file gap

**The server fetches once, before the approval.** The request for an
admission starts the fetch. The server fetches **every platform** that the
upstream lists, or the set that the request names, not only the platform of
the caller, and it stores the bytes at once. Then the approval wait starts.
For each archive the server:

1. checks the SHA-256 of the archive against the upstream checksums file,
   and, when the signature check is available, the signature of that file
   against the upstream key (OpenTofu providers).
2. computes the `h1:` hash of the content (OpenTofu providers) from the
   stored archive, entry by entry, never from files on disk.
3. stores the archive, the checksums file, the signature, the ID and the
   fingerprint of the key, and the upstream URLs with the record.

The record holds the SHA-256 of each stored archive, for each platform. The
decision record and each signature of an approval cover that list of
SHA-256 values. This closes the time-of-check to time-of-use gap:

- The server never fetches again after the approval. The mirror serves the
  stored bytes, and a byte of the archive that changes upstream does not
  change what the namespace receives.
- A byte change is a new request. A new request for a key (kind, upstream
  host, address, version) that has a record with other hashes is refused,
  with an audit entry that holds both hash lists. A person who wants the new
  bytes revokes the record first, then asks again. The approvers see the
  new hashes in the new request.
- A request that only the approval wait separates from the fetch cannot
  admit different bytes, because the approval names the hashes.

**How the mirror fills the lock file gap.** The version document of the
mirror carries the `h1:` and `zh:` hashes of each admitted platform. A
mirror alone is not enough: the CLI records only the current platform from a
mirror (see the facts). `trust_all_hashes = true` is not the main path,
because it works only after one hash matches, and it does nothing when the
source needs a signature (see the facts). The primary path has two parts,
and both are checked in the e2e suite:

1. **A lock file that the server builds.**
   `GET /api/dustpan/v1/namespaces/{namespace}/packages/lock-file?providers=...`
   answers the text of a
   `.terraform.lock.hcl` for the named providers and versions, with the
   `h1:` and `zh:` hashes of every admitted platform. A team commits it and
   needs no `tofu init` on each platform. The route checks
   `namespace:download-packages` (the action) for the namespace in the path.
   The OpenTofu format is in `internal/api/tfe/`, as section A says for
   a format that one tool reads. Question 14 asks whether the route belongs
   under the tool-neutral prefix.
2. **`tofu providers lock -net-mirror=<url> -platform=...`.** The
   documentation names this command for each platform that the team uses.
   The mirror answers the hashes of all admitted platforms to it.

`trust_all_hashes = true` is a secondary option. The launcher does not rely
on it, and the documentation names its limit.

**Signatures.** The standard library has no OpenPGP. The owner approved
`github.com/ProtonMail/go-crypto` (issue 1293), and `pkg/registry/pgpverify`
uses it. So the server checks the upstream signature of the checksums file
and gives the result as a fact: `verified`, `unsigned`, `unknown-key` or
`failed`. The record keeps the signature, the key ID and the fingerprint, and
the fact `keyChanged` tells a gate that the key of this address changed. The
fact says what the server found. It does not say that the package is good: a
valid signature of a key that an attacker controls is `verified`. Whether
`unsigned` blocks, warns or passes is the policy of the administrator. An
upstream that gives no key (the CLI allows this for `registry.opentofu.org`
only, and a WARN shows it) gives `unsigned`. The CLI of a team that sets
`OPENTOFU_ENFORCE_GPG_VALIDATION=true` still reads the mirror hashes
correctly, because the mirror gives hashes and not a signature (see the e2e
step).

**Age of the version and of the repository.** The protocols give no date of a
release. The network mirror protocol, the registry protocol and the module
protocol have no such field. A date is a claim of the upstream host (for
example the date of a GitHub release asset), so it is a signal with the origin
`host-claim` (section "The provenance signals"). When no source gives a date,
`available` is `false` and the day count is 0, as ADR 226 decision 3 asks.
The first date that the installation itself saw is a second signal, and the
server computes it: `firstSeenDays`. A policy that wants a minimum age reads
one of them and names `available`.

**Upstream provenance.** Where an upstream gives more (for example a build
attestation of a release, a SLSA provenance document or a Sigstore bundle),
the record keeps it as an opaque blob. The server checks it only when an
adapter for that format exists, and the `attestation` group says what was
checked. A document that no adapter checks is `present` and not `verified`.

**Pulumi resource plugins.** The Pulumi CLI checks no signature, and it
checks a SHA-256 only when the plugin spec has an entry for the current
platform (see the facts). So, as for OpenTofu, the server fetches the
archive of each platform at admission, keeps its SHA-256, and gives the
`checksums` map for each `packages` entry of `Pulumi.yaml` for every
admitted platform. The map is keyed by `<goos>-<goarch>`, as the CLI reads
it. The route is `GET /api/dustpan/v1/namespaces/{namespace}/packages/pulumi-checksums`,
with the action `namespace:download-packages`, as for the lock file. The
Pulumi format is in `internal/api/pulumi/`. A team commits it, and the
CLI then checks the bytes on each platform. A platform with no entry is not
checked by the CLI, so the output lists every admitted platform and the
documentation says that a missing entry gives no check. The launcher does
not rely on the map (layer 4).

**Team CI writes record no version.** A write from a team's own CI cannot
tell the server a provider version or a hash, because an OpenTofu state holds
neither and the CLI does not send them. So the server does not record
versions for those writes. The job record (layer 4) exists only for a job
on an OpenDustpan runner. A Pulumi checkpoint holds the version in its
provider inputs, and a read of it is an audit finding (section E, layer 5).

### E. Enforcement at run time: five layers

Each layer is built to stop a job that uses a package that is not vetted,
without help from the others (ADR 250). All five hold together only for a
job on an OpenDustpan runner with egress turned on in the pool. Each layer
has its own limit, and the limits are real.

1. **The CLI configuration.** For OpenTofu, the launcher writes a
   `provider_installation` block with one `network_mirror` to the mirror of
   the namespace and no `direct` method. This **amends ADR 134 decision 7**,
   which now says that the CLI configuration names the credentials helper
   and nothing else. The amendment adds the block and changes nothing about
   the helper. The launcher never sets
   `plugin_cache_may_break_dependency_lock_file`, and no plugin cache is
   shared between jobs or between namespaces (a cache can hold a package
   that nobody admitted, or that was revoked). For Pulumi, it sets
   `PULUMI_PLUGIN_DOWNLOAD_URL_OVERRIDES` with `.*` to the capability URL
   of the mirror (section B), and `PULUMI_IGNORE_AMBIENT_PLUGINS=true`. It
   stops a fetch by the CLI from any other source. It cannot stop a binary
   that the image or a plugin cache already holds.
2. **The mirror.** It serves only admitted versions and platforms, with
   their hashes. It stops a version or a platform that nobody admitted. It
   cannot stop a package that the image brings.
3. **Egress.** The sandbox can reach only the server (ADR 137 decision 6,
   RFC 16). This stops a download in a `local-exec`, a module `source` with
   a git URL, and a provider that fetches its own plugin. It cannot stop a
   fetch from the server host itself. A pool with egress off has no layer 3.
4. **The job record.** After `tofu init`, or before `pulumi up`, the
   launcher reads the lock file, `.terraform/modules/modules.json`, the
   provider directories under `.terraform/providers` and the plugin
   directory. It **hashes the installed package directories itself**
   (the `h1:` hash of each provider directory, the SHA-256 of each Pulumi
   plugin archive or binary) and does not copy a hash from the lock file. It
   reports each address, version and hash. The report is untrusted input,
   because a step in the sandbox could change the launcher's view. The
   server checks that each reported address and version is on the allowlist,
   that each hash equals a hash of the record for the platform of the
   runner, that the report is complete, and that its shape is valid. The report is
   complete when it has one entry for each package directory that the
   launcher hashed under `.terraform/providers` and in the plugin directory.
   It also needs one entry for each provider in the lock file. The lock file
   alone is not the measure, because the sandbox can edit it. On a mismatch it fails the job
   before the plan. This stops a binary from the image or a cache, and a
   hash that is not the admitted one. It cannot stop a binary that the tool
   loads after the check, and it cannot trust a launcher that the sandbox has
   already taken over.
5. **An audit finding, or a check at the lock (option).** See below.

The job record ties the exact addresses, versions and hashes to the plan,
to the apply, and to the state version or the Pulumi update that the job
writes (ADR 102). The console shows them on the state version.

**Layer 5 and ADR 224 decision 16.** `state.write` runs at the lock of the
workspace, or at the create of the update, **before** the write. At that
time the server does not have the state that the write will bring. So a
refusal at the write cannot read the new providers. Two honest forms:

1. **A non-blocking audit finding (recommended).** After a write from a
   person's own CLI, the server reads the state that it stored: the provider
   addresses of an OpenTofu state, and the `version` and `pluginDownloadURL`
   of the provider resources of a Pulumi checkpoint. It compares them with
   the allowlist and records a finding on the unit for each package that is
   not on it. It never blocks, so it cannot break a write. Limit: it shows
   the violation after it happened.
2. **A fact at `state.write`, against the current state only.** The fact
   schema of `state.write` has a new group `packages` with an `available` flag.
   It lists the providers of the **latest stored state** of the unit. A gate
   can refuse the lock when the latest state already holds a package that is
   not on the allowlist. Limit: it sees the last state and not the next
   one, so the first write with a new provider passes, and the next lock is
   refused. This amends the `state.write` schema in ADR 226.

What the server sees: an OpenTofu state names the provider address of each
resource, for example `provider["registry.opentofu.org/hashicorp/aws"]`, and
holds no version and no hash. A Pulumi checkpoint holds the version and the
download URL of each provider. So the check can compare an OpenTofu provider
address, and a Pulumi plugin version, and no hash and no module. The e2e
suite checks both with real state before the ADR (question 8).

**What a team's own CI and a laptop receive.** The team can point its CLI at
the mirror (layers 1 and 2 in its own configuration) and receive the same
hashes. The token of a person or of a team CI needs
`namespace:download-packages`, as a job token does. The server cannot see the binary that ran, the CLI configuration, or
the network of that CI or laptop. So a laptop receives no enforcement. A write
from a team's CI receives the audit finding or the fact of layer 5, and no more.
A security team that needs all five layers runs its writes on runners of
OpenDustpan with egress on, and a gate at `state.write` can refuse writes
that do not come from a job (ADR 224 decision 16 already gives the facts for
that).

### F. Untrusted input (ADR 250)

Archives, checksums files, signatures, keys and every upstream document are
untrusted input. So are the lock file, `modules.json` and the package list
that a job reports. The ADR that accepts this RFC adds them to the list of
ADR 250.

1. **Check at the boundary.**
   - An allowlist of upstream hosts. `https` only. No user info in a URL.
     The check runs on the first request and on every redirect and every
     document hop (section G).
   - A version grammar (SemVer for providers and plugins). `os` and `arch`
     from a fixed list.
   - **Modules.** The server accepts an upstream module only as an `https`
     archive URL on an allowlisted host, pinned to a commit (a 40-character
     commit in the path or the query). It refuses every go-getter form: a
     `git::` prefix, `ssh`, `github.com/...` and `bitbucket.org/...` short
     forms, `s3::`, `gcs::`, `hg::`, a `file` or `http` scheme, a user info
     part, and a query other than the commit. Most public modules use
     `git::https://github.com/...?ref=...` sources, so this rule refuses
     most of them. They wait on question 7 (a fetch by a runner).
   - The server refuses a JSON document over 1 MiB.
   - Test: one hostile value for each rule, with the fetch skipped.
2. **Encode or contain at use.**
   - The server reads an archive entry by entry and never unpacks it to
     disk. It stores the original bytes and serves them as they are. The
     checks below protect the CLI that unpacks, not the server.
   - **Zip rules.** The server refuses an archive with any of these:
     - an entry name that is absolute, or holds `..`, a backslash, a drive
       letter or a NUL,
     - two names that are equal after case folding and NFC normalization,
     - a symlink entry,
     - a setuid or setgid mode bit,
     - a local header that differs from the central directory in name, size
       or method,
     - data before the first local header or after the end record (prefix
       and suffix data),
     - an insecure path in the sense of `archive/zip` (`zip.ErrInsecurePath`).
   - **Tar and `tar.gz` rules (Pulumi plugins).** The server accepts regular
     files and directories only. It refuses any of these:
     - a hard link, a symlink, a device, a FIFO or a sparse file,
     - a name that is absolute or holds `..`, also in a PAX path record,
     - a setuid or setgid bit,
     - names that are equal after case folding and NFC normalization,
     - a gzip stream with trailing data,
     - a gzip ratio above the limit (step 3).
   - The server builds the archive URLs of the mirror from the stored
     record, never from the upstream text.
   - SQL placeholders only. The server deletes CR and LF from log values.
   - Test: a zip with `../x`, `/etc/x`, a symlink entry, a case-fold
     duplicate and a setuid bit, and a tar with a hard link, a PAX path
     `../x` and a device, with the boundary check skipped.
3. **Limit the effect.**
   - `http.MaxBytesReader` and a timeout on each fetch.
   - The server streams an archive to a temporary file inside an `os.Root`
     with a size limit, hashes it while it streams, and then moves it to the
     blob store. It reads the stored blob with `ReaderAt`. It never reads
     512 MiB into memory. The number of admissions that run at the same time
     is limited (proposal: 4 for each installation, 1 for each namespace).
   - Limits on archive size, unpacked size, entry count, and the ratio of
     unpacked to packed size (archive bombs).
   - The fetch goes through `pkg/egress` and `egresshttp`, which check the
     resolved IP of each connection against the always-denied ranges. A
     fetch uses no credential of the installation.
   - Test: a zip bomb, a 600 MiB archive and a slow upstream, each at its
     limit plus one. Also a flood of admissions over the concurrent limit.
4. **Authorize at use.**
   - The store checks the namespace of the admission and the action on it,
     in the same transaction as the write.
   - The mirror route checks `namespace:download-packages` for the
     namespace in the path and the namespace of the token. The archive
     route checks the proof of the signed URL, that the record is admitted,
     and the blob owner (section B).
   - Test: a job token of another namespace on the mirror, a signed URL of
     another namespace, and a URL for a revoked record, with the route check
     skipped.
5. **Fail closed and record.**
   - A checksum mismatch, a bad signature, an unknown key, a parse error, a
     size limit, an upstream error or an unknown value refuses the
     admission.
   - A missing entry in the job record fails the job.
   - Each refusal writes an audit entry with the upstream host, the address
     and the reason, never the body.
   - Test: each failure gives an audit entry and no stored row.

A module needs one more rule: the server fetches a module archive only from
an upstream module registry or from an allowlisted `https` archive URL, and
never runs `git` for it in the first slice. A module whose upstream answer
points to a git URL needs a later fetcher in a runner sandbox (question 7).

### G. Who fetches from upstream

1. **The server, with an upstream allowlist (recommended for the first
   slice).** The fetch reuses `pkg/egress` and `egresshttp` (ADR 129
   decision 25), with its **own** allowlist setting for package upstreams.
   With no entry, no package fetch goes out. The server checks the host at
   every hop: the discovery document, the `download_url`, the `shasums_url`
   and the signature URL, each redirect, and the release-asset CDN host that
   `github.com` redirects to. A default list for a provider from GitHub
   names `registry.opentofu.org`, `github.com` and the release-asset host.
   The default is not turned on until the installation sets it. An e2e test
   needs a loopback upstream over TLS, so a test-only entry type for a
   loopback CIDR with one port, as ADR 129 decision 25 allows for private
   addresses, serves the suite. That entry is not in a production default.
2. **A runner job of a new kind `package-fetch`.** The fetch and the
   parse run in a sandbox, and the server checks the SHA-256 of the uploaded
   bytes against a checksums file that the server fetches itself. Safer for
   a server with no outbound access, and more work. A later step. It is also
   the only way to fetch a git module source.
3. **An upload by a person.** For an air gap: the person uploads the
   archives and the checksums file, and the server checks them as in
   option 1. It can come with either option, and it comes soon after slice 1.

### H. Scans and external checks

Two kinds of scan result enter the facts, and both are signals:

- **A scan that the server runs** over the stored bytes: for example
  `govulncheck -mode=binary` for a Go provider, as RFC 10 says, or a lookup of
  the module list of the binary in OSV. The `vulnerabilities` group holds the
  scanner, the version of its database, the time of the scan, and the counts
  for each severity. A new scan of an admitted package can flag it. No scan
  admits or revokes by itself.
- **An external check**: a scanner that the administrator runs outside of
  OpenDustpan (a license scanner, a software composition analysis service, an
  internal review tool) posts its result for a vetted package through a REST
  route (ADR 17). The `external` group holds the result of each check. The
  report uses the model of RFC 90: a scanner report blob with findings, bound
  to the vetted package instead of a plan. A check is a claim of its reporter.
  The record names the reporter, the check name and the time, so the gate and
  the reader see who said it.

Whether a finding at or above a severity blocks an admission, asks for an
approval, or only shows, is the policy of the administrator. A person always
revokes: no scan revokes by itself (question 9 of the earlier list).

### I. The first slice

**Persona.** The security team of a bank runs OpenDustpan. Its platform
team asks for one community provider for OpenTofu in the namespace
`payments`. The security team approves that one version once.

**What slice 1 gives that persona.** Layers 1 and 2 only. After the
approval, a job whose launcher writes the block receives that provider from the
mirror only, with the admitted hashes, for every admitted platform, and the
lock file from the server pins them. The slice **does not** stop a provider
that the runner image brings, a provider in a plugin cache, a provider that
a `local-exec` downloads, or a team that runs `tofu` on a laptop or in its
own CI. Those need layer 3 (a pool with egress on) and layer 4 (the job
record), which come in the slice after. A security team that needs the
guarantee waits for them. Until then the slice gives them one exact, approved
version in the normal path of the tool, and a record of who approved it.

**Scope.**

- OpenTofu providers only, version 1.12 or later. No modules, no Pulumi
  resource plugins.
- `pkg/pkgmirror` with the record, the store port and its conformance suite
  in `pkg/pkgmirror/pkgmirrortest`, for SQLite and PostgreSQL.
- The decision point `package.admit` (after its ADR), the fact schema with
  the signals that the server computes (hashes, signature, first seen), the
  rules of section C that say who decides when no policy decides, the starter
  packs as examples, and the action `namespace:approve-package`.
- The server fetch from its own allowlist of upstream hosts, with the checks
  of section F, the SHA-256 against the checksums file, and the `h1:` hash
  for each platform, and the OpenPGP check of the upstream signature as a
  fact. No signal from a host (stars, repository age) in slice 1.
- The network mirror routes and the lock file route in `internal/api/tfe/`.
- Layer 1 in the launcher: the `provider_installation` block (with the
  amendment of ADR 134 decision 7).
- The admission, list and revoke routes, and a user documentation page.

**The e2e step (real OpenTofu CLI, pinned to 1.12 or later).** A test
upstream in the e2e suite serves one provider that the suite builds, with a
checksums file, for `linux_amd64` and `darwin_arm64`, over loopback TLS. The
step:

1. asks for the admission of version 1.0.0 and sees it wait for approval.
2. tries to approve it as the requester, who holds
   `namespace:manage-gates`, and sees the refusal. Then approves it as a
   second user, and sees the record with two platforms.
3. fetches the lock file route, and runs
   `tofu providers lock -net-mirror=<url>` for both platforms. Both give
   the `h1:` hash of both platforms. Then runs `tofu init` with a CLI
   configuration that names only the mirror. It runs once with
   `OPENTOFU_ENFORCE_GPG_VALIDATION` set and once without it, and both
   succeed, and the lock file holds the hashes of both platforms.
4. changes the constraint to 1.1.0 (not admitted) and checks that
   `tofu init` fails.
5. serves a changed archive for 1.0.0 upstream **with the old checksums
   file**, asks again, and checks that the admission fails on the checksum.
   Then serves a changed archive **with a changed checksums file**, asks
   again, and checks that the request is refused because the record has
   other hashes.
6. revokes 1.0.0 and checks that `tofu init` with an empty cache fails, and
   that `tofu init` with a cached `.terraform` directory and an old lock
   file can no longer download from a signed URL that was issued before the
   revocation.

## Prior art: artifact firewalls

The owner asked whether this is a solved problem. Mostly it is. Artifact
proxies and firewalls have used one shape for years: read signals about a
package, apply a policy that an administrator writes, and give a person a way
to make an exception. This section records what the vendor documents say,
as of 2026-11-07. It reads vendor documents and public specifications only.
A statement that the pages do not make is marked "not documented". Several
pages were read through a summarizing tool, so the details are as relayed and
a reviewer should check any value before the project quotes it.

### What each system does

| System | Signals | How a policy is written, and the default | Waiver | First-party and third-party |
| --- | --- | --- | --- | --- |
| **JFrog Xray** | Security (CVE severity), license and operational risk. A rule can ignore violations that have no fix version (`fix_version_dependant`). | A policy holds rules with criteria and actions: fail build, **block download** (also for unscanned artifacts, with a timeout of 60 seconds by default), notify. A watch binds a policy to repositories or builds. A grace period (in days) is available. Default policy: not documented. The block-download function for remote repositories is moving to Curation. | An **ignore rule**: scope is a watch, component, artifact, build or release bundle. Expiry is optional. The violation stays visible in the audit log. Who may create a rule and whether a reason is required: not documented. | Gates local repositories. Not documented otherwise. |
| **JFrog Curation** | Malicious package, CVE by CVSS (options: use EPSS, allow when no fix exists, allow when the CVE is already in the repository), OpenSSF score, license (allow list, block list, "no license found"), **immature** (younger than N days; 2, 14 or 30 in the wizard; option: allow when the version fixes a CVE that the repositories have), aged, end of life, integrity violation, catalog labels. | A policy is a **condition + a repository scope + an action** (`block` or `dry_run`). A dry run writes audit events only, and the vendor suggests one to two weeks before enforcing. "Compliant version selection" returns the newest version that passes, in place of an error. Default policy: not documented. A "warn" action: not found. | **Waiver request** with a reason, through the CLI or the UI. Each policy sets `Not Allowed`, `Manually Approved` (a decision-owner group is required) or `Automatically Approved`. All blocking policies must agree. Scope: one package version. **Expiry: 1 to 365 days, set in the policy. If unset, the waiver is permanent.** The shortest duration wins. On expiry the block applies again, also to cached copies. The closed requests list who decided, the duration and the time, and exports to CSV. | The public catalog covers public packages. **On-Demand Curation** scans packages from internal remote repositories separately. Some conditions are not supported there, and the age of a package is the date it entered Artifactory, which gives false alarms. |
| **Sonatype Repository Firewall** | Malicious component, release **integrity** rating, CVSS 9 or more, banned license, **age** (counted from the day Sonatype first cataloged the version, not from the publish date of the host), **namespace conflict**, unknown component. | A policy has an action for each stage. At the proxy stage the action is `Fail`, `Warn` or `No Action`. **`No Action` is the default.** Only `Fail` quarantines. Automatic release of a quarantined component happens when its violations no longer hold. The vendor recommends: block unknown components, turn on automatic release. | A **waiver** needs the "Waive policy violations" permission. A user without it submits a **waiver request**. The reviewer may change the scope, the match and the expiry, and gives a reason. Scope: the repository is recommended, the organization is "use with caution". Expiry: a duration or never, and the vendor says to use short durations. After expiry the violation returns, but **the component is not quarantined again** until a person removes it from the proxy. A waiver can stop to apply when the data under it changes (for example a changed CVE severity). Vendor advice: give the right to the project owner, the security team or the legal team, not to developers. | **Namespace confusion protection** builds a list of namespaces from hosted repositories that an administrator marks as proprietary (Maven group, npm scope). A public component in such a namespace is quarantined at the proxy. |
| **Cloudsmith** | CVE severity, license, deny rules by name or version, malware, and for the policy-as-code add-on: CVSS, EPSS, patch availability, package and repository data. **Cooldown policy**: minimum age in days, from the upstream publish date. | Standard policies quarantine what they match. **Policy as code** is Rego (OPA) over package metadata. Actions: set state (`AVAILABLE`, `QUARANTINED`, `DELETED`, `HIDDEN`), add or remove tags. A higher precedence runs first, and a `terminal` flag stops the evaluation. A request for a quarantined package gets 403. A **simulate** endpoint shows the effect. Default policy: not documented. | An **exemption** is an exact allow list in a Rego policy (package and version) that sets the state `AVAILABLE` and the tag `exempt`. **No expiry and no approval flow are built in.** The vendor suggests a monthly review and a pull request flow in Git. Decision logs record each evaluation with the reason and the actions. | The cooldown targets packages from public upstream hosts. A manual upload has no upstream publish date, so it is not covered by that policy. |
| **GitHub dependency review, Dependabot, Artifact Attestations** | Known vulnerabilities (Advisory DB), license, scope (runtime or development), package identity (purl), OpenSSF Scorecard score. Attestations are signed claims about the workflow, the repository, the organization, the commit and the event. | Action inputs: `fail-on-severity`, `allow-licenses` (an SPDX list; it cannot be combined with `deny-licenses`), `deny-packages` (purl; without a version, all versions), `allow-ghsas`, `fail-on-scopes`, `warn-on-openssf-scorecard-level`. Defaults: `fail-on-severity` is `low`, `fail-on-scopes` is `runtime`, and a Scorecard score below 3 gives a **warning, never a failure**. A license that is not found does not fail. Organization-wide enforcement is a required workflow in a ruleset. The Kubernetes policy-controller enforces nothing until a namespace carries the label `policy.sigstore.dev/include=true`, and then **an image that matches no policy is rejected**. | `allow-ghsas` and `allow-dependencies-licenses` live in the workflow file. They have no expiry, and the review of the pull request is the approval. A Dependabot dismissal has a reason code and a comment, and it is in the audit log. **Delegated alert dismissal** makes a user with write access *request* a dismissal. Only owners, security managers, or a custom role with the review permission approve. | No distinction. The policy-controller tells a source organization (`policy.organization`) from other sources. |
| **Socket.dev** | Alert types: install scripts, network access, obfuscated code, typosquat, **new author**, **recently published**, unmaintained, known malware, malware found by AI, CVEs, license, git or HTTP dependencies, telemetry, protestware. | A policy is an **ordered list of rules**. Each rule maps an alert to `Block`, `Warn`, `Monitor` or `Ignore`. The first match wins. Default ("Balanced"): block known malware. Warn on AI-found malware, critical CVEs, license violations, typosquats, protestware, obfuscated code. Monitor the other CVEs. Ignore the rest, which includes install scripts, network access and package age. | An **alert resolution** (reasons: remediated, false positive, tolerable risk, other) with a comment. It is checked **before** the policy and wins over it. Scope: from one repository to the whole organization. Expiry: not documented. A comment `@SocketSecurity ignore pkg@version` in a pull request does the same, and any contributor can write it. | Not documented. |
| **OpenSSF Scorecard and SLSA** | Scorecard: about 18 checks that score 0 to 10, each with a risk level. Critical: dangerous workflow, webhooks. High: binary artifacts, branch protection, code review, maintained, signed releases, token permissions, vulnerabilities and others. SLSA build track: L1 provenance exists, L2 a hosted build and signed provenance, L3 hardened and isolated builds. | **No policy language.** A score is data, not a decision. Tools that use it make it a warning or a threshold of their own. SLSA says that the consumer checks the provenance against its own expectations. | None. | None. |
| **Google Assured OSS** | The guarantees of the service: built from source by Google, signed provenance (SLSA level 3), continuous vulnerability scanning, SBOM and VEX. | The administrator chooses the repository. No policy language is in the overview. | Not documented. | Only the packages that Google builds carry the guarantees. The free proxy serves other packages too, without them. |
| **Artifact Hub** | A scan report of the container images in a package (Trivy), the **verified publisher** mark (the owner proves control of the repository with a metadata file), the **official** mark (a manual request), a signed mark (Helm provenance). | **Informational.** There is no threshold and no block. | Not applicable. | Official means that the publisher owns the software. Verified means that the publisher controls the repository. |
| **Inedo ProGet** | Vulnerabilities, license (a global policy and a feed policy: compliant, warn, non-compliant). | Two feeds: an unapproved feed that connects to public hosts, and an approved feed. A person **promotes** a vetted package to the approved feed, and developers see only that feed. Default: the pages do not say what an unassessed package counts as (a forum post says compliant; not verified). | License exceptions use wildcard filters with an optional expiry date. Vulnerability overrides, who may assess, and the audit: the pages could not be read. | Not documented. |

### The Go module ecosystem

Providers are Go programs, so Go gives a natural signal set for them. Athens,
the Go module proxy, is a gate for modules. The facts below are from
`go.dev/ref/mod`, `go.dev/doc/security/vuln` and the Athens documents.

- **Athens filter file.** One rule for each line. The prefix is `+` (store the
  module), `D` (fetch it directly, do not store it) or `-` (exclude it: not
  fetched from upstream). A first line with a bare `+`, `-` or `D` is the
  default. A more specific rule wins. A path may be followed by a
  comma-separated list of versions with the modifiers `~1.2.3`, `^1.2.3` and
  `<1.2.3`. The allowlist form is: a bare `-` as the default, `D` for the
  organization's own path, and `+` with a list of versions for each admitted
  module. The documents mark the filter file as deprecated for download
  routing, because of the **download mode file** (`sync`, `async`, `redirect`,
  `none`, with one block for each module pattern). `ValidatorHook` is a URL
  that Athens calls to validate a module. The request and the response format
  are not in the documents that were read. **Lesson:** include and exclude
  lists with version rules are the oldest form of the allowlist, and the
  community moved from one flat file to rules with an outcome per pattern.
- **Checksum database** (`sum.golang.org`). A transparency log (a Merkle tree)
  of the hashes in `go.sum`. The `go` command checks an inclusion proof and a
  consistency proof, and compares the hash with the bytes. A mismatch is a
  security error. Inclusion proves that everyone sees the **same bytes for a
  version**. It does not prove that the code is safe, that the author is who
  the name says, or that the version is old. A first publish that is malicious
  is logged faithfully. `GOPRIVATE` (and so `GONOSUMDB` and `GONOPROXY`)
  names the modules that skip the proxy and the database. A private module
  has **no transparency guarantee**, only `go.sum` on first use. This is the
  Go form of the first-party and third-party split.
- **Retraction and deprecation.** The author writes `retract` in `go.mod` or a
  `Deprecated:` paragraph. The `go` command does not select a retracted
  version in an upgrade, and warns. A build that already uses the version
  still works. The **author controls both marks**. An attacker who owns the
  module can add or omit them.
- **Vulnerability database** (`vuln.go.dev`, OSV format) and **govulncheck**.
  The database covers known, curated problems only. It has no severity label
  and no malicious-package feed. govulncheck in source mode reports only the
  symbols that the code can reach. In **binary mode** it has no call graph, so
  it may report problems that are not reachable. It exits with 0 only when it
  finds nothing, and with the JSON output it exits with 0 in every case, so a
  gate must read the findings, not the exit code.
- **Publish time.** The proxy answers `@v/<version>.info` with a `Time` field.
  It is optional. For a direct fetch it is the commit time, which the author
  sets. The checksum database does not cover it. A policy on age must treat it
  as untrusted. The time of the first sight in the installation is the safer
  clock (Sonatype counts from the first catalog date for the same reason).

### The common signal set

Eight signals appear in at least three of the systems above:

1. **Known malicious package** (JFrog Curation, Sonatype, Cloudsmith, Socket).
   Every system that has it treats it as a block by default.
2. **Known vulnerability with a severity** (all except Scorecard, Artifact
   Hub and the Go checksum database). Always with a **fix available**
   condition or a **reachability** condition (Xray, Curation, Cloudsmith
   with patch availability, govulncheck), because a threshold alone blocks
   too much.
3. **License** (all except the Go ecosystem). An allow list. Unknown license
   is a separate value.
4. **Age** of the version (Curation, Sonatype, Cloudsmith, Socket).
   Always a minimum age in days, with a relaxation for a version that fixes
   a known problem. Two systems count from their own first sight of the
   version, and not from the date that the publisher gives.
5. **Provenance and integrity** (Curation, Sonatype, GitHub, SLSA, Google,
   Artifact Hub, the Go checksum database): a signature, an attestation, a
   build level.
6. **Maintainer and ownership change** (Socket new author, Scorecard
   maintained, Sonatype namespace conflict, Artifact Hub verified publisher).
7. **Project posture** (Scorecard, Curation OpenSSF, Socket unmaintained).
   Advisory in every system that uses it. No system blocks on the score alone
   by default.
8. **Unscanned or unknown** (Xray, Sonatype, Curation). A package for which
   the data is not yet there. Three systems block or hold it, and one of them
   cannot waive that hold.

### The common policy primitives

- **Three outcomes, plus a rehearsal.** Block, warn (or monitor), and
  **quarantine** (the package is known to the system and is held until a
  person or a condition releases it). Every system with a block also has a
  mode that records the result and enforces nothing: dry run (Curation),
  simulate (Cloudsmith), monitor (Socket). Our outcomes are `deny`, `warn`,
  `approval:<rule id>` (a request that waits is our quarantine) and the
  `evaluate` mode of ADR 229 (the rehearsal). The mapping needs no new
  concept.
- **A threshold with a relaxation.** Severity with "fix available" or
  "reachable", age with "fixes a known problem", Scorecard score with a level
  that only warns.
- **An allow list and a deny list of licenses, of packages and of
  namespaces.** An allow list wins when both are present (GitHub refuses to
  combine them).
- **A scope.** Repository, label, runtime or development dependency. For us:
  installation, namespace, kind of package.
- **Ordered rules, the first match wins** (Socket, Cloudsmith with a terminal
  flag) or **combine all** (Cloudsmith precedence, Curation: every blocking
  policy must agree). Gates in ADR 224 combine all. This is the safer form
  for a deny.
- **A malicious-package feed blocks without a threshold.** Nobody makes it
  a score.
- **Fail closed for the unknown.** Two of the systems hold a package that
  they cannot evaluate yet. The GitHub policy-controller rejects an image that
  no policy matches. Counter-example: Sonatype's stage default is `No Action`
  and Cloudsmith leaves a package available when its first evaluation fails,
  so those two fail open until an administrator writes a policy.

### The waiver model

The pattern with the most support (Curation, Sonatype, GitHub delegated
dismissal):

1. **A request, then a decision.** The person who needs the package asks, and
   gives a reason. A person with a named permission decides. A person without
   the permission can ask and cannot decide.
2. **The scope is one version of one package** (Curation), a repository
   (Sonatype, recommended) or an advisory ID (GitHub). The wide scopes
   (organization, all versions) exist, and the vendors tell the user to avoid
   them.
3. **An expiry.** Curation: 1 to 365 days, set by the policy, with the
   shortest of several owners winning. Sonatype: a duration or never, with
   advice to use short ones. Xray: optional. Cloudsmith: none, so the vendor
   suggests a monthly review. **When a waiver ends, enforcement applies again
   and the decision is re-evaluated.** Sonatype shows the failure of the
   alternative: after expiry the component stays in the cache until a person
   removes it.
4. **A reason, the deciding person and the time are kept.** Curation exports
   them. Sonatype keeps approved requests. Xray keeps the violation visible
   in the audit log. Socket keeps the reason and the comment.
5. **The waiver can lose its meaning.** Sonatype stops a waiver when the data
   under it changes (for example a new CVE severity).
6. **Some holds cannot be waived.** Xray cannot override the "unscanned"
   block, and Curation cannot waive a block while a scan is pending.
7. **The weak forms are the ones to avoid.** A waiver in a file with no expiry
   and no separate approver (the GitHub `allow-ghsas`), a comment that any
   contributor can write (Socket), and an allow list with no expiry
   (Cloudsmith) are the forms that the vendors' own best-practice pages warn
   about, in the cases that we read.

### Guidelines for RFC 93

These follow from the above. Each is a guideline for the documentation and the
starter packs. None is a built-in verdict, because the owner decision stands.

- **G1. Start in rehearsal.** Every starter pack arrives in `evaluate` mode
  (ADR 229). The vendors suggest one to two weeks before a block.
- **G2. Use the waiver for exceptions, and the gate for rules.** A request
  that waits for an approval rule (ADR 225) is the waiver flow of Curation and
  Sonatype. The approval record holds the scope (one version of one address),
  the reason, the approvers and the time. The requester does not approve
  (question 2).
- **G3. An approval for a third-party package has an expiry.** Both vendors
  with a full flow have one, and both warn about the permanent waiver. Our
  proposal: the approval rule sets a maximum in days (the shortest wins when
  rules differ), and at the end the package goes back to the state "waits for
  a person" and the units that use it see a `warn` in their next job. This
  re-evaluation is the part that Sonatype lacks. Question 5 asks the owner.
- **G4. A reason is a required field, and a hold cannot be waived while
  scan data is missing.** The pack for a regulated organization makes the
  second a `deny` with no approval path.
- **G5. Age counts from our first sight where the host date is not safe.**
  The facts hold both: `version.ageDays` (the host date, `host-claim`) and
  `version.firstSeenDays` (our clock, `computed`). A pack that wants a
  cooldown should read the second one, or both.
- **G6. A malicious-package finding is a `deny` in every pack.** An
  administrator may still remove it. The reason is that no vendor treats this
  signal as a score.
- **G7. A severity gate has a fix-available or a reachable condition.** For
  Go binaries, read the reachable findings first. A binary-mode finding may be
  a false alarm, and the gate must say so in its reason.
- **G8. A score from outside the installation only warns.** Stars, Scorecard
  and similar values are `host-claim`. All systems that use them keep them
  advisory. The packs follow this.
- **G9. First-party packages go through the review flow of the team.**
  Curation scans private repositories apart from the public catalog. Sonatype
  protects proprietary namespaces against a public package of the same name.
  Go skips the proxy and the database for private modules. This matches the
  owner's decision, and adds one thing to the design: a **namespace conflict**
  check. A third-party package whose address lies in a namespace that the
  installation registered as private is held for review (G10).
- **G10. Name confusion is a signal.** The new fact
  `ownership.namespaceConflict` is `true` when an upstream package has the same
  name or namespace as a first-party one. It is `computed` from our own
  records. The packs `deny` it unless a person approves.
- **G11. Show the policy that decided.** Every system with an audit log keeps
  who and when. We add the matched gate and the whole fact document (ADR 229).
  This is more than the vendors keep, and it follows from "no magic".

## The provenance signals

The fact schema `package.admit/1.0` has one group for each family of signals.
A group has an `available` flag (ADR 226 decision 3). Each group has an
`origin` that says how much the server vouches for it:

- `computed`: the server read the bytes that it holds, or its own record.
- `host-claim`: a host outside the installation said it (GitHub, a registry,
  a package host). The server stores what the host said and the time of the
  read. The value is unverified.
- `reporter-claim`: a scanner or a person outside the server posted it. The
  record names the reporter. The value is unverified.

| Group | Fields (examples) | Origin | What a person can fake or what the signal misses |
| --- | --- | --- | --- |
| `ownership` | `class` (`first-party` or `third-party`), `namespaceConflict` (an upstream package has the name or namespace of a first-party one), the registry, the namespace, the publishing principal and the grant that allowed the upload | computed | Nothing, if the rule below holds: a package is `first-party` only when it is in a private registry or namespace of this installation, a principal uploaded it through the publish route, and a grant allowed it. A mirrored package is always `third-party`. A claim inside the package has no effect. |
| `package` | kind, upstream host, registry host, address, version as written, number of platforms, SHA-256 of each archive | computed | Nothing about the bytes. The address says nothing about who owns the name. |
| `publisher` | namespace or organization of the address, forge host, forge organization or user, the forge's own "verified" mark | host-claim | A name that looks like a vendor (typosquat). A forge organization that changes owner. A "verified" mark is the host's view, not ours. |
| `signature` | result (`verified`, `unsigned`, `unknown-key`, `failed`), key ID, fingerprint, `keyChanged` | computed | `verified` means "signed by this key". An attacker with the key, or with a new key at a new address, is `verified`. |
| `attestation` | format (`slsa`, `sigstore`, `openpgp-only`, `none`), `present`, `verified`, the builder and the source repository that it names, the claimed level | computed when an adapter checks it, else host-claim | A build can be reproducible and still malicious. An attestation names the builder, not the intent. |
| `version` | upstream release date, age in days, `firstSeenDays` in this installation, the count of admitted versions of the address, the yank or deprecation mark of the host | host-claim for the date, computed for `firstSeenDays` | The publisher chooses the release date on many hosts. A new version of an old package has an old repository. |
| `repository` | forge host, URL, age in days, days since the last commit, stars, forks, archived mark, number of contributors | host-claim | **Stars are cheap to buy and tell nothing about safety.** A new commit costs one empty commit. An old repository can change hands. A fork starts at age 0 with the history of the original. |
| `vulnerabilities` | scanner, database version, scan time, count for each severity, count of reachable findings where the scanner gives it, count of `malicious` findings from a malicious-package feed in OSV format | computed (scan of the stored bytes) | A scanner finds known problems only. `govulncheck` covers Go modules and the reachable symbols of a Go binary, and not other languages. A database can lag behind a disclosure. |
| `gomodule` | for a Go binary: module path and version, linked modules, `sumdbIncluded`, `retracted`, `deprecated`, `govulncheck` mode and findings, `proxyTime`. See "Go modules as a signal set for providers" | computed from the binary, host-claim for the rest | The author controls retraction and the proxy time. Inclusion in the checksum database shows the same bytes for everyone, not that the code is safe. |
| `external` | for each check: name, reporter, time, status (`pass`, `fail`, `error`), counts for each severity | reporter-claim | As good as the reporter and its credentials. The server cannot check the work of a scanner that it did not run. |
| `principal`, `source` | who asks and how: kind, credential, `api`, `cli` or `agent` (ADR 226) | computed | Nothing. These are our own records. |

Rules for the groups:

- **No score.** The server gives no combined number such as "trust 7 of 10"
  and no color that hides the facts. A score is a verdict with a hidden
  weight. A console view may sort and group by a fact. It does not rank.
- **Each fact shows its origin and its time.** The API for a vetted package
  and the console show the origin, the host that gave the value, the time of
  the read and whether the read failed. A failed read is `available: false`,
  never a default value that looks good.
- **A read from a host is an outbound call that the operator names.** The
  read of a forge API (stars, last commit) goes through `pkg/egress` with the
  package-upstream allowlist of section G. With no entry, the group is
  `available: false`. This is not phone-home (ADR 74): the operator names
  the destination, and the call carries the name of a public package and
  nothing about the installation.
- **The set of groups can grow.** A new group or a new field is a new minor
  version of the schema (ADR 226 decision 2). A fact that Cedar cannot reach
  (it has no open map and no quantifier over a set) gets a fixed field: for
  `external`, the counts and a list of check names that the gate can test
  with `contains`. The ADR that accepts this RFC fixes the exact shape.
- **Slice order.** Slice 1 gives the groups with the origin `computed`.
  The groups with `host-claim` follow, then `external`.

## Policy: the administrator decides

A policy is a gate over the facts of the previous section, at the scope of an
installation or a namespace, with the outcomes of ADR 224 (`allow`, `deny`,
`warn`, `approval:<rule id>`). It is data that a person edits and
audits, saves in `evaluate` mode first (ADR 229), and can simulate against
old requests. The administrator decides:

- which vendors, forge organizations, registries and individual versions are
  trusted, and how (an allowlist of publishers, an allowlist of addresses);
- whether stars, repository age, the time of the last commit and the age of
  the version mean anything;
- what a scan result means: a count of severities, or only reachable findings;
- which external checks must pass.

### Example policies for three kinds of organization

The documentation (a guide in `docs/guides/`) and the starter packs give
examples. A starter pack is a set of gates and approval rules, as data.
An administrator copies a pack and edits it. The server does not apply a pack
by itself. The packs below are sketches. The field names are proposals for
`package.admit/1.0`, and the exact gates need `gate/gatetest` and a review.

How to read a sketch. ADR 224 decision 14 combines the outcomes of the gates
in this order: any `deny`, then each named approval rule, then each `warn`,
then `allow`. So a pack never writes an `allow` rule. A package is admitted
when no rule below matches it. Each rule is a gate with a `when` condition
over the facts and one outcome. Every pack starts in `evaluate` mode (G1).
Every pack applies to `ownership.class == "third-party"` (the next
subsection). The prior art maps to ours like this: block is `deny`, warn is
`warn`, quarantine is a request that waits (`approval:<rule id>`), and a
rehearsal is `evaluate` mode.

#### Pack 1: startup (speed first)

The aim: no human delay for known vendors and for packages that look
normal. A person looks at the rest, and that person may be the requester.
The pack has five rules and one approval rule.

```
approval rule  package-quick-review
  approvers    holders of the approve action (the requester may approve:
               packageAdmission.authorMayApprove = true, set by the
               administrator, shown in the API)
  count        1
  expires      90 days, then the package waits again (G3)

deny   when vulnerabilities.available and vulnerabilities.malicious > 0   (G6)
deny   when ownership.namespaceConflict                                 (G10)
warn   when vulnerabilities.critical > 0 and vulnerabilities.fixAvailable
         message "A critical finding has a fixed version. Update."
approval:package-quick-review
       when not (publisher.name in ["hashicorp", "opentofu", "pulumi",
                                    "<cloud vendor>", "<team forge org>"])
approval:package-quick-review
       when version.ageDays < 7 and version.firstSeenDays < 7          (G5)
```

The signals it reads: `publisher`, `version`, `vulnerabilities`,
`ownership`. It does not read stars, repository age or Scorecard (G8). The
decision record holds the facts, the matched gate, the person and the expiry.
Compare Socket's "Balanced" preset: block the known bad, warn on the loud
risks, ignore the rest.

#### Pack 2: regulated, with a SOUP process (ISO 13485, IEC 62304)

The aim: evidence for each item of software of unknown provenance, for years.
No package enters without two people and a written risk review. The pack
blocks the cases where the evidence is not there.

```
approval rule  soup-review
  approvers    members of the group "soup-reviewers"
  count        2, from distinct people, and the requester is not one
  reason       required: the intended use, the risk review, the version
  scope        one version of one address (not a range, not all versions)
  expires      365 days at most, then a new review (G3)
  on change    a new vulnerability finding on the stored bytes re-opens it

deny   when vulnerabilities.available == false                   (G4, no waiver)
deny   when signature.result != "verified"
deny   when vulnerabilities.malicious > 0   (G6)
deny   when vulnerabilities.reachableCritical > 0 or
            vulnerabilities.reachableHigh > 0                    (G7)
deny   when external.checks["<named scanner>"] != "pass"
deny   when ownership.namespaceConflict                          (G10)
approval:soup-review   when true
```

`when true` means each third-party package waits for the review. The record
holds the whole fact document, the rule, both approvers, the reasons, the
hashes and the expiry. It never changes, and it stays while the package is
in use and after it. The rules `deny` an `unavailable` scan and give no
approval path to waive it, as Xray does for an unscanned artifact and
Curation does for a pending scan. A review is for a person to read the
evidence, not to override a missing fact.

For a provider, the `gomodule` group (below) adds two lines that this pack
writes in the same way: `deny when gomodule.retracted`, and
`deny when gomodule.sumdbIncluded == false`.

#### Pack 3: enterprise allowlist

The aim: the security team lists what is allowed, and everything else is out.
This is the Athens allowlist form (`-` as the default, then `+` with
versions) and the Curation block list with a waiver flow.

```
approval rule  allowlist-entry
  approvers    members of the group "security-team"
  count        1
  expires      180 days; the entry returns to review (G3)

deny   when address not in allowlist.addresses
         message "The address is not on the allowlist of the security team."
deny   when address in allowlist.addresses and
            version not in allowlist.versions[address]
deny   when namespace not in allowlist.namespaces[address]
deny   when vulnerabilities.malicious > 0   (G6)
deny   when ownership.namespaceConflict                          (G10)
```

The allowlist is data in the installation (a list of entries with an address,
the versions, the namespaces that may use it, the creator and the time). A
new entry is a change that needs the approval rule `allowlist-entry`. A
request for a package that is not on the list is a `deny` with a message
that tells the requester how to ask for an entry. The pack reads no stars,
no repository age and no age of the version. The decision record holds the
list entry, the matched gate and the approver of the entry. ADR 269
decision 10 already puts the SHA-256 sums of an admitted package on this
list.

The three packs do not need all signals. The table shows what each reads.

| Signal | Startup | Regulated | Allowlist |
| --- | --- | --- | --- |
| `ownership` (class, namespace conflict) | yes | yes | yes |
| `publisher` | yes | no | no (the list has the address) |
| `signature` | no | yes | no |
| `version` (age, first seen) | yes | no | no |
| `vulnerabilities` | yes, warn | yes, deny | no |
| `external` | no | yes | no |
| `gomodule` (providers) | no | yes | no |
| `repository` (stars, commit age) | no | no | no |

No pack reads `repository`. This follows the prior art (G8): no system blocks
on a project score alone.

#### Go modules as a signal set for providers

An OpenTofu provider is a Go program, and a Pulumi plugin often is. A Go
binary holds its module path, its version and the modules that it links in its
build information. The server reads it from the stored bytes, so those facts
are `computed`. The new group `gomodule` holds:

| Field | Origin | What a person can fake or what it misses |
| --- | --- | --- |
| `modulePath`, `moduleVersion`, the list of linked modules with their versions and sums, from the build information of the binary | computed | A build can lie about its source when the builder is not trusted. The binary says what the builder wrote. |
| `sumdbIncluded` (the module and version have a proof in `sum.golang.org`, and the hash equals the hash of the source module) | host-claim, or computed when the server checks the inclusion and consistency proofs itself | Inclusion shows the same bytes for everyone. It does not show that the code is safe. A malicious first publish is included. A private module is never in the database. |
| `retracted`, `deprecated` (from the `go.mod` of a newer version) | host-claim | The author controls both marks. An attacker who owns the module can omit them. |
| `govulncheck` result: mode (`source` or `binary`), database version, findings, reachable findings | computed (scan of the stored bytes) | The database has known, curated problems only, no severity label and no malware feed. In `binary` mode there is no call graph, so a finding may not be reachable. |
| `proxyTime` (the `Time` of the `.info` answer) | host-claim | The author sets it for a direct fetch. A policy on age reads `version.firstSeenDays` as well. |

Two rules follow from the Go documents. First, a gate reads the findings of
govulncheck and not its exit code, because the exit code is 0 with the JSON
output. Second, the group is `available: false` for a package that is not a Go
binary, and a gate must test the flag first (ADR 226 decision 3). The module
path rule of Go (a major version suffix, case-encoded paths) is also a
`package` check: the server rejects an address that differs from another only
by case. This is integrity input checking (section F), not provenance.

#### Waivers for third-party packages

A waiver is not a new object. It is an approval (ADR 225) for one version of
one address, with the properties from the prior art (G2 to G4):

- the requester gives a reason, and the approvers are a named group;
- the scope is one address and one version, and a wider scope needs a
  `namespace:approve-package` or `installation:approve-package` grant and a
  warning in the record;
- the approval rule sets a maximum age in days, and the effective value shows
  in the API (no magic);
- at the end, the package goes back to the state "waits for a person", the
  job decision point shows a `warn`, and the audit log has an event. The
  package does not stay in use unseen (the Sonatype failure);
- the decision record names the rule, the approvers, the reasons and the
  facts of the time.

### Two kinds of package: first-party and third-party (owner, 2026-11-07)

The owner's words: "I would make a distinction between public and private
packages. Teams often don't impose the same strict rules on their own code
because they're by definition not SOUP." ADR 351 decision 6 keeps one
mechanism, the same facts and the same gates, and adds a fact and a default.

- **The fact.** The group `ownership` has the value `first-party` or
  `third-party`. A `first-party` package is built and published by the
  organization in its own private registry or namespace. A `third-party`
  package is public or other outside code. It is SOUP in the regulated sense.
- **How the server knows.** The value is `computed` from the records of the
  installation, not claimed. A package is `first-party` only when all three
  are true: it is in a private registry or namespace of this installation
  (`source.kind = private`), a principal uploaded it through the publish
  route, and a grant of that principal allowed the upload. A package that the
  server fetched from an upstream host is `third-party`, also when its
  address or its publisher looks like the organization. The group shows the
  registry, the namespace, the principal and the grant. A field in the
  package that says "ours" has no effect. When a record is missing, the value
  is `third-party`.
- **The default.** A `first-party` package goes through the review flow that
  the team already has: the review request and the gates that the team
  already has. OpenDustpan adds no provenance policy for it. A `third-party`
  package goes through the provenance policy of the administrator. The
  example policies above and in the starter packs are policies for
  `third-party` packages.
- **The choice of the administrator.** An organization may apply strict rules
  to its own packages too. The administrator writes a gate for
  `package.admit` that reads `ownership` (for example, `first-party` packages
  need two approvers, or a build attestation as an `external` check). This is
  the choice of the administrator, as in the first owner decision. The server
  does not do it by itself.
- **No magic.** The effective policy shows, for each kind, which gates apply.
  The decision record of an admission holds the `ownership` group like any
  other group.

### Policy at a unit

A gate for `package.admit` at the scope of a namespace or an installation
decides whether a package enters the allowlist. A different question is
whether a unit may use an admitted package. For example, a regulated
namespace admits a provider, and only the units of one collection may use it.
The job record (layer 4) gives the packages that a job used, and a gate at the
decision point of the job (ADR 224) can read the list. This RFC does not add
a fact to the job decision points. Question 1 covers the amendment of the
decision point.

### "Why": the answer names the policy and the facts

An admission, a refusal and a use each have a decision record (ADR 229) that
names the gate, the rule, the version of the schema, and the whole fact
document with the origin of each group. The API call "why was this package
admitted" reads that record. A request for the **effective policy** of a
namespace lists the gates that apply to `package.admit`, from the installation
and from the namespace, and shows which gate wins. No part of the answer
comes from code. If code rules stay (the four rules of section C), they are
in the catalog of code rules.

## What this means for the private registry and for sweep plugins

One mechanism serves the three cases, with the `ownership` default of the section above. In each case the artifact is an archive set
(ADR 177), the facts have the same groups, and the administrator writes the
policy with the same gates.

**The private registry (ADR 269, RFC 101).**

- A team upload is a package with `source.kind = private`. Its signals come
  from our own records: the `publisher` is the principal who uploaded it,
  with the grant that allowed it (`computed`). The `signature` is checked
  against the signing keys that the namespace registered (ADR 269
  decision 9). There are no `repository` signals unless the team posts them
  as an `external` check, for example a build attestation from its CI.
- Today (ADR 269) an upload by a holder of the publish action is a published
  version. This RFC keeps that. An administrator who wants a review before a
  version becomes `published` (ADR 345) writes a gate for `package.admit` that
  reads `ownership` (or `source.kind`). Without such a gate, the review flow
  that the team already has (the review request and its gates) is the review
  of a `first-party` package, and the grants of the uploader decide who may
  publish. The provenance policy does not apply to it by default (owner,
  2026-11-07). The version states of ADR 345 stay the life
  cycle of a version. A revoked package is revoked for a private and a public
  package alike.
- ADR 269 decision 10 puts the allowlist of SHA-256 sums into the curation of
  public packages. This RFC keeps that. The allowlist is the record of the
  admission, and an enterprise policy can read it.
- The conformance suite and the storage of RFC 101 are shared. The upload
  route of RFC 101 and the fetch of this RFC end in the same record.

**Sweep plugins (RFC 123, PR #2572).** A sweep plugin is third-party code that
runs on a runner with a cloud credential, so it needs a trust policy of the
same kind. RFC 123 proposes that a plugin runs only with a valid signature of
the project key or of a key that the operator adds. With this decision:

- An admitted sweep plugin is a package of the kind `sweep-plugin`. Its
  release is the archive set of RFC 123 option C1. The `package.admit` facts
  hold its signature, the key, the publisher, the repository signals, the scan
  and the external checks.
- **Integrity stays in code**, in both RFCs: the launcher runs only the bytes
  with the admitted SHA-256, starts them from a file descriptor, and refuses
  any difference. A plugin whose bytes differ from the admitted record never
  runs.
- **Which signatures count is policy.** RFC 123 reads "a plugin with no valid
  signature never runs" as one fixed rule. Under this decision the fixed part
  is the integrity check. The rule "only the project key" is the default
  starter policy of an installation that applies the Enterprise pack. A
  startup may admit a plugin that is `unsigned`. RFC 123 needs this
  edit before it is accepted. This RFC does not edit PR #2572.
- A sweep plugin runs with read-only, short-lived credentials and a sandbox
  (RFC 123 options D and security table). Those are limits on the effect of a
  plugin. They are not provenance, and they stay in every policy.
- The trusted keys of an installation are records with a holder, a creator
  and a time. An administrator lists, adds and removes them (no magic).

## One-way doors

A one-way door is hard to undo after a release. These are the ones in this
design, in the order of the cost of a mistake:

1. **The fact schema `package.admit/1.0`: names and types of the fields.**
   Administrators store gates against it. A removed or renamed field is a new
   major version (ADR 226 decision 2), and every stored gate needs a check
   and a migration. Adding a field is cheap. So the first release has fewer
   fields rather than more.
2. **The origin vocabulary** (`computed`, `host-claim`, `reporter-claim`) and
   the rule that there is no combined score. Policies and the console depend
   on both.
3. **The decision point `package.admit` itself**, and the `kind` values
   (`opentofu-provider`, `opentofu-module`, `pulumi-plugin`, and the kinds for
   a private upload and a sweep plugin). They are in the decision records for
   ever. The amendments of ADRs 224, 225, 226 and 229 are accepted law.
4. **The shape of the decision record** of an admission. A regulated
   organization keeps it as evidence for years, and a change cannot rewrite it.
5. **The external-check API**: the route, the body, and the names of the
   status values. Scanners and CI jobs of others call it, and a break reaches
   code that we cannot change (as with the tool-compatible APIs).
6. **The key of a record**: kind, upstream host, address, version. Section A
   already says so.
7. **The two protocol routes that the tools store** (`/tofu/mirror/v1/...` and
   `/pulumi/plugins/v1/cap/...`) in the CLI configurations (ADR 210
   decision 2).

Two-way doors: the list of signals in a group beyond the first release, the
content of the starter packs, the thresholds in them, the retention and quota
values, the package signing key format (URLs live 10 minutes), and the
storage layout.

## Open questions

The earlier 16 questions are answered in the section "History: proposal of
2026-10-30". The owner's decision of 2026-11-07 closes the questions about
verdicts: the minimum age (10), the scanner revoking (9), and the final gate
(2, in part). Five questions remain.

1. **One decision point for every kind of package?** Does `package.admit`
   become a seventh decision point (ADR 224) with the amendments of option C.1
   and the fact groups above, for OpenTofu providers and modules, Pulumi
   resource plugins, private uploads (optional gate) and sweep plugins? The
   recommendation: yes, one point, with no built-in final gate. The
   alternative is a point for each kind, which repeats the amendments and
   splits the policy of an organization into three.
2. **When no policy decides, who decides?** The recommendation: the request
   waits for a person who holds the approve action, and the requester cannot
   approve the own request unless the installation setting
   `packageAdmission.authorMayApprove` is `true` (default `false`, shown in the
   API, a sensitive change with a warning). The alternative: a request from a
   holder of the admit action is admitted at once when no gate matches. It is
   faster for a small team, and it makes "no policy" mean "allowed".
3. **Which signals come in slice 1?** The recommendation: only the signals
   that the server computes (`package`, `signature`, `version.firstSeenDays`,
   the `vulnerabilities` scan of the stored bytes), plus the external-check
   API. The signals from a forge (`publisher`, `repository`, stars, last
   commit) follow in the next slice, because each needs an outbound read and
   an upstream allowlist entry, and each can be faked. The alternative: all
   groups in slice 1.
4. **Where do the example policies live?** The recommendation: in a guide in
   `docs/guides/` and as data in the repository, and a request creates a
   copy in `evaluate` mode (ADR 229) for the administrator to edit and turn
   on. The alternative: the guide only, and the administrator types the
   gates.
5. **Does an approval expire?** The prior art says yes (Curation 1 to 365 days,
   Sonatype a duration, with advice to use short ones), and Sonatype shows the
   failure of an expiry that does not re-open the item. The recommendation: an
   approval rule sets a maximum number of days (the shortest value wins), and at
   the end the package goes back to "waits for a person", the jobs that use it
   get a `warn`, and the audit log has an event. ADR 225 has no expiry today, so
   this is an amendment. The alternative: no expiry, as in Cloudsmith, with a
   report of old approvals for a periodic review.

## History: proposal of 2026-10-30

This section answers the 16 earlier questions. The owner's decision of
2026-11-07 changes rows 2, 9 and 10, and the table marks them. RFC 10 holds
the work items of the whole registry theme (Milestone 6), with this RFC as
its curation part.

### New facts (2026-10-30)

Each fact has a tag: **[fetched]** means that the page or the source file
was read on 2026-10-30, **[not fetched]** means a search snippet only.

- The network mirror protocol has no signature field. In
  `internal/getproviders/http_mirror_source.go`, a package from a mirror
  receives only a hash check (`NewPackageHashAuthentication`). The examples of
  the protocol page use `h1:` only.
  <https://opentofu.org/docs/internals/provider-network-mirror-protocol/>
  [fetched],
  <https://raw.githubusercontent.com/opentofu/opentofu/main/internal/getproviders/http_mirror_source.go>
  [fetched]. So the version document of the mirror gives `h1:` for each
  archive. The lock-file route gives `zh:` too, because `zh:` is the
  SHA-256 of the zip that the server keeps.
- A lock entry from a mirror holds "the checksums reported by the mirror,
  rather than the origin registry's official checksums".
  <https://opentofu.org/docs/cli/commands/providers/lock/> [fetched]
- `PULUMI_PLUGIN_DOWNLOAD_URL_OVERRIDES` is defined in
  `sdk/go/common/env/env.go`, and the code applies an override after it
  resolves an explicit `pluginDownloadURL`.
  <https://raw.githubusercontent.com/pulumi/pulumi/master/sdk/go/common/env/env.go>
  [fetched]. An older pull request text says the opposite
  <https://deepwiki.com/pulumi/pulumi/4.1-plugin-management> [not fetched],
  so the e2e step checks it with the real CLI.
- JFrog Curation blocks an "immature version" younger than a number of
  days, and Terraform is not in its list of supported technologies.
  <https://docs.jfrog.com/security/docs/list-of-available-conditions>
  [fetched], <https://docs.jfrog.com/security/docs/supported-technologies>
  [fetched]. A blocked download answers 403.
  <https://docs.jfrog.com/security/docs/how-to-manage-virtual-repository-behavior-and-curation-in-jfrog-xray>
  [fetched]
- Artifactory serves Terraform and OpenTofu providers as a network mirror
  and, from 7.125, through the provider registry protocol. With a mirror,
  the CLI "relies on the original source for GPG signature trust".
  <https://docs.jfrog.com/artifactory/docs/terraform-opentofu-and-terraform-backend-repositories>
  [fetched]
- Nexus Repository has Terraform repositories from 3.88, a proxy of
  `registry.opentofu.org` from 3.94, and the provider registry protocol. No
  network mirror was found.
  <https://help.sonatype.com/en/terraform-repositories.html> [fetched].
  Nexus Firewall quarantines only new components, and when the Firewall
  service is not available, new requests stay in quarantine until a check
  runs. <https://help.sonatype.com/en/firewall-quarantine.html> [fetched]
- A minimum release age: Renovate `minimumReleaseAge` needs a release
  timestamp from the datasource. pnpm `minimumReleaseAge` defaults to 1440
  minutes. npm `min-release-age` is in days. uv `exclude-newer` takes a
  date or a duration.
  <https://raw.githubusercontent.com/renovatebot/renovate/main/docs/usage/configuration-options.md>
  [fetched], <https://pnpm.io/settings/dependency-resolution> [fetched],
  <https://docs.npmjs.com/cli/v11/using-npm/config> [fetched],
  <https://docs.astral.sh/uv/reference/settings/> [fetched]

### Answers

| # | Question | Answer |
| --- | --- | --- |
| 1 | The names | Keep vetted package, package admission and package mirror. They do not clash with the glossary, and RFC 10 uses them too. |
| 2 | `package.admit` as a seventh decision point | **Changed on 2026-11-07.** Still an owner decision (it amends ADRs 224, 225, 226 and 229), now question 1. Option C.1 with no built-in final gate: the administrator writes the gates. |
| 3 | Installation admissions that namespaces inherit | Yes. A namespace can revoke an inherited package for itself. It cannot admit a package that the installation revoked. |
| 4 | OpenPGP | Decided. The owner approved `github.com/ProtonMail/go-crypto` on 2026-10-20 (issue 1293, which also asks for an ADR that records it), and `pkg/registry/pgpverify` uses it. So slice 1 checks the upstream signature with that adapter, and the signature result is `verified`, `unsigned`, `unknown-key` or `failed`, never `not-checked`. An earlier version of this proposal asked the owner again (D2). That question is withdrawn. |
| 5 | Who fetches | The server, through `pkg/egress` with its own allowlist of upstream hosts, off until set (G.1). The upload of a person for an air gap (G.3) in the same milestone. A runner job only for git module sources (G.2, answer 7). |
| 6 | Layer 4 | A failure before the plan, in the slice after slice 1. A warning teaches teams to ignore it. |
| 7 | Modules | Both. Modules from an upstream module registry and `https` archives in the server fetch. Modules from git at a full commit through a runner job of the kind `package-fetch`, in a sandbox, after the server fetch works. Most public modules use git sources, so without it the feature serves few modules. |
| 8 | Layer 5 | The audit finding that does not block (option 1). |
| 9 | Can a scanner revoke | No. A scanner report gives facts and a finding. A person revokes. Still holds. A policy may deny an admission on a finding, and that is the policy of the administrator. |
| 10 | Minimum age | **Changed on 2026-11-07.** A signal, not a rule. `version.ageDays` and `version.firstSeenDays` are facts with the origin `host-claim` and `computed`. The starter packs may read them. No built-in gate asks for 14 days. |
| 11 | Re-signing | No, as ADR 269 decision 7 decides for private releases. |
| 12 | RFC 10 and this RFC | RFC 10 keeps private publishing, the version life cycle and the order of the work. This RFC is the curation part. |
| 13 | Pending storage | 10 GiB of pending blobs for each namespace, an installation setting. A pending request expires after 7 days. |
| 14 | The lock-file and checksums routes | Under `/api/dustpan/v1/namespaces/{namespace}/packages/`, because a person and a CI call them, not the CLI. The format code stays in the tool package: `pkg/workspace` gives the lock-file text and `pkg/stack` the `checksums` map, as pure functions. The route calls them through a port that the wiring fills, so no adapter calls another (ADR 12). |
| 15 | The rotate route | A rotation is a create: `POST /api/dustpan/v1/package-keys` makes the next key, and `DELETE /api/dustpan/v1/package-keys/{id}` ends an old key at once. Both need `installation:manage-packages`, a fresh authentication and a reason. |
| 16 | The storage of the package signing key | A secret item of the scope `@server`, under the rules of ADR 129 decision 10, with no route that lists or reads it. The text of section B that says "not a secret item" is replaced. The denylist of deleted key fingerprints stays outside the backup. |

The work items are in RFC 10, "Proposal (2026-10-30)".

## Proposed glossary lines

**Provenance signal**:
A fact about a package that the server computes or stores, with its origin
(`computed`, `host-claim` or `reporter-claim`) and the time of its read. A
signal never decides. A gate that an administrator writes reads signals.
_Avoid_: Trust score, rating, reputation

**Vetted package**:
One version of an OpenTofu provider, an OpenTofu module, a Pulumi resource
plugin or a sweep plugin that a namespace admitted, with an archive and its hashes for each
platform, and the record of its admission.
_Avoid_: Approved provider, artifact (that is a plan or a log of a job), dependency

**Package admission**:
The change that adds a vetted package to the allowlist of a namespace. It
meets the gates of `package.admit`.
_Avoid_: Import, approval (alone)

**Package mirror**:
The routes that serve the vetted packages of a namespace through the
protocols of each tool: the provider network mirror and the module registry
for OpenTofu, and the plugin download for the Pulumi CLI.
_Avoid_: Registry (alone), proxy, cache

## Outcome

Partly decided. ADR 351 records the owner's decision of 2026-11-07: OpenDustpan
computes provenance signals and administrators write the policy. The decision
point, the fact schema and the rest of this RFC wait for the owner's answers.
