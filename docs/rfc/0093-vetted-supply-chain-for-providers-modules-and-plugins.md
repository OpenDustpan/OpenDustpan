---
rfc: 93
title: A vetted supply chain for OpenTofu providers and modules and Pulumi resource plugins
status: discussing
created: 2026-10-20
---

# RFC 93. A vetted supply chain for OpenTofu providers and modules and Pulumi resource plugins

Status: discussing. Nothing in this RFC is decided. No code exists for it. The
owner answers the questions at the end (ADR 15). The section "Proposal
(2026-10-30): answers to the open questions" gives a recommendation for
each.

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
   `approval:supply-chain` makes the admission wait for the approvers of
   that rule. The fact schema `package.admit/1.0` holds the kind, the
   upstream host, the address, the version, the number of platforms, the
   signature result (`verified`, `unsigned`, `unknown-key`, `not-checked` or
   `failed`), the `principal` group of ADR 226 (`kind`, `credential`) and a
   `source` group with `kind` only (`api`, `cli` or `agent`), so a gate can
   refuse an admission that an agent or an access token asks for, a `keyChanged` fact (the signing key of this address differs
   from the key of an earlier admission), the SHA-256 of each archive, and
   two groups with an `available` flag as ADR 226 decision 3 asks: `age`
   (the age of the upstream version, see section D) and `scan` (for later,
   section H). A built-in gate and its approval rule are the answer to
   self-approval, see below.

   **Amendments that option C.1 makes.** The ADR that accepts C.1 changes:
   - ADR 224 decision 2: a seventh decision point, `package.admit`.
   - ADR 224 decision 4: the Cedar request has the namespace, or the
     installation, as its resource. It has no unit. One admission gives one
     request and one decision record.
   - ADR 224 decision 5: the gates that apply are the gates of the
     installation and of the namespace. A gate for `package.admit` at the
     scope of a collection or a unit is refused at save (`gate.ErrInvalid`),
     because no collection or unit is in the request.
   - ADR 224 decisions 13 and 14 apply with the namespace or the installation
     as the one resource: the combining runs once for it, and an error of a
     gate stops the admission (`gate.ErrRuleFailed`). A break-glass for a gate
     in error passes a final gate of the installation only with
     `installation:break-gate-error`.
   - ADR 229 decision 1: the record "for the change" is a record for the
     admission. It names the admission request in place of a job or change
     order, and it holds one resource entry (the namespace or the
     installation) in place of one entry for each unit.
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
     they cover the gates and the rule of `package.admit`. The new actions
     of this RFC are in its own table below, and they are not actions for
     the configuration of gates. The ADR keeps the text of those two rows
     and adds `package.admit` to the examples.

   **The built-in gate cannot be bypassed.** The installation holds a
   built-in starter gate for `package.admit` with `final: true`, and the
   built-in approval rule `package-admission` (one approval,
   `authorMayApprove: false`, approvers: `holders`). The approve action of
   the built-in rule is `installation:approve-package`, for the admission of
   a namespace and for the admission of the installation.
   Both the gate and the rule live at installation scope, as a final gate does
   (ADR 224 decision 12), so no narrower scope deletes or relaxes them. A
   rule with the ID `package-admission` at a narrower scope gives
   `gate.ErrFinal`. Four rules, each with its own test (proposal names):
   - **No gate means approval.** An admission that finds no gate in its
     chain does not admit. It waits for approval under the built-in rule.
     The code never reads "no gate" as "allowed". This fails closed
     (ADR 250). Test: `TestPackageAdmitNoGateNeedsApproval`.
   - **Managing gates is not approving.** A requester who holds
     `namespace:manage-gates` can save a gate for the namespace, and that
     gate cannot lower the final gate of the installation. The requester
     cannot approve the own admission, because the approve action is
     separate, the author is not eligible, and a job token, a service
     principal, an access token or an agent never approves (ADR 225
     decision 7). Test: `TestPackageAdmitRequesterWithManageGatesCannotApprove`,
     which also tries to save a namespace gate that returns `allow`, and to
     approve with the same session.
   - **No self-admit.** The route for an admission has no way to set the
     state to admitted. Only the engine, after the decision record, writes
     it. Test: `TestPackageAdmitRouteCannotSetAdmitted`.
   - **A namespace grant does not make an approver.** `holders` of the
     built-in rule resolves the approve action only at the scope of the
     rule, which is the installation. The approvers of every admission under
     the built-in rule, also the admission of a namespace, hold
     `installation:approve-package`. A namespace administrator can grant
     `namespace:approve-package`, and with a namespace principal home can
     create a second person, but that grant makes nobody an approver of the
     built-in rule. `namespace:approve-package` serves only the rules that a
     namespace saves itself. Test:
     `TestPackageAdmitNamespaceGrantDoesNotMakeApprover`.

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
   entry. This is the minimum for a small team. It is not a mode of option
   1, because option 1 never reads "no gate" as "allowed". A small team
   instead edits the built-in approval rule at the installation (one
   approver, who may be the author). A change of the approval rule that a
   final gate names counts as a change of the gate, and it makes the gate
   weaker (ADR 224 decision 12, effect 4). So the edit needs
   `installation:manage-gates` (ADR 230 decision 2), a `reason`, a fresh
   authentication (ADR 227) and an audit entry of the kind `change`. It does
   not need `installation:manage-packages`, so a person who manages
   admissions cannot weaken the control that governs them.

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
| `installation:manage-packages` | installation | request and revoke the admissions of the installation, and rotate the package signing key. It does not change gates or the built-in rule (that needs `installation:manage-gates`) |

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

**Signatures.** The standard library has no OpenPGP. In slice 1 there is no
OpenPGP dependency (question 4). The slice checks the SHA-256 of the archive
against the upstream checksums file, keeps the hashes, and needs an
approval. The record keeps the signature, the key ID and the fingerprint, and
the fact `keyChanged` tells a gate that the key of this address changed. The
signature result is `not-checked`, so a gate can ask for a stricter
approval. A later slice can add
`github.com/ProtonMail/go-crypto` after the owner reviews it (question 4). An upstream
that gives no key (the CLI allows this for `registry.opentofu.org` only,
and a WARN shows it) gives `unsigned`. The CLI of a team that sets
`OPENTOFU_ENFORCE_GPG_VALIDATION=true` still reads the mirror hashes
correctly, because the mirror gives hashes and not a signature (see the e2e
step).

**Age of the upstream version.** The protocols give no date of a release. The
network mirror protocol, the registry protocol and the module protocol have
no such field. A GitHub release has a date, but it is a source of the
upstream host, not of the protocol. So the first slice has no age. The fact
`age.available` is `false`, and `age.days` is 0, as ADR 226 decision 3 asks.
A later slice can add a source, for example the date of the release asset
on the upstream host, or the date of the first admission request of the
version in this installation. A gate that needs the age names `available`.

**Upstream provenance.** Where an upstream gives more (for example a build
attestation of a release), the record keeps it as an opaque blob, and a
later fact can read it. No check depends on it in the first slice.

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

### H. Later: scanning hooks

A scanner (for example `govulncheck -mode=binary` for Go providers, as
RFC 10 says, or a license scanner) reads a vetted package and gives a
report. The report uses the model of RFC 90: a scanner report blob with
scanner findings, bound to the vetted package instead of a plan. The
`scan` group of `package.admit` then reads its counts, so a gate can deny
an admission with a finding at or above a severity. A new scan of an
admitted package can flag it, and a person revokes it. No scan revokes by
itself in this RFC (question 9).

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
- The decision point `package.admit` (after its ADR), the built-in gate and
  rule, and the action `namespace:approve-package`.
- The server fetch from its own allowlist of upstream hosts, with the checks
  of section F, the SHA-256 against the checksums file, and the `h1:` hash
  for each platform. No OpenPGP check.
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

## Open questions

1. Are the names right: vetted package, package admission, package mirror?
   Or "approved provider" and a word for each tool?
2. Does `package.admit` become a seventh decision point with the amendments
   listed in option C.1, and a final installation gate that fails closed? Or
   a fixed approval (option C.2)? The recommendation is C.1.
3. Does the installation hold admissions that each namespace inherits, and
   can a namespace narrow them, as gates do?
4. Does slice 1 go without an OpenPGP dependency (the recommendation: the
   SHA-256 against the checksums file, the stored hashes, the approval, and
   the key ID, fingerprint and `keyChanged` fact), and a later review of
   `github.com/ProtonMail/go-crypto`?

   Status (2026-10-27): not answered. Slice 1 follows the recommendation,
   because section D says that the standard library has no OpenPGP. The
   later review needs a human decision. ADR 7 does not state a review rule.
   It pins dependencies and requires versions at least two weeks old.
   CLAUDE.md asks for a question before a new dependency. The owner decides
   the review before the owner fixes the scope of slice 2.
5. Does the server fetch from upstream through `pkg/egress` with its own
   allowlist, off until set (option G.1), with an upload for an air gap soon
   after (G.3), and a fetch by a runner only for git module sources
   (G.2)? That is the recommendation.
6. Is the job record (layer 4) a failure before the plan, or a warning in
   the first slice? The recommendation: a failure, in the slice after slice 1.
7. Modules: only from an upstream module registry, or also from git at a
   commit, fetched in a runner sandbox? Most public modules use git sources,
   so the answer decides how many modules the feature can serve.
8. Layer 5: an audit finding (recommended), or a fact at `state.write`
   against the current state only? Or neither, given what state shows?
9. Can a scanner report revoke a package by itself, or does a person always
   revoke?
10. A minimum age for a new upstream version before admission: what is the
    source of the date (the protocols give none), and is it a fact for a gate
    or a fixed rule?
11. Does the mirror sign what it serves again with a key of the namespace
    (RFC 10 asks the same)?
12. Does RFC 10 keep private publishing and the version lifecycle, and drop
    its curation part in favor of this RFC?
13. What quota for pending blobs does a namespace receive? How long does a
    pending request stay before it expires? (section B, pending storage)
14. Do the lock-file route and the Pulumi checksums route sit under
    `/api/dustpan/v1/namespaces/{namespace}/packages/`, as section D
    proposes? Section A says that a format that only one tool reads lives
    in that tool's adapter.
15. Is `POST /api/dustpan/v1/package-keys/rotate` the right shape? ADR 210
    decision 5 gives its custom methods on a resource with an `{id}`. This
    route acts on the collection.
16. Section B says that the server seals the package signing key in the scope
    `@server`, and that it is not a secret item. ADR 129 decision 10 says
    that a secret item of that scope is a secret item. Which storage is
    intended?

## Proposal (2026-10-30): answers to the open questions

This section answers the open questions above, so that the RFC is ready to
accept. RFC 10 holds the work items of the whole registry theme
(Milestone 6), with this RFC as its curation part. Three answers are owner
decisions: they amend accepted ADRs or add a dependency.

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
| 2 | `package.admit` as a seventh decision point | **Owner decision D1** (it amends ADRs 224, 225, 226 and 229). Recommended: option C.1, with the final installation gate that fails closed. |
| 3 | Installation admissions that namespaces inherit | Yes. A namespace can revoke an inherited package for itself. It cannot admit a package that the installation revoked. |
| 4 | OpenPGP | **Owner decision D2**, one question with RFC 101 question 6. Recommended: add `github.com/ProtonMail/go-crypto` (BSD-3-Clause) now, at a release at least two weeks old, behind the port `registry.SignatureVerifier` only. ADR 269 decision 2 already says that the signature is checked at upload, and that check needs the library. The mirror then sets the signature result `verified` or `failed`, not `not-checked`. |
| 5 | Who fetches | The server, through `pkg/egress` with its own allowlist of upstream hosts, off until set (G.1). The upload of a person for an air gap (G.3) in the same milestone. A runner job only for git module sources (G.2, answer 7). |
| 6 | Layer 4 | A failure before the plan, in the slice after slice 1. A warning teaches teams to ignore it. |
| 7 | Modules | Both. Modules from an upstream module registry and `https` archives in the server fetch. Modules from git at a full commit through a runner job of the kind `package-fetch`, in a sandbox, after the server fetch works. Most public modules use git sources, so without it the feature serves few modules. |
| 8 | Layer 5 | The audit finding that does not block (option 1). |
| 9 | Can a scanner revoke | No. A scanner report gives facts and a finding. A person revokes. |
| 10 | Minimum age | A fact for a gate, never a fixed rule. The source of `age.days` is the release date that the upstream host gives for the archive, for example the date of a GitHub release asset. The fetch records it. Else `age.available` is `false`. The starter pack holds a gate "approval when `age.available` is false or `age.days < 14`", as the immature-version rule of JFrog Curation does. |
| 11 | Re-signing | No, as ADR 269 decision 7 decides for private releases. |
| 12 | RFC 10 and this RFC | RFC 10 keeps private publishing, the version life cycle and the order of the work. This RFC is the curation part. |
| 13 | Pending storage | 10 GiB of pending blobs for each namespace, an installation setting. A pending request expires after 7 days. |
| 14 | The lock-file and checksums routes | Under `/api/dustpan/v1/namespaces/{namespace}/packages/`, because a person and a CI call them, not the CLI. The format code stays in the tool package: `pkg/workspace` gives the lock-file text and `pkg/stack` the `checksums` map, as pure functions. The route calls them through a port that the wiring fills, so no adapter calls another (ADR 12). |
| 15 | The rotate route | A rotation is a create: `POST /api/dustpan/v1/package-keys` makes the next key, and `DELETE /api/dustpan/v1/package-keys/{id}` ends an old key at once. Both need `installation:manage-packages`, a fresh authentication and a reason. |
| 16 | The storage of the package signing key | A secret item of the scope `@server`, under the rules of ADR 129 decision 10, with no route that lists or reads it. The text of section B that says "not a secret item" is replaced. The denylist of deleted key fingerprints stays outside the backup. |

The work items are in RFC 10, "Proposal (2026-10-30)".

## Proposed glossary lines

**Vetted package**:
One version of an OpenTofu provider, an OpenTofu module or a Pulumi resource
plugin that a namespace admitted, with an archive and its hashes for each
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

None yet.
