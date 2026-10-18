# 200. Profiles: config, secrets and credentials for every tool

Status: accepted · 2026-10-18 · ADRs 201 to 209 give the parts · accepts parts of RFC 5

## Context

Teams give the same values to many units: regions, account IDs, API keys,
database passwords and cloud credentials. Each unit keeps its own copy
today, in `Pulumi.<stack>.yaml`, in `*.tfvars` files or in the secrets of a
CI. [RFC 5](../rfc/0005-profiles.md) proposes a **profile**: a named set of
config, secrets and credentials in a namespace, which a run of any tool
reads. Accepted ADRs already depend on profiles: ADR 51 (attachments), ADR
100 (the job fixes its profile), ADR 101, ADR 133 and ADR 134 (delivery to a
runner), ADRs 110 to 112 (the profile of a blueprint), ADR 122 (the observe
of Dusty) and ADR 164 (no profile for a sweep).

The project owner decided on 18 October 2026 (owner):

1. Complete feature parity with Pulumi ESC, in our own format. No
   compatibility with the ESC language. A migration tool may come later.
   Each feature of the ESC docs is checked and marked.
2. OpenDustpan becomes an OIDC issuer: a discovery document, a JWKS, key
   storage and rotation, an audience for each target and short lifetimes.
   The login providers, the generic OIDC token and the external provider
   use it.
3. Richer identity federation than ESC: the issued token carries claims for
   the namespace, the collection, the tool, the unit, the actor, the action,
   the VCS ref and its type and protection, the commit, the repository, the
   change order, the job and the runner pool, so that rules on attributes are
   easy to write downstream. A stable claim set, a `sub` format that cloud
   trust policies can match with wildcards, custom audiences, and a
   statement of the safe claims, with no e-mail address of a person by
   default.
4. One provider port with the kinds login, secrets and outputs, plus
   rotators. Each integration is an adapter with a conformance suite. The
   core is shared and tool-neutral (ADR 12, ADR 13).
5. The external adapter protocol: an HTTPS `POST` with a signed JWT (`sub`,
   `aud`, `exp`, `iat`, `jti`, a hash of the body, and the rich claims),
   documented so that an adapter is easy to write. Never a claim of
   compatibility with another product.
6. An outputs provider over our own inventory, so that a Pulumi stack can
   read the outputs of an OpenTofu workspace, and the reverse.
7. Login providers also for Keycloak and its cloud-native siblings, where
   they fit.
8. OpenTofu reads profiles through an OpenDustpan provider, built with
   providerkit, with a data source, an opt-in ephemeral resource, a
   never-store mark and a warning about other state backends.
9. The Pulumi CLI reads profiles through the Pulumi Cloud-compatible API
   where the CLI supports it. Each tool keeps its own words.
10. A careful design that is easy to integrate with, with worked examples
    for a homelab user, a small team and a platform team. All personas are
    equal.

The project lead gave the design to an agent, with these decisions. ADRs
200 to 209 record them and the design that follows from them. A decision
of the owner has the label "(owner)". The other decisions are the design of
these ADRs, and the owner accepts or refuses them with the pull request.
This pull request holds design only: ADRs, ports and interfaces, and no
implementation. RFC 5 keeps the questions that are still open, each with a
recommendation.

The research note `docs/research/profiles-esc-parity-and-oidc-claims.md`
holds the parity table of ESC, the comparison of claims, and what each
external system accepts, with the sources and their dates.

### One-way doors

| Decision | Why it is hard to undo |
| --- | --- |
| One issuer URL for each namespace (ADR 204 decision 1) | Clouds hold the issuer URL in their trust. |
| The claim names and values, version 1 (ADR 205 decision 1) | Trust policies and Vault roles hold them. |
| The subject format and the order of its keys (ADR 205 decision 6) | Trust policies hold subjects. |
| The body and the JWT of the external adapter protocol, version 1 (ADR 208) | Adapters that teams host hold the shape. |
| The document format, `schemaVersion` 1 (ADR 201) | Stored revisions hold it. |
| The ID prefixes `prf-`, `pos-`, `oik-` and `oit-` | IDs are in audit entries, links, tokens and backups. |
| The audit entry of an open (ADR 203 decision 9) | The audit chain hashes each entry. |

## Decision

1. **A domain package `pkg/profile` (owner).** The package is tool-neutral.
   It imports no tool package, no store adapter and no transport. No tool
   package imports it. The wiring of the server connects it to the tool
   packages, the job model and the runner protocol, as ADR 128 decision 2
   does for `pkg/agent`. ADR 206 decision 1 gives the layout.
2. **The objects.**

   | Object | ADR |
   | --- | --- |
   | Profile, revision, revision tag, label, profile folder | 202 |
   | The document, imports, entries, provider blocks, exports, checks | 201 |
   | Profile draft, change rule, open rule, access request | 202 |
   | Rotator block and rotation record | 202 |
   | Open, open session, profile attachment | 203 |
   | The issuer of a namespace and its keys | 204 |
   | The claims and the subject | 205 |
   | Profile provider and rotator ports | 206 |
   | The built-in adapters | 207 |
   | The external adapter protocol | 208 |
   | The provider of OpenTofu, the Pulumi paths and the run command | 209 |

3. **The errors.** `pkg/profile` holds these sentinels. Each port wraps
   them with `fmt.Errorf("...: %w", err)`, and an adapter translates each
   driver error into one of them.

   | Error | When |
   | --- | --- |
   | `ErrInvalid` | A bad field, document, block, audience or template. |
   | `ErrNotFound` | An unknown profile, revision, tag, draft or session, or a caller without `profile:read`. |
   | `ErrConflict` | A write on a stale read. |
   | `ErrSchemaVersion` | A document with a higher `schemaVersion`. |
   | `ErrImportCycle` | A cycle of imports or `profiles[...]` reads. |
   | `ErrReference` | A path that does not exist at open. |
   | `ErrFinal` | A replace of a final value. |
   | `ErrCheckFailed` | A type or a rule of `checks` that fails. |
   | `ErrProvider` | A provider block that failed at open. It wraps the error of the port. |
   | `ErrDenied`, `ErrUnavailable`, `ErrTooLarge` | The errors of the provider port (ADR 206 decision 7). |
   | `ErrSecretVariable` | A secret in `exports.variables` for a job on a runner. |
   | `ErrNeverStore` | A value marked never store for a consumer that stores it. |
   | `ErrOpenRule` | A job of a profile with an open rule. |
   | `ErrInUse` | A rename or a delete of a profile that another object names. |
   | `ErrIssuerNotConfigured` | A token with no `publicURL`. |
   | `ErrSubjectTooLong` | A subject template whose subject can pass 600 characters. |
   | `ErrLimit` | A write or an open above a limit. The error names the limit. |

4. **The ports.** The ports of ADRs 204 and 206 (`Issuer`, `Provider`,
   `Rotator`, `TokenSource`) and these ports of the store and the wiring
   are in `pkg/profile`. Each method that writes takes a `namespace.Fence`
   and the audit entries of the write, appends them in the transaction,
   returns `namespace.ErrFenced` for a stale fence, and changes nothing on
   an error.

   ```go
   // Store persists profiles, revisions, tags, labels, rules, drafts,
   // access requests, rotation records and attachments.
   type Store interface {
       // Create stores p with revision 1. ErrInvalid, ErrConflict for a
       // name in use, ErrLimit.
       Create(ctx context.Context, p Profile, r Revision, f namespace.Fence, e []audit.Entry) (Profile, error)
       // Get reads one profile by name or ID. ErrNotFound.
       Get(ctx context.Context, ns namespace.Name, ref Ref) (Profile, error)
       // List reads one page. ErrInvalid for a bad filter or cursor.
       List(ctx context.Context, q Filter) (Page, error)
       // Revision reads one revision by number, tag or draft.
       // ErrNotFound, also for a retracted or redacted revision.
       Revision(ctx context.Context, id ID, at At) (Revision, error)
       // Save adds revision r on top of base and moves latest.
       // ErrConflict for a stale base, ErrInvalid.
       Save(ctx context.Context, id ID, r Revision, base int, f namespace.Fence, e []audit.Entry) (Revision, error)
       // SetTag creates or moves tag t, or deletes it with rev 0.
       // ErrInvalid for latest, ErrNotFound for an unknown revision.
       SetTag(ctx context.Context, id ID, t string, rev int, f namespace.Fence, e []audit.Entry) error
       // Retract marks rev retracted and moves its tags to with.
       // ErrInvalid for the newest revision.
       Retract(ctx context.Context, id ID, rev, with int, reason string, f namespace.Fence, e []audit.Entry) error
       // PutDraft, SubmitReview and ApplyDraft hold the drafts of ADR 202
       // decision 6. ApplyDraft checks the approvals of the rule in its
       // transaction. ErrConflict, ErrInvalid.
       PutDraft(ctx context.Context, id ID, d Draft, f namespace.Fence) (Draft, error)
       SubmitReview(ctx context.Context, id ID, d DraftID, v Review, f namespace.Fence, e []audit.Entry) (Draft, error)
       ApplyDraft(ctx context.Context, id ID, d DraftID, f namespace.Fence, e []audit.Entry) (Revision, error)
       // Rotated stores the outcome of one rotation of one block, in one
       // transaction of its record. ErrConflict for a stale record.
       Rotated(ctx context.Context, id ID, block string, o RotationOutcome, f namespace.Fence, e []audit.Entry) (RotationRecord, error)
       // Attach and Detach hold the attachments of ADR 203 decision 5.
       // ErrInvalid for an object of another namespace.
       Attach(ctx context.Context, a Attachment, f namespace.Fence, e []audit.Entry) (Attachment, error)
       Detach(ctx context.Context, a AttachmentID, f namespace.Fence, e []audit.Entry) error
       // Attached answers the attachments of a unit in merge order, each
       // resolved to a revision. ErrNotFound for an unknown unit.
       Attached(ctx context.Context, u identity.Object) ([]Resolved, error)
       // Delete, Restore and Rename hold the lifecycle of ADR 202 decision
       // 9. ErrInUse names the referrers.
       Delete(ctx context.Context, id ID, f namespace.Fence, e []audit.Entry) error
       Restore(ctx context.Context, id ID, f namespace.Fence, e []audit.Entry) (Profile, error)
       Rename(ctx context.Context, id ID, name Name, f namespace.Fence, e []audit.Entry) (Profile, error)
   }

   // Sessions holds open sessions (ADR 203 decision 3).
   type Sessions interface {
       // Put stores a sealed result until expires. ErrLimit above 100
       // live sessions for the principal.
       Put(ctx context.Context, s Session) (SessionID, error)
       // Get reads a session of the principal. ErrNotFound for another
       // principal, an unknown session or a session that ended.
       Get(ctx context.Context, id SessionID, principal string) (Session, error)
       // Sweep deletes each session that ended before now.
       Sweep(ctx context.Context, now time.Time) (int, error)
   }

   // UnitOutputs reads the outputs of units for outputs/opendustpan
   // (ADR 207 decision 7). The wiring implements it over the masked reads
   // of the tool packages. ErrNotFound, ErrUnavailable.
   type UnitOutputs interface {
       Outputs(ctx context.Context, a Actor, u identity.Object, names []string, secrets bool) (UnitOutputSet, error)
   }

   // Service is what the REST API, the Pulumi paths, the runner
   // protocol and Dusty call. It checks the actions of ADR 203, builds
   // the context, writes the audit entries and calls the ports.
   type Service interface {
       Open(ctx context.Context, a Actor, r OpenRequest) (Opened, error)
       Check(ctx context.Context, a Actor, ns namespace.Name, doc []byte) (Diagnostics, error)
       // The other methods map one to one to the routes of ADR 203
       // decision 12.
   }
   ```

   `profiletest` holds the suites of `Store`, `Sessions` and the keys of
   the issuer. A reference adapter in memory, in the tests of the package,
   runs them, so each case runs before a SQL adapter exists.
5. **How this fits the accepted ADRs.**

   | ADR | What it says | How profiles fit |
   | --- | --- | --- |
   | 51 decision 8 | An attachment can name a namespace, a collection or a unit. | ADR 203 decision 5: attachments on each of the three, merged from wide to narrow. |
   | 100 decision 5 | The server fixes the profile of a job at start. | The spec holds the list of attached profiles, each with a fixed revision. A later change of an attachment or a tag does not change a job. |
   | 100 decision 20 | The job token is an access token of the principal, for the unit. | A job token opens only the profiles of its spec (ADR 203 decision 4). |
   | 101 decision 10, 133 decision 12, 134 decisions 6 and 7 | Credentials reach the tool step as files on a memory-backed volume. A variable holds a path. The plan phase gets read-only credentials. | ADR 203 decision 6: `exports.files` and the default exports are files. A secret in `exports.variables` fails the job start. The plan phase opens with `access` `read`. |
   | 134 decision 8 | The launcher masks each secret. | Each secret of an open is in the mask set. |
   | 175 decision 3 | The profile names the variable of a credential file of a provider wrapper. | `exports.files`, for example `IPAM_TOKEN_FILE`. |
   | 110 decision 6, 111 decision 3 | A render holds no secret. A secret comes from the profile of the blueprint at run time. | The blueprint profile becomes a unit attachment (ADR 203 decision 5). |
   | 112 decision 8 | `platform.profile`, and the one-home rule against the keys that the profile sets. | The keys that a profile sets are the keys of `exports.opentofu` and `exports.pulumi`. The render check reads the attachments of the namespace and the collection of the new unit too. |
   | 122 decision 8, 124 | Dusty observes with short-lived read-only credentials of the profile of the unit. An agent sees no decrypted secret by default. | The consumer `observe` runs the login blocks in the server with `access` `read`. The block for an observe is the login block with `observe: true`, else the only login block of the cloud of the unit. An agent open answers masked values. |
   | 164 decision 4 | A sweep names no profile, and the server holds no cloud credential for a sweep. | A sweep reads no profile. The claim set has no action for a sweep. ADR 164 open question 2 stays open. |
   | 54 | The server trusts external issuers for workload identity. | The trust rules also refuse each issuer of this installation (ADR 204 decision 7). This adds a rule to ADR 54. |

6. **Conflicts with accepted ADRs.**
   - **ADR 12**, "OpenDustpan ships no client plugins or wrappers." The
     owner decided the provider of OpenTofu and the run command. ADR 209
     decision 1 supersedes the sentence for these two programs.
   - **ADR 101 decision 1**: `Executor.Start` answers `ErrInvalid` for a
     spec with "no profile". With attachments, a unit can have no profile,
     for example a homelab unit with no secret. This ADR does not change
     the rule. Until RFC 5 has an answer, a unit needs at least one
     attachment for a runner job. The recommendation in RFC 5 is to allow
     an empty list.
   - **ESC parity and ADR 101 decision 10.** ESC sets secrets as
     environment variables of a command. On a runner, no credential passes
     through a variable. ADR 201 decision 9 keeps the rule of ADR 101 on a
     runner, and allows the variable only for the run command on a
     workstation (ADR 209 decision 8).
7. **Parity with ESC.** The research note has one row for each feature of
   ESC, with its source, and marks each one: the same in our format, our
   own form, more than ESC, or not taken with the reason. The features that
   we do not take are the evaluation of ESC syntax (owner), `policyConfig`
   (the policy port of ADR 114 has its own inputs), the AWS Lambda
   connector of rotations (ADR 207 decision 9), the setup commands that
   change a cloud account (`pulumi env setup`), and a webhook format that
   starts a deployment, which waits for run triggers (RFC 24).
8. **Terms.** `GLOSSARY.md` gets: Profile, Profile revision, Revision tag,
   Profile folder, Entry, Provider block, Profile provider, Rotator,
   Rotation record, Exports, Never store, Open, Open session, Profile
   attachment, Change rule, Profile draft, Open rule, Access request,
   Issuer, Issued token, Subject template, External adapter. The word for
   a profile in the paths of the Pulumi Cloud-compatible API is
   `environments`, because the CLI sends it (ADR 209 decision 6).

## Worked examples

### A homelab

One person runs OpenTofu for a home network, with a DNS API token and no
cloud account.

```yaml
# profile "home"
schemaVersion: 1
values:
  dns:
    zone: home.example.net
    apiToken: { $secret: "<token>" }
```

```hcl
data "opendustpan_profile" "home" {
  profile = "home"
}

provider "dns" {
  api_token = data.opendustpan_profile.home.secrets["dns.apiToken"]
}
```

`tofu login dustpan.home.example.net` gives the provider its token. The
workspace state is sealed in OpenDustpan, so the token is not in plain
text at rest. For a script, `opendustpan profile run home -- ./renew.sh`
gives the same values. No issuer, no runner and no approval is necessary.
If the same person later adds an AWS account, the issuer of the namespace
can live in a public bucket (ADR 204 decision 3), and a login block of five
lines gives short-lived AWS credentials.

### A small team

Four people run Pulumi and OpenTofu on AWS, with one runner pool. They make
one profile and attach it to the collection `prod`:

```yaml
# profile "aws/prod"
schemaVersion: 1
providers:
  aws:
    use: login/aws
    with:
      roleArn: arn:aws:iam::111122223333:role/${ context.access == "write" ? "deploy" : "read" }
    export: true
values:
  region: eu-west-1
exports:
  variables:
    AWS_REGION: ${ values.region }
  pulumi:
    aws:region: ${ values.region }
  opentofu:
    region: ${ values.region }
```

The trust policy of the role `deploy` trusts only applies of the
collection:

```json
{
  "Effect": "Allow",
  "Principal": { "Federated": "arn:aws:iam::111122223333:oidc-provider/dustpan.example.com/oidc/acme" },
  "Action": "sts:AssumeRoleWithWebIdentity",
  "Condition": {
    "StringEquals": { "dustpan.example.com/oidc/acme:aud": "sts.amazonaws.com" },
    "StringLike": { "dustpan.example.com/oidc/acme:sub": "namespace:acme:collection:prod:*:access:write" }
  }
}
```

The role `read` also trusts `access:read` and `access:open`, so a person
can preview from a workstation with `environment: [aws/prod]` in the stack
config. The plan phase of each job gets the role `read`, and only an
approved apply gets the role `deploy`. The units need no change of code to
read the profile on the runner.

### A platform team

A platform team serves forty teams across AWS and Azure, with Vault and a
shared network that OpenTofu manages.

```yaml
# profile "platform/base", attached to the namespace
schemaVersion: 1
values:
  tags:
    costCenter: { $type: { kind: string, pattern: "^cc-[0-9]{4}$" }, $value: cc-0000 }
    managedBy: { $final: true, $value: opendustpan }
checks:
  - rule: context.access != "write" || context.refProtected == "true"
    message: Applies read this profile only from a protected ref.
```

```yaml
# profile "payments/prod", attached to the collection "payments-prod"
schemaVersion: 1
providers:
  network:
    use: outputs/opendustpan
    with:
      units:
        core: { unit: "opentofu:acme/network-prod", outputs: [vpc_id, private_subnet_ids] }
  vault:
    use: login/vault
    with: { address: https://vault.internal:8200, role: payments-prod }
  db:
    use: secrets/vault
    login: vault
    with:
      read:
        password: { path: database/creds/payments, field: password }
    neverStore: true
values:
  tags:
    costCenter: cc-4711
  vpcId: ${ providers.network.core.vpc_id }
  dbPassword: ${ providers.db.password }
exports:
  pulumi:
    payments:vpcId: ${ values.vpcId }
    payments:dbPassword: ${ values.dbPassword }
```

- The Pulumi stacks of the team read the VPC of an OpenTofu workspace with
  no copy of the value.
- The Vault role binds `collection` and `ref_protected` with
  `bound_claims`. The database password is a dynamic secret, marked never
  store, so an OpenTofu unit reads it only with the ephemeral resource.
- The profile `payments/prod` has a change rule with two approvals by the
  group `payments-leads`, and an open rule for people, so a person needs
  an approved access request to read it outside a job.
- Azure units use the subject template `namespace`, `collection`, `access`,
  so each collection needs two federated credentials, inside the limit of
  20 for each identity.
- A blueprint names `payments/prod` in `platform.profile`, so each unit of
  the Catalog gets the attachment.

## Consequences

- One profile serves the Pulumi CLI, OpenTofu, a runner and any command,
  each in the words of its tool.
- The design is large. Its implementation comes in slices, each with its
  conformance suite and its e2e steps first. RFC 5 lists a proposed order.
- `GLOSSARY.md` gets the terms of decision 8. RFC 5 gets the status
  `discussing`, the accepted parts, and the open questions.
- The status line of ADR 12 names ADR 209.

## Open questions

RFC 5 keeps them, each with a recommendation.
