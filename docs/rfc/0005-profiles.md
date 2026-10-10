---
rfc: 5
title: Profiles, shared config and secrets for every tool
status: draft
created: 2026-10-10
---

# RFC 5. Profiles, shared config and secrets for every tool

Status: draft. The name and the scope are proposals.

## Problem

Teams give the same values to many units: cloud regions, account IDs, API
keys, database passwords, and cloud credentials. Today each unit holds its
own copy. A Pulumi stack keeps them in `Pulumi.<stack>.yaml` (secrets
encrypted by the `secret` package). An OpenTofu workspace keeps them in
`*.tfvars` files, environment variables or CI secrets. When a value changes,
someone changes every copy, and stale copies stay behind.

Other products fill this gap:

- Pulumi ESC holds named sets of config and secrets that import each other,
  pull values from external stores, and give short-lived cloud credentials
  through OIDC. Any tool can use it through `esc run`.
- HCP Terraform and Terraform Enterprise split the same job into variable
  sets (flat, shared variables for many workspaces), dynamic provider
  credentials (OIDC) and HashiCorp Vault.

A backend for many tools is a natural home for one shared set of values that
every tool reads.

## Proposal

A **profile** is a named set of config, secrets and credentials in a
namespace. A run of any tool can read it.

- **Values.** A profile holds plain values, secret values and credential
  sources. The `secret` package encrypts secret values, with the profile as
  its own scope, so each profile has its own data key.
- **Imports.** A profile can import other profiles in the same namespace.
  Later imports win, and the profile's own values win over all imports. An
  import cycle is an error when the profile is saved.
- **Credentials.** A credential source gives a short-lived credential when a
  run opens the profile: for example an AWS role through OIDC. The profile
  stores the trust settings, never a long-lived key, where the cloud allows
  that.
- **External stores.** Later, a value can come from an external store (Vault,
  AWS Secrets Manager). Each store is an adapter behind a port, like the
  storage adapters (ADR 2).
- **Access.** Opening a profile is an action of its own, separate from
  reading a unit. A person can deploy a unit without seeing the profile's
  secrets in the console.
- **Audit.** Every open of a profile is recorded: who, which run, which
  values. The audit log never holds the values.

Profiles are part of the shared core (ADR 12). They belong to no tool, and no
tool's model changes for them (ADR 13).

## How each tool reads a profile

- **Pulumi CLI.** A stack's config can import ESC environments
  (`environment:` in `Pulumi.<stack>.yaml`). The CLI then asks its backend for
  the values. If OpenDustpan serves that part of the Pulumi Cloud-compatible
  API, the Pulumi CLI reads profiles with no extra tool. This needs a check
  against the real CLI with `make e2e`.
- **OpenTofu.** OpenTofu has no way to ask its backend for variables when it
  runs on a workstation. Under the TFE-compatible API, workspace variables and
  variable sets apply only to remote runs. So OpenTofu reads profiles through
  a runner (RFC 6), which passes the values as `TF_VAR_*` and environment
  variables. On a workstation, OpenTofu reads no profile.

## Options

1. **Profiles as above.**
2. **Do nothing.** Teams keep values in each tool's files and in CI secrets.
   This is cheap and loses the main reason to share one backend.
3. **ESC-compatible only.** Serve the ESC API for the Pulumi CLI and nothing
   for OpenTofu. This adds a feature for one tool to the shared core, which
   ADR 12 forbids.
4. **A CLI wrapper** (`dustpan run --profile x -- tofu plan`). It would serve
   OpenTofu on workstations. ADR 12 says OpenDustpan ships no client plugins
   or wrappers, so this needs a new ADR.

## Proposed glossary entry

**Profile**:
A named set of config, secrets and credential sources in a namespace, which a
run of any tool can read.
_Avoid_: Environment, context, config, variable set, vault

The person's own settings page is then "Account", never "Profile".

## Open questions

- The Pulumi CLI calls these "environments". Does the Pulumi
  Cloud-compatible API keep that word on the wire, while the console says
  "profile"? ADR 13 says each tool keeps its own words in its own views.
- Does a profile belong to a namespace only, or also to a group of units
  (RFC 4)?
- Which credential sources come first: AWS, Google Cloud, Azure, Vault?
- Are profiles versioned, so that a run records which version it read?
- Is option 4 worth a new ADR, so that OpenTofu on a workstation can read a
  profile?

## Outcome

None yet.
