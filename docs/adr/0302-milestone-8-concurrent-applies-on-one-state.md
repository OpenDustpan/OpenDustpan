# 302. Milestone 8: concurrent applies on one state

Status: accepted · 2026-11-01. Decided by the project owner

Changed by:

- ADR 368 extends decision 6: the answers of RFC 129, except one principal for
  each job on the TFE-compatible path.
- ADR 369 extends decision 6: the actor of a job and its credential on the
  TFE-compatible path.

Changes:

- Extends ADR 280: milestone 8 follows milestone 7, and it uses the
  milestone 7 model of several instances on one database.

## Context

Today a unit takes one lock for the whole state. In a workspace of OpenTofu
or a stack of the Pulumi CLI, two teams that change two different parts must
take turns. A large workspace makes this wait long.

Research on 1 November 2026 gave three facts:

- A spike with OpenTofu 1.11.14 ran two applies at the same time on one
  state, and the two results merged.
- A merge behind the plain CLI protocol is not safe. The server sees the
  write set of an apply only at the upload, after the calls to the cloud
  provider ran. The server cannot refuse an overlap before the damage is
  done.
- A whole-unit lock does not stop two diverged branches from reverting each
  other. The rule of ADR 299, "apply only the latest commit", guards that
  case. A lock does not.

On 1 November 2026 the project owner decided the points below. Each decision
has the label "(owner)".

## Decision

1. **Concurrent applies are a feature of OpenDustpan (owner).** Concurrent
   applies on one state, also called resource-level locking, are a feature
   that OpenDustpan builds. OpenDustpan does not implement the protocol of
   any other product for this feature.
2. **The design is job-aware (owner).**
   - A runner job registers its saved plan as a claim before the apply.
   - The server refuses overlapping claims before anything runs. It always
     refuses an overlap. A runner job turns the refusal into the status
     "blocked". The local `dustpan` wrapper waits: it tries the claim again
     after the refusal until the blocking claim ends, or until a timeout.
   - The server serves each job the state version that the plan used
     as its base.
   - The server merges each upload one resource instance at a time.
   - Work that the server cannot prove disjoint takes turns. The research
     gives six cases that keep the whole-unit lock: data sources, providers
     that take their settings from resources, root outputs, client-encrypted
     state, a version or lineage mismatch, and a local CLI run without the
     wrapper of decision 4. These six cases are the starting set under the
     owner's rule that the rules are strict in the first design. The RFC of decision 6
     may add a case. It may not remove one without a decision of the
     project owner.
3. **The order of tools (owner).** OpenTofu is first. The Pulumi CLI follows
   after its own spike. Splitting a plan with `-target` comes later. It is a
   speed feature only, on top of the claim model. It is not part of the
   first design.
4. **A local CLI wrapper (owner).** The `dustpan` CLI wraps the real `tofu`
   or `pulumi` CLI on the machine of the user, in the same way as the runner
   wrapper does. A local run can then take part: it registers claims, and it
   can use the other features that today work only in OpenDustpan runners.
5. **Milestone 8 (owner).** These decisions form milestone 8, after
   milestone 7. The first task of milestone 8 is a pre-flight: an
   inventory of each feature that works only inside OpenDustpan runners
   today, because it needs the CLI wrapper. Milestone 8 ports each of
   these features to the local wrapper. The inventory can mark an entry that
   cannot be ported, with the reason. The project owner decides what
   happens to such an entry. This ADR does not change the release gate of
   ADR 279.
6. **The design order (owner).** An RFC for concurrent applies comes first.
   The code of milestone 8 comes after the project owner accepts that
   RFC as an ADR. The scope of the RFC is:
   - A change to decision 3 of ADR 26: under a claim, the server assigns the
     serial of a state version, not the client.
   - A parent pointer and a claim ID for each state version.
   - A fence for each claim.
   - One principal for each job on the TFE-compatible path.
   - The retry interval and the timeout of the local wrapper.
   - A change to the rule of ADR 299 that a replacement is never disjoint.
     The RFC proposes a closure test in its place.

   This ADR does not decide these points. The RFC answers them.

## The milestone

`docs/roadmap.md` holds the goal, the work items and the exit test of
milestone 8. The exit test uses two teams that apply to disjoint parts of
one workspace at the same time through OpenDustpan runners. A local
`dustpan` CLI run of a third change waits on an overlap, and runs at the same
time when the claims do not overlap.

## Consequences

- The roadmap gets milestone 8 after milestone 7.
- The RFC for concurrent applies is the next design work. No code of
  claims, merges or the wrapper exists before the ADR that accepts it.
- The tool-compatible APIs keep the shapes that the real CLIs expect. A
  claim is a feature of OpenDustpan, and a CLI that does not use the wrapper
  keeps the whole-unit lock.
- ADR 26 decision 3 and the ADR 299 rule on replacements stay as they are
  until the ADR that accepts the RFC changes them.
- The inventory can add work to milestone 8: each runner-only feature that
  it finds needs a port to the local wrapper.
- The first release gate does not change (ADR 279).
