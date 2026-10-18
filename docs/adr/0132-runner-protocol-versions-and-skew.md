# 132. Versions of the runner protocol and the skew between releases

Status: proposed · 2026-10-18

## Context

The runners and the launchers of a team update on the schedule of the
team, not of the server. A hosted server updates on its own schedule.
[ADR 72](0072-scale-out-on-one-database.md) decision 11 lets binaries of
release R-2 to R of the server run together on one schema.

The project owner decided on 17 October 2026 that the protocol allows two
releases of skew between the server and a runner (owner). The project
allows breaking changes until 1.0, except for the Pulumi Cloud-compatible
and TFE-compatible APIs. The runner protocol is neither, so this ADR sets
its own rules.

## Decision

1. **The release of a runner is the release of its launcher.** A runner
   starts the launcher of its own release: the Kubernetes runner names the
   launcher image with its own release, and the machine runner ships the
   launcher binary in its own release archive. So one number describes
   both, and the server checks each.
2. **The skew rule (owner).** A server of release `X.Y.Z` accepts a runner
   or a launcher of release `X.Y.*`, `X.(Y-1).*` and `X.(Y-2).*`
   (`runner.CheckSkew`). A patch release never counts. The server refuses:
   - a client that is newer than the server,
   - a client more than two minor releases older,
   - a client of another major release.

   The refusal is the code `version-unsupported` with the status 426. Its
   body names the range that the server accepts, so the console and the
   runner log show the release to install.
3. **A server updates first.** A runner newer than its server is refused.
   So the order of an update is: the server, then the runners. The rolling
   deploy of the server keeps R-2 to R on one schema. The runner skew uses
   the same number, so a runner of R-2 works with every instance of a
   rolling deploy.
4. **Every request carries the release.** The header `Dustpan-Release`
   carries the release of the sender on each request, and of the server on
   each answer. The server records the release of a runner at each
   heartbeat. The console shows each runner with its release, and marks a
   runner that the next minor release of the server would refuse.
5. **One path for each protocol version.** The protocol version is the `1`
   of `/runner/v1` (`runner.ProtocolVersion`). Inside one path, a release
   only adds fields, paths, error codes and values. It never removes or
   renames one, and never changes the meaning of one. Each reader ignores
   a field that it does not know. A change that an old runner cannot read
   in this way needs `/runner/v2`, and the server then serves both paths
   for at least two minor releases.
6. **The assignment has a version.** The work of a phase or of a scan is
   the **assignment**: the steps, the source, the plan and the settings
   that the launcher reads. Its shape has a version
   (`runner.AssignmentVersion`). The server stores the version that it
   wrote as the version of the durable operation of the phase or scan. A
   runner sends the highest version that its launcher reads, and the claim
   never takes an operation above it (`durable.ClaimRequest.MaxVersions`).
   So a phase with a new field that an old launcher cannot honor waits for
   a new runner, instead of a run that drops the field. The console shows
   such a phase as "waiting for a runner of release X.Y or later".
7. **A new field that changes behavior raises the version.** A field that
   an old launcher can ignore with no change of what the phase does does
   not raise the assignment version. A field that changes what a step
   does, which credentials it gets or what it changes raises it. A
   table-driven test of the HTTP adapter will hold the list of fields of
   each version.
8. **The protocol tests run three releases.** The tests of each release
   run the runner and the launcher of the two minor releases before it
   against the new server, with the e2e suite of each tool.

## Consequences

- A team updates its runners at most every two minor releases of the
  server. A hosted server tells each team before a release that would
  refuse its runners.
- A field that changes behavior needs a new runner before a job can use it.
  The console shows the job that waits for it.
- Until 1.0, a minor release can still break the protocol through
  `/runner/v2`. The two-release window then applies to the old path.

## Open questions

- At 1.0 the major release changes. Does the server of 1.0 accept runners
  of 0.N and 0.(N-1), so that the first major release keeps the window?
  The rule of decision 2 refuses them today.
- Does the console offer the update of a Kubernetes runner, for example
  with the Helm command, or only show the release to install?
