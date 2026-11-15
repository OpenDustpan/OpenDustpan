# Why profiles and blueprints stay the recommended path

This page says why profiles and blueprints stay the recommended path for new
setups, and where the Terragrunt path fits. It describes the server as it is
today.

OpenDustpan is pre-alpha. Do not keep production state on it.

## What each path gives

- **Profiles hold config, secrets and credentials with numbered revisions.**
  A profile names its revision at each use, so a reader can tell which values
  a run used. A Terragrunt unit holds its values in its own files, and the
  server never sees which revision a run used.
- **Blueprints compile before they store.** The server compiles every
  blueprint expression before it stores the version, and it refuses a version
  that does not compile. A Terragrunt run reaches the server only at apply
  time, so a mistake in a unit surfaces later.
- **One change covers many units.** A blueprint version applies the same
  reviewed change to every unit that uses it. A local run-all applies the
  files on the laptop, and each unit can carry a different edit without a
  record of the difference.
- **The units list is the only cross-unit view of a run-all.** Each Terragrunt
  unit reports to its own workspace only. The server records no object for
  the run itself, so there is nothing to approve, hold or blame as one run.

## Where the Terragrunt path fits

- **A team that already uses Terragrunt keeps its practice.** The local
  run-all needs no wrapper and no change to the Terragrunt configuration.
  Each unit keeps its own workspace, lock, state versions and history. See
  [Run Terragrunt locally](../guides/terragrunt.md#run-terragrunt-locally).
- **One workspace per unit directory.** Two unit directories that name one
  workspace merge their state into one chain under one lock. The guide
  requires one workspace name per unit directory.
- **New setups start with profiles and blueprints.** They give revisions,
  compile checks and one reviewed change across units. A team that already
  runs Terragrunt can move to them unit by unit. See
  [Publish a blueprint](../guides/publish-a-blueprint.md).

## Limits of this page

- The runner fan-out for Terragrunt is planned work. It adds a discovery job
  and one job per unit under a change order. This page covers the local path
  only.
