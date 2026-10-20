# 257. Providers and modules: the tools' own registry protocols first, and no OCI endpoint now

Status: accepted · 2026-10-20. Decided by the product manager under the owner's delegation of 20 October 2026. Answers the protocol part of the open question of ADR 170 and the second open question of ADR 177. Accepts part of RFC 10

## Context

Decided by the product manager under the owner's delegation of 20 October
2026, with the owner's decision rule.

ADR 170 and ADR 177 leave open whether OpenDustpan serves the provider
registry protocol of OpenTofu, and a plugin download URL of the Pulumi CLI,
for providers that teams build. RFC 10 asks which protocol comes first. RFC
93 (draft) proposes a mirror of vetted packages through the provider network
mirror protocol and the module registry protocol.

The facts on 2026-10-20:

- **HTTP protocols.** OpenTofu and the Terraform CLI speak the provider
  registry protocol (`providers.v1`), the module registry protocol
  (`modules.v1`, both through `/.well-known/terraform.json`) and the
  provider network mirror protocol, which needs no discovery
  (<https://opentofu.org/docs/internals/provider-registry-protocol/>,
  <https://opentofu.org/docs/internals/module-registry-protocol/>,
  <https://opentofu.org/docs/internals/provider-network-mirror-protocol/>).
  The TFE-compatible API already serves the discovery document.
- **OCI in OpenTofu.** 1.10.0 added modules from OCI (`oci://` sources)
  and OCI registries as a kind of provider mirror (`oci_mirror` in
  `provider_installation`). 1.13.0 added transient credentials tracked for each repository and
  `tofu providers lock -oci-mirror`. OCI cannot yet be the primary source
  of a provider. This needs a registry with OCI Distribution 1.1.0
  (<https://opentofu.org/docs/cli/oci_registries/>, the change logs of v1.10
  and v1.13).
- **OCI elsewhere.** The docs of the Terraform CLI list no OCI method for
  providers or modules. The Pulumi CLI fetches plugins from a
  `pluginDownloadURL` (`github://`, `gitlab://`, `http(s)://`, `git://`) or
  from its fallback sources, and has no OCI support
  (`sdk/go/common/workspace/plugins.go`).
- ADR 177 writes the archives, the checksums and the signature in the
  layout that a registry, a network mirror and a filesystem mirror read.

So the HTTP protocols serve every supported tool, and OCI serves OpenTofu
only. An OCI registry is a large protocol (OCI Distribution, with manifests,
blobs, tags and referrers), and teams that use OCI already run one.

## Decision

1. **The tools' own protocols.** When OpenDustpan serves providers and
   modules, it serves them through the HTTP protocols of the tools: the
   provider network mirror and the module registry protocol for OpenTofu,
   the provider registry protocol for providers that a team publishes
   under the host of the server, and the plugin download URL for the
   Pulumi CLI. RFC 10 and RFC 93 keep the order of these, the curation and
   the routes.
2. **No OCI endpoint now.** OpenDustpan serves no OCI Distribution API. It
   is not an OCI registry.
3. **OCI stays open to teams.** A team that runs an OCI registry keeps
   `oci_mirror` and `oci://` sources. Nothing in OpenDustpan stops them.
   The user documentation says that OpenDustpan does not see or curate
   what a tool fetches from such a registry.
4. **The build tool can write OCI later.** A command of `dustpan-provider`
   that pushes the archives of ADR 177 to an OCI registry of the team is a
   possible later change of ADR 177. It is not decided here.
5. **A new RFC reopens OCI.** An OCI endpoint comes back only through an
   RFC, when a supported tool other than OpenTofu fetches from OCI, or when
   OCI becomes the primary source of providers in OpenTofu.

Benchmark: AWS CodeArtifact and Google Artifact Registry serve each package
format through the native protocol of its client. Both serve OCI images
from a separate registry service. PCI DSS and ISO 27001 ask for a known
list of software sources, which the protocols of decision 1 give for every
supported tool.

## Consequences

- One set of routes serves OpenTofu and the Pulumi CLI, and a later
  Terraform CLI support (RFC 53) needs no other protocol.
- A team that standardized on OCI has no curation from OpenDustpan for the
  artifacts there. It keeps its registry and its own controls.
- The server needs no OCI code, no manifest parser and no tag store.
