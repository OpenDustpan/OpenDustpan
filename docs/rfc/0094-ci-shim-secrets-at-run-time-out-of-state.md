---
rfc: 94
title: The CI shim, which gives secrets to a tool in a team's own CI at run time and keeps them out of state
status: draft
created: 2026-10-20
---

# RFC 94. The CI shim, which gives secrets to a tool in a team's own CI at run time and keeps them out of state

Status: draft. Nothing in this RFC is decided. No code exists for it. The
owner answers the questions at the end.

## Problem

Many teams run OpenTofu or the Pulumi CLI in their own CI, not on an
OpenDustpan runner. They keep a static OpenDustpan token and their cloud
secrets in the secret settings of the CI. They give the secrets to the tool
as `TF_VAR_*` variables or through a `data "external"` source. OpenTofu
writes the result of a data source, and each value that reaches a resource,
to the plan and to the state in plain text. `sensitive = true` hides a
value in the output only.

OpenTofu 1.11 has a real answer: ephemeral values and write-only
attributes. Few modules use them yet. The Pulumi CLI encrypts a value in
state only when the program marks it secret.

The owner asked for a **CI shim**: a small, optional program in the team's
own CI. It exchanges the identity token of the CI job for a short-lived
OpenDustpan token, gets the secrets that the unit needs, starts the tool
with them, and records what the tool read. The owner also asked what other
work the shim can do (the comment on the theme issue).

This RFC asks:

1. What the shim is, what it is not, and where its code lives.
2. How a CI job proves its identity, and what the token that it gets can do.
3. How the secrets reach the tool, and which secrets stay out of the state
   of each tool.
4. What OpenDustpan can promise about secrets in state, and what it cannot.
5. Which other uses of the shim come in the first slice, and which later.

In this RFC, a **CI run** is one start of the shim in a team's own CI. It
is not a job: the server schedules no work and holds no claim.

## What is decided already

- **ADR 12, amended by ADR 209 decision 1.** OpenDustpan ships two client
  programs only: the OpenDustpan provider and the run command
  (`opendustpan profile run`). Each tool still connects through the
  protocol that its CLI speaks. A plain backend with no client program
  keeps working.
- **ADR 15.** An RFC holds open questions. An ADR records a decision after
  the owner accepts it.
- **ADR 17.** Every feature has a REST API, and every route declares its
  actions.
- **ADR 54.** A workload sends a JWT of its issuer to an exchange endpoint.
  The server checks the signature, the issuer, the audience and the
  expiry, then applies the **trust rules** of one namespace. A match gives
  a short-lived OpenDustpan access token for the principal that the rule
  names (credential kind `trust-token`, principal kind `workload`). A rule
  never gives access to another namespace. Each exchange writes an audit
  entry. ADR 54 holds the design. No code exists yet.
- **ADR 69 decision 6.** An exchanged workload token lives 1 hour. It is a
  setting.
- **ADR 101 decision 10, ADR 133 decision 12, ADR 134 decisions 6 to 8.**
  A job on an OpenDustpan runner already gets its secrets: as files of
  mode 0600 on a memory-backed volume, never in a variable, for the tool
  step only. OpenTofu gets the job token through a credentials helper
  that answers for the server host only. The launcher masks each secret
  of 4 bytes or more in each log. `job.IsCredentialEnv` is the
  credential-variable rule.
- **ADR 203.** An open of a profile resolves its values for one caller. A
  direct open (any credential that is not a job token) needs
  `profile:open` on each profile that it evaluates. Each open writes one
  audit entry with the paths and revisions, never a value. A refused open
  writes an entry too. An open never answers a part of the values as if
  it were all of them. Decision 4 says: "The job is not a hard boundary."
  The code of the unit can read each file and variable that the job gets.
- **ADR 205 decisions 3 and 4.** The server sets every claim of a token
  that it issues from its own records. An open with no job gets
  `access: open`, never `write`. An open that names a unit from a client
  does not get the unit claims, because the server cannot prove that the
  client runs for that unit.
- **ADR 209.** The OpenDustpan provider has a data source and an opt-in
  ephemeral resource, both `opendustpan_profile`. A value marked **never
  store** is readable only through the ephemeral resource. The run command
  writes `exports.files` to a directory of mode 0700 (memory-backed when
  one exists), sets the variables, starts the command as an argument list
  with no shell, and masks each secret in its output with `[masked]`.
- **ADR 213 decision 10 and ADR 224 decision 16.** Manual locks exist. The
  gates of `state.write` run before a write from a team's own CLI or CI.
- **ADR 240.** The principal kind decides the audit kind of a state
  download: a person gives `reveal`, a workload gives `tool-operation`.
- **ADR 250.** Every security path has at least two layers. Each layer
  stops the attack alone and has its own test. A CI token, a state file
  and each output of the tool are untrusted input.
- **RFCs 90 to 93** name the shim as a later producer: the scanner upload
  (RFC 90, option A1), the Markdown of the plan review view (RFC 91,
  section H), the client trace ID (RFC 92, option A4.1), and the CLI
  configuration of the mirror (RFC 93, section E, "What a team's own CI
  and a laptop receive").

## ADRs this RFC would amend

- **ADR 209 decision 1**, only if the owner picks option A2 or A3 (a third
  client program). A1 (a mode of the run command) needs no amendment.
- **ADR 54 decision 5**, only if the owner accepts the scope of a trust
  rule (B4). Today the grants of the principal decide everything.
- **ADR 203 decision 1**, to add the consumer `ci` (C1), and decision 9,
  if the owner accepts the audit kind of G3.
- **ADR 205 decision 3**, only if the owner accepts the `ci_*` claims (B6).
  The claim set is a one-way door, so this is an open question, not a
  recommendation for slice 1.

## Facts from primary sources

Checked on 2026-10-20.

### GitHub Actions OIDC tokens

From <https://docs.github.com/en/actions/reference/security/oidc> and
<https://docs.github.com/en/actions/concepts/security/openid-connect>:

- **Issuer.** `https://token.actions.githubusercontent.com`.
- **How a job gets one.** The workflow or the job must set
  `permissions: id-token: write`. The docs say: "Without `id-token: write`,
  the OIDC JWT ID token cannot be requested." The runner sets
  `ACTIONS_ID_TOKEN_REQUEST_URL` and `ACTIONS_ID_TOKEN_REQUEST_TOKEN`. A
  program sends a request to that URL with that bearer token, and can add
  `&audience=<value>`.
- **Audience.** The default `aud` is the URL of the repository owner, for
  example `https://github.com/octo-org`. The caller sets a custom one.
- **Claims.** Standard: `aud`, `iss`, `sub`, `exp`, `iat`, `jti`, `nbf`.
  Custom: `actor`, `actor_id`, `base_ref`, `head_ref`, `check_run_id`,
  `environment`, `event_name`, `job_workflow_ref`, `job_workflow_sha`,
  `ref`, `ref_type`, `repository`, `repository_id`, `repository_owner`,
  `repository_owner_id`, `repository_visibility`, `repo_property_*`,
  `run_id`, `run_number`, `run_attempt`, `runner_environment`
  (`github-hosted` or `self-hosted`), `workflow`, `workflow_ref`,
  `workflow_sha`.
- **Not in the list:** `ref_protected`, and a `sha` of the commit (only
  `workflow_sha` and `job_workflow_sha`).
- **Subject.** `repo:ORG/REPO:` and then the context, for example
  `ref:refs/heads/main`, `environment:prod` or `pull_request`. Repositories
  made after 15 July 2026, or that opt in, get the immutable form
  `repo:OWNER@OWNER-ID/REPO@REPO-ID:...`.
- **Lifetime.** The reference does not give one. In the example token,
  `exp - iat` is 300 seconds. The docs say nothing about replay.
- **Fork pull requests.** The OIDC pages do not say whether a workflow
  that a pull request from a fork starts can get a token. We could not
  find it in a primary source. An e2e item.
- **Masks.** <https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-commands>:
  a step writes `::add-mask::<value>` to its standard output. The docs warn:
  "Make sure you register the secret with 'add-mask' before outputting it
  in the build logs". A variable that a step writes to `GITHUB_ENV` is in
  each later step of the job.

### GitLab CI ID tokens

From <https://docs.gitlab.com/ci/secrets/id_token_authentication/>:

- **How a job gets one.** The job names each token under `id_tokens`, each
  with an `aud`. GitLab sets a variable with that name, for example
  `$FIRST_ID_TOKEN`. The job cannot ask for a new token while it runs.
- **Issuer.** The URL of the GitLab instance.
- **Claims.** Standard: `iss`, `sub`, `aud`, `exp`, `nbf`, `iat`, `jti`.
  Custom: `namespace_id`, `namespace_path`, `project_id`, `project_path`,
  `user_id`, `user_login`, `user_email`, `pipeline_id`, `pipeline_source`,
  `job_id`, `ref`, `ref_type`, `ref_path`, `ref_protected`, `environment`,
  `environment_protected`, `deployment_tier`, `runner_id`,
  `runner_environment`, `sha`, `ci_config_ref_uri`, `ci_config_sha`,
  `project_visibility`, and others.
- **Subject.** `project_path:{group}/{project}:ref_type:{type}:ref:{branch_name}`
  by default. The projects API can change it.
- **Lifetime.** "Expiry equals the job's timeout if one is set, otherwise
  5 minutes." A job timeout can be hours. GitLab signs the token with RS256.
- **Personal data.** The claims can hold `user_login` and `user_email`.

### OpenTofu ephemeral values and write-only attributes

From <https://opentofu.org/docs/language/ephemerality/>,
<https://opentofu.org/docs/language/ephemerality/ephemeral-resources/>,
<https://opentofu.org/docs/language/ephemerality/write-only-attributes/> and
<https://opentofu.org/docs/language/values/variables/>:

- **Version.** "Ephemeral resources can be used only with OpenTofu from
  v1.11 onwards." "Write-only attributes can be used only with OpenTofu
  v1.11 onwards." The release blog of 1.11.0
  (<https://opentofu.org/blog/opentofu-1-11-0>) is from December 2025.
- **What stays out.** An ephemeral value is "never stored in state or
  plan data". A write-only attribute "will always be written into the state
  and plan with a null value". For an ephemeral variable, "Only the
  variable names will be saved in the plan".
- **Where an ephemeral value can go.** Ephemeral resources, variables,
  outputs and locals, provider configuration, provisioners, `connection`
  blocks and write-only attributes. Any other use is an error. It cannot
  go in `count` or `for_each`.
- **What does not stay out.** "Sensitive values ... will still be stored in
  plaintext in the state and plan." Provisioner output is not suppressed
  for an ephemeral value.
- **A provider must support it.** An ephemeral resource and a write-only
  attribute work only with a provider that has them. The docs give
  `secret_string_wo` with `secret_string_wo_version` of a secret version
  resource as an example.
- **`TF_VAR_`.** OpenTofu reads `TF_VAR_<name>` for a declared variable,
  with the lowest precedence. The docs warn that ephemeral variables of a
  root module "require careful handling around `-var`/`-var-file`".

### The Pulumi CLI

From <https://www.pulumi.com/docs/iac/concepts/secrets/> and
<https://www.pulumi.com/docs/iac/concepts/resources/providers/>:

- **State holds everything, encrypted where marked.** "Values marked as
  secret are encrypted before they're persisted". A value is secret when
  `pulumi config set --secret`, `Output.secret`, `additionalSecretOutputs`
  or a secret input makes it so. Each value derived from a secret is
  secret.
- **Plain text in state.** Resource IDs are always plain text. An unmarked
  value stays plain text. The docs say that Pulumi "cannot guarantee that
  the `apply` callback itself will not expose the secret value".
- **Secrets providers.** `default` (the service), `passphrase`, `awskms`,
  `azurekeyvault`, `gcpkms`, `hashivault`. With the service provider,
  OpenDustpan holds the key of each stack (`pkg/secret`).
- **Provider credentials.** A default provider reads its settings from the
  stack config, or "implicitly through methods like environment variables
  or well-known file locations". "Explicit providers are themselves Pulumi
  resources, and their configuration values are Pulumi inputs." The docs
  we read do not say whether a variable that a provider reads by itself
  reaches the state. An e2e item.
- **Environment imports.** ADR 209 records, from the source of the CLI:
  the CLI sets `environmentVariables` of an `environment:` import in its
  own process, and writes each of `files` to a temporary file that it does
  not delete. It encrypts each secret of `pulumiConfig` and sends the
  ciphertext in the update.
- We found no ephemeral value or write-only input in the Pulumi docs that
  we read.

### How a tool reads a credential from its environment

- An OpenTofu provider configuration that reads a value from a variable
  or a file at run time (for example a shared credentials file named by
  `AWS_SHARED_CREDENTIALS_FILE`) does not put the value in the
  configuration. OpenTofu does not store a provider configuration in
  state. A provider configuration from an ephemeral value stays out of
  the plan too (above). The e2e suite checks both.
- OpenTofu reads a token for a host from `TF_TOKEN_<host>`, the CLI
  configuration, or a credentials helper (ADR 209 decision 4 lists the
  order).
- The Pulumi CLI reads its backend token from `PULUMI_ACCESS_TOKEN` or the
  credentials file of `PULUMI_HOME` (ADR 134 decision 7 uses the file).

## Options and recommendations

### A. What the shim is, and where its code lives

**What it does, in order:**

1. Gets the identity token of the CI job (B).
2. Exchanges it for an OpenDustpan trust token, for one namespace.
3. Opens the profiles of the unit with that token (C).
4. Checks for a manual lock of the unit, and stops with a clear message
   when one exists (I).
5. Starts the tool as an argument list, with the values in the process of
   the tool only, and masks the values in its output (C).
6. Sends the CI run record when the tool ends (F).

**What it is not.**

- **Not a runner.** It claims no job, holds no job session and runs no
  launcher. A job on an OpenDustpan runner already gets its secrets (ADR
  134 decision 7), and the shim adds nothing there.
- **Not a gate.** It never decides alone. Each check that it makes, the
  server also makes (owner comment: "the shim is one layer, not the only
  one").
- **Not required.** A team that does not use it keeps a plain backend.
- **It never changes what the tool does.** It does not edit the code, add
  arguments that change the plan, or read the state.

**Where the code lives.**

1. **A1. A CI mode of the run command:** `opendustpan profile run --ci`.
   No amendment of ADR 209. Against: the `opendustpan` binary is the whole
   server, so each CI job downloads the server.
2. **A2. A new command of the same binary:** `opendustpan ci run`. Same
   download. A new client program, so it amends ADR 209 decision 1.
3. **A3. A small static binary of its own**, for example `dustpan-ci`,
   pure Go with the standard library only, as the launcher is (ADR 134
   decision 1). It shares the code of the launcher for masks, the
   environment and the credentials helper (`launcher.Masker`,
   `launcher.BuildEnv`, `launcher.OpenTofuAnswer`). It amends ADR 209
   decision 1.
4. **A4. A thin action or component for each CI platform** that downloads
   A3, checks its SHA-256 and starts it. It holds no logic.

**Recommendation.** A3, with A4 later. A small binary with no server code
is less to download and less to trust in each CI job, and it has the same
masks and the same credentials helper as a runner. A1 is the fallback if
the owner wants no third client program.

### B. The identity exchange

**B1. How the shim gets the CI token.**

- GitHub Actions: from `ACTIONS_ID_TOKEN_REQUEST_URL` with
  `ACTIONS_ID_TOKEN_REQUEST_TOKEN`, with the audience of B2.
- GitLab CI: from the variable that `--id-token-env <name>` names. The
  job declares it under `id_tokens` with the audience of B2.
- Never from the command line, and never from a file in the work tree.

**B2. Audience binding.** Options:

1. The public URL of the installation.
2. The public URL and the namespace ID, for example
   `https://dustpan.example.com/ns/ns-k3v9q2m7x4t8p1w6z5r0`.
3. Any audience that the trust rule names.

**Recommendation.** B2.2 as the default that the console shows, and a rule
that names its audience explicitly (B2.3) for teams with their own form.
The server refuses a token whose `aud` is not the audience of the matched
rule. A token for one namespace then never works at another, also when
two namespaces trust the same issuer.

**B3. Replay limits and lifetime.** A CI token is a bearer token. A GitLab
token can live as long as the job timeout. Recommendation:

- Each `jti` exchanges once. The server keeps the `jti` with the issuer
  until the `exp` of the CI token, and refuses a second exchange. A token
  with no `jti` fails.
- The server refuses a CI token whose `iat` is more than 10 minutes in the
  past, or whose `exp - iat` is more than 6 hours, and each token that is
  expired or not yet valid, with a clock skew of 60 seconds.
- The trust token lives for the setting of ADR 69 decision 6 (1 hour by
  default), and never longer than the `exp` of the CI token plus 1 hour.
  A GitHub job can ask for a new CI token and exchange again. A GitLab job
  cannot, so a GitLab CI run longer than the trust token fails. Question 3.

**B4. The scope of a trust rule.** ADR 54 gives the matched principal all
its grants. A CI token for one repository then reaches every unit that the
principal can reach. Options:

1. No scope. One principal for each repository and its grants.
2. The rule can narrow, never widen: a list of units or collections, and
   a list of profiles. The trust token holds the intersection of the
   grants and the scope.

**Recommendation.** B4.2. It is a smaller blast radius for one token, and
it amends ADR 54 decision 5.

**B5. Claim conditions.** A rule matches on claims. The console offers
`repository`, `ref` and `environment` for GitHub Actions, and
`project_path`, `ref` and `ref_protected` for GitLab CI. The docs warn that
a rule on `sub` alone with a wildcard matches pull requests, and that
GitHub Actions has no `ref_protected` claim (facts). A condition on a claim
that the token does not hold fails closed.

**B6. Claims for clouds.** A cloud credential from a login block of a
profile is a token of the issuer of ADR 204, with the claims of ADR 205.
An open with a trust token gets `access: open`, `actor_type: workload` and
no unit claims. Options:

1. Keep ADR 205 as it is. A cloud trust policy names the `actor_id` of the
   workload principal.
2. Add `ci_issuer`, `ci_subject` and `ci_ref` from the verified CI token,
   and the unit claims when the scope of the rule (B4.2) holds exactly
   one unit.

**Recommendation.** B6.1 for slice 1. B6.2 changes the claim set, which is
a one-way door, so it is question 4.

### C. Delivery to the tool

**C1. The consumer.** The shim opens each profile with a new consumer `ci`.
It behaves as `run` (ADR 209 decision 8) with three changes: no open
session, the trust token only, and the answer of `exports.opentofu` and
`exports.pulumi` too. This amends ADR 203 decision 1. Option: reuse `run`,
with no amendment. Recommendation: `ci`, so the audit and the access
review can tell a CI run from a workstation.

**C2. Which profiles.** The shim names the unit (`--unit`), not the
profiles. The server answers the profiles that the unit attaches (ADR 203
decision 5), each at the revision of its tag at that time, and refuses a
profile that the trust token cannot open. The shim cannot ask for another
profile. Option: the shim names profiles, as the run command does.
Recommendation: the attachments, so the CI and the runner read the same
profiles.

**C3. How each value reaches the tool.**

| Kind of value | How the shim gives it | Out of the CI job's environment |
| --- | --- | --- |
| The OpenDustpan token for the tool and the provider | OpenTofu: a CLI configuration that names the credentials helper `dustpan`, which answers the trust token for the server host only (as `launcher.OpenTofuAnswer`). Pulumi: the credentials file of a new `PULUMI_HOME`. | yes |
| `exports.files`, and each credential file of a block | files of mode 0600 under a directory of mode 0700, on a memory-backed directory when one exists. The shim deletes them when the tool ends, also on a signal. | yes |
| `exports.variables`, plain | variables of the tool process | yes |
| `exports.variables`, secret | refused in slice 1 (question 5) | yes |
| `exports.opentofu` | a variables file in the same directory, and `-var-file=<path>` for `plan` and `apply` | yes |
| `exports.pulumi` | a stack settings file with `environment` entries, as `launcher.PulumiConfigFile` | yes |

- The shim never writes to `GITHUB_ENV`, never sets a variable of the CI
  job, and never writes a file in the work tree.
- The shim never passes its own environment to the tool. It passes the
  variables of the CI job that the tool needs (`PATH`, `HOME`, the
  variables of the platform) and drops each name that
  `job.IsCredentialEnv` matches, except the names that the profile sets.
  This is the same rule as on a runner, not a second rule.
- **With a trust token, the Pulumi paths answer empty
  `environmentVariables` and `files`**, as they do for a job token (ADR
  209 decision 5). The shim already wrote them, and the CLI would write
  the files to a temporary directory that it does not delete.

### D. Secrets out of state: what can stay out, what cannot

**OpenTofu 1.11 and later.**

| Secret | Out of the plan and the state? | How |
| --- | --- | --- |
| A provider credential (a cloud key, a token) | yes | a credential file or a variable that the provider reads, or `ephemeral "opendustpan_profile"` into the provider block |
| A value that a resource needs once (an initial password) | yes, where the provider has a write-only attribute | the ephemeral resource into the write-only attribute and its version attribute |
| A value for a provisioner or a `connection` block | yes | an ephemeral value. Provisioner output is not masked by OpenTofu, so the shim mask is the only mask there. |
| A value in `exports.opentofu` (a variables file) | **no**, unless the variable is `ephemeral = true` | OpenTofu keeps the values of non-ephemeral variables in a saved plan. The server seals the stored plan (ADR 102). |
| The result of a data source (`data "opendustpan_profile"`, `data "external"`) | **no** | stored in plain text in the plan and the state |
| A value that a resource makes or returns (a generated password, a new access key, a private key) | **no** | stored in the state. Only state encryption protects it. |
| An attribute of a resource with no write-only form | **no** | stored |

**OpenTofu before 1.11.** No ephemeral value and no write-only attribute.
Only a provider credential from a file or a variable stays out.

**The Pulumi CLI.** Everything that a program gives to a resource is in
the state. A provider credential that the provider reads from a file or a
variable by itself is not a resource input (e2e item). A secret config
value is in the state as ciphertext of the secrets provider of the stack.
An unmarked value and each resource ID are plain text.

**What OpenDustpan can promise.**

- The shim never writes a value to the CI job environment, the CI log
  (beyond its mask), the work tree or the CI run record.
- The server never stores a value in the CI run record or an audit entry.
- The server seals each OpenTofu state and each stored plan at rest (ADR
  41 decision 10), and holds the key of a Pulumi stack with the service
  secrets provider.
- The leak check (E) detects an exact copy of an injected value in a
  state that the server receives.

**What OpenDustpan cannot promise.**

- That the tool or the IaC code does not copy a value into the state. Only
  the module decides that, through ephemeral values and write-only
  attributes.
- That a value stays out of a plan file or a state file that the team
  keeps elsewhere, for example a plan artifact of the CI.
- That the leak check finds a changed copy (base64, part of a string, a
  hash), a value shorter than its minimum length, or a value that the shim
  did not inject.
- Anything for a team that does not use the shim.

The docs say "keeps secrets out of state when the module uses ephemeral
values", never "keeps secrets out of state".

### E. The leak check

**E1. Where the fingerprints come from.** The server opened each value
for the CI run, so it computes the fingerprints itself. No value goes back
from the shim. For each secret leaf of 8 bytes or more, the server keeps
HMAC-SHA-256 with a random key of the CI run. The server seals the key and
the fingerprints with a data key of the namespace, and deletes them
when the trust token ends plus 1 hour.

**E2. The check.** At each state write of the unit with that trust token,
the server walks the string leaves of the new state (OpenTofu), or the
plain-text leaves (Pulumi, where a secret leaf is ciphertext), computes the
HMAC of each leaf with the key of the CI run, and looks for a match. It
compares whole leaves only. It never logs a leaf.

**E3. What a match does.** Options:

1. A finding on the state version and the CI run, a notification, and an
   audit entry with the path of the leaf and the name of the profile entry.
2. Refuse the state write.

**Recommendation.** E3.1 only. A refused state write after an apply loses
the record of real resources, which is worse than the leak. A refusal
belongs before the apply, at a plan upload (I, later). State sealing stays
on as the last layer.

**E4. Limits.** Only exact copies. Only values of 8 bytes or more, so a
short or common value (`true`, a region) does not match by chance. Only
writes with the trust token of the CI run. At most 1,000 fingerprints for
each CI run and a state of at most the size limit of a state write.

### F. The CI run record

One record for each CI run, with the ID prefix `cir-`:

- the unit, the tool and its version (`tofu version -json`, `pulumi
  version`), the command and its exit code,
- the principal, the trust rule, the issuer and the CI claims that the
  rule read (`repository`, `ref`, `run_id`, `run_attempt`, or the GitLab
  `project_path`, `ref`, `pipeline_id`, `job_id`), never `user_email`,
- the commit (`sha` for GitLab CI, and `GITHUB_SHA` for GitHub Actions, marked
  as declared by the CI, because no claim holds it),
- each profile, revision and path that the open answered, never a value,
- the state versions that the CI run wrote, and the leak findings,
- a link to the CI job, built from the claims, and a link to the record in
  the CI log.

The shim sends the record when the tool ends, also on a failure. The
server makes the record at the exchange and closes it with that send, so a
CI run that dies still has its open record. A record that is not closed
after the trust token ends gets the outcome `unknown`.

### G. Authorization and audit

**G1. Routes and actions.** Paths are proposals, under
`/api/dustpan/v1/namespaces/{namespace}` (ADR 210).

| Method and path | Action |
| --- | --- |
| `POST /token-exchange` | none (public list of the route test). A trust rule decides. Rate-limited for each namespace and each issuer. |
| `POST /units/{unit}/ci-runs/open` | `profile:open` on each attached profile, and `unit:record-ci-run` on the unit |
| `PATCH /ci-runs/{id}` | the trust token that opened the CI run, only |
| `GET /ci-runs`, `GET /ci-runs/{id}` | `unit:read` on the unit |
| `GET`, `POST`, `PATCH`, `DELETE /trust-rules` and `/trust-rules/{id}` | as ADR 54 decision 8 |

The server checks the unit of the trust token scope (B4.2) and the action
in the same transaction as the write.

**G2. Each secret read.** Each open writes the entry of ADR 203 decision
9: each profile, revision and path, the CI run, the trust rule and the
issuer, never a value.

**G3. The audit kind.** A workload reads for a tool, so the entry is not a
`reveal`. Options: keep the kind `access` of ADR 203, or follow ADR 240
and write `reveal` when a person runs the shim with a personal token (on a
laptop, or with a token in a CI secret). Recommendation: follow ADR 240,
so a person who sees values is always a `reveal`. It amends ADR 203
decision 9.

**G4. Denials.** Each refused exchange writes an audit entry: the issuer,
the reason (bad signature, unknown issuer, wrong audience, replayed `jti`,
expired, no rule matched, a claim condition failed), the SHA-256 of the
`jti`, and never the token. An unknown issuer has no namespace scope yet,
so the entry goes to the scope `@server`, with the flood limit of the
denial audit (pull request 1108, in review).

### H. Defense in depth (ADR 250)

**The shim runs untrusted code.** The tool runs the IaC code, providers,
provisioners and external data sources of the team, in the same CI job
and as the same user. Each value that the shim gives to the tool is
visible to that code: it can read the process environment, the files, and
ask the credentials helper. It can send a value over the network, or write
it to the log in a changed form that no mask finds. The shim and the
server do not prevent this. The boundary is what the trust token can open
(B4) and what the cloud trust policy accepts (ADR 203 decision 4). The
security docs say so in their first lines.

| Layer | What it does | Its test |
| --- | --- | --- |
| Check at the boundary (server) | JWT: `alg` from an allowlist (`RS256`, `ES256`), never `none`. The `kid` is in the cached JWKS of the issuer. `iss`, `aud`, `exp`, `nbf`, `iat` and `jti` are present and valid. At most 16 KiB. Claim values are strict UTF-8 with no control character. The unit name and the profile paths have the rules of their own routes. | a token for each rule, sent to the verifier with the route and the rule match skipped |
| Check at the boundary (shim) | The server URL is `https`. The name in `--id-token-env` matches `[A-Z_][A-Z0-9_]*`. The unit name has the rules of the server. Each answered file name is one path segment from an allowlist. Each variable name passes the step variable rules of `pkg/job`. | a hostile answer of the server with the server checks skipped |
| Encode or contain | The tool starts with `exec.Command` and an argument list, never a shell. No value goes on a command line. Files go under `os.Root` of the directory of the CI run. The shim drops CR and LF from each log value. SQL uses placeholders only. | a value with a shell character, a path with `..`, a value with CR and LF |
| Limit the effect | The scope of the trust rule (B4.2). The trust token is for one namespace, for at most 1 hour. One exchange for each `jti`. The credentials helper answers the server host only. Values stay in memory and on a memory-backed directory, and the shim deletes them at the end. | a second exchange of one `jti`, and the helper asked for another host |
| Authorize at use | Each open checks `profile:open` and the scope for the unit. Each state write and each record update checks that the CI run belongs to the token. | a trust token of unit A that writes the record of unit B |
| Fail closed and record | Each error of the exchange, of an open or of a check stops the shim before the tool starts. The shim exits with code 2 and one line that names the failure. It never starts the tool with a part of the values. Each denial writes an audit entry (G4). | an open that fails for one profile of three: the tool does not start, an audit entry exists |

**Masks.** The shim masks each secret value of 4 bytes or more in the
standard output and the standard error of the tool, with `[masked]`, also
across a chunk boundary (`launcher.Masker`). It does not use
`::add-mask::` by default, because that writes the value to the standard
output, and so to the log when the output is not the GitHub Actions runner
(question 6). GitLab CI has no mask that a job can add at run time. A mask
finds exact copies only.

### I. Other uses of the shim

| Use | Slice | Why |
| --- | --- | --- |
| Secret delivery and the trust token (A to C) | 1 | the theme |
| The CI run record, names only (F) | 1 | small, and needed to show what a CI run read |
| A manual lock check before the tool starts | 1 | one read of the lock. The server refuses the write anyway (ADR 213 decision 10), so this gives only a clear message earlier. |
| The leak check (E) | 2 | needs the walk of each state format |
| The client trace ID (RFC 92, A4.1) | 2 | one header |
| The scanner upload (RFC 90, A1) | 2 | the upload route exists then, and the trust token is the credential |
| The CLI configuration of the mirror, and the check of lock file hashes and of the tool version (RFC 93) | after RFC 93 is accepted | RFC 93 says that a team CI gets no enforcement. The shim adds layer 1 only, and the server still sees only the state. |
| Stale plans: record the state version of a plan, and refuse an apply when the state changed | later | needs a plan upload from CI. OpenTofu already refuses a stale saved plan. |
| Renew short-lived credentials between a long plan and its apply | later | needs the renewal of ADR 203 decision 7 for a CI run |
| Approvals and gates in the team's own CI: send the plan JSON, wait for the approval | later | needs a plan upload, the plan binding of ADR 102 decision 7 for a CI run, and RFC 91 for the view. Large. |
| The Markdown of the plan review view as a comment (RFC 91, H) | later | needs the CI approvals above |
| The actions that a plan will run and that an apply ran (issue 1090) | later | needs that theme's design |
| Drift summaries and cost facts | later | needs a plan upload |

### J. The first slice

**Persona.** A platform team on GitHub Actions runs OpenTofu 1.11 against
OpenDustpan. Today a cloud access key is a secret of the repository. The
team wants the CI to hold no OpenDustpan token and no cloud key, and the
key to stay out of the state.

**Needs first:** the trust rule exchange of ADR 54 (OIDC discovery only),
and the open of ADR 203 for static secrets and `exports.files`. Login
blocks (ADR 207) and the OpenDustpan provider (ADR 209) are not needed for
slice 1: the cloud credential is a static secret of a profile, delivered
as a shared credentials file.

1. The routes of G1, the trust rule scope (B4.2), the `jti` store (B3) and
   the denial audit (G4).
2. The shim (A3) with B1 for GitHub Actions and GitLab CI, C1 to C3, the
   masks, the manual lock check and the CI run record (F).
3. The docs page: the workflow, the trust rule, the profile, and the table
   of section D with its warnings.
4. **The e2e step.** The test starts a test OIDC issuer (a discovery
   document and a JWKS on loopback with TLS and its own CA) and a stub of
   `ACTIONS_ID_TOKEN_REQUEST_URL` that signs GitHub-shaped tokens. It
   makes a trust rule for `repository` `acme/infra` and `ref`
   `refs/heads/main`, scoped to one workspace, and a profile with a secret
   file that holds 32 random bytes as a canary. The OpenTofu module gets
   the SHA-256 of the canary as a plain variable, uses a `local-exec`
   provisioner that checks the file against it, and writes a resource
   with a plain value. The test runs the real `tofu init`,
   `tofu plan -out` and `tofu apply` of each supported version from 1.11,
   each through the shim, against the TFE-compatible API. It checks:
   - the apply succeeds, and the stored state version, the saved plan file
     and the CI log hold no canary,
   - the CI run record names the profile, the revision and the paths, the
     tool version and the state version,
   - one audit entry for each open, with no value,
   - the CI job environment after the shim holds no new variable,
   - a token with another audience, a replayed `jti`, an expired token,
     and a token for `refs/heads/feature` each fail before the tool
     starts, each with an audit entry,
   - with a manual lock on the workspace, the shim stops before `tofu
     init` with a message that names the lock.

   A second case runs `pulumi up` through the shim with the same profile,
   and checks that no canary is in the checkpoint in plain text.

Later: the leak check (slice 2), login blocks with B6, the other uses of
section I.

## Open questions

The owner keeps these. Each recommendation is the author's.

1. Where the code lives: a mode of the run command (A1), a command of the
   server binary (A2), or a small binary of its own (A3), which amends ADR
   209 decision 1? Recommendation: A3.
2. Can a trust rule narrow the grants of its principal to units and
   profiles (B4.2), which amends ADR 54 decision 5? Recommendation: yes.
3. A GitLab CI token cannot be renewed in the job. Is a GitLab CI run
   limited to the trust token lifetime (1 hour by default), or does the
   shim renew its trust token through the server while its CI run is open?
   Recommendation: the limit in slice 1, and a renewal through the CI run
   record later, at most until the `exp` of the CI token.
4. Do issued cloud tokens get `ci_*` claims and the unit claims for a
   trust rule scoped to one unit (B6.2)? It changes the claim set, a
   one-way door. Recommendation: not in slice 1. Decide with the first
   login block in CI.
5. Can a CI run give a secret as a variable of the tool process, for an
   SDK that reads only a variable, or for `TF_VAR_` of an ephemeral
   variable? The shim cannot see whether the variable is ephemeral, so a
   secret in `TF_VAR_` of a non-ephemeral variable goes to the plan and the
   state. Recommendation: files only in slice 1. Later, an opt-in mark on
   the profile entry, with `job.IsCredentialEnv` deciding which names need
   it.
6. `::add-mask::` on GitHub Actions: never, opt-in, or on by default when
   the shim detects the runner? Recommendation: opt-in, only when the
   standard output is the runner, and always with the shim mask too.
7. The leak check: a finding only (E3.1), or also a refusal by policy?
   Recommendation: a finding only at the state write, and a refusal only at a
   plan upload, later.
8. The audit kind of an open by a person through the shim: `access` (ADR
   203) or `reveal` (ADR 240 style)? Recommendation: `reveal`.
9. Which CI platforms in slice 1: GitHub Actions only, or also GitLab CI?
   GitLab CI has `ref_protected` and `sha` claims, which GitHub Actions
   lacks. Recommendation: both. Forgejo Actions comes after a check of its docs.
10. The audience form of B2.2: the namespace ID in a path, or a URN?
    Recommendation: the URL form, so the console can show one string.

## Proposed glossary lines

- **CI shim**: the optional client program that a team runs in its own
  CI. It exchanges the identity token of the CI job for a trust token,
  gives the values of the unit's profiles to the tool process only, and
  sends the CI run record. _Avoid_: CI runner, adapter, wrapper (alone),
  runner (that is an OpenDustpan runner).
- **CI run**: one start of the CI shim, recorded with the ID prefix `cir-`.
  It is not a job. _Avoid_: run (alone), job, pipeline.
- **Leak check**: the server compares each whole string leaf of a state
  that a CI run writes with fingerprints of the secrets of that CI run, and
  records a finding for a match. _Avoid_: secret scanning, leak detection.

## Outcome

Empty until the RFC is accepted (link the ADR) or dropped (say why).
