---
rfc: 10
title: Artifact registry and curated upstreams
status: draft
created: 2026-10-10
---

# RFC 10. Artifact registry and curated upstreams

Status: draft.

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

## Open questions

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

None yet.
