# 22. Apache License 2.0

Status: accepted · 2026-10-10

## Context

The README said that the license was not chosen. Issue
[#30](https://github.com/OpenDustpan/OpenDustpan/issues/30) asks which
license the server takes. The research is in
[`docs/research/licenses.md`](../research/licenses.md).

The root package `dustpan` is an SDK that other companies import into their
own programs (ADR 2). So the license affects more than the binary. Every
dependency in `go.mod` allows each candidate license. The choice depends on
goals, not on limits.

The project owner delegated this decision to an agent session on 2026-10-10.
The owner allowed the reviewed recommendation of the agent to become the
decision. The owner can supersede it with a new ADR.

## Decision

- **OpenDustpan is under the Apache License 2.0.** The `LICENSE` file at the
  root holds the unmodified text.
- **Contributions use the Developer Certificate of Origin.** A contributor
  signs off each commit with `git commit -s`. There is no CLA.
- **The Console takes the same license.** The Console repository
  (`OpenDustpan/console`) is separate (ADR 8). A follow-up change in that
  repository sets the license.

Reasons:

- Apache-2.0 lets a company embed the SDK in a closed product.
- It has an express patent grant.
- The Pulumi CLI, Atlantis and Terrakube use it.
- Every dependency allows it.
- The one source-available move in this field, the Terraform BSL change,
  caused a fork. A license change after adoption has a visible cost.

## Consequences

- Relicensing gets hard when outside contributions arrive without a CLA. This
  choice is meant to be final.
- MPL-2.0 was the main alternative (OpenTofu, OTF). The project did not choose
  it. Its file-level copyleft adds friction for embedders, with little gain for
  a server SDK.
- The project rejected AGPL-3.0, because it limits embedding. It rejected
  the source-available licenses (BSL, ELv2, FSL), because they are not open
  source.
- Apache-2.0 grants no trademark rights. A trademark policy, if the project
  needs one, is a separate decision.
- A follow-up change in the Console repository replaces `"license":
  "UNLICENSED"` with the same license and adds its `LICENSE` file.
- Contributors must sign off their commits.
