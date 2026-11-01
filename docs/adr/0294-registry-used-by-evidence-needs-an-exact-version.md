# 294. Registry "Used by" evidence needs an exact version

Status: accepted · 2026-10-31. Decided by the project owner on 31 October 2026

## Context

The page of a registry release shows which stacks use it ("Used by"). A Pulumi checkpoint can name a provider with an exact version, or with a range or no version. A guess can tell an operator that a stack uses a release that it does not use.

## Decision

1. **Exact inputs only (owner).** A provider version shows in "Used by" only when the Pulumi checkpoint has an exact `inputs.version` for it. Otherwise the page shows "Version not known". The server never guesses a version, for example from a lock file or from the newest release. (owner)
2. **Self-service request (owner).** A request in the catalog (Self-service) shows the exact program version, with a label that says it is the program version. (owner)
3. **No mixed labels.** The page never shows a program version as a provider version, or the opposite.

## Consequences

- The "Used by" query reads `inputs.version` and does not compute a version.
- A test uses a checkpoint with no `inputs.version` and a checkpoint with a range, and expects "Version not known" for both.
