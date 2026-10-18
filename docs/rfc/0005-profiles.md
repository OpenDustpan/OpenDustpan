---
rfc: 5
title: Profiles, shared config and secrets for every tool
status: discussing
created: 2026-10-10
---

# RFC 5. Profiles, shared config and secrets for every tool

Status: discussing. The owner accepted the core of profiles on 2026-10-18:
ADRs 200 to 209 hold it. The questions below are still open, each with a
recommendation.

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
  through OIDC. Any tool can use it through `pulumi env run`.
- HCP Terraform and Terraform Enterprise split the same job into variable
  sets (flat, shared variables for many workspaces), dynamic provider
  credentials (OIDC) and HashiCorp Vault.

A backend for many tools is a natural home for one shared set of values that
every tool reads.

## Accepted

On 2026-10-18 the project owner decided ten points. ADR 200 lists them
with the design that follows:

| ADR | Part |
| --- | --- |
| [200](../adr/0200-profiles-config-secrets-and-credentials-for-every-tool.md) | The decisions of the owner, the package, the ports, the fit with accepted ADRs, the conflicts, the worked examples |
| [201](../adr/0201-the-profile-document-imports-and-expressions.md) | The document, imports, Expr, entries, provider blocks, exports, types and checks |
| [202](../adr/0202-secrets-revisions-tags-drafts-and-rotation.md) | Static secrets, revisions, tags, labels, change rules and drafts, open rules, rotation, lifecycle |
| [203](../adr/0203-opening-a-profile-delivery-actions-and-audit.md) | Open, sessions, attachments, delivery to runners, actions, routes, audit, notifications |
| [204](../adr/0204-opendustpan-as-an-oidc-issuer.md) | The OIDC issuer of each namespace |
| [205](../adr/0205-the-claims-of-an-issued-token-and-the-subject.md) | The claims and the subject |
| [206](../adr/0206-the-profile-provider-port-rotators-and-conformance.md) | The provider port, rotators and conformance suites |
| [207](../adr/0207-the-built-in-profile-providers.md) | The built-in adapters, with Keycloak and its siblings |
| [208](../adr/0208-the-external-adapter-protocol.md) | The external adapter protocol |
| [209](../adr/0209-how-each-tool-reads-a-profile.md) | The provider of OpenTofu, the Pulumi CLI paths, the run command |

The research note
[profiles-esc-parity-and-oidc-claims](../research/profiles-esc-parity-and-oidc-claims.md)
holds the ESC parity table, the claim comparison and the facts about each
external system.

Option 4 of the first draft, a command for any tool, is now part of ADR
209 (`opendustpan profile run`). The text "On a workstation, OpenTofu reads
no profile" of the first draft no longer holds: the provider of ADR 209
reads profiles on a workstation.

## Open questions

Each question has the recommendation of the project manager or of the
design, and is not decided.

1. **The word on the Pulumi Cloud-compatible API.** The paths say
   `environments`, because the CLI sends them. Do the messages of that API
   and the Pulumi views of the console also say "environment", while every
   other place says "profile"? Recommendation: yes. It follows ADR 13: each
   tool keeps its own words in its own views.
2. **The first cloud logins.** Recommendation: AWS, Google Cloud and Azure
   first, then Vault.
3. **Each run records the revision that it read.** A job does, because the
   spec fixes the revisions (ADR 100 decision 5, ADR 203 decision 5). A
   Pulumi update outside a job sends `stack.environments` with versions
   (ADR 209 decision 5). Recommendation: the history item of each update
   and each state version links the profiles and the revisions that the run
   read, also for a run outside a job, so that a person can answer "which
   password did this apply use".
4. **Who owns a profile.** Recommendation: a profile belongs to one
   namespace, and attachments put it on a namespace, a collection or a unit
   (ADR 51 decision 8, ADR 203 decision 5). No profile belongs to a
   collection.
5. **Rotation in the first release.** Recommendation: `password`,
   `passphrase`, `external`, `postgres` and `aws-iam` first. `mysql` needs
   a new dependency (question 12). `azure-app-secret` and `snowflake-user`
   come next.
6. **The open rule and jobs.** ADR 203 decision 5 refuses a job whose
   profiles have an open rule. Recommendation: the job waits in
   `awaiting-approval` with a gate, and an approver of the open rule
   approves it. For a job of a change order, an approval of the change
   order by an approver of the open rule counts.
7. **A runner job with no profile.** ADR 101 decision 1 refuses a spec with
   no profile. Recommendation: allow a job with no attachment, so that a
   unit with no secret needs no empty profile. This needs a change of ADR
   101.
8. **The actions `dedrift` and `unseal`.** The project manager named them
   for the `action` claim. No operation has them yet. Recommendation:
   reserve the names, and let the ADR of each operation add its value to
   ADR 205 decision 5.
9. **A claim for the blueprint.** Recommendation: add `blueprint` and
   `catalog_request` when the Catalog runs jobs, as an addition to the
   claim set.
10. **Where the provider of OpenTofu lives.** Recommendation: its own
    repository, built with the provider SDK of providerkit, with its own
    release line and the gates of ADR 170 decision 4.
11. **The `pulumi env` commands of the first release.** Recommendation:
    `open`, `run`, `ls`, `init`, `get` and `edit` first, then `version` and
    `version tag`. Each one needs its e2e step first.
12. **New dependencies.** `mysql` needs a MySQL driver. A 1Password service
    account needs the SDK of 1Password. Recommendation: ask the owner for
    each one, and ship without them first.
13. **Kubernetes and SDKs.** ESC has an External Secrets Operator provider,
    a Secrets Store CSI provider and SDKs in four languages.
    Recommendation: a later RFC, on the REST API of ADR 203.
14. **A migration tool from ESC.** Recommendation: a later command that
    reads ESC YAML and writes the format of ADR 201, with a report of each
    function that it cannot map.
15. **Tokens for sweeps and job sessions.** ADR 133 and ADR 164 ask whether
    the server signs a token for a sweep or a job session. The issuer of ADR
    204 exists now. Recommendation: a separate RFC, because ADR 164
    decision 4 holds that the server never takes part in the credentials of
    a sweep.
16. **Attached profiles for the Pulumi CLI outside a job.** The Pulumi CLI
    can read a stack config that the backend stores, but then ignores the
    stack file. Recommendation: no. Outside a job, a person names the
    profiles in `environment:` of the stack file.
17. **The order of work.** Recommendation, each slice with its conformance
    suite and its e2e steps first:
    1. The document, the store, static secrets, open, the REST API and the
       run command.
    2. The issuer and `login/aws`, `login/gcp`, `login/azure`, `login/oidc`.
    3. Delivery to runners.
    4. The Pulumi CLI paths.
    5. The provider of OpenTofu, with the ephemeral resource.
    6. `login/vault`, `secrets/vault`, `outputs/opendustpan`.
    7. Change rules, drafts and open rules.
    8. Rotation.
    9. The other adapters.

## Outcome

Accepted in part: ADRs 200 to 209. The open questions above stay open.
