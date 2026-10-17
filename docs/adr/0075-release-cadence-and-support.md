# 75. Release cadence and security support

Status: accepted · 2026-10-17

## Context

ADR 21 sets the first tag, `v0.1.0`, and leaves the release cadence and the
support policy as follow-ups. ADR 72 decision 11 lets binaries of release R-2
to R run together on one schema, and decision 10 tests an upgrade from each
supported old release.

The project owner decided: a minor release every 8 weeks, patch releases at
any time, and security fixes for the latest three minor versions.

## Decision

1. **A minor release every 8 weeks.** The project tags a minor release from
   `main` every 8 weeks. Before 1.0, a minor release is `v0.N.0`. The first
   one is `v0.1.0` (ADR 21), and its tag starts the 8-week count.
2. **Patch releases at any time.** A patch release `v0.N.P` comes from the
   release branch of its minor version. It holds fixes only, no new feature.
3. **Security fixes for the latest three minor versions.** When the project
   fixes a security problem, it releases a patch for each of the latest three
   minor versions. An older minor version gets no fix.
4. **Supported releases.** The latest three minor versions are the supported
   releases of ADR 72 decision 10. The full upgrade test starts from each of
   them.

## Consequences

- The three supported minor versions match the R-2 to R window of ADR 72
  decision 11. An operator on a supported release can upgrade to the newest
  release in one step.
- `SECURITY.md` names the supported versions when the first tag exists.
- Each patch release needs a release branch and the release workflow of
  ADR 21 for that branch.
- A minor release that misses its date does not move the next date.
- Breaking changes before 1.0 (ADR 73) still pass through these releases. The
  release notes of each minor release list them.
