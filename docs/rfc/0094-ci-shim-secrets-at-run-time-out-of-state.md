---
rfc: 94
title: The CI shim, which gives secrets to a tool in a team's own CI at run time, keeps them out of state, and encrypts the local plan and state files at rest
status: draft
created: 2026-10-20
---

# RFC 94. The CI shim, which gives secrets to a tool in a team's own CI at run time, keeps them out of state, and encrypts the local plan and state files at rest

Status: draft. The scope decisions of section K, and the log redaction and
memory limits of K9 and K10, are decided (ADR 352). Nothing
else in this RFC is decided. No code exists for the shim. The owner answers
the questions at the end.

The shim has two jobs: secrets at run time that stay out of state (sections
A to J), and encryption at rest of the plan files and state files that the
tool writes in the team's CI (section K). A team that runs the tool without
the shim is out of scope for both jobs.

Read section H first. The shim runs in the same CI job as the code that it
starts. It is a convenience layer, not a security boundary. Slice 1 moves a
long-lived cloud key from the settings of the CI into an OpenDustpan
profile. It does not delete that key.

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
state only when the program marks it secret. Pulumi has write-only fields
too, but it still writes their values to the state, as secrets (facts).

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
6. How the shim encrypts at rest the plan files and state files that the
   tool writes in the team's CI, and what that cannot protect (K).

In this RFC, a **CI run** is one start of the shim in a team's own CI. It
is not a job: the server schedules no work and holds no claim.

## What is decided already

- **ADR 41 decisions 2 and 10, and ADR 151 decision 7 (owner, 2026-10-11 and
  2026-10-17).** A state read masks secret values. The server seals OpenTofu
  state at rest. **Built or open?** Checked on main on 2026-11-07: the workspace
  service seals each new OpenTofu state through the `workspace.Sealer` port
  (`pkg/workspace/service.go`, `storeContent`), and `secret.Service` in
  `pkg/secret` implements it with the data key of the workspace scope. The
  `dustpan` package wires it. So the sealing of ADR 41 decision 10 is built.
  The move to an item key for each state version (ADR 151 decision 7) is not
  built for OpenTofu state: `pkg/workspace` has no reference to the item
  sealer. It is an open work item. The Pulumi update events use the item
  sealer (`pkg/stack/event_seal.go`).
- **ADR 352 (owner, 2026-11-07).** The shim encrypts at rest the plan files
  and state files that the tool writes in the team's CI. A team that runs the
  tool without the shim is out of scope. The docs say so in plain words and
  give the one command that adopts the shim. The shim stays a convenience
  layer, not a security boundary.
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
- **ADR 223 decisions 1 and 6.** An access limit is one Cedar `forbid`
  policy. It holds for a token that a trust rule issued (credential kind
  `trust-token`). A trust rule still decides whether the server issues a
  token, and never passes a limit. So an installation can already narrow
  every trust token with a limit. B4 asks whether the rule itself narrows
  too.
- **ADR 210 decision 6.** A mutating route takes an `Idempotency-Key`. A
  route whose answer holds a credential never replays it: a replay answers
  409 `request.idempotency-key-replayed-secret`. A route that does not take
  the key declares it, and a request with the key answers 400.
- **ADR 72 decisions 5 and 8.** Control tables stay outside the restore
  swap. A counter that limits failures is a windowed counter in the
  database, shared by all instances.
- **ADR 202 decision 7.** A profile can have an open rule: an open needs an
  approved access request first.
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
- **ADR 209 decision 5**, to apply its rule for a job token (empty
  `environmentVariables` and `files` on the Pulumi paths) to a trust token of
  a CI run too. The amendment covers trust tokens of CI runs only. It
  changes nothing for a personal token or an access token of a person.
- **ADR 54 decision 5**, only if the owner accepts the scope of a trust
  rule (B4.2). Today the grants of the principal decide everything. B4.3
  (an access limit with a trust-rule fact) needs a new fact in the access
  schema of ADR 226 decision 2, not an amendment of ADR 54.
- **ADR 210 decision 6**, only if the owner picks the amendment of B3
  (the exchange route takes the key and replays by nonce). The
  recommendation needs none: the route declares that it takes no key.
- **ADR 203 decision 1**, to add the consumer `ci` (C1). The consumer `ci`
  takes trust tokens only.
- **ADR 205 decision 3**, only if the owner picks B6.2 (the `ci_*` claims,
  and the unit claims when the scope holds one unit). The claim set is a
  one-way door, so this is an open question, not a recommendation for slice 1.
- **ADR 72 decision 5**, a note for the future ADR: the control list gets the
  `jti` table (B3), the table of the fingerprints and the table of the CI run
  keys (E1). The coverage test of ADR 72 decision 5 reads that list.
- **ADR 203 decision 12**, a note for the future ADR: its body text says that
  an open with no session ignores the key. ADR 210 decision 6 amends it, and
  B3 says which reading this RFC follows.

## New dependency

**Status: needs owner approval.** The server must check JWTs of other
issuers and read their JWKS. The standard library has no JWS or JWK code. Options: a maintained JWT and JWK
library, or about 300 lines of our own code on `crypto/rsa` and
`crypto/ecdsa`. Both are a new dependency or new security code. CLAUDE.md
says to ask before a new dependency. **The owner must approve the choice
before any code.** The shim itself verifies no token, so the shim needs no
such library.

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
- **The docs table does not list `ref_protected`, and it does not list a
  `sha` of the commit** (only `workflow_sha` and `job_workflow_sha`). But the
  live discovery document
  (<https://token.actions.githubusercontent.com/.well-known/openid-configuration>,
  read on 2026-10-20) lists both `ref_protected` and `sha` in
  `claims_supported`. It also lists `enterprise`, `enterprise_id`,
  `environment_node_id`, `issuer_scope` and `check_run_id`. The example
  token of the concepts page has `sha`. A token probably holds both claims,
  but a list of supported claims is not proof. An e2e item: a real token from
  a test workflow shows which claims it holds, for a push, a pull request and
  an `environment` job.
- **Subject.** `repo:ORG/REPO:` and then the context, for example
  `ref:refs/heads/main`, `environment:prod` or `pull_request`. Repositories
  made after 15 July 2026, or that opt in, get the immutable form
  `repo:OWNER@OWNER-ID/REPO@REPO-ID:...`.
- **Lifetime.** The reference does not give one. In the example token,
  `exp - iat` is 300 seconds. The docs say nothing about replay.
- **Fork pull requests and `pull_request_target`.** The OIDC pages do not
  say whether a workflow that a pull request from a fork starts can get a
  token. We could not find it in a primary source. An e2e item. A
  `pull_request_target` workflow runs in the context of the base
  repository, so its token has the claims of the base repository (B5).
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
- **Lifetime.** "The expiry time for the token is set to the job's timeout
  if specified, or 5 minutes if no timeout is specified." A job timeout can
  be hours. The token has no renewal. "The ID token is encoded by using RS256
  and signed with a dedicated private key."
- **Merge requests from a fork.** From
  <https://docs.gitlab.com/ci/cloud_services/>: "When the source project and
  the target project are different, merge request pipelines have
  `ref_protected:false`." The docs advise a trust policy to require
  `ref_protected:true`.
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
  secret are encrypted before they're persisted". The secrets page says:
  secrets "are marked internally as needing encryption before persisting in
  the state file". A value is secret when
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
- **Write-only fields exist, but they reach the state.** From
  <https://www.pulumi.com/docs/iac/concepts/secrets/write-only-fields/>:
  "Write-only fields are resource properties that can be set during
  resource creation but are never returned by the cloud provider's API."
  "Pulumi supports these properties mainly for schema parity with the
  underlying Terraform provider." When Pulumi meets one: "Its initial value
  gets written to Pulumi state inputs as a Secret. It will never appear in
  state outputs." So the value is in the state, as ciphertext of the secrets
  provider. Pulumi has no ephemeral value in the sense of OpenTofu. Issue
  3201 of `pulumi/pulumi-terraform-bridge` has the title "write-only fields:
  Do not write to state" and is OPEN (read on 2026-10-20). It says that
  encrypted write-only values are written to state. A commit from pull
  request 3215 refers to it. So the documented behavior (a Secret in state
  inputs) and the wish of that issue (no write to state) differ, and the
  current bridge behavior can change between versions. An e2e item checks
  the behavior of the bridge version under test (J).

### OpenTofu state and plan encryption, and local plan files of Pulumi

Checked on 2026-11-07. From <https://opentofu.org/docs/language/state/encryption/>
(the page of the current docs, which lists OpenTofu 1.13 as the latest
release), <https://opentofu.org/blog/opentofu-1-7-0/> and
<https://www.pulumi.com/docs/iac/concepts/update-plans/>,
<https://www.pulumi.com/docs/iac/concepts/secrets/>,
<https://www.pulumi.com/docs/iac/cli/commands/pulumi_preview/>.

- **Version.** The release blog of OpenTofu 1.7.0 has the title "OpenTofu 1.7.0
  is out with State Encryption, ...". The docs page does not name a minimum
  version in the text that we read. So "1.7 and later" rests on the release
  blog.
- **What it does.** "OpenTofu supports encrypting state and plan files at
  rest, both for local storage and when using a backend." Configuration is
  in a `terraform { encryption { ... } }` block, or in the environment
  variable `TF_ENCRYPTION`. "Both solutions are equivalent and if you use
  both, OpenTofu will merge the two configurations, overriding any
  code-based settings with the environment ones."
- **The block.** `key_provider "<type>" "<name>"`, `method "<type>" "<name>"`
  with `keys = key_provider...`, and a `state {}` block and a `plan {}`
  block, each with `method` and an optional `fallback { method = ... }`. The
  only method is `aes_gcm`. `enforced = true` in `state` or `plan` stops
  OpenTofu from writing plain data when the environment variable is absent.
  The docs show it for the environment case: "to prevent unencrypted data
  from being written in the absence of an environment variable".
- **Key providers.** `pbkdf2` (passphrase of at least 16 characters, 200,000
  iterations at least, 600,000 by default), `aws_kms`, `gcp_kms`,
  `azure_vault`, `openbao` and `external`. For a key management system,
  "OpenTofu generates a fresh data encryption key for each state or plan file
  and wraps it with the key you reference". The docs advise a separate key
  management key for each state file. The OpenBao provider reads `BAO_TOKEN`
  and `BAO_ADDR`. `pbkdf2` takes `chain = key_provider...` to "receive the
  passphrase from another key provider". The `external` provider runs a
  command, and the docs say: "You can use this provider in conjunction with
  the `chain` option in the PBKDF2 key provider to input a passphrase from an
  external program." The protocol has three steps: the program writes a
  one-line header (`{"magic":"OpenTofu-External-Key-Provider","version":1}`),
  OpenTofu writes metadata to its standard input, and the program writes the
  key information to its standard output.
- **Why PBKDF2 and not a short static key.** "AES-GCM ... suffers from 'key
  saturation'." The docs advise a key-derivation provider with "a long and
  complex passphrase", or a key management system that rotates keys.
- **Rollover.** A `fallback` block names the old method. OpenTofu reads with
  the fallback when the new method fails, and always writes with the new
  method. Do not rename a key provider or a method after data exists: the
  encrypted data holds their names as metadata. The option
  `encrypted_metadata_alias` fixes the metadata key, so a rename is safe.
- **Plain data is refused.** With encryption on, OpenTofu refuses to read
  plain data unless an `unencrypted` method is a `fallback`. The docs show the
  migration in both directions.
- **What it does not protect.** The docs say: "encryption does not protect
  against data loss ... and it also does not protect against replay attack",
  and "OpenTofu does not and cannot protect the sensitive values in the state
  file from the person running the `tofu` command". Without the key, the
  files "become unrecoverable".
- **Remote backend.** ADR 41 records, from the source of the CLI, that the
  `cloud` backend writes the state with the encryption, reads it back, and
  sends plain state, `json-state` and `json-state-outputs` to the server. So
  client-side encryption does not change what the server receives. It
  protects the files in the work tree of the CI, and the saved plan.
- **Pulumi plan file.** `pulumi preview --save-plan <path>` ("[PREVIEW]") saves
  the proposed operations to a plan file. The page of update plans says that
  the format is "currently experimental and subject to change", and shows a
  JSON document with `manifest`, `config` and `resourcePlans`. The reference
  of `pulumi preview` says: `--show-secrets` shows secrets in plain text in
  the CLI output, and "if used with --save-plan the secrets will also be shown
  in the plan file". So a default plan file does not hold plain secrets by
  that flag, but the docs do not say what it holds for an unmarked value. An
  e2e item. We found no option of the CLI that encrypts the plan file.
- **Pulumi secrets provider and local files.** Secrets "are encrypted before
  they're persisted". The providers are `default` (the service), `passphrase`,
  `awskms`, `azurekeyvault`, `gcpkms` and `hashivault`. With the service
  backend, the checkpoint stays on the server. The CLI keeps its credentials
  in `~/.pulumi/credentials.json` (state and backends page), which is the
  `PULUMI_HOME` file that the shim already puts in a throwaway directory (C3).
  The stack config file `Pulumi.<stack>.yaml` holds secret values as
  ciphertext (secrets page). The plan file is the one local file of Pulumi
  that we know can hold values and has no CLI encryption.

### How a tool reads a credential from its environment

- An OpenTofu provider configuration that reads a value from a variable
  or a file at run time (for example a shared credentials file named by
  `AWS_SHARED_CREDENTIALS_FILE`) does not put the value in the
  configuration. OpenTofu does not store a provider configuration in
  state. A provider configuration from an ephemeral value stays out of
  the plan too (above). The e2e suite checks both.
- From <https://opentofu.org/docs/cli/config/environment-variables/>: "The
  value of TF_CLI_ARGS will specify additional arguments to the
  command-line." "These arguments are inserted directly after the
  subcommand (such as plan) and before any flags specified directly on the
  command-line. This behavior ensures that flags on the command-line take
  precedence over environment variables." `TF_CLI_ARGS_name` affects that
  command only. "The value of the flag is parsed as if you typed it
  directly to the shell. Double and single quotes are allowed to capture
  strings and arguments will be separated by spaces otherwise."
- OpenTofu reads a token for a host from `TF_TOKEN_<host>`, the CLI
  configuration, or a credentials helper (ADR 209 decision 4 lists the
  order).
- The Pulumi CLI reads its backend token from `PULUMI_ACCESS_TOKEN` or the
  credentials file of `PULUMI_HOME` (ADR 134 decision 7 uses the file).

## Options and recommendations

### A. What the shim is, and where its code lives

**What it does, in order:**

1. Gets the identity token of the CI job (B).
2. Exchanges it for an OpenDustpan trust token, for one namespace. One start
   of the shim makes one exchange (B3).
3. Opens the profiles of the unit with that token (C). The server makes
   the CI run record in this call, because the exchange does not know the
   unit (F).
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
- **It does not change what the tool does.** It does not edit the code or
  read the state. It adds only the variables file of `exports.opentofu`
  (C3). To do that, it reads the arguments of the tool for the subcommand,
  or it sets `TF_CLI_ARGS_<command>` (C3). It accepts an argument list
  only, never a shell string.

**Where the code lives.**

1. **A1. A CI mode of the run command:** `opendustpan profile run --ci`.
   No amendment of ADR 209. Against: the `opendustpan` binary is the whole
   server, so each CI job downloads the server.
2. **A2. A new command of the same binary:** `opendustpan ci run`. Same
   download. A new client program, so it amends ADR 209 decision 1.
3. **A3. A small static binary of its own**, for example `dustpan-ci`,
   pure Go with no cgo, as the launcher is (ADR 134 decision 1). It
   imports the code of the launcher for masks, the environment and the
   credentials helper (`launcher.Masker`, `launcher.BuildEnv`,
   `launcher.OpenTofuAnswer`). Its dependencies are then those of
   `pkg/launcher`, not the standard library alone. The e2e suite or the
   release build measures the size of the binary. `launcher.PulumiConfigFile`
   does not exist yet and is planned work (C3). A3 amends ADR 209 decision 1.
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
- The shim does not pass the CI token to the tool (H).

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

**B3. Replay limits, lifetime and many starts in one job.** A CI token is
a bearer token. A GitLab token lives for the job timeout and the job cannot
renew it (facts). A GitHub job can ask for a new token at any time. Rules
that are the same for each platform:

- Each `jti` exchanges once, with one exception: the nonce retry below.
  The server keeps the `jti` with the issuer, in the database, in a table
  with a unique key on issuer and `jti`. The exchange does an insert that
  fails on a duplicate, in the same transaction as the issue of the trust
  token. Server instances hold no state in memory for this (ADR 72). The
  row stays until the `exp` of the CI token plus the clock skew. A token
  with no `jti` fails.
- **The `jti` table is a control table (ADR 72 decision 5).** A restore does
  not replace it, on PostgreSQL and on SQLite. Else a restore to an older
  state would empty the table, and a CI token that the server already
  exchanged could exchange again. The same holds for the table of the
  fingerprints of E and for the keys of the CI runs. The coverage test of
  ADR 72 decision 5 lists them in the control list. Rows are short-lived, so
  the table stays small. Fallback if the owner wants these tables in the
  backup list: after a restore the server refuses each CI token with an
  `iat` before the time of the restore.
- **Namespace export and import.** Proposal: the `jti` table is not part of
  them. The server keys it by issuer and `jti`, not by namespace. The ADRs read for
  this RFC define no export of one namespace. ADR 42 exports tables.
- **The counter of the exchange rate limit** (G1) is a windowed counter in
  the database, shared by all instances, as the sign-in failure budget is
  (ADR 72 decision 8). A counter in the memory of one instance would give
  N times the limit with N instances.
- The server refuses an expired token and a token not yet valid, with a
  clock skew of 60 seconds, and a token whose `exp - iat` is more than 6
  hours.
- The trust token lives for the setting of ADR 69 decision 6 (1 hour by
  default), and never longer than the `exp` of the CI token plus 1 hour.

The problem: one `iat` window of 10 minutes and one exchange for each `jti`
break a GitLab job that starts the shim three times (`init`, `plan -out`,
`apply`). The second start has the same token, with the same `jti`, and an
`iat` that is older than 10 minutes. Options:

1. **One start runs a command list.** `dustpan-ci run --unit U -- tofu init
   -- tofu plan -out=p -- tofu apply p` (the exact form is for the design).
   One exchange, one open, one CI run record. The commands run in order, and
   the shim stops at the first failure. The trust token stays in the memory
   of the shim. The OpenTofu credentials helper answers it from that memory
   (C3). For Pulumi, the shim writes it to the credentials file of a new
   `PULUMI_HOME`, on a memory-backed directory when one exists. The shim
   deletes that file when the tool ends, also on a signal (C3). No later start
   reads the token. This differs from B3.3, which keeps the token on disk
   between the starts. The Pulumi file exists only while the tool runs.
   Against: the team must
   write the steps of the job as one call.
2. **An `iat` window for each platform.** 10 minutes for GitHub Actions. For
   GitLab CI, the window is the `exp` of the token (at most 6 hours). The
   `jti` still exchanges once, so many starts still fail. This alone does not
   solve the problem.
3. **One exchange for each job, and the trust token kept in a file** under a
   directory of mode 0700 that a later start reads. Many starts, one
   exchange. The exposure: the trust token (up to 1 hour) sits on disk
   between the starts, and the code of every tool start and every later step
   of the job can read it. That equals a CI token that leaks to the code,
   for up to 1 hour, with one namespace and the scope of the rule (B4.2).

**Recommendation.** B3.1 for slice 1, with the GitHub window of 10 minutes
and the GitLab window of B3.2. The docs show GitLab jobs with one call.
B3.3 is not in slice 1, because it keeps a bearer token on disk. If the owner
wants B3.3 later, the file is bound to the CI job (`job_id` or `run_id` and
`run_attempt`) and deleted by the job at its end.

**A retry after the server accepted the `jti`.** The shim sends the
exchange request again when the network fails after the server wrote the
`jti` row but before the shim read the answer. A second exchange of the same
`jti` is refused, so the job would fail. Design: the shim makes a random
nonce of 128 bits for each start, and sends it with the CI token. The server
stores the SHA-256 of the nonce with the `jti` row. The rules:

- A second exchange with the same `jti` and the same nonce, in 5 minutes
  from the first, is the **one exception** to "each `jti` exchanges once".
  It gets a new trust token for the same principal and the same token
  family, and the server revokes each earlier trust token of the family. The
  token family is the ID of the `jti` row. The server stores no token.
- **At most 3 retries** for each `jti`. A fourth is refused with an audit
  entry, as is a retry after the 5 minutes.
- A second exchange with another nonce is a replay: refused, with an audit
  entry. A holder of the CI token only does not know the nonce. A holder
  that also reads the memory of the shim can retry as the shim does, but
  that holder has the trust token already.
- **Two exchanges at the same time with the same `jti` and nonce.** The
  insert of the `jti` row decides which request is the first. A retry is a
  compare-and-set on the retry count of the row. One request wins and gets
  its trust token. The other gets 409 with the error code
  `request.exchange-conflict` and an audit entry. It never gets the token
  of the winner, because the server stores none. The shim may send the
  request again, and that counts against the cap of 3.

**Why this is not an `Idempotency-Key`.** ADR 210 decision 6 says that a
route whose answer holds a credential never replays it: a replay answers 409
`request.idempotency-key-replayed-secret`. The exchange answer holds a
trust token, so a key would give the shim an error, not a token. Options:

1. **The exchange route declares that it takes no `Idempotency-Key`**
   (decision 6 then answers 400 for a request that sends one). The nonce
   gives the safe retry. The nonce differs from a key in three ways: the
   server never replays a stored answer (it issues a new credential and
   revokes the old one), the request must carry a valid CI token as well,
   and the server stores only a hash of the nonce, never the answer.
2. Amend decision 6 for the exchange route: the key and the nonce are one
   value. Against: a decision of the owner (2026-10-18) changes for one
   route, and the rule "a replay never hands a credential again" gets an
   exception.

**Recommendation.** Option 1: the route declares that it takes no key. No
amendment.

**A lost answer of `ci-runs/open`.** The same rule applies. The answer of
the open holds profile values, so the route declares that it takes no
`Idempotency-Key` either. The shim recovers with a **new open**. A new open
makes a new CI run record and writes its own audit entry (ADR 203 decision
9), so the audit shows both reads. The record of the lost open gets the
outcome `unknown` when the trust token ends (F). The e2e suite tests a lost
answer of the exchange and of the open with a proxy that drops it.

**Which rule wins for consumer `ci`.** Two ADRs answer an open with no
session in two ways. ADR 203 decision 12 says that such an open ignores the
`Idempotency-Key`. ADR 210 decision 6 says that a route that does not take the
key answers 400, and that the server never ignores the key without an error.
ADR 203 names ADR 210 decision 6 as the amendment of its decision 12 in its
status line, so ADR 210 decision 6 is the newer decision on this point. This
RFC follows it: `ci-runs/open` declares that it takes no key, and a request
with the key answers 400. The body text of ADR 203 decision 12 still says that
it ignores the key. The owner must check this reading.

**B4. The scope of a trust rule.** ADR 54 gives the matched principal all
its grants. A CI token for one repository then reaches every unit that the
principal can reach. ADR 223 already lets an installation narrow every
trust token with an access limit (decisions 1 and 6). Options:

1. No scope in the rule. One principal for each repository and its grants.
   An administrator can add access limits.
2. The rule can narrow, never widen: a list of units or collections, and
   a list of profiles. The trust token holds the intersection of the
   grants and the scope.
3. Access limits with a trust-rule fact. The access schema (ADR 226
   decision 2) gets the fact `trust_rule` (the ID of the matched rule) for a
   trust token. An administrator writes a Cedar `forbid` limit such as
   "forbid every action on a unit that is not `U` when the trust rule is
   `R`". No new narrowing mechanism: the Authorizer, the audit and the
   fail-closed rule of ADR 223 already exist.

**Recommendation.** B4.2, and B4.3 as the layer above it. They combine:
the scope of the rule travels with the rule, so the person who makes the
rule in the console sets the scope in the same form and cannot forget it. An
access limit is a separate object with its own scope and its own action
(ADR 223 decisions 2 and 9), so it suits an administrator who wants one ceiling
for many rules. Each stops the attack alone (ADR 250). If the owner wants
one mechanism only, B4.3 is smaller (no amendment of ADR 54) but a rule with
no limit has no scope. B4.2 is still recommended as the default, because a
new rule is narrow without a second object. The scope is narrow-only. It
is enforced in the Authorizer, for every action of the trust token (not in
the handlers of single routes), and it fails closed: a token with a scope
and an action or object that the scope does not name is denied. A read of
the state of another workspace, for example by `terraform_remote_state`,
fails unless that workspace is in the scope. Each denial writes an audit
entry (ADR 54 decision 8).

**B5. Claim conditions.** A rule matches on claims. The console offers
`repository`, `ref` and `environment` for GitHub Actions, and
`project_path`, `ref` and `ref_protected` for GitLab CI. A rule on `sub`
alone with a wildcard matches pull requests. A condition on a claim that the
token does not hold fails closed. Whether a GitHub token holds
`ref_protected` is an e2e item (facts).

A `pull_request_target` workflow runs the code of the base repository with
its secrets and its OIDC token, and the pull request can influence what it
runs. A fork pull request is code from outside the team. Options for a rule
that gives write access (apply):

1. `repository` and `ref` only.
2. Also a condition on `event_name` (for example `push` only) and on
   `job_workflow_ref` or `workflow_ref`, so that only one workflow file at
   one ref can use the rule.
3. Also `environment`, with deployment protection rules on the GitHub
   environment (required reviewers, a branch limit). GitHub then withholds
   the token until the protection passes.

**Recommendation.** B5.3 for apply, and B5.2 for plan. The console shows
`environment` for every rule that can write state. The console denies
`event_name: pull_request_target` by default, and the user must delete the
deny rule on purpose. Whether GitHub withholds an OIDC token from a
workflow that a fork pull request starts is an e2e item. For GitLab CI, a
merge request pipeline from a fork has `ref_protected: false` (facts), so
the console requires `ref_protected: true` in each GitLab rule that can
write.

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

**B7. Downloading the discovery document and the JWKS.** The server downloads
a URL that comes from outside, so this is security code (H).

- The server downloads only for an `iss` that a trust rule of the namespace
  of the request names. It decides this from the database, before any
  network call. An unknown issuer is denied with no download (G4).
- `https` only. The server follows no redirect to another scheme or host.
  The `jwks_uri` of the document passes the same checks.
- The server checks the resolved IP address when the connection opens, not
  before, so a changed DNS answer cannot help. It denies loopback, private,
  link-local, unspecified and multicast addresses. A test-only option allows
  the loopback issuer of the e2e suite. The option is set in code by the
  harness. No flag and no environment variable sets it.
- Limits: the discovery document at most 64 KiB, the JWKS at most 256 KiB,
  5 seconds to connect and 10 seconds in all, at most 100 keys. The cache
  holds at most 1,000 issuers, at most 20 for each namespace, for 1 hour. An
  unknown `kid` causes at most one new download for each issuer in 5 minutes.
- The `issuer` of the discovery document must equal the `iss` of the token,
  byte for byte.

### C. Delivery to the tool

**C1. The consumer.** The shim opens each profile with a new consumer `ci`.
It behaves as `run` (ADR 209 decision 8) with three changes: no open
session, the trust token only, and the answer of `exports.opentofu` and
`exports.pulumi` too. **The consumer `ci` takes trust tokens only.** A
person who needs the values on a laptop or in a shell uses `profile run`.
The server refuses `ci` for any other credential kind. This amends ADR 203
decision 1. Option: reuse `run`, with no amendment. Recommendation: `ci`, so
the audit and the access review can tell a CI run from a workstation.

**C2. Which profiles.** The shim names the unit (`--unit`), not the
profiles. The server answers the profiles that the unit attaches (ADR 203
decision 5), each at the revision of its tag at that time, and refuses a
profile that the trust token cannot open. The shim cannot ask for another
profile. Option: the shim names profiles, as the run command does.
Recommendation: the attachments, so the CI and the runner read the same
profiles.

**Open rules (ADR 202 decision 7).** A trust token has no person who can
ask for an access request and wait for an approval. So an open of the unit
that reaches a profile with an open rule (also by an import) **fails
closed**, as a job does (`profile.ErrOpenRule`). The shim stops before the
tool starts, with exit code 2 and a message that names the profile. The
server writes a refused-open audit entry. A team that needs a gated
profile in a CI run uses a job on a runner, or waits for a later RFC about
approvals in a team's own CI (I).

**C3. How each value reaches the tool.**

| Kind of value | How the shim gives it | Out of the CI job's environment |
| --- | --- | --- |
| The OpenDustpan token for the tool and the provider | OpenTofu: a CLI configuration that names the credentials helper `dustpan`, which answers the trust token for the server host only (as `launcher.OpenTofuAnswer`), from memory. Pulumi: the credentials file of a new `PULUMI_HOME`, on a memory-backed directory when one exists, deleted when the tool ends, also on a signal. | yes |
| `exports.files`, and each credential file of a block | files of mode 0600 under a directory of mode 0700, on a memory-backed directory when one exists. The shim deletes them when the tool ends, also on a signal. | yes |
| `exports.variables`, plain | variables of the tool process | yes |
| `exports.variables`, secret | refused in slice 1 (question 5) | yes |
| `exports.opentofu` | a variables file in the same directory, passed as `-var-file=<path>` (see below). Slice 1 refuses a secret leaf (see the proposal below). | yes |
| `exports.pulumi` | a stack settings file with `environment` entries, with the planned `launcher.PulumiConfigFile` (it does not exist yet) | yes |

- **How the variables file reaches the tool.** The shim must add
  `-var-file=<path>`, or set `TF_CLI_ARGS_<command>` (facts). The first way
  means that the shim parses the arguments of the tool to find the
  subcommand. The second way needs no parsing. Recommendation: the second
  way, if the e2e suite shows that the tool reads it, and the first way as
  the fallback. Either way the shim reads no other argument. Rules for the
  second way:
  1. **Quote the path.** The tool parses the value "as if you typed it
     directly to the shell". The shim writes the path in double quotes and
     accepts a directory name only from an allowlist (`[A-Za-z0-9._/-]`),
     else it stops (exit code 2). A hostile path then cannot add an
     argument.
  2. **Keep the existing value.** If the CI job already sets
     `TF_CLI_ARGS_<command>`, the shim keeps that value and places its flag
     first, before the existing value. It never drops a flag of the team.
     A flag of the team in that value comes later, so it takes precedence
     over the flag of the shim (see 6).
  3. **Which commands.** The shim sets the variable for each command that
     reads variables: `plan`, `apply`, `destroy`, `refresh`, `import`,
     `console` and `test`. The e2e suite checks each one. A command that
     does not read variables ignores the flag.
  4. **Child processes inherit it.** A `tofu` that a `local-exec`
     provisioner or an external program starts reads the same variable
     and the same file. That is the same exposure as the files (H): the
     code of the module can read each file anyway.
  5. **No value in the variable.** It holds a path only, so no secret value
     reaches the environment of the CI job, the process list or a log
     through it.
  6. **A user flag can override a profile value.** The tool inserts the
     value of `TF_CLI_ARGS` before the flags of the command line, so a
     `-var` or `-var-file` of the team on the command line takes precedence
     over the file of the shim. That includes a key that the profile marks
     `$final` (ADR 201 decision 3), which no import or attachment can
     replace. Options: (a) accept it and document the limit: the team
     controls its own command line, and the server cannot see it. (b) The
     shim refuses `-var`, `-var-file` and a `TF_CLI_ARGS*` value of the
     team for a key that the profile marks final, and stops with exit code
     2 and a message that names the key. (c) The shim passes its file
     last. Recommendation: (b) for slice 1, because the shim already reads
     the arguments to find the subcommand in the fallback way, and (a)
     is only a documented limit, not a promise. The docs say that the shim
     is not a boundary (H): a team that edits its own command line can also
     skip the shim. Option (b) reads `-var` and `-var-file` and parses the
     team's var files. That parsing reads an untrusted format, so it is
     security code (H). It needs its own tests, one for each step (ADR 250).
  E2e item: the OpenTofu docs say that an ephemeral variable "must also be
  set when running tofu apply" with a saved plan file. They do not say
  whether `tofu apply <saved plan>` refuses `-var-file` for a variable that
  is not ephemeral. If it refuses, the shim passes the file to `apply` only
  for a saved plan that needs an ephemeral variable.
- The shim never writes to `GITHUB_ENV`, never sets a variable of the CI
  job, and never writes a file in the work tree.
- The shim never passes its own environment to the tool. It passes the
  variables of the CI job that the tool needs (`PATH`, `HOME`, the
  variables of the platform) and drops each name that
  `job.IsCredentialEnv` matches, except the names that the profile sets.
  This is the same rule as on a runner, not a second rule. It also drops,
  by name and not by that rule, `ACTIONS_ID_TOKEN_REQUEST_TOKEN`,
  `ACTIONS_ID_TOKEN_REQUEST_URL` and the variable that `--id-token-env`
  names (H).
- **With a trust token of a CI run, the Pulumi paths answer empty
  `environmentVariables` and `files`**, as they do for a job token (ADR
  209 decision 5, which this RFC amends for CI-run trust tokens only). The
  shim already wrote them, and the CLI would write the files to a
  temporary directory that it does not delete.
- **Secret leaves in `exports.opentofu` (proposal).** A non-ephemeral
  variable goes to the plan and to the state in plain text (D). A plan that a
  CI job saves stays in the team's CI, and the server does not seal that plan
  file. The shim cannot see whether a variable is ephemeral (question 5).
  **Proposal:** in slice 1, the server refuses a value that the profile marks
  secret in `exports.opentofu` for a CI run. The open fails closed and names
  the profile entry, and the shim stops before the tool starts. The cost: a
  team with ephemeral variables cannot use this path in slice 1. The owner
  can accept that cost, or accept a documented limit instead.

**C4. The token of the tool (proposal).** The tool gets the trust token that
the shim gets. The same token can call `ci-runs/open` and update the CI run
record (G1). So the tool holds more rights than it needs. Options:

1. **One token for the shim and the tool.** The design of B3 and C3.
2. **A second, narrower token for the tool.** It covers the state and lock
   actions of the unit only. The open and record token stays in the memory of
   the shim. This design does not say how the server issues the second token.

**Proposal:** option 2. The e2e suite checks that each step of the tool works
with the narrow token, and that the server refuses the narrow token on the open and
record routes.

### D. Secrets out of state: what can stay out, what cannot

**OpenTofu 1.11 and later.**

| Secret | Out of the plan and the state? | How |
| --- | --- | --- |
| A provider credential (a cloud key, a token) | yes | a credential file or a variable that the provider reads, or `ephemeral "opendustpan_profile"` into the provider block |
| A value that a resource needs once (an initial password) | yes, where the provider has a write-only attribute | the ephemeral resource into the write-only attribute and its version attribute |
| A value for a provisioner or a `connection` block | yes | an ephemeral value. Provisioner output is not masked by OpenTofu, so the shim mask is the only mask there. |
| A value in `exports.opentofu` (a variables file) | **no**, unless the variable is `ephemeral = true` | OpenTofu keeps the values of non-ephemeral variables in a saved plan. A plan that a CI job saves with `-out` stays in the team's CI (an artifact, a cache). The server seals a plan that it stores itself (ADR 102 decision 4 and ADR 151), not a plan file of the team's CI. The docs say: "OpenTofu supports encrypting state and plan files at rest". Recommendation, now decided in scope (owner, 2026-11-07): the shim turns on state and plan encryption (OpenTofu 1.7 and later) with `enforced = true` (K). A team without the shim is out of scope. |
| The result of a data source (`data "opendustpan_profile"`, `data "external"`) | **no** | stored in plain text in the plan and the state |
| A value that a resource makes or returns (a generated password, a new access key, a private key) | **no** | stored in the state. Only state encryption protects it. |
| An attribute of a resource with no write-only form | **no** | stored |

**OpenTofu before 1.11.** No ephemeral value and no write-only attribute.
Only a provider credential from a file or a variable stays out.

**The Pulumi CLI.** Everything that a program gives to a resource is in
the state. A provider credential that the provider reads from a file or a
variable by itself is not a resource input (e2e item). A secret config
value is in the state as ciphertext of the secrets provider of the stack.
An unmarked value and each resource ID are plain text. Pulumi has
write-only fields on bridged providers, but "its initial value gets written
to Pulumi state inputs as a Secret": the value is in the state as
ciphertext, not out of it (facts). So for Pulumi the promise is
"encrypted in state when marked, or when it is a write-only field", never
"out of state".

**What OpenDustpan can promise.**

- The shim never writes a value to the CI job environment, the CI log
  (beyond its mask), the work tree or the CI run record.
- The server never stores a value in the CI run record or an audit entry.
- The server seals each OpenTofu state at rest (ADR 41 decision 10) and each
  stored plan with an item key (ADR 102 decision 4, ADR 151), and holds the key of a Pulumi stack with the service
  secrets provider.
- The leak check (E) detects an exact copy of an injected value in a
  state that the server receives.
- The plan files and state files that the tool writes in a CI run with the
  shim are encrypted at rest with a key from a profile (K).

**What OpenDustpan cannot promise.**

- That the tool or the IaC code does not copy a value into the state. Only
  the module decides that, through ephemeral values and write-only
  attributes.
- That a value stays out of a plan file or a state file that the team
  keeps elsewhere, for example a plan artifact of the CI. The shim encrypts
  these files at rest (K), but the value is still in the file, and in the
  memory and the log of the tool (K6).
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

**E4. Limits.** A team that turns on OpenTofu state encryption (D)
uploads a state that the server cannot read, so the walk of E2 finds
nothing. The leak check then reports "not checked" for that state version,
never "clean". Only exact copies. Only values of 8 bytes or more, so a
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
- the commit: the `sha` claim for GitLab CI. For GitHub Actions, the `sha`
  claim if the e2e suite shows that the token holds it. Otherwise
  `GITHUB_SHA`, marked as declared by the CI. Do not mark `GITHUB_SHA` as
  declared until the e2e suite shows that the token has no `sha`,
- each profile, revision and path that the open answered, never a value,
- the state versions that the CI run wrote, and the leak findings,
- a link to the CI job, built from the claims, and a link to the record in
  the CI log.

**Where the record is made.** At `POST /units/{unit}/ci-runs/open`, not at
the exchange. The exchange does not know the unit: the request names a
namespace and a CI token only. The open names the unit, so the server
can check the scope, the profiles and the open rules, and make the record
in the same transaction as the audit entry of the open. The exchange
makes no CI run record. It makes a **token family**: the ID of the `jti`
row (B3). The record stores the family ID.

A CI run is closed only by the **current trust token of the CI run**: the
trust token that is the newest of its family. After a nonce retry (B3), the server revokes each
earlier token, so only the token of the last answer can update the
record. The shim sends the record when the tool ends, also on a failure.
A CI run that dies still has its open record. A record that is not closed
after its trust token ends gets the outcome `unknown`. A second open with
the same family (the recovery from a lost answer of the first open, B3)
makes a second record. The first one stays and ends as `unknown`.

### G. Authorization and audit

**G1. Routes and actions.** Paths are proposals, under
`/api/dustpan/v1/namespaces/{namespace}` (ADR 210).

| Method and path | Action |
| --- | --- |
| `POST /token-exchange` | none (public list of the route test). A trust rule decides. Rate-limited for each namespace and each issuer, with a counter in the database that all instances share (B3). The route takes no `Idempotency-Key` (B3). |
| `POST /units/{unit}/ci-runs/open` | `profile:open` on each attached profile, and `unit:record-ci-run` on the unit. Makes the CI run record (F). Takes no `Idempotency-Key` (B3). |
| `PATCH /ci-runs/{id}` | `unit:record-ci-run` on the unit of the CI run, and a check that the trust token is the current one of the CI run (F) |
| `GET /ci-runs`, `GET /ci-runs/{id}` | `unit:read` on the unit |
| `GET`, `POST`, `PATCH`, `DELETE /trust-rules` and `/trust-rules/{id}` | as ADR 54 decision 8 |

The server checks the unit of the trust token scope (B4.2) and the action
in the same transaction as the write.

**G2. Each secret read.** Each open writes the entry of ADR 203 decision
9: each profile, revision and path, the CI run, the trust rule and the
issuer, never a value.

**G3. The audit kind.** A workload reads for a tool, so the entry is not a
`reveal`. ADR 240 already says that a workload gives `tool-operation`. The
consumer `ci` takes trust tokens only (C1), so a person never opens values
through the shim, and no amendment of ADR 203 decision 9 is needed.

**G4. Denials.** Each refused exchange writes an audit entry: the issuer,
the reason (bad signature, unknown issuer, wrong audience, replayed `jti`,
expired, no rule matched, a claim condition failed), the SHA-256 of the
`jti`, and never the token. The exchange request names its namespace in the
path. When that namespace exists, the denial goes to the audit of that
namespace, also for an issuer that no rule of it names. Only when the
namespace does not exist does the entry go to the scope `@server`. The
answer to the caller is the same in both cases, so it is not a way to find
out which namespaces exist. Both scopes use the flood limit of the denial
audit (pull request 1108, merged).

### H. Defense in depth (ADR 250)

**The shim runs untrusted code, and it is not the boundary.** The tool runs
the IaC code, providers, provisioners and external data sources of the
team, in the same CI job and as the same user. Each value that the shim
gives to the tool is visible to that code: it can read the process
environment, the files, and ask the credentials helper. It can send a value
over the network, or write it to the log in a changed form that no mask
finds.

Any other step of the same job can do more. On GitHub Actions, a step with
`id-token: write` asks `ACTIONS_ID_TOKEN_REQUEST_URL` for a token with any
audience, and sends it to the exchange endpoint, with no shim. On GitLab CI,
the variable of each declared token is in each script line of the job. The
shim drops `ACTIONS_ID_TOKEN_REQUEST_TOKEN`, `ACTIONS_ID_TOKEN_REQUEST_URL`
and the variable named by `--id-token-env` from the environment of the tool,
by name. That limits one path for the tool process. It is not a boundary. The
boundary is the trust rule (its claim conditions, B5) and its scope (B4.2),
what the trust token can open, and what the cloud trust policy accepts (ADR
205 decision 4). The security docs say so in their first lines. The same holds for the
encryption at rest of section K: it protects a file that leaves the job, not
the memory, the log or the code of the job (K6).

**Blast radius of slice 1.** Slice 1 moves a long-lived cloud key from the
CI settings into a profile. Each CI run that matches the rule, and the
code that it runs, can read the key and send it out. A stolen CI token gives
one exchange, then a trust token of up to 1 hour, then every profile in the
scope, and so a key that does not expire. B4.2 limits the scope, but the key
itself is permanent. The next step that ends this is a login block
(ADR 207): the cloud gives a short-lived credential to the workload, and the
profile holds no cloud key. The docs page of slice 1 (J) says this in a
warning at the top.

| Layer | What it does | Its test |
| --- | --- | --- |
| Download of discovery and JWKS (server) | See B7. The server downloads only for an `iss` that a trust rule of the namespace in the request names. It decides this from the database before any network call. `https` only, with no redirect to another scheme or host. The server checks the resolved IP address when the connection opens. It denies loopback, private, link-local (also `169.254.169.254`), unspecified and multicast addresses. Size limits, timeouts and a cache limit apply. The `issuer` of the discovery document equals `iss`. | one test for each of these cases. An `iss` of no rule: no network call happens. An `http` URL. A redirect. A host that resolves to each denied address class, also a DNS answer that changes after the first check, because the check runs at connect. A response over the size limit. A slow response. A full cache. A discovery `issuer` that differs from `iss`. Each test runs with the other checks skipped. |
| Check at the boundary (server) | JWT: `alg` from an allowlist (`RS256`, `ES256`), never `none`. The `kid` is in the cached JWKS of the issuer. `iss`, `aud`, `exp`, `nbf`, `iat` and `jti` are present and valid. At most 16 KiB. Claim values are strict UTF-8 with no control character. The unit name and the profile paths have the rules of their own routes. | a token for each rule, sent to the verifier with the route and the rule match skipped |
| Check at the boundary (shim) | The server URL is `https`. The name in `--id-token-env` matches `[A-Z_][A-Z0-9_]*`. The unit name has the rules of the server. Each answered file name is one path segment from an allowlist. Each variable name passes the step variable rules of `pkg/job`. | a hostile answer of the server with the server checks skipped |
| Encode or contain | The tool starts with `exec.Command` and an argument list, never a shell. No value goes on a command line. Files go under `os.Root` of the directory of the CI run. The shim drops CR and LF from each log value. SQL uses placeholders only. | a value with a shell character, a path with `..`, a value with CR and LF |
| Limit the effect | The scope of the trust rule (B4.2). The trust token is for one namespace, for at most 1 hour. One exchange for each `jti` (and at most 3 nonce retries). The credentials helper answers the server host only. Values stay in memory and on a memory-backed directory, and the shim deletes them at the end. | a second exchange of one `jti` with another nonce, a fourth retry, and the helper asked for another host |
| Authorize at use | Each open checks `profile:open` and the scope for the unit. Each state write and each record update checks the token. It must be the newest token of its family, the current trust token of the CI run. | a trust token of unit A that writes the record of unit B. A revoked earlier token that updates the record after a nonce retry. |
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
| A manual lock check before the tool starts | 1 | one read of the lock. The server refuses the lock request of the tool (an OpenTofu lock, or the start of a Pulumi update) anyway (ADR 213 decision 10). It does not refuse the state write of a tool that already holds its lock. So this gives a clear message before the tool starts. |
| Encryption at rest of the local plan and state files (K) | 2 (2a to 2d) | needs the key entry of a profile and the shim. Owner scope decision of 2026-11-07. |
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
team wants the CI to hold no OpenDustpan token and no cloud key in its
settings, and the key to stay out of the state. In slice 1 the key moves
into a profile. It is still a long-lived key (H). Login blocks are the
recommended next step.

**Needs first:** the trust rule exchange of ADR 54 (OIDC discovery only),
and the open of ADR 203 for static secrets and `exports.files`. Login
blocks (ADR 207) and the OpenDustpan provider (ADR 209) are not needed for
slice 1: the cloud credential is a static secret of a profile, delivered
as a shared credentials file.

1. The routes of G1, the trust rule scope (B4.2), the `jti` store (B3), the
   download rules of B7 and the denial audit (G4). The trust rules, the
   exchange and the CI runs live in one new tool-neutral domain package,
   for example `pkg/trust`, with a store port, its conformance suite and an
   adapter for each dialect. CI runs are part of the backup tables, so a
   restore keeps the record of what each run read. The `jti` table, the
   fingerprints of E and the keys of the CI runs are control tables: a
   restore keeps them (B3).
2. The shim (A3) with B1 for GitHub Actions and GitLab CI, C1 to C3, the
   masks, the manual lock check and the CI run record (F).
3. The docs page: the workflow, the trust rule, the profile, and the table
   of section D with its warnings. A warning at the top says: slice 1 puts a
   long-lived cloud key into a profile. Each CI run that matches the rule,
   and the code that it runs, can read and send out that key. A stolen CI
   token gives access to every profile in the scope. Use login blocks for
   short-lived credentials as soon as they are available. A second
   warning: the server refuses a CI token whose lifetime (`exp - iat`) is
   more than 6 hours (B3), so a GitLab job with a timeout above 6 hours
   cannot use the shim. The page says to split such a job or to use a job
   on a runner. A third warning: the trust token lives 1 hour by default (B3,
   ADR 69 decision 6). A state write after the trust token ends fails. A job
   that runs longer than 1 hour can fail in the middle of an apply. The
   renewal is later (question 3).
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
   - a token shaped like `pull_request_target` (`event_name`), a token with
     another `workflow_ref`, and a token of a fork pull request each fail,
     with an audit entry. A real fork pull request on a test repository
     shows whether GitHub withholds the token.
   - a lost answer of the exchange, retried with the same nonce, works and
     revokes the first trust token. A retry with another nonce fails, a
     fourth retry fails, and two concurrent exchanges with one `jti` and
     one nonce give one token and one 409. A request to the exchange route
     with an `Idempotency-Key` answers 400. A record update with the
     revoked first token fails,
   - a lost answer of `ci-runs/open`: the shim opens again, two records and
     two audit entries exist, and the first record ends as `unknown`,
   - a restore keeps the `jti` table: a CI token exchanged before the
     restore does not exchange again after it,
   - a profile with an open rule (ADR 202 decision 7): the open fails
     closed, the tool does not start, an audit entry exists,
   - `TF_CLI_ARGS_<command>`: a path with a space or a quote stops the
     shim, an existing value of the team stays, a nested `tofu` in a
     `local-exec` provisioner reads the file, and `plan`, `apply`,
     `destroy`, `refresh`, `import`, `console` and `test` each read it. A
     `-var` of the command line for a key that the profile marks `$final`
     is refused by the shim (C3, option b),
   - a GitLab-shaped job with a command list of `init`, `plan -out` and
     `apply` works with one exchange,
   - the shim masks the canary when the module prints it. A base64 copy of
     the canary is not masked, and the test shows this on purpose, to show
     the limit of a mask,
   - the environment of the tool holds no `ACTIONS_ID_TOKEN_REQUEST_*`
     variable and no variable named by `--id-token-env`,
   - a read of the state of another workspace by `terraform_remote_state`
     fails when that workspace is not in the scope,
   - the real claims of a GitHub token: whether `ref_protected` and `sha`
     are present, and the value of `GITHUB_SHA` against `sha`.
   - with a manual lock on the workspace, the shim stops before `tofu
     init` with a message that names the lock.

   A second case runs `pulumi up` through the shim with the same profile,
   and checks that no canary is in the checkpoint in plain text. A third
   case sets a write-only field of a bridged provider to the canary and
   reads the checkpoint of the bridge version under test, to show what
   issue 3201 describes today (facts).

**Open e2e items.** Each item is also named where it arises.

- Real GitHub claims: whether a token holds `ref_protected` and `sha`, and the
  value of `GITHUB_SHA` (facts, B5, F).
- The fork pull request token: whether GitHub withholds an OIDC token from a
  workflow that a fork pull request starts (B5).
- Provider environment credentials in Pulumi state: whether a variable that a
  provider reads by itself reaches the state (D).
- Apply of a saved plan with `-var-file`: whether `tofu apply` refuses the
  file for a variable that is not ephemeral (C3).
- Bridge write-only behavior per version: how the bridge version under test
  stores a write-only value (facts, the third case above).

Slice 1 sets no encryption at rest. The slices 2a to 2d of section K add it,
each with its own e2e step. A KMS key provider (K3 option 3) comes after login
blocks.

Later: the leak check (slice 2), login blocks with B6, the other uses of
section I.

### K. Encryption at rest of the local plan and state files

**Decided (owner, 2026-11-07, ADR 352).** The shim encrypts at rest the plan
files and state files that the tool writes in the team's CI. A team that runs
the tool without the shim is out of scope. The docs say so in plain words,
and they give the one command that adopts the shim. The design below is a
proposal.

**What is already true on the server.** State that reaches the server is
sealed at rest, and a read masks secrets (ADR 41, decisions 2 and 10). The
sealing of OpenTofu state with the data key of the workspace is built. The
move to an item key is open work (what is decided already). Section K adds
the files that never reach the server: they stay in the work tree, in a CI
artifact or in a CI cache.

**K1. Which files.**

| Tool | File | Why it matters |
| --- | --- | --- |
| OpenTofu | the saved plan (`-out`) | holds the values of non-ephemeral variables and the result of data sources (D). Many teams keep it as an artifact between a plan job and an apply job. |
| OpenTofu | a state file in the work tree | with the TFE-compatible backend the state lives on the server. A local copy appears from `tofu state pull`, or from a team that adds a local backend for a test. An e2e item lists the files that a real run leaves in the work tree. |
| Pulumi | the plan file of `--save-plan` | JSON, experimental format, holds `config` and the plan of each resource (facts). The CLI has no option to encrypt it. |
| Pulumi | the credentials file in `PULUMI_HOME` | the shim already puts it in a throwaway directory and deletes it (B3.1, C3) |

The shim does not touch a file that the team writes by other means, for
example the output of `pulumi stack export` that a script redirects to a
file. It cannot know the path. The docs say so.

**K2. OpenTofu: the `encryption` block, set by the shim.**

The shim sets `TF_ENCRYPTION` for the tool process only. The value is code
that OpenTofu reads, as in this example. The names are fixed by the shim.

```hcl
key_provider "external" "opendustpan" {
  command = ["dustpan-ci", "key-provider"]
}
key_provider "pbkdf2" "opendustpan" {
  chain                    = key_provider.external.opendustpan
  encrypted_metadata_alias = "opendustpan"
}
method "aes_gcm" "opendustpan" {
  keys = key_provider.pbkdf2.opendustpan
}
state {
  method   = method.aes_gcm.opendustpan
  enforced = true
}
plan {
  method   = method.aes_gcm.opendustpan
  enforced = true
}
```

- **Why PBKDF2 and not a raw key.** The docs warn about AES-GCM key
  saturation and advise a key-derivation provider with a long passphrase.
  The profile entry is that passphrase (K3).
- **Why `enforced = true`.** OpenTofu then refuses to write a plain plan or
  plain state, also when the team leaves the shim out of a later step of the
  job. A plain plan file that reaches an `apply` step fails there, which is
  the failure that we want.
- **Why a fixed `encrypted_metadata_alias`.** The encrypted data holds the
  name of its key provider. A fixed alias lets a later version of the shim
  rename the provider without breaking old plan files. This is a one-way
  door (K7).
- **If the team has its own `encryption` block.** OpenTofu merges the
  environment configuration over the code, and the environment wins. The shim
  cannot read the HCL of the team. So the shim stops with exit code 2 when
  `TF_ENCRYPTION` is already set, and the docs say that the shim owns the
  `encryption` configuration of a run. A team that wants its own KMS key
  chooses K3 option 3 later. A code block with other names stays in the
  merge. An e2e item shows the result.
- **Existing plain files.** The first run on a unit that has plain local
  state needs the `unencrypted` method as a `fallback` for one run. The shim
  does that only with `--migrate-unencrypted`, and the CI run record notes
  it. The default fails closed.

**K3. Where the key comes from.** The key is one secret entry of a profile of
the unit (`at-rest-key` by default, set by `--file-key-entry`). It has at
least 32 random bytes, as base64. OpenTofu needs 16 characters at least for
PBKDF2. The entry is opened with the trust token under the rules of ADR 203,
so the open writes an audit entry without a value, and the CI run record
names the profile, the revision and the path (F). Options for delivery to the
tool:

1. **The key in `TF_ENCRYPTION` as a `passphrase`.** Simplest. The key is in
   the environment of the tool and of each provisioner. This breaks the rule
   of C and question 5 (files only in slice 1).
2. **The `external` key provider that runs the shim** (above). The key is a
   file in the memory-backed run directory, mode 0600, and the environment
   holds its path only. `dustpan-ci key-provider` follows the protocol of the
   docs: header, metadata on standard input, key on standard output. The code
   of the tool can still read the file, as it can read any file that the shim
   gives (H).
3. **A KMS key provider** (`aws_kms`, `gcp_kms`, `azure_vault`, `openbao`).
   The profile holds the non-secret settings (the key ID, the region, the
   address) and the cloud credential is delivered as in slice 1. OpenTofu
   makes a new data key for each file, and the KMS policy decides who can
   decrypt. The docs advise a separate KMS key for each state file. This
   needs a cloud credential with a KMS permission, so it comes after login
   blocks (ADR 207).

**Recommendation.** Option 2 in slice 1, option 3 later, never option 1.

**K4. Rotation.** A profile has numbered revisions. A run uses the newest
revision as the `method`, and the previous revision as the `fallback` of the
`state` block and the `plan` block, so a plan file from the last revision
still opens. The number of older revisions is a setting of the shim (1 by
default). OpenTofu always writes with the new method. A plan that waits for
approval longer than the fallback window cannot open: the apply fails closed
and the docs say so. A lost key means lost files, as the docs of OpenTofu
say, so the key is the profile entry and the backup of the profile (ADR 30)
holds it.

**K5. Pulumi: a file format of our own.** The CLI has no encryption for the
plan file, so the shim does it.

- The shim reads the argument list of `preview` and `up` for `--save-plan
  <path>` and `--plan <path>`. It accepts the exact forms `--flag path` and
  `--flag=path` only, never a shell string. For another form it stops.
- Before the tool starts, it replaces the path with a file in the run
  directory. For `--save-plan`, the tool writes there. After the tool ends,
  the shim seals that file into the path that the team named, and deletes the
  plain file, also on a signal. For `--plan`, the shim opens the file that
  the team named into the run directory first.
- The format has a magic number, a version, a random 16-byte salt, a random
  12-byte nonce and AES-256-GCM (`crypto/cipher`). The key is HKDF-SHA-256
  (`crypto/hkdf`, already used in `pkg/secret`) of the profile entry with the
  salt. The associated data are the header, the unit reference, and the
  purpose `pulumi-plan`. A file for another unit or another purpose does not
  open. No new dependency.
- A file with no magic number is refused: the shim never passes a plain plan
  to `--plan`, as `enforced = true` does for OpenTofu.
- The same key, rotation and fallback rules as K3 and K4 apply.

**K6. What encryption at rest cannot protect (residual risk).** The docs of
the shim list these in the first lines of the page, next to H.

- **The memory of the tool.** The tool decrypts the plan and the state to
  use them. The decrypted data is in its memory, and in swap or a core dump
  if the CI host allows it. The code that the tool runs can read it. The
  shim cannot encrypt process memory. It can apply the mitigations of K10.
- **The logs and the output.** `tofu show`, `tofu output`, `tofu plan`
  output, `TF_LOG` debug output, `pulumi preview --show-secrets` and
  `--logtostderr` can print values in plain text. The shim mask finds exact
  copies only (H).
- **The person and the job that run the tool.** OpenTofu says it cannot
  protect values "from the person running the `tofu` command". The same CI
  job holds the key (K3), so any step of the job that can reach the profile
  entry can open the files. Encryption protects a file that leaves the job:
  an artifact, a cache, a copy of a disk, a log that uploads the work tree.
- **Replay and loss.** The docs of OpenTofu say that encryption does not
  protect against an old file that an attacker puts back, or against loss of
  the file. A lost key is a lost plan file.
- **The leak check.** E4 says that a state that the client encrypts is not
  readable by the leak check. ADR 41 records, from the source of the CLI,
  that the `cloud` backend sends plain state, but its decision 8 also
  handles a state that the client encrypted. An e2e item shows which one
  holds for the TFE-compatible API with the shim's `TF_ENCRYPTION`, and E4
  follows the result.
- **A team that does not use the shim.** Out of scope (ADR 352). The docs say
  it, and give the one command that adopts the shim, for example
  `dustpan-ci run --unit <unit> -- tofu plan -out=plan` (the exact form is for
  the design, B3).

**K9. Logs: capture, redaction and storage (owner, 2026-11-07).** The shim
runs the tool as a child process and captures its standard output and
standard error.

- **What it redacts.** Every secret value that the shim knows: the profile
  values that it injected, and the at-rest key (K3). For each value it
  redacts the raw form, the standard base64 form, the URL-safe base64 form
  and the URL-encoded form. Each line of a multi-line value is redacted by
  itself. It keeps the minimum length of 4 bytes of the mask (H) and handles a
  value that spans two chunks (`launcher.Masker`, extended for the encoded
  forms). The replacement is `[masked]`.
- **What it sends.** The full redacted log, to the backend, as part of the
  CI run record (F). It sends the redacted stream only, never the raw bytes.
  A person reads it with a read grant on the unit. The proposed action is
  `unit:read-ci-log`. Each read writes an audit entry without a value. The
  server seals the log at rest with a key of the CI run record, like the
  other stored logs (ADR 151). An upload has a size limit. A log that is
  longer gets a marker and the CI run record notes it.
- **What the CI log shows.** A shim setting, `--ci-log`, has two values:
  `redacted` (the redacted stream) and `summary` (the exit code, the
  counts, and a link to the full redacted log in the backend). The
  recommendation is `summary` as the default: the CI log is often readable by
  more people than the unit, and the backend log has a read grant. The owner
  can set the default to `redacted`.
- **If the upload fails.** The shim retries from a file on the memory-backed
  directory. If it still fails, it exits with code 3 and one line that names
  the failure. It never writes the raw stream to the CI log as a fallback.
- **Limits.** A secret that the tool changes into a form that the shim does
  not know is not caught: a hash, a part of a string, a reversed or
  encrypted value, a value cut into pieces by output of several lines or
  separate writes, a value in another encoding. The redaction is a
  convenience layer, like the mask of H. The server can also run the
  fingerprints of E over a stored log later. This is not in the first slice
  of K.

**K10. Mitigations for process memory.** The shim cannot encrypt the memory
of the tool. These mitigations reduce the exposure, where the platform
allows them. Each is best effort. The shim logs a line for each one that it
could not apply, and the CI run record notes it.

- `RLIMIT_CORE=0` for the shim and, by inheritance, for the tool. No core
  dump holds the decrypted plan, the state or the key.
- On Linux, `PR_SET_DUMPABLE=0` for the shim process, so another process of
  the same user cannot read its memory through `/proc`. The setting does not
  pass to the tool after `exec`. Linux and macOS only (ADR 278).
- No swap where the platform allows it: the shim locks the pages that hold
  the key (`mlock`), and the run directory is memory-backed. The shim cannot
  turn off swap for the tool or for the host. Swap is a setting of the CI
  host.
- The key and the decrypted files stay for the shortest time: the shim
  deletes the run directory when the tool ends, also on a signal.

**Out of scope for the shim: confidential computing.** SEV-SNP and TDX
protect memory from the host. They are a property of the runner
infrastructure that the team chooses, not of the shim. The docs mention them
only as a choice of the infrastructure.

**K7. One-way doors.** Each item below is hard to change after a tag ships it.

- The names in `TF_ENCRYPTION` and the fixed `encrypted_metadata_alias`
  (`opendustpan`). A plan file keeps them as metadata.
- The default key entry name `at-rest-key` and its length rule.
- The Pulumi file format: the magic number, the version byte, the key
  derivation and the associated data. A plan file that a CI job stores for
  days must open after an upgrade of the shim.
- `enforced = true` as the default. A team that shipped with it cannot
  easily turn it off, because plain data is then a failure.
- The names and forms of the redaction (K9): the replacement text and the set
  of encoded forms. A tool or a script of a team may parse `[masked]`.
- The action name `unit:read-ci-log` and the meaning of `--ci-log`.
- That the key lives in a profile. A later key store (a KMS) would add a
  provider next to it and would not replace it.

**K8. Slices and e2e steps.** Each slice lands with its e2e step first
(ADR 12).

| Slice | Content | E2e step |
| --- | --- | --- |
| 2a | OpenTofu: `TF_ENCRYPTION` with `external` and `pbkdf2` (K2, K3 option 2), `enforced`, the key from the profile, the fixed alias. | The test runs real `tofu init`, `plan -out` and `apply` of each supported version from 1.11 through the shim. It checks: the plan file has no canary and is not valid JSON of a plan (`tofu show` without the shim fails); the apply of the same file through the shim works; a plain plan file made without the shim makes `apply` fail; the environment of the tool holds no key value; a pre-set `TF_ENCRYPTION` stops the shim with exit code 2; a code `encryption` block with other names merges as the docs say; the open of the key entry writes one audit entry without a value. |
| 2b | Rotation (K4). | The test rotates the key entry, then applies a plan file made with the old revision: it works. It applies a file two revisions old: it fails closed. |
| 2c | Pulumi: the file format and the path rewrite (K5). | The test runs real `pulumi preview --save-plan` and `up --plan` through the shim. It checks: the stored file has the magic number and holds no canary; `up --plan` with a plain file or a file for another unit fails before the tool starts; the plain file is gone after the run, also after `SIGTERM`; the form `--save-plan=path` and `--save-plan path` both work; an unknown form stops. It also lists what a default plan file holds, to close the open e2e item of the facts. |
| 2d | Logs (K9) and the memory mitigations (K10). | The test runs a module that prints the canary raw, in standard base64, in URL-safe base64 and URL-encoded, on standard output and on standard error, also across a chunk boundary. It checks: none of the four forms is in the CI log or in the stored log; a hex copy and a reversed copy are not masked, on purpose, to show the limit; the stored log needs the read grant, and a read writes one audit entry without a value; `--ci-log=summary` shows the link only; a failed upload exits with code 3 and writes no raw stream; `/proc/<pid>/limits` of the tool shows a core limit of 0; the open item `unit:read-ci-log` is in the route table. |
| 3 | A KMS key provider (K3 option 3), after login blocks. | A test with a local OpenBao transit engine: the data key is new for each file, and a second unit with another key cannot open the file. |

The e2e suite checks the OpenTofu minimum version of the docs for the
`encryption` block (1.7) against the supported versions, which start at 1.11
for the first slice (J).

## Open questions

The owner keeps these. Each recommendation is the author's.

1. Where the code lives: a mode of the run command (A1), a command of the
   server binary (A2), or a small binary of its own (A3), which amends ADR
   209 decision 1? Recommendation: A3, with A1 as the fallback.
2. Can a trust rule narrow the grants of its principal to units and
   profiles (B4.2), which amends ADR 54 decision 5, with access limits that
   read a trust-rule fact (B4.3) above it? Recommendation: yes, B4.2
   narrow-only, enforced centrally in the Authorizer, and audited under ADR
   54 decision 8, and B4.3 as an option for administrators.
3. A GitLab CI token cannot be renewed in the job, and lives for the job
   timeout. Does slice 1 use one start with a command list (B3.1), a kept
   trust token (B3.3), or the limit of the trust token lifetime (1 hour by
   default) with a renewal through the CI run record later, at most until
   the `exp` of the CI token? Recommendation: B3.1 with the limit, and the
   renewal later. Until a renewal exists, a state write after the trust
   token ends fails (J.3).
4. Do issued cloud tokens get `ci_*` claims and the unit claims for a
   trust rule scoped to one unit (B6.2)? It changes the claim set, a
   one-way door. Recommendation: no unit claims from a CI run, and `ci_*`
   claims only as a separate decision with the first CI login block. Slice
   1 is B6.1.
5. Can a CI run give a secret as a variable of the tool process, for an
   SDK that reads only a variable, or for `TF_VAR_` of an ephemeral
   variable? The shim cannot see whether the variable is ephemeral, so a
   secret in `TF_VAR_` of a non-ephemeral variable goes to the plan and the
   state. Recommendation: files only in slice 1. Later, an opt-in mark on
   the profile entry, with `job.IsCredentialEnv` deciding which names need
   it. The same open question applies to the refusal of secret leaves in
   `exports.opentofu` (C3).
6. `::add-mask::` on GitHub Actions: never, opt-in, or on by default when
   the shim detects the runner? Recommendation: opt-in, only when the
   standard output is the runner, and always with the shim mask too.
7. The leak check: a finding only (E3.1), or also a refusal by policy?
   Recommendation: a finding only at the state write, and a refusal only at a
   plan upload, later.
8. Which CI platforms in slice 1: GitHub Actions only, or also GitLab CI?
   The discovery document of GitHub lists `ref_protected` and `sha`, but
   the docs table does not, so an e2e item decides what GitHub tokens hold.
   This is no reason to prefer GitLab. Recommendation: both. Forgejo
   Actions comes after a check of its docs.
9. The audience form of B2.2: the namespace ID in a path, or a URN?
   Recommendation: the URL form, so the console can show one string.
10. The JWT and JWK code of the server: a library, or our own code on the
    standard library (New dependency)? Recommendation: a maintained
    library, after the owner reviews its license and its age.
11. Does slice 1 go ahead with the blast radius of H, or only after the
    first login block? Recommendation: slice 1 first, with the warning,
    and login blocks next.

**Questions added for section K (at most 4).** The scope of K is decided
(ADR 352). These four open the design.

12. Who sets the key of the files, and where does the key come from? The
    options are K3 options 1 to 3. Recommendation: option 2 (the key is a
    secret entry of a profile, and the `external` key provider of OpenTofu
    asks the shim for it), and a person creates the entry with one documented
    command.
    _ELI5:_ The files get a lock. The key to the lock must live somewhere.
    We put it in the same locked cupboard as the other secrets, the profile.
    The tool asks the shim for the key and never keeps it in its pocket (the
    environment).
13. Does `enforced = true` become the default, so that a plain plan file or
    plain state file makes the tool fail? Recommendation: yes. It is a
    one-way door (K7). A team with old plain files uses `--migrate-unencrypted`
    for one run.
    _ELI5:_ Should the door lock itself when you leave? If it does, nobody
    leaves a plain plan lying around by mistake. The price: an old plain file
    does not open until you say once that it is fine.
14. May the shim own the `encryption` configuration of a run, and stop when
    the team sets `TF_ENCRYPTION` itself (K2)? Recommendation: yes, and a team
    with its own KMS key waits for K3 option 3.
    _ELI5:_ Two people cannot both choose the lock for one door. The shim
    chooses it. If you want to pick your own lock, tell us, and we add a way
    to bring it later.
15. Pulumi has no plan file encryption, so the shim writes a file format of
    our own (K5), and the format is a one-way door (K7). Does the owner accept
    a format of our own, or should Pulumi plan files stay out of scope until
    the CLI has the option? Recommendation: accept the format. It uses the
    standard library only, and a magic number and a version byte let it change
    later.
    _ELI5:_ Pulumi has no lock for its plan file, so we put the plan in our
    own locked box. If we later find a better box, the label on the box
    tells us which kind it is, so old boxes still open.

## Proposed glossary lines

- **CI shim**: the optional client program that a team runs in its own
  CI. It exchanges the identity token of the CI job for a trust token,
  gives the values of the unit's profiles to the tool process only, and
  sends the CI run record. _Avoid_: CI runner, adapter, wrapper (alone),
  runner (that is an OpenDustpan runner).
- **CI run**: one start of the CI shim, recorded with the ID prefix `cir-`.
  It is not a job. _Avoid_: run (alone), job, pipeline.
- **At-rest key**: the secret entry of a profile that the CI shim uses to
  encrypt the plan files and state files that the tool writes in the team's
  CI. _Avoid_: file password, state key (that is a key of the server).
- **Leak check**: the server compares each whole string leaf of a state
  that a CI run writes with fingerprints of the secrets of that CI run, and
  records a finding for a match. _Avoid_: secret scanning, leak detection.

## Outcome

Empty until the RFC is accepted (link the ADR) or dropped (say why).

Partly decided: [ADR 352](../adr/0352-the-ci-shim-encrypts-the-plan-and-state-files-of-the-team-ci-at-rest.md)
(owner, 2026-11-07) decides the scope of section K, the log redaction of K9
and the memory mitigations of K10. The rest of this RFC stays
open.
