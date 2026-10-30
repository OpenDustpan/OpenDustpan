---
rfc: 10
title: Artifact registry and curated upstreams
status: discussing
created: 2026-10-10
---

# RFC 10. Artifact registry and curated upstreams

Status: discussing. The section "Proposal (2026-10-30)" is ready to accept,
with one owner decision (D3) here and one in RFC 93 (D1).

## Problem

Infrastructure code depends on artifacts that come from outside: OpenTofu
providers and modules, Pulumi resource plugins and component packages.
Teams also publish their own. Today they fetch public artifacts from the
internet at plan time, and host private ones somewhere else. Regulated teams
need two more things:

- **Curation.** Only artifacts that someone approved can enter the network.
- **Scanning.** Known vulnerabilities block or flag an artifact before use.

## What each tool fetches, and how

- **OpenTofu providers** come through the provider registry protocol, or a
  network mirror (`provider_installation { network_mirror { ... } }`). Both
  are documented HTTP protocols. Providers are signed, and OpenTofu checks
  the signatures and the checksums in its lock file.
- **OpenTofu modules** come through the module registry protocol, or straight
  from git.
- **Pulumi resource plugins** download from a URL: the plugin's
  `pluginDownloadURL`, or a mirror set for the CLI.
- **Pulumi components and packages** come from package managers (npm, PyPI,
  Go modules, NuGet) or from git.

So the registry serves standard protocols, and teams point their tools at it.
No tool needs a plugin (ADR 12).

## Proposal

- **Two parts, two roles** (RFC 7):
  - The **registry** serves artifacts to tools inside the network: the
    OpenTofu provider and module registry protocols, a provider network
    mirror, and plugin downloads for the Pulumi CLI.
  - The **upstream proxy** fetches from public sources, curates and scans.
    It can run in the DMZ. The registry pulls approved artifacts from it.
- **Storage is a port.** Artifact bytes go to the blob store by default.
  Teams that already run Artifactory, Nexus, or GitHub and GitLab package
  registries can point the registry at them as sources, or as storage. A
  source is where an artifact comes from. A store is where we keep it.
- **Curation.** An artifact enters the registry only by a rule: an allow
  list of namespaces and versions, a minimum age for new versions (ADR 7
  already applies this to our own dependencies), and a person's approval
  where the rule asks for one.
- **Scanning.** Most providers are Go binaries. `govulncheck -mode=binary`
  can report the known Go vulnerabilities whose code the binary contains.
  The proxy scans each provider binary when it fetches it, keeps the report
  with the artifact, and blocks or flags it by rule.
- **Provenance.** The registry keeps the upstream signature, the checksums
  and the scan report for each artifact, and shows them in the console.

## Limits of govulncheck

- It knows only Go code. Pulumi components in TypeScript or Python need
  another scanner, such as OSV-Scanner on their lock files.
- In binary mode it sees less than in source mode. Stripped or old binaries
  can hide symbols, so a clean report is weaker evidence than a clean source
  scan.
- It reports known vulnerabilities only. It is one signal for curation, not
  a security review.

## Options

1. **Registry and upstream proxy** (above).
2. **Point teams at their existing artifact managers.** Artifactory and
   Nexus already proxy many of these protocols. This adds nothing to
   OpenDustpan, and teams without those products get nothing.
3. **Registry only, no proxy.** Private artifacts only, no curation of public
   ones. It is smaller, and it misses the regulated-team case.

## Proposal (2026-10-30)

This section makes the rest of RFC 10 ready to accept, for Milestone 6. It
covers the curation of public packages (through RFC 93), the network
mirror, Pulumi resource plugins, modules, scanning, the version life cycle
and the registry files in a backup. It does not decide again what ADRs 257
and 269 decide.

### What is decided or built already

- ADR 257: the tools' own HTTP protocols, and no OCI endpoint now.
- ADR 269 decides these points:
  - Own providers come first, by an upload of the archive set of ADR 177.
  - The address host is the host of the server.
  - A namespace never reads the registry of another namespace.
  - The server does not sign again.
  - Module sources come by upload first, and from git tags second.
  - Each namespace registers its own signing keys.
  - The allowlist of SHA-256 sums belongs to curation (RFC 93).
  - A yank and a revoke both keep the record.
- Built: `pkg/registry` (the address, the release, the ports, the suite
  `registrytest`), `internal/registry/service` (the upload, the staged files,
  the publish and the reads), the provider network mirror of uploaded
  providers, and the copy of registry release files in a backup (pull
  request 1986).
- In progress in another session: registry modules by upload (M6-15).

### Facts

The facts of RFC 93, sections "Facts from primary sources" and "New facts
(2026-10-30)", hold. Each has a tag there. The products that teams use
today, in short:

- Artifactory serves a network mirror and the provider registry protocol
  for OpenTofu, and JFrog Curation does not list Terraform as a supported
  technology [fetched, links in RFC 93].
- Nexus Repository serves the provider registry protocol for OpenTofu from
  3.88, and Nexus Firewall quarantines new components and fails closed when
  its service is down [fetched, links in RFC 93].

So no common artifact manager curates OpenTofu providers today. Option 2
of this RFC ("point teams at their existing artifact managers") does not
give the curation part.

### Answers to the open questions

| Question | Answer |
| --- | --- |
| Which protocol first | Decided by ADR 269: the provider network mirror, by upload. Next: modules by upload (M6-15). Then the curated mirror of RFC 93. Then Pulumi resource plugins. |
| Does the registry sign again | No (ADR 269 decision 7). |
| How a team publishes a private provider | By upload (ADR 269 decision 2), and from a git tag that a runner builds (decision 7). Both go through the same checks. |
| One cache for each namespace, or one for the server | A copy for each namespace (RFC 93, "Who owns a blob", option 1), so the isolation of ADR 220 stays whole. |
| Which scanners | **Owner decision D3.** Recommended: no scanner in the server. A runner job of the kind `package-scan` runs a scanner image of the team, for example `govulncheck -mode=binary` or OSV-Scanner. It uploads a report in the model of RFC 90. The fact group `scan` of `package.admit` reads it. Option 2: a scanner in the server. It adds a tool, its database and its network access to the server. Option 3: no scanning in Milestone 6. |

### Decisions

1. **Pulumi resource plugins of a team.** A team uploads the Pulumi archive
   set of ADR 177 decision 5: one `tar.gz` for each target, and a checksums
   file of the SHA-256 of each archive. `pkg/registry` gets a second
   release shape, `registry.PluginRelease`, with the kind `pulumi-plugin`,
   as its package text already says. The upload checks the tar rules of
   RFC 93 section F, step 2, and the SHA-256 of each archive against the
   checksums file. The Pulumi CLI checks no signature, so a plugin release
   has no signature (RFC 93 facts). The address is the namespace and the
   plugin name.
2. **How the Pulumi CLI downloads a plugin of a team.** Through the routes
   `/pulumi/plugins/v1/cap/{capability}/...` of RFC 93 section B (the
   capability in a path segment, because the CLI sends no credentials). A
   runner job gets the capability from the launcher, at each phase. A
   person or a team CI calls `POST /api/dustpan/v1/namespaces/{namespace}/registry/plugin-download-urls`,
   with `namespace:download-packages`, and gets a capability that lives at
   most 12 hours, for `PULUMI_PLUGIN_DOWNLOAD_URL_OVERRIDES`. The same
   routes serve the vetted plugins of RFC 93. A checksums route gives the
   `checksums` map for the `packages` entries of `Pulumi.yaml` (RFC 93
   section D).

   The capability of a job has two layers that a capability of a person
   does not have: it needs a running job phase, and the job log redacts it.
   So the capability of a person gets its own layers, each with its own
   test:
   - **Bound.** The server stores a record for each capability: an ID with
     the prefix `pdu-`, the principal, the access token or the session
     that made it, the namespace, the expiry and the SHA-256 of the value.
     The value holds the ID, the key ID and a MAC. A value whose record does
     not exist, or whose namespace or principal differs from the record, is
     refused with 404 and an audit entry.
   - **Checked at each use.** The server checks the MAC, the expiry, that
     the record is not revoked, that the principal is enabled and still
     holds `namespace:download-packages` on the namespace, and that the
     release is not revoked.
   - **Revocable.** `DELETE .../registry/plugin-download-urls/{id}` revokes
     one, by its principal or a namespace administrator. A disable of the
     principal, and a revoke of the access token or the end of the session
     that made it, revoke each capability that it made. A principal has at
     most 20 live capabilities.
   - **Audited.** Each use writes an audit entry: the capability ID, the
     principal, the release and the platform, never the value.
   - **Kept out of logs.** The access log, the traces and the metric labels
     of the server hold the route pattern, never the path segment. No job
     log redacts this value, so this is the only layer for logs. A test
     sends a download and searches the access log, the traces and the
     metrics for the value.
3. **Modules from git tags.** After M6-15, the second source of ADR 269
   decision 8: a principal names a forge connection, a repository, a
   directory and a tag. The server gets the commit of the tag through the
   source pipeline (ADR 135, `source.fetch`), makes the module archive from
   the tree of that commit, and stores the commit and the SHA-256 with the
   release. A tag that moves later changes nothing: a version never changes
   (RFC 101 section E).
4. **The life cycle of a version.** This amends ADR 269 decision 11, which
   has a yank and a revoke, with two more states. A version has one of
   these states, and each change writes an audit entry with a reason:
   - `published`: in the index, served.
   - `deprecated`: in the index, served, with a message of at most 500
     characters. The API, the console and the job record of a runner job
     that installs it (RFC 93, layer 4) show the message. HCP Terraform
     shows such a warning in the runs of its consumers [fetched, link in
     RFC 56].
   - `update-required`: in the index, served, with a message and the lowest
     version that a unit must use. A runner job that installs it fails
     before the plan, at layer 4, with the message and that version. A
     download by a person or a team CI still works, so a lock file still
     works, and the server cannot see that run (RFC 93 section E).
   - `yanked` (ADR 269): out of the index, still served, so a lock file
     still works.
   - `revoked` (ADR 269): blocked at once.

   The yank and the revoke are decided and not built yet (issue 951).
   Work item R3 builds all five states.
5. **Which units use which version.** The job record of RFC 93 layer 4
   holds the address, the version and the hash of each provider and plugin
   that a runner job installed. The route
   `GET .../namespaces/{namespace}/registry/{kind}/{name}/versions/{version}/users`
   lists the units whose last runner job installed it. A unit that no runner
   job ran has no record, and the answer says so: the server never guesses
   from state (an OpenTofu state holds no provider version).
6. **Registry files in a backup.** The rule: a backup holds each blob that a
   readable record names, and it says in its manifest which blobs it did
   not copy.
   - Registry release files: copied, built (pull request 1986).
   - The archives of vetted packages, in the prefix of each namespace and in
     the staging prefix of the installation (RFC 93, "Who owns a blob"):
     copied, with the size and the MD5 of the stored bytes, as release
     files are. A revoked package that keeps its blobs for forensics
     (RFC 93 section B) is copied while its blobs exist.
   - Pulumi plugin archives (decision 1): copied.
   - Staged files of an upload, and the blobs of a pending admission: not
     copied. The manifest lists them as not copied. A restore deletes each
     staged row, and ends each pending admission `expired` with the reason
     `restored-without-files`. So a restore never brings back a pending
     admission with no bytes, and `backup.ErrBlobsNotBacked` keeps its
     meaning.
   - The package signing keys (RFC 93 answer 16): in the backup, sealed.
     The denylist of deleted key fingerprints is not in the backup.
   - A namespace backup (ADR 218 decision 4) holds the releases, the vetted
     package records and the archive copies of its namespace, and never a
     blob of the installation staging prefix.
   - **A restored blob is untrusted input** (ADR 250). The restore checks
     the SHA-256 of each registry and package blob against its record, and
     refuses the restore with `backup.ErrCorrupt` on a mismatch. The mirror
     routes check the SHA-256 of a blob again on the first read after a
     restore, as a second layer.

7. **Providers from git tags, built by a runner.** A namespace can save a
   **build recipe** for a provider: a forge connection, a repository, a tag
   pattern, a runner pool, an image and the steps (for example the
   commands of the build tool of ADR 177). A person with
   `registry:publish` asks for a build of one tag.
   - The build is not a job of a unit (ADR 100 decision 2), so it runs as a
     claim of a new pool purpose, `builds` (this amends ADR 130 decision
     3). A pool for builds claims only `registry.build.<pool ID>`.
   - The server gets the commit of the tag through the source pipeline (ADR
     135) and stores it. The launcher runs the steps on that snapshot.
   - The steps sign the checksums file with a key from a profile export
     (`files`) of the recipe. The server never holds the private key in
     its own store.
   - The launcher uploads the archive set through its session. The server
     then runs every check of an upload by a person: the names, the
     SHA-256 of each archive, and the signature with a trusted signing key
     of the namespace (ADR 269 decision 2). So a build can never skip a
     check.
   - The server checks `registry:publish` of the person at the start and
     again at the publish.
   - The release records the recipe, the tag, the commit and the build
     claim.
   - This also amends ADR 131 and ADR 132 (a new claim kind and its
     assignment).
8. **Tests that gate a version.** A namespace can save a **test recipe**
   for each registry kind: a runner pool of the purpose `builds`, an image
   and the steps, for example `tofu test` for a module, or the acceptance
   tests of the team with the staged provider in a filesystem mirror. With
   a recipe, a publish does not publish at once:
   - The release goes to the state `testing`. Only the test claim reads the
     staged files, through its session.
   - The test claim runs the steps. When each step passes, the release
     becomes `published`. A failure, a timeout or a lost claim makes the
     release `refused`, with the log of the claim. A refused version can
     never be published again. A fix is a new version (RFC 101 section E).
   - This changes the publish flow of ADR 269 and of
     `registry.Store.Publish`: a published version is still never changed,
     and a version in `testing` is not in the index.
   - HCP Terraform runs the tests of a module before it publishes a
     version (from the competitor review in the research doc "IaC and TACOS
     landscape") [not fetched].

### Ports

- `registry.PluginRelease` and a `PluginStore` port with its cases in
  `registrytest`, or one store with the kind in the key, as the adapter
  item decides with the suite first.
- `pkg/pkgmirror` of RFC 93 section A: the record of a vetted package, the
  `Store` port with the suite `pkgmirrortest`, the `Upstream` port with one
  adapter for each kind, and the `Formatter` port for the lock-file text
  and the `checksums` map (RFC 93 answer 14).
- `pkgmirror.Scanner` for a scan report, if owner decision D3 takes the
  runner job.
- The backup rules add rows to the table list and to the blob walk of
  `pkg/backup`. They add no port.

### End-to-end steps (real CLIs)

1. **Pulumi plugin of a team.** Upload the Pulumi archive set of a provider
   that the suite builds. Run `pulumi up` on a runner and on the test host
   with `PULUMI_PLUGIN_DOWNLOAD_URL_OVERRIDES` from the download URL route.
   Check that the CLI fetched the plugin from the server, also for a
   package whose schema names another `pluginDownloadURL`. Revoke the
   version, and check that a new download fails.
2. **Module from a git tag.** Publish a module from a tag of a repository
   of the fake GitHub host. Run `tofu init` and `tofu plan` with the server
   as the module registry. Move the tag, and check that the module version
   still gives the first commit.
3. **The curated mirror.** The e2e step of RFC 93 section I, with the
   OpenPGP check of `pkg/registry/pgpverify`.
4. **Builds and tests.** Save a build recipe and a test recipe. Push a tag
   to the fake GitHub host and ask for a build. Check that the release
   passes `testing`, becomes `published`, and that `tofu init` installs it.
   Make the test fail on a second tag, and check `refused`.
5. **Life cycle.** Set a version `update-required`, and check that a runner
   job that installs it fails before the plan, and that a download by a
   person still works. Yank a version and check the index.
6. **Backup.** Publish a provider, a module and a plugin, admit a vetted
   provider, and leave one upload staged and one admission pending. Back
   up, restore into a new installation, and run `tofu init` and
   `pulumi up` against the restored server. Check that the pending
   admission is `expired` with `restored-without-files`. Change one byte of
   a blob in a copy of the backup, and check that the restore refuses it.

### Security tests (ADR 250)

1. **Boundary.** A plugin archive with a hard link, a symlink, a device, a
   PAX path `../x`, a setuid bit, a case-fold duplicate and trailing gzip
   data. A checksums file with a name that is not in the upload. A tag name
   with `..` and a ref that is not a tag. A build recipe with a step that is
   not an argument list, and a tag pattern that matches a branch.
2. **Contain.** The server never unpacks a plugin to a disk. The archive
   URL and the capability are built from the record, never from the request.
   A build upload goes through the same check function as an upload by a
   person, and a test calls that function with the session of a build and a
   bad signature. The access log, the traces and the metric labels hold no
   capability value.
3. **Limit.** A gzip ratio over the limit, an archive over 512 MiB, the
   12-hour bound of a download capability, the 21st live capability of a
   principal, and a build or a test claim past its deadline (it ends
   `refused`).
4. **Authorize at use.** A capability of another namespace, of a revoked
   version, after its expiry, and with a deleted key. A capability of a
   person after a revoke, after a disable of the person, after a revoke of
   the access token that made it, and after a loss of
   `namespace:download-packages`. A build that the person lost
   `registry:publish` for between the start and the publish. A test claim
   that asks for the staged files of another release. A users list of a
   version for a caller with no `unit:read` on a unit: the unit is not
   named, and the answer counts it as hidden.
5. **Fail closed.** A restore of a changed blob, a restore of a backup whose
   manifest does not list a missing staged file, and a mirror read of a blob
   whose SHA-256 differs from its record. A test claim that ends with no
   result leaves the release `refused`, never `published`. A capability
   with no record gives 404 and an audit entry.

### Work items

Sizes and conflict groups as in RFC 111. M6-15 is registry modules by
upload.

| ID | Item | Size | Conflict group | Depends on |
| --- | --- | --- | --- | --- |
| R2 | Modules from git tags | M | `registry` | M6-15 |
| R3 | The life cycle: `deprecated`, `update-required`, `yanked`, `revoked`, with the layer 4 failure of `update-required` | M | `registry`, `store-migrations` | R9 for the layer 4 failure |
| R4 | The package signing key: the secret item, create and delete routes, the denylist, the backup row | M | `pkgmirror-keys`, `store-migrations` | none |
| R5 | `pkg/pkgmirror`: record, `Store`, `pkgmirrortest`, SQLite and PostgreSQL adapters | M | `pkgmirror`, `store-migrations` | none |
| R6 | Admission: `package.admit` with its built-in gate and rule. The server fetch through `pkg/egress`, with an upstream allowlist. The archive checks, the `h1:` hash, the signature check with `pgpverify`. The upload for an air gap | L | `pkgmirror`, `gate-facts` | R5, owner decision D1 |
| R7 | The curated network mirror routes, the signed archive URLs, the lock-file route | M | `api-tfe` | R4, R6 |
| R8 | Layer 1 in the launcher: the `provider_installation` block and the Pulumi overrides | S | `launcher` | R7, R11 |
| R9 | Layer 4: the job record report of the launcher and its check in the server | M | `launcher`, `job-start`, `store-migrations` | R8 |
| R10 | Layer 5: the audit finding after a write | S | `pkgmirror` | R6 |
| R11 | Pulumi resource plugins: `PluginRelease`, upload, capability routes, the download URL route with its five layers, the checksums route, admission of public plugins | L | `registry`, `api-pulumi`, `store-migrations` | R4, R6 |
| R12 | Modules in the curated mirror: the module registry routes for vetted modules, and the runner job `package-fetch` for git sources | L | `pkgmirror`, `runner-protocol` | R6 |
| R13 | Backups: the vetted package blobs, the plugin blobs, the staging rules at restore, the SHA-256 check at restore, the namespace backup | M | `backup` | R5, R11 |
| R14 | The scan job `package-scan` and the fact group `scan` | M | `pkgmirror`, `runner-protocol`, `store-migrations` | R6, owner decision D3 |
| R15 | The e2e steps above. The docs pages "Publish a Pulumi plugin", "Vetted providers, modules and plugins" and "Build and test registry versions". The backup guide | L | `e2e`, `docs` | R2, R3, R7, R11, R13, R17, R18 |
| R16 | The users route of a version | S | `registry` | R9 |
| R17 | Builds: the pool purpose `builds`, the build recipe with its store, the claim kind `registry.build.<pool ID>`, the upload through the session | L | `registry`, `runner-protocol`, `store-migrations` | R2 |
| R18 | Tests that gate a version: the test recipe, the states `testing` and `refused`, the change of the publish flow | M | `registry`, `store-migrations` | R17 |

Order: R4 and R5 first (parallel), R2 after M6-15, then R6 and R17, then
R7, R11 and R18, then R8, R9, R10, R12 and R13, then R3, R14 and R16, then
R15. There is no R1: the OpenPGP library is approved and built
(`pkg/registry/pgpverify`).

## Open questions

The section "Proposal (2026-10-30)" answers each question below.


- Which protocol first: the OpenTofu provider mirror (smallest, most
  requested) or the module registry?
- Does the registry sign what it serves again, with its own key, after
  curation?
- How does a team publish a private provider: an API, a CLI, or a git tag
  that a runner (RFC 6) builds?
- Does the proxy keep artifacts for each namespace, or share one cache for
  the server?
- Which scanners beyond govulncheck are built in, and which are adapters?

## Gaps from the competitor review

Added 2026-10-10 from the research doc "IaC and TACOS landscape". Not yet
worked into the proposal above.

- Publish modules and providers from git tags, with tests that gate a version
  (HCP Terraform, Spacelift, Harness, OTF).
- Version lifecycle: supported, update required, deprecated, revoked (HCP
  Terraform, Harness).
- Which units use which version, for upgrades (RFC 56).

## Outcome

Accepted in part as [ADR 257](../adr/0257-the-tools-own-registry-protocols-first-no-oci-endpoint.md)
on 2026-10-20: the tools' own HTTP protocols, and no OCI endpoint now. The
order of the protocols and the curation stay open.
