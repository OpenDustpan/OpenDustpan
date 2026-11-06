# 177. Building and releasing a provider wrapper

Status: accepted · 2026-10-18 · part of ADR 170. Open question answered in part by ADR 257

Changed by:

- ADR 257 extends: the second open question.
- ADR 316 extends: the OpenPGP library.

## Context

A team owns its provider wrappers. It builds them from a declaration, or
from Go code that uses the SDK, and releases them to its own users.
OpenTofu installs a provider from a provider registry, a network mirror or a
filesystem mirror. The Pulumi CLI installs a plugin from a download URL.
Each tool expects archives with names of its own.

ADR 176 makes one binary that serves both tools under two names.

## Decision

1. **A build tool, `dustpan-provider`.** It lives in
   `cmd/dustpan-provider` of `opendustpan/providerkit`. It has these
   commands:
   - `check <declaration>`: parse and check a declaration, and, when the
     declaration names an OpenAPI document, compare the two (ADR 171
     decision 12).
   - `derive --openapi <document>`: write a first declaration from chosen
     operations of an OpenAPI document.
   - `build <declaration> --version <semver>`: write a generated `main`
     package that embeds the declaration with `go:embed`, and build it with
     `go build` for each target.
   - `package`: write the release archives, the checksums and the Pulumi
     package schema.
2. **A Go build, not a generic binary.** The build makes a new binary that
   holds the declaration, with `-trimpath` and `CGO_ENABLED=0`. A generic
   binary that reads a declaration at run time would split one release into
   two files that can drift. The build needs a Go toolchain. An SDK
   provider is Go code anyway, so both kinds build the same way.
3. **Targets.** `linux` and `darwin`, each on `amd64` and `arm64`. Windows
   waits for the CLI engine (ADR 174).
4. **Archives for OpenTofu.** For each target, a zip archive named
   `terraform-provider-<name>_<version>_<os>_<arch>.zip`, with the binary
   `terraform-provider-<name>_v<version>`. Next to them,
   `terraform-provider-<name>_<version>_SHA256SUMS` and its detached
   signature. This is the layout that a registry, a network mirror and a
   filesystem mirror of OpenTofu read.
5. **Archives for Pulumi.** For each target, a gzipped tar archive named
   `pulumi-resource-<name>-v<version>-<os>-<arch>.tar.gz`, with the binary
   `pulumi-resource-<name>`. The package schema names the download URL of
   the team in `pluginDownloadURL`.
6. **One binary in both archives.** The two archives of one target hold the
   same bytes under two names. The checksums show this.
7. **Versions.** The version of a provider is the team's semantic version.
   The binary also records the version of `providerkit` that built it, and
   the schema version of the declaration, in its Go build information.
8. **Archives that the build can repeat.** The archives hold files in a
   fixed order, with fixed times and modes. Two builds of one commit with
   one toolchain give the same archives.

## Consequences

- A team releases a provider from its own CI, with one `go` toolchain and the
  build tool. It needs no service of OpenDustpan.
- A team that already runs a private provider registry or a network mirror
  for OpenTofu puts the archives there. A team with neither uses a
  filesystem mirror in the image of the team that its runner jobs use.
- Teams that use both tools release both archive sets from one build.

## Open questions

- Signing. OpenTofu checks a signature of the checksums file when a registry
  gives a signing key. Signing in Go needs an OpenPGP library, for example
  `github.com/ProtonMail/go-crypto` (BSD-3-Clause), which is a new
  dependency. The other way is to call `gpg` in the CI of the team. The
  project owner decides.
- Whether OpenDustpan serves the provider registry protocol of OpenTofu and
  a plugin download URL for the Pulumi CLI, for the providers that teams
  build. RFC 10 (artifact registry) is the place for that question.
- A container image with the build tool and a Go toolchain, for teams that
  do not keep Go in their CI.
