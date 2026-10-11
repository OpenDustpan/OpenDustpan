# 29. Importing existing state

Status: accepted · 2026-10-11

## Context

[RFC 42](../rfc/0042-migration-import.md) asks for a careful move of state,
history and secrets from other backends into OpenDustpan. A brief of
2026-10-11 checked it against the code, the Pulumi CLI source and the OpenTofu
source. It informed this ADR.

In this repository, "migration" already means a SQL schema migration. This ADR
uses the word "import" for the move of a unit into OpenDustpan, and "source"
for the place the state comes from. `GLOSSARY.md` defines both words.

The server has all routes that an import needs for Pulumi and OpenTofu. It has
no import feature. The Pulumi CLI exports every version of a service stack and
encrypts the secrets again at `stack import`. OpenTofu `init -migrate-state`
moves state from a backend into a `cloud` block. It does not move state from a
`cloud` block. In an empty workspace, it also does not keep the lineage and the
serial. A stack config is not on the server. The server has no job runner
(RFC 6) and no audit log (RFC 22).

The project owner delegated this decision to an agent session on 2026-10-10.
The owner allowed the reviewed recommendation of the agent to become the
decision. The owner can supersede it with a new ADR.

## Decision

This ADR accepts the principles and the safety rules of RFC 42 and its first
sources. It does not accept an import resource, source adapters or a runner.

1. **An import is a tested guide.** In milestone 3, a team imports state with
   the CLI of each tool, from a guide that the e2e suite runs. The server gets
   no import engine, no source credentials and no source adapter. The guide is
   not a product feature, so ADR 17 asks no new API for it. An import resource,
   its dry-run report, source ports and runner execution wait for RFC 6, RFC 19
   and RFC 22.
2. **Four first sources.**
   - P1: a Pulumi service backend.
   - P2: a Pulumi DIY backend (file, S3, GCS, Azure Blob).
   - T1: any OpenTofu backend, or local state.
   - T2: HCP Terraform or TFE.

   The Kubernetes operators and other products are not supported.
3. **Pulumi secrets.** For P1, the guide pipes `pulumi stack export` of the
   source into `pulumi stack import` of the destination. It uses two saved
   logins and does not use `--show-secrets`. The CLI decrypts through the
   source and encrypts through OpenDustpan. No plain text file exists. The
   guide moves config secrets with `pulumi config --show-secrets --json` and
   `pulumi config set --secret`. For P2, the stack keeps its passphrase or KMS
   provider. The guide shows `pulumi stack change-secrets-provider` as an
   option.
4. **OpenTofu state.** For T1, the guide uses `tofu init -migrate-state
   -force-copy` for each workspace. The state moves as it is, and client-side
   encryption stays. For T2, the guide uses the TFE API of the source and of
   OpenDustpan. It replays state versions from the oldest to the newest, with
   their serial, lineage and MD5. `tofu init -migrate-state` cannot move state
   out of a `cloud` block.
5. **History.** For P1, the guide replays the last N versions as import
   updates. For T2, it replays the last N state versions. The team chooses N.
   The guide suggests 10. The original kind, time and user of a version are
   lost. P2 and T1 move the current state only, because the CLI has no history
   to export. A backfill API for history waits for RFC 22.
6. **Checks after an import.** For Pulumi, the exports of the source and the
   destination must match, and `up` must show no changes. For OpenTofu, the
   resources and the outputs of `state pull` must match, and `plan` must show
   no changes. The lineage and the serial of an OpenTofu state may change in an
   empty workspace. The guide records both values. This replaces the check
   "serial or version, lineage" of RFC 42 for OpenTofu.
7. **Safety rules.** The guide gives a read-only dry run and a list of what it
   does not move: stack config, variables, teams, policies, schedules, triggers
   and run history. It locks the OpenTofu source where the source allows it,
   and tells the team to stop pipelines where it does not. It checks that the
   source has no new writer. It resumes a failed import. It never deletes in the
   source. The source stays the record until the team switches.
8. **No new server code.** The imports of this ADR use existing routes. The TFE
   list of state versions (ADR 28) must exist when OpenDustpan is the source.
9. **How the project states support.** The documents say what the e2e suite
   drove: a Pulumi service backend and a TFE-compatible API, both with
   OpenDustpan as the source. They never say that the suite tested Pulumi Cloud
   or HCP Terraform.
10. **Order of work.** The e2e step comes first for each source (ADR 12). The
    guide follows the steps.

## Consequences

- Open work:
  1. A second server in the e2e harness, saved logins for two URLs in one
     Pulumi home, and a stdlib `http` backend for OpenTofu.
  2. The e2e steps for P1, P2, T1 and T2 (see the exit test).
  3. The guide, with the commands that the steps ran, and the replay script for
     T2.
  4. The glossary terms "import" and "source", so that "migration" keeps its
     meaning of a SQL schema migration.
- A team that imports a stack with a long history stores one full checkpoint
  for each version it replays. The guide says so.
- History that P1 imports shows kind `import` and the time of the import.
- OpenTofu state that Terraform wrote with a version newer than 1.5 may not
  load in OpenTofu. This is a limit of the tools. The guide says so.
- Two behaviors come from a reading of the source code and no run yet. First,
  OpenTofu may reset lineage and serial in an empty workspace at
  `-migrate-state`. Second, the Pulumi CLI may decrypt through the source with
  the saved login at `stack import`. The first e2e steps decide both. If the
  second one fails, decision 3 needs a new ADR.
- The exit test of this part of milestone 3 is this. On Linux, for each Pulumi
  CLI version and each OpenTofu version in the e2e matrix, on SQLite and on
  PostgreSQL, the e2e suite drives:
  1. P1: the pipe from server S to server D, the history of the last N
     versions, secrets that decrypt on D, `up` with no changes, and S
     unchanged.
  2. P1 config secrets through `config --show-secrets --json` and
     `config set --secret`.
  3. P2: a `file://` stack with the passphrase provider moves, and `up` shows no
     changes.
  4. T1: local state and an `http` backend move with
     `init -migrate-state -force-copy`, a second workspace moves, and
     client-side encryption stays.
  5. T2: three state versions replay with their serial, lineage and MD5, a
     normal `apply` follows, and a second run moves nothing.
  6. A missing namespace and a wrong token give clear errors.

  The full Pulumi and OpenTofu scenarios still pass on the same server.
