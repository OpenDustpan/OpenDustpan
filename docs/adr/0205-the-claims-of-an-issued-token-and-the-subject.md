# 205. The claims of an issued token, and the subject

Status: accepted · 2026-10-18 · part of ADR 200. Phase table extended by ADR 258

## Context

The project owner decided on 18 October 2026 that a token of the
OpenDustpan issuer (ADR 204) carries more claims than the tokens of other
products (owner):

- the namespace, the collection, the tool and the unit,
- the actor: a person, a service principal, a workload or an agent, and
  who started a job on whose behalf,
- the action, for example plan, preview, up, apply or destroy,
- the VCS ref, the ref type, whether the ref is protected, the commit and
  the repository when the server knows it,
- the change order and the job when they exist, and the runner pool,

so that downstream rules on attributes are easy to write. The owner asked
for a stable claim set, a `sub` format that cloud trust policies can match
with wildcards, custom audiences, and a statement of which claims are safe.
A person's e-mail address is not in a token by default (owner).

The research note `docs/research/profiles-esc-parity-and-oidc-claims.md`
compares the claims of seven products: Pulumi ESC and Pulumi Deployments,
GitLab CI, GitHub Actions, HCP Terraform, Spacelift, Buildkite and
CircleCI. The facts that shape this ADR:

- **AWS** reads only `sub`, `aud`, `amr`, `email` and `oaud` of a token
  from an issuer that it does not name. Custom claims are for named
  products only. So each fact that an AWS trust policy must check has to be
  in `sub`. `StringLike` with `*` works on `sub`.
- **Azure** needs an exact match of issuer, subject and audience. It has no
  wildcards for an issuer such as ours, and allows at most 20 federated
  credentials for one identity, each field at most 600 characters. So a
  `sub` must not hold a value that changes with each run, and one unit must
  have few `sub` values.
- **Google Cloud** maps claims with CEL, and limits `google.subject` to 127
  characters. A long `sub` must map through a shorter claim.
- **Vault** matches any claim with `bound_claims`, also with globs and JSON
  pointers.
- Products that put names in `sub` met renames and names that another
  tenant registers again. The newest designs use IDs. The products of the
  research put the phase or a read and write scope in `sub` when they run
  IaC: `run_phase`, `scope`, `operation`.
- GitLab puts the login and the e-mail address of the person in each token
  by default. HCP Terraform, Spacelift and Buildkite put no personal data in
  a token.

## Decision

1. **A flat claim set, version 1 (one-way door).** Each claim name is
   snake_case at the top level of the token, so that Vault and CEL read it
   with no pointer. Each value is a string, with three exceptions:
   `profile_revision` is a number, `act` is an object (decision 3), and the
   AWS tags claim is an object (decision 3). A boolean fact is the string `"true"` or `"false"`, so that
   it reads the same in `sub` and in a claim. A claim whose fact the server
   does not know is absent. It is never empty and never `null`.

   **One-way door:** trust policies in clouds hold the names and the values.
   A new claim is an addition. A change of the name or of the meaning of a
   claim needs a new claim name.
2. **The registered claims.**

   | Claim | Value |
   | --- | --- |
   | `iss` | The issuer of the namespace (ADR 204 decision 1). |
   | `sub` | The subject of decision 6. |
   | `aud` | One string: the audience of the login block (ADR 204 decision 7). Never a list. |
   | `iat` | The time of the issue, in Unix seconds. |
   | `nbf` | `iat` minus 30 seconds, so that a token from a server whose clock runs up to 30 seconds ahead still verifies at a cloud. |
   | `exp` | `iat` plus the lifetime (ADR 204 decision 6). |
   | `jti` | A random ID with the prefix `oit-`. |

3. **The OpenDustpan claims.**

   | Claim | Value | When present |
   | --- | --- | --- |
   | `namespace`, `namespace_id` | The name and the stable ID of the namespace. | Always. |
   | `purpose` | `login` for a token of a login provider or of `login/oidc`, `adapter` for a call of an external adapter (ADR 208). | Always. |
   | `profile`, `profile_id` | The profile that holds the provider block. | Always. |
   | `profile_revision` | The revision of that profile that the open read. | Always. |
   | `root_profile`, `root_profile_id` | The profile that the caller opened. It differs from `profile` when the block comes from an import. | Always. |
   | `access` | `read`, `write`, `rotate` or `open` (decision 4). | Always. |
   | `action` | The tool-neutral action (decision 5). | Always. |
   | `tool_action` | The word of the tool for the action, for example `preview`, `up`, `plan` or `apply`. | When a job opens. |
   | `phase` | `plan` or `apply`: the kind of the job phase (ADR 100 decision 4). | When a job opens. |
   | `tool` | `pulumi` or `opentofu`. | When the open has a unit. |
   | `unit`, `unit_id` | The tool's own name of the unit, for example `web/prod`, and its stable unit ID. | When the open has a unit. |
   | `unit_ref` | The unit reference, for example `pulumi:acme/web/prod`. | When the open has a unit. |
   | `collection`, `collection_id` | The collection of the unit (ADR 51). | When the open has a unit. |
   | `actor_type` | `person`, `service-principal`, `workload` or `agent`. | Always. |
   | `actor_id` | The stable ID of the principal on whose authority the open runs. | Always. |
   | `actor` | The login name of that principal. | Only when the namespace turns it on (decision 8). |
   | `act` | An object: the agent that acts for the actor, with `sub` (`agent:<agent ref>`) and `actor_type` `agent`, as in RFC 8693 section 4.1. | When an agent acts for a person (ADR 100 decision 17). |
   | `trigger` | What started the job: `person`, `change-order`, `catalog`, `schedule`, `agent` or `api`. | When a job opens. |
   | `job` | The job ID. | When a job opens. |
   | `change_order`, `change_order_revision` | The change order of the job and its revision (ADR 190). | When the job has a change order origin. |
   | `runner_pool`, `runner_pool_id` | The name and the ID of the pool of the claim (ADR 130). | When a job opens. |
   | `repository` | The forge host and the path of the repository, for example `github.com/acme/infra`. | When the job has a git code location. |
   | `ref`, `ref_type` | The full ref, for example `refs/heads/main`, and `branch` or `tag`. | Only when the server resolved the ref (below). |
   | `ref_protected` | `"true"` when the forge reports a protected ref, else `"false"`. | Only when the server resolved the ref and the forge answered (below). |
   | `commit` | The commit of the phase (ADR 100 decision 5). | Only when the server resolved the ref (below). |
   | `source` | `git`, or `file-set` for a source with no commit (ADR 199). | When a job opens. |
   | `https://aws.amazon.com/tags` | An object with `principal_tags`: each claim that `sessionTags` of a `login/aws` block names, with its value, as AWS reads session tags (ADR 207 decision 3). The block names the claims. The server sets the values. | When the block sets `sessionTags`. |

   **The ref claims.** A job spec comes in part from the caller, so the
   server does not trust a ref or a commit that the spec names. The claims
   `ref`, `ref_type`, `ref_protected` and `commit` are present only when
   all of these are true:
   - At job start, the server resolved the commit itself as the head of
     the named ref, through the forge connection of the repository (the
     forge connections of ADR 213, in review). The request named a ref, not a commit.
   - For `ref_protected`, the server asked the forge, at the same time and
     with the credential of that same forge connection, whether the ref is
     protected.

   The server records the four values in the job at start, and they stay
   fixed for the job, also when the ref moves or its protection changes
   later. In each other case, for example a job that names a commit, a
   source with no forge connection, or a forge that does not answer, the
   server leaves out the four claims. A trust policy or a check that needs
   one of them then fails closed: the Vault example of ADR 200 and a
   `checks` rule on `context.refProtected` refuse such a job.

   The server takes each value from its own records: the job spec, the
   claim, the principal, the unit and the forge. No caller, no profile
   author and no adapter sets a claim. An open that a client asks for with
   a unit name, for example from a provider on a workstation, does not get
   the unit claims, because the server cannot prove that the client runs
   for that unit (decision 4).
4. **`access` says what a cloud can trust.** The server sets it from facts
   that it controls:
   - `read`: the plan phase of a job, and an observe of Dusty (ADR 122
     decision 8). The plan phase of a job changes nothing (ADR 100
     decisions 3 and 4).
   - `write`: the apply phase of a job of the operation `apply`,
     `destroy` or `refresh`. It runs on a runner after the gates of the job
     or the approvals of its change order. A `refresh` has no plan phase, so
     a trust policy that wants only reviewed changes also matches
     `action:apply` with a template that holds `action`.
   - `rotate`: a rotation (ADR 202 decision 8). It changes a credential,
     has no unit and no job, and so never matches `access:write`.
   - `open`: every other open, for example a person in the console, the
     run command (ADR 209) or a provider on a workstation.

   A trust policy that allows a role that writes only for `access:write`
   then allows it only for the apply phase of a job. A person who
   applies from a workstation gets `access:open`, and the team decides in
   the cloud whether a role trusts it.
5. **`action` names what runs.** The tool-neutral values are the job
   operations `plan`, `apply`, `destroy` and `refresh` (ADR 100 decision 3),
   `open` and `observe` for an open with no job, and `rotate` for a rotation (ADR 202 decision 8). `tool_action` keeps the
   word of each tool:

   | `action` | Pulumi `tool_action` | OpenTofu `tool_action` |
   | --- | --- | --- |
   | `plan` | `preview` | `plan` |
   | `apply` | `up` | `apply` |
   | `destroy` | `destroy` | `destroy` |
   | `refresh` | `refresh` | `refresh` |

   An apply job and a destroy job open in their plan phase with `access`
   `read` and their own `action`. A later ADR that adds an operation adds
   its value to this table. RFC 5 lists the names that the project manager
   gave and that have no operation yet.
6. **The subject (one-way door).** `sub` is a list of `key:value` pairs,
   joined with `:`, in the fixed order of this list:

   `namespace`, `namespace_id`, `collection`, `collection_id`, `tool`,
   `unit`, `unit_id`, `profile`, `profile_id`, `root_profile`, `access`,
   `phase`, `action`, `ref_type`, `ref`, `ref_protected`, `runner_pool`,
   `actor_type`, `actor_id`.

   - A **subject template** names the keys. The default template is
     `namespace`, `collection`, `tool`, `unit` and `access`, for example
     `namespace:acme:collection:payments@<collection ID>:tool:pulumi:unit:web/prod@<unit ID>:access:write`.
   - In `sub`, the keys `collection`, `unit`, `profile`, `root_profile`
     and `runner_pool` hold the name, `@` and the stable ID, as the
     immutable subjects of GitHub do. A person reads the name, and a trust policy matches the ID.
     A unit that a rename gives an old name, or a new collection or
     runner pool with the name of a deleted one, has another ID, so it
     never matches the trust of the old object. The namespace is in the issuer URL by its ID (ADR
     204 decision 1), so `namespace` holds the name only.
   - A template must start with `namespace` or `namespace_id`, and holds
     only the keys of the list. A per-run value (`job`, `commit`, `jti`,
     `change_order`) is never a key, so one unit has few subjects, and Azure
     can match each one exactly.
   - The server leaves out a key whose fact the open does not have, with its label.
     An open with no unit gives
     `namespace:acme:access:open`. The label of each value stays in `sub`,
     so a subject never looks like a subject with another key.
   - In a value, `%` becomes `%25`, `:` becomes `%3A` and `@` becomes
     `%40`. So a value never makes a new pair, and a `name@ID` value always
     splits at its one `@`.
   - The server refuses a template whose longest subject can pass 600
     characters (`profile.ErrSubjectTooLong`). The docs say that a subject
     above 127 characters does not fit `google.subject`, and show the
     mapping `google.subject = assertion.unit_id` for that case.
   - The namespace sets its template (ADR 204 decision 9). A login block
     can name its own template (ADR 201). A namespace can stop a block
     from doing so.

   **One-way door:** cloud trust policies hold subjects. A new key goes
   at the end of the order. The order never changes.
7. **Trust policy shapes.** The docs give one example for each cloud. With
   the default template:
   - AWS, a role for applies of one collection: `StringLike` on `sub`
     `namespace:acme:collection:payments@<collection ID>:*:access:write`, and
     `StringEquals` on `aud` `sts.amazonaws.com`.
   - Azure, one federated credential for each subject, for example
     `namespace:acme:collection:payments@<collection ID>:tool:opentofu:unit:network@<unit ID>:access:write`.
     A team with many units uses the template `namespace`, `collection`,
     `access` for Azure. It then needs two credentials for each collection.
   - Google Cloud: `google.subject = assertion.unit_id`, `attribute.access =
     assertion.access`, and a condition on `assertion.collection`.
   - Vault: `bound_claims` with `access`, `collection` and `ref_protected`.
   - **The trade-off of the default template.** The default template has
     no `ref_protected`, so that one unit has few subjects and Azure fits
     two credentials for each collection. AWS reads only `sub`, so an AWS
     role with the default template trusts each apply job of the
     collection from any branch, when the namespace has no gates. A team
     that wants only protected refs adds `ref_protected` to the template,
     for example `namespace`, `collection`, `tool`, `unit`, `access`,
     `ref_protected`, and matches `*:access:write:ref_protected:true`.
     A job with no resolved ref then has no `ref_protected` key in `sub`
     (decision 3), so it fails closed. The docs show this template, and
     ADR 200 uses it in the small-team example.
8. **Personal data.** The safe default holds no personal data:
   `actor_id` is an opaque ID. The namespace setting `includeActorName`
   adds `actor`, the login name. No setting adds an e-mail address, a
   full name or the groups of a person. The docs say that a login name
   can change and can be personal data, and that a trust rule matches
   `actor_id`. The audit entry of an issue records the subject and `jti`,
   never the token (ADR 204 decision 10).
9. **The context of an expression is the claim set.** An expression of a
   profile (ADR 201 decision 5) reads the facts of decision 3 as `context`,
   with each claim name in camelCase, for example `context.access`,
   `context.toolAction` or `context.refProtected`. The claim
   `profile_revision` is `context.revision`. `context.openDuration` is the
   one fact that is not a claim. One table in `pkg/profile` holds each claim,
   its name in `context` and its source (`profile.Claims`). A table-driven
   test checks each claim of decision 3 against it.
10. **What other products give, and what we do not copy.** We take the
    phase and the scope in `sub` (HCP Terraform, Spacelift), the
    `key:value` pairs that describe themselves (GitHub, Spacelift), and the
    stable IDs as claims (Pulumi ESC `/oidc/v2`, GitHub immutable subjects).
    We do not take the commit in `sub` (Buildkite), the e-mail address in a
    token (GitLab) or claim names in camelCase (Spacelift).

## Consequences

- A cloud trust policy can tell the plan phase from the apply phase, and a
  job from a person on a workstation, with only `sub`.
- `pkg/profile` holds `Claims`, `Subject`, `SubjectTemplate` and the
  escape rule. Table-driven tests cover each key, each absent fact, the
  escape and the length check.
- A rename of a unit or a collection changes the name part of the default
  subject. A trust policy that matches the name then refuses the new
  subject, so the change fails closed. A trust policy that matches only the
  ID part (`unit:*@<unit ID>`) keeps working.
- The research note holds the comparison table. A later version of a
  product may change its claims. The note has the date of each source.

## Open questions

RFC 5 keeps these open:

- The action names `dedrift` and `unseal`, which no operation has yet.
- Whether a claim names the blueprint or the Catalog request of a unit.
