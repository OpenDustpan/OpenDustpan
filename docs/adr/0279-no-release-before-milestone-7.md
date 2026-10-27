# 279. No release before the exit tests of milestones 1 to 7 pass

Status: accepted · 2026-10-27. Decided by the project owner. Supersedes the "When" decision of ADR 21

## Context

ADR 21 put the first tagged release, `v0.1.0`, after milestone 2: the Pulumi
CLI and OpenTofu both run end to end on one node. ADR 33 extended the gate of
that release. The roadmap shows the release as a marker after milestone 2.

The exit tests of milestones 1, 2 and 3 pass. No `v*` tag exists. The roadmap now has six
milestones, and ADR 280 adds milestone 7, high availability on one database.
A shape that a tag ships keeps its upgrade path (ADR 72). So the content of
the first release decides which shapes the project must keep from then on.

On 27 October 2026 the project owner decided when the first release comes.

## Decision

1. **No release until milestones 1 to 7 pass.** The project makes no
   release and pushes no `v*` tag until the exit test of each of milestones
   1 to 7 in `docs/roadmap.md` passes. Milestone 7 is the milestone of
   ADR 280. (owner)
2. **The gate of ADR 21 stays.** The checklist of ADR 21 "Gate", with the
   extension of ADR 33, stays a part of the gate. Decision 1 adds to it. The
   version (`v0.1.0`), the artifacts, the console tag and the process of
   ADR 21 do not change. (owner)
3. **The roadmap marker moves.** In `docs/roadmap.md`, the marker of the
   first tagged release moves from after milestone 2 to after milestone 7.
   Its checklist gets one item for each milestone.

## Superseded decisions

| ADR | Decision | Superseded by | What changes |
| --- | --- | --- | --- |
| 21 | "When" | Decision 1 | The first tagged release comes after milestones 1 to 7, not after milestone 2. |

The other decisions of ADR 21 stay.

## Consequences

- The tool-compatible APIs stay contracts, as before (ADR 73). Before the
  first tag, the console and the tool-neutral APIs can still change and
  break, for a longer time than ADR 21 expected.
- No shape gets an upgrade path from a tag until the release. Until then,
  the collapse of untagged migrations before the next tag (ADR 73) is the
  rule for the migration files.
- The steps of the upgrade test and of `make mixed-version-test` that need a
  previous tag do not run until the first tag exists.
- Milestones 6 and 7 can run at the same time. Each one blocks the release.
- The README and the roadmap say that no release exists. They do not give a
  date.
