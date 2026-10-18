# 21. The first tagged release

Status: accepted · 2026-10-10 · gate extended by ADR 33 · the API rules of "Version" superseded in part by ADR 73

## Context

OpenDustpan has no release process. The repository has no git tag, no
`LICENSE` file, no Dockerfile, no release workflow and no `--version` flag. CI
builds and tests the code, but it does not cross-build for other platforms. The
roadmap holds a marker for the first tagged release, and it does not say what
the release is or what must be true before the tag. The ticket
[Define the first tagged release](https://github.com/OpenDustpan/OpenDustpan/issues/27)
holds the questions and a brief with a recommendation for each.

The project owner delegated this decision to an agent session on 2026-10-10.
The owner allowed the reviewed recommendation of the agent to become the
decision. The owner can supersede it with a new ADR.

ADR 8 puts the server and the console in separate repositories with their own
releases. RFC 35 (packaging) is a draft. It leaves open the container image
and where TLS ends. OpenTofu needs TLS (RFC 1).

## Decision

- **When.** The first tagged release comes after milestone 2: the Pulumi CLI
  and OpenTofu both run end to end on one node.
- **Version.** The tag is `v0.1.0`. It is pre-1.0. The API rules in
  `CLAUDE.md` apply from this tag. The project does not promise stable Go SDK
  ports or a stable database schema before 1.0. An upgrade from the previous
  tag must work, because migrations only go forward. For `v0.1.0`, the previous
  state is an empty database.
- **Artifacts.** The release ships binary archives that are cross-built, with
  checksums. It also ships one container image for linux/amd64 and
  linux/arm64. The image includes the console. The details of the image and of
  TLS follow RFC 35, which the project must accept first.
- **Console.** The console gets its own tag in its own repository (ADR 8).
  Each server image pins one console tag.
- **Gate.** Before the tag, all of these are true:
  - A `LICENSE` file is in both repositories (ticket "Choose the license").
  - The binary has a `--version` flag, and the build stamps the version.
  - An upgrade test starts from the previous tag. For `v0.1.0`, it starts from
    an empty database.
  - The install docs exist, and they include TLS.
  - The e2e suite passes for both tools on SQLite and on PostgreSQL.
- **Process.** A workflow that a tag triggers makes the release. A new release
  tool (for example GoReleaser) needs a "yes" from the owner first
  (`CLAUDE.md`: ask before adding a dependency).
- **Roadmap.** In `docs/roadmap.md`, the release stays a marker between
  milestones. This gate is its checklist.

## Consequences

- Open follow-ups, each for its own RFC or ticket:
  - The support policy and the release cadence: which versions get fixes.
  - The image registry, and signing of images and checksums (provenance, SBOM).
  - Whether RFC 35 splits into "image and TLS" for the first release and
    "Helm chart" for later.
  - The console version handshake (ADR 8), for example through
    `/api/capabilities`.
  - A license check of the dependencies before the tag.
  - Where the docs live (RFC 57), because it affects the install docs.
- The first release waits for the license, RFC 35 and the console release.
  Milestone 3 (backups, import, upgrade checks) is not in `v0.1.0`. The release
  notes must say what is missing.
- Forward-only migrations become a rule that users can rely on from `v0.1.0`.
  No downgrade path exists.
