---
rfc: 5
title: Profiles, shared config and secrets for every tool
status: discussing
created: 2026-10-10
---

# RFC 5. Profiles, shared config and secrets for every tool

Status: discussing. On 2026-10-18 the owner decided the ten points that
ADR 200 lists. ADRs 200 to 209 hold the design that follows from them, and
the owner accepts or refuses that design on the merge of its pull request.
The questions below are open, each with its options and a
recommendation. Question 25 records a decision of the project manager.

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

Each question gives its options, the trade-off of each, and the
recommendation of the project manager or of the design. A question is not
decided until the owner decides it. Where the ADRs already use one option,
the question says so, and the owner accepts it on the merge or picks
another option.

1. **The word on the Pulumi Cloud-compatible API.** The paths say
   `environments`, because the CLI sends them.
   - A. The messages of that API and the Pulumi views of the console say
     "environment". Every other place says "profile". Each tool keeps its
     own words in its own views (ADR 13), but the product has two words for
     one thing.
   - B. "Profile" everywhere, also in the messages of that API. One word,
     but a Pulumi user reads a word that the CLI does not use.
   - C. "Environment" everywhere. One word, but it collides with the
     environment of a process and with the words of OpenTofu users.

   Recommendation: A.
2. **The first cloud logins.**
   - A. AWS, Google Cloud and Azure first, then Vault. It covers most
     clouds first. `secrets/vault` needs `login/vault` (ADR 207 decision 6),
     so Vault users read Vault through `secrets/external` and an adapter of
     their own until `login/vault` ships.
   - B. AWS and Vault first. It covers fewer clouds, but gives dynamic
     secrets early.
   - C. All four in one slice. The first release comes later.

   Recommendation: A.
3. **Each run records the revision that it read.** A job does, because the
   spec fixes the revisions (ADR 100 decision 5, ADR 203 decision 5). A
   Pulumi update outside a job sends `stack.environments` with versions
   (ADR 209 decision 5).
   - A. The history item of each update and each state version links the
     profiles and the revisions that the run read, also for a run outside a
     job. A person can answer "which password did this apply use". It needs
     one more link table.
   - B. Only jobs record it. Less work, but a run from a workstation has no
     record.
   - C. Record it in the audit log only. No new table, but the history view
     cannot show it.

   Recommendation: A.
4. **Who owns a profile.**
   - A. A profile belongs to one namespace, and attachments put it on a
     namespace, a collection or a unit (ADR 51 decision 8, ADR 203 decision
     5). Permissions stay on the namespace.
   - B. A profile can also belong to a collection. Collection owners manage
     their own profiles, but grants and names get a second level.
   - C. A profile can belong to the installation, for values that all
     namespaces share. It breaks the isolation of namespaces.

   Recommendation: A.
5. **Rotation in the first release.**
   - A. `password`, `passphrase`, `external`, `postgres` and `aws-iam`
     first. `mysql` needs a new dependency (question 12). `azure-app-secret`
     and `snowflake-user` come next.
   - B. Only `password`, `passphrase` and `external`. Less code to secure,
     but no database rotation without an adapter.
   - C. All rotators of ADR 207 in one slice. The first release comes later
     and needs the new dependencies.

   Recommendation: A.
6. **The open rule and jobs.** ADR 203 decision 5 refuses a job whose
   profiles have an open rule.
   - A. Refuse the job, as now. Simple and safe, but a profile with an open
     rule cannot be used by a job at all.
   - B. The job waits in `awaiting-approval` with a gate, and an approver of
     the open rule approves it. For a job of a change order, an approval of
     the change order by an approver of the open rule counts. It uses the
     gates of ADR 103, and one approval covers one job.
   - C. An approved access request of the person who starts the job counts
     for the job. No new gate, but the approval is for a person and not for
     the code that the job runs.

   Recommendation: B.
7. **A runner job with no profile.** ADR 101 decision 1 refuses a spec with
   no profile.
   - A. Keep the rule. Each unit needs a profile, also an empty one.
   - B. Allow a job with no attachment, so that a unit with no secret needs
     no empty profile. This needs a change of ADR 101.
   - C. The server makes an empty profile for each such unit. No change of
     ADR 101, but many empty objects.

   Recommendation: B.
8. **The actions `dedrift` and `unseal`.** The project manager named them
   for the `action` claim. No operation has them yet.
   - A. Reserve the names, and let the ADR of each operation add its value to
     ADR 205 decision 5.
   - B. Add them now. A trust policy can name them, but no token carries
     them yet.
   - C. Drop them. A later ADR may then pick other names.

   Recommendation: A.
9. **A claim for the blueprint.**
   - A. Add `blueprint` and `catalog_request` when the Catalog runs jobs,
     as an addition to the claim set.
   - B. Add them now, empty until the Catalog runs jobs.
   - C. No claim. A trust policy reads the unit only.

   Recommendation: A.
10. **Where the provider of OpenTofu lives.**
    - A. Its own repository, built with the provider SDK of providerkit, with
      its own release line and the gates of ADR 170 decision 4. It
      releases on the cadence of OpenTofu, but it is one more repository.
    - B. In this repository, released with the server. One release, but
      the provider then follows the server's version and tag.
    - C. In the repository of the provider wrappers of ADR 170. It shares
      their gates, but mixes a profile reader with the wrappers.

    Recommendation: A.
11. **The `pulumi env` commands of the first release.**
    - A. `open`, `run`, `ls`, `init`, `get` and `edit` first, then `version`
      and `version tag`. Each one needs its e2e step first.
    - B. Only `open` and `run`. Reads work first, and a person edits through
      the REST API and the console.
    - C. Every command that the CLI has. More e2e steps before the first
      release.

    Recommendation: A.
12. **New dependencies.** `mysql` needs a MySQL driver. A 1Password service
    account needs the SDK of 1Password.
    - A. Ask the owner for each one, and ship without them first.
    - B. Add both now. More features, but more code to review and license.
    - C. Only through the external adapter protocol of ADR 208. No new
      dependency, but each user runs an adapter.

    Recommendation: A, with C as the path until the owner decides.
13. **Kubernetes and SDKs.** ESC has an External Secrets Operator provider,
    a Secrets Store CSI provider and SDKs in four languages.
    - A. A later RFC, on the REST API of ADR 203.
    - B. An External Secrets Operator provider in the first release. It
      helps Kubernetes users early, but is more work before the core is
      stable.
    - C. No Kubernetes integration. Users call the REST API.

    Recommendation: A.
14. **A migration tool from ESC.**
    - A. A later command that reads ESC YAML and writes the format of ADR
      201, with a report of each function that it cannot map.
    - B. A guide only. No code, but each user maps by hand.
    - C. Read ESC YAML on open, with no conversion. It ties the document
      format to another product.

    Recommendation: A.
15. **Tokens for sweeps and job sessions.** ADR 133 and ADR 164 ask whether
    the server signs a token for a sweep or a job session. The issuer of ADR
    204 exists now.
    - A. A separate RFC, because ADR 164 decision 4 holds that the server
      never takes part in the credentials of a sweep.
    - B. Use the issuer for sweeps now. It changes ADR 164 decision 4.
    - C. Never. Sweeps keep their own credentials.

    Recommendation: A.
16. **Attached profiles for the Pulumi CLI outside a job.** The Pulumi CLI
    can read a stack config that the backend stores, but then ignores the
    stack file.
    - A. No. Outside a job, a person names the profiles in `environment:` of
      the stack file. The stack file stays the source of truth on a
      workstation.
    - B. Yes. The backend serves the attached profiles as the stack config.
      A person needs no `environment:` list, but the CLI then ignores the
      local stack file.
    - C. Yes, but only for stacks with no local stack file. Two behaviors to
      explain.

    Recommendation: A.
17. **The order of work.**
    - A. In slices, each slice with its conformance suite and its e2e steps
      first:
      1. The document, the store, static secrets, open, the REST API and the
         run command.
      2. The issuer and `login/aws`, `login/gcp`, `login/azure`,
         `login/oidc`.
      3. Delivery to runners.
      4. The Pulumi CLI paths.
      5. The provider of OpenTofu, with the ephemeral resource.
      6. `login/vault`, `secrets/vault`, `outputs/opendustpan`.
      7. Change rules, drafts and open rules.
      8. Rotation.
      9. The other adapters.
    - B. The Pulumi CLI paths before the issuer. Pulumi users get static
      profiles early, but cloud logins come later.
    - C. Change rules and open rules before delivery to runners. More
      control earlier, but jobs get profiles later.

    Recommendation: A.
18. **What a person with only `profile:write` can change.** A block input
    can be an expression, so a plain value can decide where a provider or
    rotator block sends secrets or an issued token.
    - A. The routing change rule: any change to a path that a block input
      reads (also through other paths), any change to imports or exports,
      and any rollback or tag move that changes the resolved inputs of a
      block needs `profile:open`. A diff compares evaluated hosts. Authors
      keep expressions in inputs, but the server must track what each
      input reads.
    - B. Destination inputs (`url`, `address`, `tokenUrl` and the like)
      must be literals. Simple to check, but a profile cannot compute a
      host from a shared value.
    - C. No rule. A writer can send secrets to a host of their own.

    The ADRs use A (ADR 202 decision 2). Recommendation: A.
19. **The default grant of `profile:write`.**
    - A. Each role with `unit:write` gets `profile:write`. A person who
      deploys can change any profile, also a guardrail profile.
    - B. No profile action beyond `profile:read` for `unit:write`. A grant
      gives more. Least privilege, designed against ISO/IEC 27001:2022
      A.8.2, but an admin grants writers by hand.
    - C. Grant it, but protect namespace attachments by default. Fewer
      grants, but a writer can still change profiles that units use.

    The ADRs use B (ADR 203 decision 11). Recommendation: B.
20. **The host of the issuer.**
    - A. The installation `publicURL`. No setting, but a change of the API
      host breaks every cloud trust.
    - B. An `issuerBaseURL` installation setting, separate from the API
      host. The API host can change, but the setting is one more one-way
      door.
    - C. Only a per-namespace `issuerURL`. Each namespace decides, but each
      one must publish its own files.

    The ADRs use B, with the per-namespace `issuerURL` of C as an option
    (ADR 204). Recommendation: B.
21. **The OpenTofu data source and login blocks.** OpenTofu reads a data
    source in the plan, and its result goes into the saved plan.
    - A. The data source refuses the outputs of login blocks and each value
      with an expiry. Those come only from the ephemeral resource. Safe,
      but an author must know which one to use.
    - B. Allow them and document the risk. Simple, but credentials sit in
      saved plans and can expire before the apply.
    - C. The data source refuses every secret. Safest, but static secrets
      also need the ephemeral resource, which older OpenTofu versions do
      not have.

    The ADRs use A (ADR 209). Recommendation: A.
22. **Whose authority a Catalog attachment uses.** A blueprint names a
    profile, and an attach needs `profile:open`.
    - A. The authority of the blueprint publisher, checked at publish time,
      at each open of a job and audited. A requester needs no
      `profile:open`, but a publisher decides for each later request. The
      cost of a departure: when the publisher leaves or loses an action,
      each job of each unit of the blueprint fails with
      `profile.ErrAttachmentAuthority` until a person re-authorizes each
      attachment, or the units move to a version of another publisher (ADR
      203 decisions 4 and 5). The daily check and the access review find
      such attachments before a job fails.
    - B. The authority of the requester. Each requester then needs
      `profile:open`, so most Catalog users cannot attach the platform
      profile.
    - C. A stated exception in the authorization rules, with an audit entry.
      It works, but it is a special case in the policy check.
    - D. A service principal of the Catalog as the authority, which an
      admin grants `profile:open` and `profile:attach` on each platform
      profile. No person leaves, so no departure breaks jobs. But one
      principal then opens each profile that any blueprint names, and its
      grants need their own review.

    The ADRs use A (ADR 203). Recommendation: A, with re-authorize and the
    access review.
23. **The scope of `profileProviderNetworks`.** It allows provider blocks to
    reach private networks.
    - A. The whole installation. Simple, but on a shared installation each
      namespace's authors can reach every allowed network, and the answers
      come back as values.
    - B. For each namespace. An operator allows a network for the
      namespaces that need it.
    - C. For each host and port. The narrowest, but each new host needs an
      operator change.
    - D. B and C together: each entry is a CIDR block or a `host:port`,
      and names the namespaces that can use it. The operator picks the
      narrowest form that works.

    The ADRs use D (ADR 206 decision 6). Recommendation: D.
24. **The hard stop of an issuer key at an `issuerURL`.** When the copy
    check fails, the active key keeps signing after 90 days (ADR 204
    decision 3).
    - A. Warn from day 90 in each open answer, in a banner of the console
      and in a `WARN` log, and stop at day 180 with
      `profile.ErrIssuerKeyExpired`. A key never signs for more than 180
      days, but a homelab that misses the warnings loses its cloud logins
      at day 180.
    - B. No stop: warn, and keep signing. Logins never stop, but a key can
      sign for years, and a key that leaked keeps working.
    - C. Stop at day 90, as for a namespace with no `issuerURL`. The
      shortest life of a key, but each missed copy stops the logins at
      once.

    The ADRs use A (ADR 204 decision 3). Recommendation: A.
25. **A block in a merge (decided by the project manager).** Imports and
    attachments merge maps key by key. A block of `providers` or
    `rotators` that merged key by key could take one input of a narrower
    profile and send the secret inputs of a wider profile to its host.
    - A. A block is atomic in a merge: a later block of the same name
      replaces the earlier block whole, and runs in the own tree of its own
      profile. A value of a profile reads the block of its own tree.
    - B. A block name that two profiles of one merge use fails the open
      with a named error. Safe, but a narrower attachment cannot replace a
      block of a wider one at all.
    - C. Merge key by key, as for values. Short, but it lets a narrower
      profile redirect the secrets of a wider one.

    Decision of the project manager on 18 October 2026: A (ADR 201
    decision 3). The owner accepts it or picks another option on the
    merge.

## Outcome

Accepted in part: ADRs 200 to 209, on the merge of their pull request. The
open questions above stay open.
