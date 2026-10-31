# 207. The built-in profile providers

Status: accepted · 2026-10-18 · part of ADR 200

Changed by:

- ADR 273 changes decision 7: the attachment authority needs
  unit:read-outputs.
- ADR 286 changes: the words catalog and promotion become self-service and
  failover.

## Context

The project owner decided on 18 October 2026 (owner):

- Complete parity with the providers of Pulumi ESC:
  - login providers for AWS, Azure, Google Cloud, GitHub, Vault, Doppler,
    Infisical, Snowflake and a generic OIDC token, with a claims debugger,
  - secrets providers for 1Password, AWS Secrets Manager, AWS Parameter
    Store, Azure Key Vault, Google Cloud Secret Manager, Doppler,
    Infisical, Vault and an external adapter,
  - outputs providers for stack outputs and OpenTofu state outputs,
  - rotators.
- An outputs provider over the inventory of OpenDustpan, so that a Pulumi
  stack can read the outputs of an OpenTofu workspace, and the reverse.
- Login providers also for Keycloak and similar identity providers, for
  example Zitadel, Authentik, Dex and Ory, where they fit. RFC 8693 token
  exchange is a likely way.

The research note `docs/research/profiles-esc-parity-and-oidc-claims.md`
lists what each external system accepts. The facts that decide a design
here:

- **Keycloak** trusts an external JWT best through federated client
  authentication: `client_credentials` with the JWT as a `jwt-bearer`
  client assertion, an identity provider in the realm, and an exact
  `sub` for each client. It is supported from Keycloak 26.6, as the
  research of 2026-10-18 found. Check this version against the release
  notes of Keycloak before the adapter ships. Its JWT
  Authorization Grant (RFC 7523) needs a Keycloak user that already exists
  for each `sub`. Its standard token exchange takes only tokens of its own
  realm.
- **Authentik** accepts the same client assertion from a federated OIDC
  source. It does not check `aud` and `iss` by default, so an expression
  policy must check them.
- **Ory Hydra** accepts RFC 7523 with a trust relationship that holds a
  fixed key. Each key rotation needs a new trust relationship.
- **Dex** has RFC 8693 token exchange with a `connector_id`, but does not
  check `aud` of an ID token.
- **Zitadel**, Authelia, Kanidm and Casdoor cannot trust an external issuer
  for a machine login.
- **1Password** has no documented way to trust an external JWT. Its
  service accounts work through its SDK. Its Connect server has a REST API.
- **GitHub** has no way to turn an external JWT into a token. A GitHub App
  key makes an installation token.
- ESC reaches a private database for a rotation through a function in the
  cloud of the team. ESC reads OpenTofu state only from S3 and from
  HCP Terraform.

## Decision

1. **The names.** Each adapter has a kind and a name. The value of `use` in
   a block is `<kind>/<name>`. The rotators have their own key, `rotators`
   (ADR 202 decision 8). A table of the docs lists each adapter with its
   inputs, its outputs, its default exports and its e2e status.
2. **Two modes of a cloud login.** A cloud login block has `mode`:
   - `exchange` (the default): the server sends the token of the issuer to
     the cloud and gets a session. The open answers the credential. Each
     consumer can use it: the run command, the Pulumi CLI, the provider of
     OpenTofu and a runner.
   - `token`: the server mints the token, and the open answers the token
     and the settings that the SDK of the cloud needs. The SDK in the tool
     does the exchange and renews the session. The server never calls the
     cloud and never holds a cloud credential. On a runner, the launcher
     writes the token to a file and gets a new one before it ends (ADR 203
     decision 7).

   An operator who wants no call from the server to a cloud uses `token`.
3. **Cloud logins.**

   | `use` | The exchange | Main inputs | Default audience | Default exports |
   | --- | --- | --- | --- | --- |
   | `login/aws` | `AssumeRoleWithWebIdentity` | `roleArn`, `sessionName`, `duration` (15 minutes to 12 hours, 1 hour by default), `policyArns`, `sessionTags`, `region` | `sts.amazonaws.com` | `exchange`: a shared credentials file in `AWS_SHARED_CREDENTIALS_FILE`. `token`: the token in `AWS_WEB_IDENTITY_TOKEN_FILE`, and `AWS_ROLE_ARN`. |
   | `login/gcp` | Security Token Service exchange, then an optional impersonation of a service account | `projectNumber`, `pool`, `provider`, `serviceAccount`, `tokenLifetime` | the resource name of the pool provider | `exchange`: the access token in `CLOUDSDK_AUTH_ACCESS_TOKEN_FILE`. `token`: an external account configuration in `GOOGLE_APPLICATION_CREDENTIALS`, with the token in a second file. |
   | `login/azure` | `client_credentials` with the token as a client assertion | `tenantId`, `clientId`, `subscriptionId`, `scope` | `api://AzureADTokenExchange` | `token` (the default for Azure): the token in `AZURE_FEDERATED_TOKEN_FILE` and `ARM_OIDC_TOKEN_FILE_PATH`, with `AZURE_CLIENT_ID`, `AZURE_TENANT_ID`, `ARM_CLIENT_ID`, `ARM_TENANT_ID`, `ARM_SUBSCRIPTION_ID` and `ARM_USE_OIDC`. |
   | `login/kubernetes` | none: the API server checks the token itself | `server`, `certificateAuthority`, `audience` | the `server` URL | A kubeconfig in `KUBECONFIG` whose user reads the token from a file (`tokenFile`). |

   - `sessionTags` of `login/aws` names claims of ADR 205, for example
     `[collection, access]`. The token then carries the claim
     `https://aws.amazon.com/tags` with those values, so IAM policies can
     read them as `aws:PrincipalTag`. The role needs `sts:TagSession`.
   - `login/aws` also takes `static` with an access key from `$secret`
     entries, for parity. The docs say that a static key lives long and can
     leak, and show the OIDC form first (ADR 54 decision 7).
   - The default exports hold only paths in variables, and no credential.
     So they work on a runner with no change (ADR 134 decision 6). The
     names of the variables are those that the SDK of each cloud and the
     OpenTofu providers read. `make e2e` checks each name with the real
     tool before an adapter ships.
4. **Logins to secret stores and other services.**

   | `use` | The login | Main inputs | Default audience |
   | --- | --- | --- | --- |
   | `login/vault` | The JWT auth method of Vault or OpenBao | `address`, `mount` (`jwt` by default), `role`, `namespace` | the `address` |
   | `login/doppler` | The OIDC identity of a Doppler service account | `identityId`, `audience` | none: the block sets the audience of the identity |
   | `login/infisical` | The OIDC auth of an Infisical machine identity | `siteUrl`, `identityId`, `audience` | none: the block sets an audience of the identity |
   | `login/snowflake` | Workload identity federation of a Snowflake service user | `account`, `user`, `role`, `audience` | none: the block sets a value of `OIDC_AUDIENCE_LIST` of the user |
   | `login/github` | An installation token of a GitHub App, from the key of the app | `appId`, `privateKey` (a secret), `owner`, `repositories`, `permissions`, `host` | none |
   | `login/oidc` | No exchange: the token itself | `audience` (required), `tokenLifetime`, `subject` | none |
   | `login/oauth` | A token endpoint of OAuth 2.0 (decision 5) | `tokenUrl`, `grant`, `clientId`, `scope`, `audience` | the issuer of the target |
   | `login/external` | The external adapter protocol (ADR 208) | `url`, `request` | the full `url`, always |

   - `login/snowflake` gives the token. The Snowflake driver of the tool
     does the login, because Snowflake gives a session and not a token
     that another process can use.
   - `login/github` is the one login that holds a long-lived key: the key
     of the app, as a `$secret` entry. GitHub has no OIDC trust for an
     external issuer. The token lives 1 hour.
   - The docs warn that the default token lifetime of Infisical is 30 days,
     and show how to set it short.
5. **Keycloak and its siblings, through one adapter.** `login/oauth` speaks
   three grants. `grant` names one:
   - `client-assertion`: `client_credentials` with the token as a
     `jwt-bearer` client assertion (RFC 7523 section 2.2). Keycloak
     (federated client authentication, 26.6 and later, to check before
     the adapter ships) and Authentik use
     it.
   - `jwt-bearer`: the JWT authorization grant (RFC 7523 section 2.1).
     Ory Hydra and the JWT Authorization Grant of Keycloak use it.
   - `token-exchange`: RFC 8693, with `subject_token_type`
     `urn:ietf:params:oauth:token-type:jwt` and optional `audience`,
     `resource` and `requested_token_type`. Dex uses it, with the extra
     input `connectorId`.

   `preset` fills the defaults of one product: `keycloak`, `authentik`,
   `hydra` or `dex`. The guide of each preset gives the setup on the side of
   the identity provider, with these warnings:
   - Keycloak: the `aud` must be the issuer of the realm, so the preset sets
     it. Each client matches one exact `sub`.
   - Authentik: add an expression policy that checks `iss` and `aud`.
     Without it, Authentik checks only the signature and `exp`.
   - Ory Hydra: a trust relationship holds a fixed key. After each key
     rotation of the issuer (ADR 204 decision 5), add a trust for the next
     key before it becomes active. The notice `issuer.key-next` gives the
     time.
   - Dex: Dex does not check the `aud` of the token. The guide says that
     any token of the issuer of the namespace can then get a Dex token, and
     shows a connector for each namespace.

   Zitadel, Authelia, Kanidm and Casdoor get no preset, because none of them
   can trust an external issuer for a machine. The docs say so, and point to
   `login/oidc` and to the external adapter for such a system.
6. **Secrets providers.**

   | `use` | Store | Main inputs | Login |
   | --- | --- | --- | --- |
   | `secrets/aws-secrets-manager` | AWS Secrets Manager | `region`, `get: {name: {secretId, versionId, versionStage, key}}` | `login/aws` |
   | `secrets/aws-parameter-store` | AWS Systems Manager Parameter Store | `region`, `get: {name: {name, decrypt}}` | `login/aws` |
   | `secrets/azure-key-vault` | Azure Key Vault | `vault`, `get: {name: {name, version}}` | `login/azure` in `exchange` mode, scope of Key Vault |
   | `secrets/gcp-secret-manager` | Google Cloud Secret Manager | `get: {name: {name, version}}` | `login/gcp` in `exchange` mode |
   | `secrets/vault` | Vault or OpenBao, any read path: KV version 1 or 2, and dynamic engines such as database credentials | `read: {name: {path, field}}` | `login/vault` |
   | `secrets/doppler` | Doppler | `project`, `config`, `get: {name: {name}}` | `login/doppler` |
   | `secrets/infisical` | Infisical | `get: {name: {projectId, environment, secretKey, secretPath}}` | `login/infisical` |
   | `secrets/1password` | 1Password, through a Connect server | `connectUrl`, `get: {name: {ref}}` with `op://vault/item/field` | a Connect token as a `$secret` |
   | `secrets/external` | The external adapter protocol (ADR 208) | `url`, `request` | none |

   Each output is secret. A login block that a secrets block names must be
   in `exchange` mode, because the server makes the call. Each block that
   calls a host on a private network can trust an internal CA with the
   input `caCertificates` or the installation setting
   `profileCACertificates` (ADR 206 decision 6). This applies to
   `login/vault`, `login/oauth`, `login/oidc`, `secrets/vault`,
   `secrets/1password`, `outputs/opentofu-state` with `http` or `remote`,
   the `postgres` and `mysql` rotators, and each external block.
   - A dynamic engine of `secrets/vault`, for example database
     credentials, gives a value with a lease. The server keeps the lease
     ID with the open session (ADR 203). When the session ends or expires,
     the server revokes each lease of the session with the same login
     (`sys/leases/revoke`). The revoke is best effort: when it fails, the
     server writes an audit entry and the lease ends at its own TTL in
     Vault. An open with no session, for example an open of a job, revokes
     its leases when the job ends. The docs say to give each dynamic role
     a short TTL, because a revoke can fail.

   The server calls the REST API of each store with the standard library,
   and signs AWS calls with the SigV4 code that the S3 blob adapter
   already has.
7. **Outputs providers.**

   | `use` | Reads | Main inputs |
   | --- | --- | --- |
   | `outputs/opendustpan` | The outputs of a unit of the same namespace, of any tool | `units: {name: {unit: "opentofu:acme/network", outputs: [vpc_id, subnet_ids]}}` |
   | `outputs/opentofu-state` | The outputs of a state that another backend holds | `backend: s3`, `http` or `remote`, with its settings and login |
   | `outputs/external` | Outputs from the external adapter protocol (ADR 208) | `url`, `request` |

   - `outputs/opendustpan` is the bridge between the tools (owner). A
     Pulumi stack reads the outputs of an OpenTofu workspace, and the
     reverse. It reads the outputs of the last state version through a port
     of the wiring, `profile.UnitOutputs`, over the masked reads of the tool
     packages (ADR 41), as the options of the Catalog do (ADR 113 decision
     6). It names each output with the words of its tool, and keeps the
     secret mark of each output.
   - A save resolves each `unit` reference to the ID of the unit and stores
     the ID with the revision. An open reads the unit with that ID, not
     with its name. When a person deletes the unit and makes a new unit
     with the same name, the new unit does not match, and the open of the
     block fails with `ErrRemoteNotFound`, which names the reference. A
     person who wants the new unit saves the profile again.
   - The access check is for the attachment authority of the open (ADR
     203 decision 4), at each open: it needs `unit:read` on the source
     unit, and `unit:read-secrets` for a secret output (ADR 41). For a
     direct open, the attachment authority is the caller. For a job open,
     it is the principal that made the attachment, or the blueprint
     publisher for a Catalog attachment, and not the person who started
     the job. The save checks the same for the author, so
     an author cannot put into a profile an output that the author cannot
     read.
   - **A job open needs both rules.** For an open by a job, the output
     must pass both: an output-sharing rule of the source unit names the
     unit of the job, and the attachment authority holds `unit:read`, and
     `unit:read-secrets` for a secret output, on the source unit. The open
     gets the intersection of the two, never more. A missing sharing rule
     fails the block with `identity.ErrDenied`, which names the source unit
     and the unit of the job. A missing action fails the open with
     `profile.ErrAttachmentAuthority` (ADR 203 decision 4). ADR 213
     decision 1 (in review) gives the same intersection for a read of the
     job token through the API, with the principal that started the job. A
     read of the job token uses the rule of ADR 213. An open of a profile
     uses the rule of this decision. ADR 200 decision 9 lists this for ADR
     213.
   - `outputs/opentofu-state` reads S3 (with `login/aws`), the HTTP backend
     of OpenTofu, and the `remote` backend of a TFE-compatible API, for
     example another installation. It gives the root outputs.
8. **Rotators.**

   | Rotator | Rotates | Login |
   | --- | --- | --- |
   | `aws-iam` | The access keys of an IAM user, two at a time | `login/aws` |
   | `azure-app-secret` | A client secret of an app registration | `login/azure` |
   | `postgres` | The password of one of two database users, in turn | a managing user, as `$secret` entries |
   | `mysql` | The same for MySQL | a managing user, as `$secret` entries |
   | `snowflake-user` | The key pair of a Snowflake user | `login/snowflake` |
   | `password` | A random password, with length and classes of characters | none |
   | `passphrase` | A random passphrase of words | none |
   | `external` | Anything, through the external adapter protocol (ADR 208) | none |

   `postgres` and `mysql` reach a database on a private network through the
   setting of ADR 206 decision 6. `postgres` uses the PostgreSQL driver that
   the server already links. `mysql` needs a MySQL driver, which is a new
   dependency. RFC 5 asks the project owner about it, and about which
   rotators the first release ships.
9. **What we do not copy.** ESC has no login provider for Kubernetes and no
   OAuth 2.0 adapter. We add both. ESC reads OpenTofu state from S3 and from
   HCP Terraform only. We add the HTTP backend and the TFE-compatible API of
   any server. We do not ship a function in the cloud of the team for a
   rotation of a private database. A team gives the server a route to the
   network, or uses the external rotator inside its network.

## Consequences

- Each adapter in this ADR is one package under `pkg/profile/adapter/`,
  with its conformance run (ADR 206 decision 8) and its e2e step against
  the real system.
- The SigV4 code moves from `pkg/blob/s3` to a shared internal package, so
  that the blob adapter and the AWS adapters both use it, and no adapter
  imports another.
- A 1Password service account needs the SDK of 1Password, which is a new
  dependency. Version 1 uses the Connect server. RFC 5 keeps the service
  account open.
- The default exports of a cloud login are one line of a profile
  (`export: true`), so a homelab user writes about ten lines for a working
  AWS login (ADR 200, the examples).
