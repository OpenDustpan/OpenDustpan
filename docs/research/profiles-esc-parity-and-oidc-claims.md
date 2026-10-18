# Research: Pulumi ESC parity, OIDC claims, and what external systems accept

Question: For profiles (RFC 5, ADRs 200 to 209), which features does Pulumi
ESC have today? Which claims do CI and IaC products put in their OIDC
tokens, and what do cloud trust policies read? What does each external
system (clouds, secret stores, identity providers) accept from an external
OIDC issuer?

Date: 2026-10-18

This note gives facts and the mark of each ESC feature in our design. It is
not a claim of compatibility with any product. Sources were read on the
date above. A later version of a product can change any row.

Markers in the source column:

- A path such as `/docs/esc/concepts/imports/` is a page under
  `https://www.pulumi.com`. The Markdown source of these pages is
  `github.com/pulumi/docs` at a commit of 2026-10-17. The provider inputs come
  from `data/esc_schemas.json` in that repository (2026-09-19).
- "OpenAPI" is the Pulumi Cloud API spec at
  `https://api.pulumi.com/api/openapi/pulumi-spec.json`.
- "not verified" means a hint and no primary statement.

## 1. ESC parity

Marks: **same** (the feature exists in profiles, in our format), **own**
(the same need, met in another form), **more** (profiles do more),
**not taken** (with the reason), **later** (an open question of RFC 5).

### 1.1 Document, imports and composition

| ESC feature | Source | Profiles | Mark |
| --- | --- | --- | --- |
| YAML with `imports` and `values` | /docs/esc/concepts/environments/ | YAML or JSON with `schemaVersion`, `imports`, `providers`, `rotators`, `values`, `exports`, `checks` (ADR 201 decision 1) | own |
| Imports merge in order, JSON Merge Patch, local wins, `null` unsets | /docs/esc/concepts/imports/ | Same rules (ADR 201 decision 3) | same |
| `merge: false` imports, read through `imports[...]` | /docs/esc/concepts/imports/#import-options | Same (ADR 201 decision 3) | same |
| Implicit import `${environments.<project>.<env>.<path>}` | /docs/esc/concepts/builtin-properties/ | `profiles["folder/name"].values.path` (ADR 201 decision 3) | same |
| Each reference evaluated once per open | /docs/esc/concepts/imports/#evaluation | Same (ADR 201 decision 3, ADR 203 decision 2) | same |
| Imports across projects, not across organizations | /docs/esc/concepts/imports/ | Across folders, not across namespaces | same |
| Pinned imports `env@tag`, `env@3` | /docs/esc/concepts/versioning/ | Same (ADR 201 decision 3) | same |
| Draft references `@draft:<id>` | blog: preview ESC changes with draft references | `name@draft:<ID>`, plan only (ADR 202 decision 6) | same |
| Cycles reported as diagnostics | `pulumi/esc` `eval/eval.go` | `ErrImportCycle` at save and at open | same |
| Open permission on each loaded environment | blog: ESC OIDC tokens for any service | `profile:open` on each evaluated profile (ADR 203 decision 4) | same |
| Early-bound references | /docs/esc/concepts/interpolations-and-references/ | Same (ADR 201 decision 4) | same |
| Stack `environment:` list as an anonymous environment | /docs/iac/cli/commands/pulumi_config_env_add/ | Served on the Pulumi Cloud-compatible API (ADR 209 decision 5) | same |

### 1.2 Interpolation, context and functions

| ESC feature | Source | Profiles | Mark |
| --- | --- | --- | --- |
| `${path}` and interpolation in strings | /docs/esc/concepts/interpolations-and-references/ | `${ expr }` with Expr (ADR 201 decision 4) | own |
| `context` (user, organization, token type and team, current and root environment, open duration) | /docs/esc/concepts/builtin-properties/#context | `context` with every claim of ADR 205: unit, collection, access, phase, ref and more (ADR 201 decision 5) | more |
| 14 functions: `secret`, `concat`, `final`, `fromBase64`, `fromJSON`, `join`, `open`, `rotate`, `split`, `toBase64`, `toJSON`, `toString`, `toYAML`, `validate` | /docs/esc/concepts/builtin-functions/ | Each one, as an Expr function, an entry key or a block (ADR 201 decision 8) | same |
| No `fromYAML` | /docs/esc/concepts/builtin-functions/ | `fromYAML` added | more |
| `fn::final` warns and keeps the parent value | /docs/esc/concepts/builtin-functions/fn-final/ | `$final` refuses the save (ADR 201 decision 6) | own |
| `fn::validate` with JSON Schema | /docs/esc/concepts/builtin-functions/fn-validate/ | `$type` and `checks`, with no new dependency (ADR 201 decisions 10 and 11) | own |
| Rules on the context of an open | none found | `checks` can refuse an open by its context | more |

### 1.3 Secrets and outputs

| ESC feature | Source | Profiles | Mark |
| --- | --- | --- | --- |
| `fn::secret` sealed at save, ciphertext in the document | /docs/esc/concepts/builtin-functions/fn-secret/ | `$secret`, stored as `$sealed`, bound to the profile scope (ADR 202 decision 1) | same |
| Customer-managed keys (AWS KMS) | /docs/esc/concepts/customer-managed-keys/ | The `Wrapper` port (ADR 23 decision 8, RFC 33) | later |
| Show secrets of a definition (`--show-secrets`, decrypt API) | /docs/iac/cli/commands/pulumi_env_get/ | `showSecrets` with `profile:open` (ADR 202 decision 1) | same |
| Encrypt or decrypt a definition with no save | OpenAPI `EncryptEnvironmentSecrets` | `POST .../check` takes `$secret` and answers diagnostics. No route answers ciphertext with no save. | own |
| Secretness propagates through functions | /docs/esc/concepts/builtin-functions/fn-secret/ | Marks from the syntax tree of each expression (ADR 201 decision 12) | same |
| `environmentVariables`, `files`, `pulumiConfig` | /docs/esc/concepts/outputs/ | `exports.variables`, `exports.files`, `exports.pulumi`, and `exports.opentofu` (ADR 201 decision 9) | more |
| `policyConfig` for policy packs | /docs/esc/concepts/outputs/#policyconfig | The policy port of ADR 114 has its own inputs | not taken |
| A value that no consumer may store | none found | `$neverStore` (ADR 201 decision 6, ADR 209 decision 3) | more |

### 1.4 Versions and tags

| ESC feature | Source | Profiles | Mark |
| --- | --- | --- | --- |
| Immutable revisions, `latest` | /docs/esc/concepts/versioning/ | Same (ADR 202 decisions 2 and 3) | same |
| Revision tags | /docs/esc/concepts/versioning/#tagging-versions | Same, at most 32 (ADR 202 decision 3) | same |
| Environment tags (key and value) | /docs/iac/cli/commands/pulumi_env_tag/ | Labels (ADR 202 decision 4) | same |
| History, describe a version | /docs/iac/cli/commands/pulumi_env_version_history/ | Revisions route (ADR 203 decision 12) | same |
| Rollback, which fails in practice with rotators | /docs/iac/cli/commands/pulumi_env_version_rollback/ | Rollback keeps the current rotation state (ADR 202 decision 5) | more |
| Retract, tags move | /docs/iac/cli/commands/pulumi_env_version_retract/ | Same (ADR 202 decision 5) | same |
| Diff of versions or environments | /docs/iac/cli/commands/pulumi_env_diff/ | Same, and changed provider hosts (ADR 202 decision 5) | more |
| ETag and `If-Match` | OpenAPI `UpdateEnvironment` | Same (ADR 202 decision 2) | same |
| Redaction of a revision | none found | `profile:redact` with item keys (ADR 202 decision 2) | more |

### 1.5 Login providers

| ESC feature | Source | Profiles | Mark |
| --- | --- | --- | --- |
| `aws-login` (OIDC or static) | /docs/esc/providers/login/aws-login/ | `login/aws`, modes `exchange` and `token`, session tags (ADR 207 decision 3) | more |
| `azure-login` | /docs/esc/providers/login/azure-login/ | `login/azure` | same |
| `gcp-login` | /docs/esc/providers/login/gcp-login/ | `login/gcp` | same |
| `vault-login` (JWT or token) | /docs/esc/providers/login/vault-login/ | `login/vault`, also OpenBao | same |
| `gh-login` (GitHub App) | /docs/esc/providers/login/gh-login/ | `login/github` | same |
| `doppler-login`, `infisical-login`, `snowflake-login` | /docs/esc/providers/login/ | `login/doppler`, `login/infisical`, `login/snowflake` | same |
| `oidc` (a token for any audience, launched 2026-10-15) | /docs/esc/providers/login/oidc/ | `login/oidc` with the subject template of the namespace or the block (ADR 207 decision 4) | more |
| `debug-oidc-claims` | /docs/esc/providers/login/debug-oidc-claims/ | `POST /api/issuer/{ns}/claims-preview`, and the subject above the editor (ADR 204 decision 12) | same |
| `subjectAttributes` (6 attributes) | /docs/esc/providers/login/aws-login/ | Subject templates from 19 keys (ADR 205 decision 6) | more |
| Kubernetes, OAuth 2.0 identity providers | not in the catalog | `login/kubernetes`, `login/oauth` with presets (ADR 207 decisions 3 and 5) | more |
| `pulumi env setup aws`, `azure`, `gcp` (changes a cloud account) | /docs/iac/cli/commands/pulumi_env_setup/ | The docs show each trust policy. No command changes a cloud account. | not taken |
| `pulumi env provider ...` (writes a login block) | /docs/iac/cli/commands/pulumi_env_provider/ | `export: true` makes a login block one short block | own |

### 1.6 Secrets providers

| ESC feature | Source | Profiles | Mark |
| --- | --- | --- | --- |
| `1password-secrets` (service account token) | /docs/esc/providers/secrets/1password-secrets/ | `secrets/1password` through a Connect server. A service account needs a new dependency. | later |
| `aws-secrets`, `aws-parameter-store` | /docs/esc/providers/secrets/ | `secrets/aws-secrets-manager`, `secrets/aws-parameter-store` | same |
| `azure-secrets` | /docs/esc/providers/secrets/azure-secrets/ | `secrets/azure-key-vault` | same |
| `gcp-secrets` | /docs/esc/providers/secrets/gcp-secrets/ | `secrets/gcp-secret-manager` | same |
| `vault-secrets` | /docs/esc/providers/secrets/vault-secrets/ | `secrets/vault`, any read path | same |
| `doppler-secrets`, `infisical-secrets` | /docs/esc/providers/secrets/ | `secrets/doppler`, `secrets/infisical` | same |
| `external` (signed JWT with a hash of the body) | /docs/esc/providers/secrets/external/ | The external adapter protocol, for every kind and for rotators (ADR 208) | more |

### 1.7 IaC providers

| ESC feature | Source | Profiles | Mark |
| --- | --- | --- | --- |
| `pulumi-stacks` | /docs/esc/providers/iac/pulumi-stacks/ | `outputs/opendustpan`, for stacks and workspaces (ADR 207 decision 7) | more |
| `terraform-state` (S3, HCP Terraform) | /docs/esc/providers/iac/terraform-state/ | `outputs/opentofu-state` (S3, HTTP, a TFE-compatible API) | more |

### 1.8 Rotators

| ESC feature | Source | Profiles | Mark |
| --- | --- | --- | --- |
| `aws-iam`, `azure-app-secret`, `postgres`, `mysql`, `snowflake-user`, `password`, `passphrase`, `external` | /docs/esc/providers/rotators/ | The same 8 (ADR 207 decision 8). `mysql` needs a new dependency. | same |
| State in the document, a new revision each rotation | /docs/esc/concepts/rotators/ | A sealed rotation record outside the document (ADR 202 decision 8) | own |
| One schedule for each environment, at most daily | /docs/iac/cli/commands/pulumi_env_schedule_new/ | One schedule for each block, at most daily | more |
| Manual rotation, by path | /docs/iac/cli/commands/pulumi_env_rotate/ | `POST .../rotate`, by block | same |
| Rotation history | OpenAPI `ListEnvironmentSecretRotationHistory` | `GET .../rotations` | same |
| AWS Lambda connector for private databases | /docs/esc/operations/rotation/aws-lambda/ | A network setting of the operator, or the external rotator | not taken |

### 1.9 Approvals

| ESC feature | Source | Profiles | Mark |
| --- | --- | --- | --- |
| Approval rulesets on updates: drafts, approvers, count, re-approval | /docs/esc/concepts/approvals/ | Change rule and drafts. Self-approval is never allowed (ADR 202 decision 6). | same |
| Concurrent drafts, one for each user | blog: ESC concurrent change requests | Same | same |
| Three-way merge of a draft that is behind | /docs/esc/concepts/approvals/ | Same | same |
| Open approvals (just-in-time access) | /docs/esc/concepts/approvals/#open-approvals | Open rule and access requests (ADR 202 decision 7) | same |
| Gated deployments through open approvals | /docs/deployments/guides/gated-deployments/ | The gates of a job, with the open rule: RFC 5 | later |

### 1.10 Webhooks

| ESC feature | Source | Profiles | Mark |
| --- | --- | --- | --- |
| Events of environments, revisions, tags, imports and rotations | /docs/esc/concepts/webhooks/ | Notification types of ADR 203 decision 10, plus drafts and access requests | more |
| Signed delivery, delivery log, redelivery, ping | /docs/esc/concepts/webhooks/ | Webhook sinks of ADR 145 (two active secrets) | same |
| `slack`, `ms_teams` formats | /docs/iac/cli/commands/pulumi_env_webhook_new/ | Chat sinks of ADR 145 | same |
| `pulumi_deployments` format (starts a deployment) | /docs/esc/concepts/webhooks/#deployment-webhooks | Run triggers (RFC 24) | later |

### 1.11 Commands and clients

| ESC feature | Source | Profiles | Mark |
| --- | --- | --- | --- |
| `pulumi env run` (variables, files, masks, `-i`) | /docs/iac/cli/commands/pulumi_env_run/ | `opendustpan profile run`, and `pulumi env run` against the server (ADR 209 decisions 5 and 8) | same |
| Temporary files of `files` | /docs/esc/concepts/outputs/#files (removal not verified) | Memory-backed when possible, deleted at the end (ADR 209 decision 8) | more |
| `pulumi env open --format dotenv|json|shell` (direnv) | /docs/iac/cli/commands/pulumi_env_open/ | `opendustpan profile open --format` | same |
| `init`, `edit`, `get`, `set`, `ls`, `rm`, `clone`, `version`, `tag` | /docs/iac/cli/commands/pulumi_env/ | REST API of ADR 203. `pulumi env` subset: RFC 5 | later |
| `--override-env` on stack operations | /docs/iac/cli/commands/pulumi_up/ | Needs a check with the real CLI | later |
| SDKs (TypeScript, Python, Go, .NET), Automation API | /docs/esc/concepts/sdks/ | The REST API (ADR 17). SDKs: RFC 5 | later |
| Kubernetes External Secrets Operator, Secrets Store CSI | /docs/esc/integrations/kubernetes/ | RFC 5 | later |
| GitHub Action | /docs/esc/guides/integrate-with/github-actions/ | `opendustpan profile run` in any CI | own |
| A provider for OpenTofu (data source, ephemeral resource) | not found | `opendustpan_profile` (ADR 209 decisions 2 and 3) | more |

### 1.12 Lifecycle and access

| ESC feature | Source | Profiles | Mark |
| --- | --- | --- | --- |
| Projects | blog: ESC projects and environment tags | Profile folders (ADR 202 decision 9) | same |
| Rename only through clone | OpenAPI `CloneEnvironment` | Rename keeps the ID (ADR 202 decision 9) | more |
| Clone with options | /docs/iac/cli/commands/pulumi_env_clone/ | Same, never with grants or rules | same |
| Soft delete and restore, deletion protection | /docs/esc/administration/deletion-protection/ | Same, recovery window of 30 days | same |
| Referrers ("Imported By") | /docs/iac/cli/commands/pulumi_env_referrer_list/ | Referrers, with units and revisions read in 90 days | same |
| Permission sets read, open, write, admin | /docs/esc/concepts/environments/#access-control | Eight actions (ADR 203 decision 11) | more |
| Ownership transfer | OpenAPI `ReassignEnvironmentOwnership` | Profiles have no owner. Grants decide (ADR 52). | not taken |
| Audit events, including issued OIDC tokens | /docs/esc/administration/audit-logs/ | An entry for each open and each token (ADR 203 decision 9, ADR 204 decision 10) | same |
| Open sessions with a duration, 2 hours by default | OpenAPI `OpenEnvironment` | Same (ADR 203 decision 3) | same |
| Check of a document, provider and context schemas | OpenAPI `CheckEnvironment` | `POST .../check`, `GET /api/profiles/-/adapters`, `GET /api/profiles/-/context-schema` | same |
| Attach to many stacks by collection | none found | Profile attachments on a namespace, a collection or a unit (ADR 203 decision 5) | more |

## 2. OIDC claims of CI and IaC products

Sources: the docs of each product and the live discovery document of each
issuer, read with curl on the date above.

| Concept | Pulumi ESC and Deployments | GitLab CI | GitHub Actions | HCP Terraform | Spacelift | Buildkite | CircleCI | OpenDustpan (ADR 205) |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| Issuer | one (`/oidc`, `/oidc/v2`) | one for each instance | one | one for each instance | one for each account | one | one for each organization | one for each namespace |
| Tenant | `org`; v2: `org_id` | `namespace_id`, `namespace_path` | `repository_owner`, `_id` | `terraform_organization_*` | in `iss` | `organization_*` | `org-id` | `namespace`, `namespace_id` |
| Project or group | env project; Deployments `project` | `project_id`, `project_path` | `repository`, `_id` | `terraform_project_*` | space | `pipeline_*` | `project-id` | `collection`, `collection_id` |
| Unit | Deployments `stack`, `stackId` | none | none | `terraform_workspace_*` | `callerType`, `callerId` | none | none | `tool`, `unit`, `unit_id`, `unit_ref` |
| Run | Deployments `deployment` | `pipeline_id`, `job_id` | `run_id`, `run_attempt` | `terraform_run_id` | `runId` | `build_*`, `job_id` | `pipeline-id`, `job-id` | `job`, `change_order` |
| Phase or scope | Deployments `operation`, `scope` | `environment_action` | none | `terraform_run_phase` | `runType`, `scope` | `step_key` | none | `action`, `tool_action`, `phase`, `access` |
| Actor | `trigger_user`; v2 `actor`, `actor_id` | `user_id`, `user_login`, `user_email` | `actor`, `actor_id` | none | none | none | in `sub` | `actor_type`, `actor_id`, `act`; `actor` opt-in |
| Ref, type, protected | none | `ref`, `ref_type`, `ref_protected` | `ref`, `ref_type`, `ref_protected` | `ref` (not documented) | none | `build_branch`, `build_tag` | `vcs-ref` | `ref`, `ref_type`, `ref_protected` |
| Commit | none | `sha` | `sha` | none | none | `build_commit` | none | `commit` |
| Runner | none | `runner_id`, `runner_environment` | `runner_environment` | none | none | `agent_id`, queue | none | `runner_pool`, `runner_pool_id` |
| Default `sub` | `pulumi:environments:org:<org>:env:<project>/<env>`; v2 IDs | `project_path:<p>:ref_type:<t>:ref:<r>` | `repo:<o>/<r>:ref:<ref>` or `:environment:<e>` | `organization:<o>:project:<p>:workspace:<w>:run_phase:<plan|apply>` | `space:<s>:<type>:<id>:run_type:<t>:scope:<read|write>` | `organization:...:pipeline:...:ref:...:commit:...:step:...` | `org/<id>/project/<id>/user/<id>` | `namespace:<n>:collection:<c>:tool:<t>:unit:<u>:access:<a>` |
| `sub` can change | 6 attributes; v2 no | per project | org and repo templates | no | org template | one ID claim | no | templates from 19 keys, for the namespace or a block |
| Default `aud` | `<cloud>:<org>`; v2 set by caller | instance URL, set per token | `https://github.com/<owner>`, set by caller | per cloud, for example `aws.workload.identity` | account host | `https://buildkite.com/<org>` | org ID | per adapter, set per block |
| Lifetime | not documented | job timeout, else 5 min | about 5 min (example) | phase timeout | 1 h | 5 min | 1 h | 5 min, 15 min in files, at most 60 min |
| Personal data by default | login | login and e-mail | login | none | none | none | user ID in `sub` | none |
| Signing | RS256 | RS256 | RS256 | RS256 | RS256 (not verified) | RS256 | RS256 | RS256 |

Sources of section 2:
`/docs/esc/environments/configuring-oidc/`,
`/docs/esc/providers/login/oidc/`,
`/docs/pulumi-cloud/deployments/oidc/`,
`https://docs.gitlab.com/ci/secrets/id_token_authentication/`,
`https://docs.github.com/en/actions/reference/security/oidc`,
`https://docs.github.com/en/rest/actions/oidc`,
`https://developer.hashicorp.com/terraform/cloud-docs/workspaces/dynamic-provider-credentials/workload-identity-tokens`,
`https://docs.spacelift.io/integrations/cloud-providers/oidc`,
`https://buildkite.com/docs/agent/v3/cli-oidc`,
`https://circleci.com/docs/guides/permissions-authentication/openid-connect-tokens/`.

## 3. What a relying party reads

| Relying party | What it matches | Limits | Source |
| --- | --- | --- | --- |
| AWS IAM | For an issuer that AWS does not name: `sub`, `aud`, `amr`, `email`, `oaud`, with `StringLike`. Session tags from `https://aws.amazon.com/tags`. | Session 15 minutes to 12 hours. Token up to 20,000 characters. JWKS up to 100 RSA keys. No thumbprint for a host with a public certificate. | `https://docs.aws.amazon.com/IAM/latest/UserGuide/reference_policies_iam-condition-keys.html`, `https://docs.aws.amazon.com/STS/latest/APIReference/API_AssumeRoleWithWebIdentity.html` |
| Azure | Exact issuer, subject and audience. Flexible matching is in preview for three named issuers only. | 20 federated credentials for each identity, 600 characters for each field, `RS256` only. | `https://learn.microsoft.com/en-us/entra/workload-id/workload-identity-federation-considerations` |
| Google Cloud | CEL over any claim, 50 custom attributes. | `google.subject` at most 127 characters. `exp - iat` at most 24 hours. `RS256` or `ES256`. Upload of up to 8 keys. | `https://docs.cloud.google.com/iam/docs/workload-identity-federation-with-other-providers` |
| Vault, OpenBao | `bound_audiences` (required for a token with `aud`), `bound_claims` with globs and JSON pointers. OpenBao 2.3 adds CEL roles. | | `https://developer.hashicorp.com/vault/docs/auth/jwt`, `https://openbao.org/docs/auth/jwt/` |
| Kubernetes | Structured authentication configuration, CEL claim rules (GA in 1.34). | 64 JWT authenticators. | `https://kubernetes.io/docs/reference/access-authn-authz/authentication/` |
| Snowflake | Issuer and subject of a service user, a list of audiences. | Keys cached up to 60 minutes. `PS` algorithms refused. Issuer must be public. | `https://docs.snowflake.com/en/user-guide/workload-identity-federation` |
| Doppler | Audience, subject and claims of a service account identity. | Team and Enterprise plans. | `https://docs.doppler.com/docs/service-account-identities` |
| Infisical | Subject, audiences and claims, with globs. | Needs discovery. Default token lifetime of 30 days. | `https://infisical.com/docs/documentation/platform/identities/oidc-auth/general` |

## 4. Identity providers

| System | Fits | How | Source |
| --- | --- | --- | --- |
| Keycloak 26.6 | yes | Federated client authentication: `client_credentials` with a `jwt-bearer` client assertion, exact `sub` for each client, `aud` the realm issuer. | `https://www.keycloak.org/2026/01/federated-client-authentication` |
| Keycloak JWT Authorization Grant | partly | RFC 7523 grant. The `sub` must be a linked Keycloak user that exists. | `https://www.keycloak.org/securing-apps/jwt-authorization-grant` |
| Keycloak token exchange V2 | no | Tokens of its own realm only. | `https://www.keycloak.org/securing-apps/token-exchange` |
| Authentik | yes, with a policy | Client assertion from a federated OIDC source. Checks only the signature and `exp` by default. | `https://docs.goauthentik.io/add-secure-apps/providers/oauth2/client_credentials/` |
| Ory Hydra | partly | RFC 7523 trust relationship with a fixed key. A new trust at each key rotation. | `https://www.ory.com/docs/hydra/guides/jwt` |
| Dex | weak | RFC 8693 with `connector_id`. No check of `aud` for an ID token. | `https://dexidp.io/docs/guides/token-exchange/` |
| Zitadel | no | Token exchange and JWT profile take only keys and tokens of Zitadel. | `https://zitadel.com/docs/guides/integrate/token-exchange` |
| Authelia, Kanidm, Casdoor | no | No trust of an external issuer for a machine. | the docs of each |
| 1Password | not yet | Credential Broker in preview, through one SDK, with a shared key. | `https://www.1password.dev/environments/credential-broker` |
| GitHub | no | An installation token needs the key of a GitHub App. | the GitHub Apps docs |

## 5. The Pulumi CLI and the backend

From the source of `github.com/pulumi/pulumi` at commit
`b9e87ddf20c455810052cab51319a7eb3fc899c1` (`pkg/cmd/pulumi/config/io.go`,
`pkg/backend/httpstate/environments.go`, `pkg/cmd/esc/cli/client/client.go`):

- The stack config `environment:` list becomes `{"imports": [...]}`, sent to
  `POST /api/esc/environments/{org}/yaml/open?duration=2h0m0s`. Then `GET
  .../yaml/open/{id}` reads `properties` with `pulumiConfig`,
  `environmentVariables` and `files`.
- The CLI encrypts each secret of `pulumiConfig` with the secrets provider
  of the stack and sends it as ciphertext in the update. It sets
  `environmentVariables` in its own process. It writes `files` to
  temporary files and does not delete them.
- No capability gates these calls. The CLI sends them to any HTTPS backend.
- The ESC client moved into the Pulumi CLI in v3.251.0. The standalone
  `esc` CLI is retired. The project-scoped paths came with esc v0.10.0
  (Pulumi CLI v3.132.0).
- Not verified: the first CLI version of the cloud-stored stack config, and
  the server side of each path. `make e2e` decides.
