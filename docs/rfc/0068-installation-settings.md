---
rfc: 68
title: "Installation settings: inventory, settings through the API, and live reload"
status: draft
created: 2026-10-13
---

# RFC 68. Installation settings: inventory, settings through the API, and live reload

Status: draft.

## Problem

The project owner asked for five things, relayed by the product manager on
2026-10-13:

1. An inventory of the settings that apply to the whole installation, not to
   one namespace.
2. Each of them can be set through the API. So the server stores it in the
   database, not only in a flag, an environment variable or a file.
3. The server reloads its configuration while it runs, for example when a
   configuration file or a Kubernetes ConfigMap changes.
4. "Config file values ALWAYS override the UI/DB values." The console shows
   such a setting greyed out on its admin screen.
5. The aim: possibly allow multi-tenancy for a hosted service later.

The owner wrote "instance-scoped". In `GLOSSARY.md` an instance is one
server process, and the word for the whole deployment is _installation_. So
this RFC says "installation setting" for a setting that applies to the whole
installation, and "instance setting" only for a value that each server
process can hold differently.

Facts from the code on `main` (commit `5f1292c`):

- Every server setting is a flag with an environment variable, declared once
  in `internal/cli/settings.go`. The secrets (the key-encryption key, the
  bootstrap admin tokens, the S3 credentials) come from environment
  variables or files only.
- There is no configuration file.
- No setting changes while the server runs. `dustpan.NewServer` copies each
  value into the adapters and services once. A change needs a restart.
- Two values are in the database and change at run time: the installation
  state (`normal` or `maintenance`, ADR 38) and the override switches of
  each namespace (ADR 44). Each instance reads the installation state with a
  cache of 2 seconds (`pkg/maintenance`, `DefaultCacheLimit`).
- ADR 17 makes an exception for "settings that the server reads before it
  starts". It also says: "Settings that change at run time are" features,
  so each one needs a REST API with an action. Live reload therefore pulls
  every reloadable setting into the API.

This RFC decides nothing (ADR 15). It gives the inventory, the options for
each design point, and a recommendation.

## Owner direction (2026-10-13)

The project owner gave this direction on 2026-10-13. The product manager
relayed it, and the project lead passed it to this RFC. It decides nothing
in this RFC (ADR 15). The design must meet it.

The project designs with a hosted service in mind, on the model of Temporal:

1. The open-source server stays feature complete.
2. The hosted service sells only operations: the same software, run by the
   project, at a flat price that covers costs and a modest margin.
3. No feature is only in the hosted service. SSO, MFA, audit and RBAC stay
   in the open-source server.
4. Hosted limits and defaults are installation settings (this RFC), not code
   paths. Sign-up and billing stay outside the server binary.
5. Multi-tenancy has two options for a separate RFC: one small installation
   for each customer first, or one shared installation with a tenant layer
   above namespaces. The project lead decides.

See "No setting gates a feature" and "Multi-tenancy" for what this RFC does
with points 3 to 5.

### The hosted service (2026-10-13)

The owner decided more about the hosted service on 2026-10-13. The product
manager relayed it. These are the owner's decisions for the hosted service.
In this RFC they decide nothing (ADR 15).

1. One hosted tier: a flat price for each organization, with no limit on
   users and resources. The price is not decided.
2. No free tier and no trial. To try OpenDustpan, a person runs the binary.
3. No dedicated tier.
4. Fair-use limits on the stored bytes, the history kept and the API
   request rate. These are installation settings in this RFC. See
   "Fair-use limits".
5. Free reference architectures, tested in CI: the single binary with
   SQLite, one virtual machine with PostgreSQL and S3-compatible storage,
   and AWS (ECS, Aurora, S3). This is not a setting. It is context only.
   Each of them keeps backups and tested restores (ADR 30, ADR 42), with or
   without replication. Replication does not replace a backup (owner,
   2026-10-13, see RFC 37).

"Organization" is the owner's word for a customer of the hosted service. In
`GLOSSARY.md` an organization is a namespace in a tool protocol. With the
lead recommendation of one installation for each customer (see
"Multi-tenancy"), one customer is one installation that can hold many
namespaces.

## Terms in this RFC

- **Setting**: one named value that changes how the server works, for
  example `log.level`.
- **Static layer**: the values that the operator sets outside the API: a
  flag, an environment variable, or a configuration file.
- **Database layer**: the values that the API writes and the database
  keeps.
- **Default**: the value built into the binary.
- **Effective value**: the value that the server uses after it applies the
  precedence.
- **Locked**: a setting whose effective value comes from the static layer.
  The API refuses to change it.
- **Bootstrap setting**: a setting that the server needs before it can read
  the database, or a setting whose change changes trust. It is never in the
  database layer.
- **Live setting**: a setting whose new value takes effect without a
  restart. **Restart setting**: a setting whose new value takes effect only
  after a restart.

These words are proposals. The ADR that accepts this RFC adds the words that
stay to `GLOSSARY.md`.

## Inventory 1: settings in the code today

"Live today" is "no" for every flag, because `NewServer` reads each value
once. "Scope" is _installation_ when one value must hold for every instance,
and _instance_ when each process can hold its own value.

### Bootstrap settings: static, never in the database

| Setting | Flag / environment | Source today | Why it stays static |
| --- | --- | --- | --- |
| Database | `--database` / `DUSTPAN_DATABASE` | flag, env | The server needs it to open the database. The value can hold a credential. |
| Region | `--region` / `DUSTPAN_REGION` | flag, env | ADR 4: a namespace accepts writes only in its home region. A change moves write authority and the fence. |
| Listen address | `--listen` / `DUSTPAN_LISTEN` | flag, env | The server binds it before it opens the database. |
| TLS certificate and key paths | `--tls-cert`, `--tls-key` / `DUSTPAN_TLS_CERT`, `DUSTPAN_TLS_KEY` | flag, env | The trust root of the server. ADR 27 reads the files once at start. See "TLS files" for a reload of the file content with static paths. |
| Key-encryption key | `DUSTPAN_KEY_ENCRYPTION_KEY`, `--key-encryption-key-file` / `DUSTPAN_KEY_ENCRYPTION_KEY_FILE` | env, file | ADR 23: the key protects the database. A key in the database protects nothing. |
| Make a key file | `--generate-key-encryption-key` / `DUSTPAN_GENERATE_KEY_ENCRYPTION_KEY` | flag, env | An action at the first start, not a setting. |
| Bootstrap admin tokens | `DUSTPAN_ADMIN_TOKEN`, `DUSTPAN_ADMIN_TOKEN_FILE` | env, file | The break-glass credential (ADR 57, pending in #467). It must work when the database is empty or wrong. The help text says: "a change to the list needs a restart". |
| Blob store | `--blob-store`, `--blob-dir` / `DUSTPAN_BLOB_STORE`, `DUSTPAN_BLOB_DIR` | flag, env | Database rows name blob keys in this store. A change while the server runs makes those rows point at nothing. |
| S3 endpoint and region | `--s3-endpoint`, `--s3-region` / `DUSTPAN_S3_ENDPOINT`, `DUSTPAN_S3_REGION` | flag, env | Part of the identity of the blob store. |
| S3 credentials | `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN` | env | A credential. ADR 31 reads it from the environment only. |
| Console directory | `--console-dir` / `DUSTPAN_CONSOLE_DIR` | flag, env | A local path on each instance. |
| Backup target | `--backup-target` / `DUSTPAN_BACKUP_TARGET` | flag, env | ADR 30 decision 9: the target is "never a request field. So a token cannot send data to another place." See "The backup target". |

### Settings that can move to the database

| Setting | Flag / environment | Scope | Live after this RFC | Note |
| --- | --- | --- | --- | --- |
| Public URL | `--public-url` / `DUSTPAN_PUBLIC_URL` | installation | live | It shapes absolute URLs, for example upload URLs. A wrong value can lock every client out. See "Lock-out and recovery". |
| Allowed origins | `--allowed-origins` / `DUSTPAN_ALLOWED_ORIGINS` | installation | live | A wrong value locks the console out. See "Lock-out and recovery". |
| Log level | `--log-level` / `DUSTPAN_LOG_LEVEL` | installation | live | A `slog.LevelVar` makes it live at low cost. |
| Log format | `--log-format` / `DUSTPAN_LOG_FORMAT` | installation | restart | The server builds the log handler once. |
| Largest checkpoint | `--max-checkpoint-bytes` / `DUSTPAN_MAX_CHECKPOINT_BYTES` | installation | live | The update service reads it for each request. |
| Largest secrets batch | `--max-secret-batch-bytes` / `DUSTPAN_MAX_SECRET_BATCH_BYTES` | installation | live | As above. |
| Largest OpenTofu state | `--max-state-bytes` / `DUSTPAN_MAX_STATE_BYTES` | installation | live | As above. |
| Large bodies in flight | `--max-body-bytes-in-flight` / `DUSTPAN_MAX_BODY_BYTES_IN_FLIGHT` | **instance** | restart | It bounds the memory of one process (RFC 58). Instances with different memory can need different values. A resize of the live semaphore needs care. |
| Checkpoint delta cutoff | `--checkpoint-delta-cutoff` / `DUSTPAN_CHECKPOINT_DELTA_CUTOFF` | installation | live | The server sends it at the start of an update. A running update keeps its value. |
| Pulumi journaling off | `--disable-pulumi-journaling` / `DUSTPAN_DISABLE_PULUMI_JOURNALING` | installation | live | Read at the start of an update. A running update keeps its mode. |

### Values that are already in the database

| Value | Scope | Note |
| --- | --- | --- |
| Installation state (`normal`, `maintenance`) | installation | ADR 38 decision 4. The `installation` table, in the backup table list. Each instance caches it for 2 seconds. ADR 48 asks each instance before a restore applies. It is state, not a setting, but it is the model for how each instance sees a change. |
| Override switches | namespace | ADR 44 decision 6. A route with its own action. Out of scope here: this RFC covers installation settings. |

### Not settings

- The SDK options `WithLogger`, `WithClock`, `WithAuthorizer`,
  `WithListener`, `WithVersion` and the store options are ports and wiring
  (ADR 35).
- The flags of client commands (`--server`, `--ca-file`, `--backup`,
  `--check-only`, `--force`) configure a client (ADR 39), not the server.

## Inventory 2: planned settings

The settings below are in an accepted ADR or an RFC, and not in the code.
Two sources feed this list: the product manager's list, relayed by the
project lead on 2026-10-13, and a read of `docs/adr`, `docs/rfc` and the
branch of PR #467. "Pending" means an ADR in PR #467 (ADRs 51 to 57) that is
not merged. "Layer" is the proposal of this RFC: _API_ means the database
layer with the static layer over it, _static_ means flag, environment or
file only.

Most planned settings have no name yet. Only ADR 60 (`--audit` and the
others), ADR 48 (`--advertise-address`), RFC 7 (`--role`) and ADR 33
(`--migrate=never`) name a flag.

### Identity and sign-in

| Setting | Planned in | State | Secret | Scope | Layer | Note |
| --- | --- | --- | --- | --- | --- | --- |
| Built-in MFA on or off | ADR 46 decision 5 | accepted | no | installation | API, live | Off by default. |
| Require MFA for all users | ADR 46 decision 5 | accepted | no | installation | API, live | |
| Require MFA for a secret reveal | ADR 46 decision 7 | accepted | no | installation | API, live | A setting of its own. |
| MFA freshness window for token creation and for a reveal | ADR 56 decisions 2 to 4 | pending | no | open: "installation, a namespace, or both" | API, live | Default 15 minutes. A change writes an audit entry. |
| Console session idle timeout and maximum length | RFC 21 open question | lead proposal | no | installation | API, live | Lead: 15 minutes and 12 hours. |
| Require MFA for the bootstrap admin token | RFC 21 open question, ADR 57 | lead proposal | no | installation | static | It touches break-glass. Keep it out of the API. |
| OIDC or OAuth 2.0 provider: issuer, client ID, client secret, user-info endpoint, claim mapping | ADR 6, ADR 55 decisions 1 and 7 | pending | client secret | installation | API, live | ADR 55 decision 11: each method "that changes at run time has a REST API and an action". No field names exist. |
| SAML provider and service provider key | ADR 55 decision 1 | pending | key | installation | API, live | No field names exist. |
| CAS server | ADR 55 decision 1 | pending | no | installation | API, live | |
| LDAP server, bind credentials, sync interval | ADR 55 decision 1, RFC 21 open question | pending | bind password | installation | API, live | |
| SCIM endpoint token | ADR 55 decisions 1 and 4 | pending | token | installation | API, live | |
| Revoke at once on a SCIM or LDAP removal | ADR 55 decision 9 | pending | no | installation | API, live | Off by default: disable and keep for review. |
| Passkey sign-in for local users | ADR 55 decision 5 | pending | no | installation | API, live | No doc names a WebAuthn relying party ID. It can follow from the public URL. |
| Trusted-header sign-in, its proxy list, its header names | ADR 55 decision 6 | pending | no | installation | static | Off by default. A wrong proxy list through the API lets anyone sign in as anyone. Same risk class as the backup target. |
| Break-glass mode (1, 2 or 3) | ADR 57 decisions 1 to 3 | pending | no | installation | static at first boot | ADR 57 decision 6 gives it an API. A change after first boot is open in RFC 21. |
| Break-glass mode 2 public key | ADR 57 decision 1 | pending | no (public key) | installation | static | |
| Bootstrap admin token for break-glass mode 1 | ADR 57 decision 4 | pending | yes | installation | static | ADR 57 decision 6 excepts it from the API. |

### Tokens and workload identity

| Setting | Planned in | State | Secret | Scope | Layer | Note |
| --- | --- | --- | --- | --- | --- | --- |
| Maximum access-token lifetime | ADR 53 decision 2, RFC 20 | pending | no | installation, and a namespace can make it stricter | API, live | Lead: 1 year. |
| Inactivity period | ADR 53 decisions 4 and 6 | pending | no | as above | API, live | Default 90 days (PCI DSS 8.2.6). An administrator can set any value, also no limit (owner, 2026-10-13). |
| Inactivity disable on or off, for tokens, user accounts and break-glass | ADR 53 decisions 5 and 6 | pending | no | as above | API, live | Three switches. A change writes an audit entry. |
| Expiry warning lead times | RFC 20 open question | lead proposal | no | installation | API, live | Lead: 14 and 3 days. Needs SMTP. |
| JWT or OIDC issuers for workload identity | ADR 54 decision 3 | pending | no | installation, with trust rules per namespace | API, live | |
| mTLS trust bundle (SPIFFE) | ADR 54 decision 4 | pending | no | installation | API, live | A certificate bundle, not a secret. |
| mTLS header from a proxy, with its proxy list | RFC 20 open question | lead proposal | no | installation | static | Same risk as trusted-header sign-in. |
| Lifetime of an exchanged token | RFC 20 open question | lead proposal | no | installation | API, live | Lead: 1 hour. |

### Audit and logs

| Setting | Planned in | State | Secret | Scope | Layer | Note |
| --- | --- | --- | --- | --- | --- | --- |
| Audit log on or off (`--audit`) | ADR 60 decision 2 | accepted | no | installation | static | Turning the audit log off through the API lets an attacker hide the next actions. Proposal: static, or API with a fresh MFA check. Open question. |
| Audit sinks (`--audit-sink`) | ADR 60 decisions 13 and 15 | accepted | options can be | installation | static, API later with an allowlist | "A later ADR can make sinks configurable through the API at run time." See "The backup target". |
| Log sinks (`--log-sink`) | ADR 60 decision 14 | accepted | options can be | installation | static, API later with an allowlist | |
| Sink secrets (webhook HMAC key, OTLP header) | ADR 60 decision 15 | accepted | yes | installation | static | Environment or file only. |
| Hash chain (`--audit-chain`) | ADR 60 decision 17 | accepted | no | installation | API, live | A change writes an entry. |
| Signed checkpoints (`--audit-checkpoints`) | ADR 60 decision 18 | accepted | no | installation | API, live | Needs the chain. |
| Signing key (`DUSTPAN_AUDIT_SIGNING_KEY_FILE`) | ADR 60 decision 18 | accepted | yes | installation | static | ADR 23 decision 1 rules. |
| Trusted proxies for the source IP | ADR 60 decision 21, RFC 22 | open | no | installation | static | ADR 27 decision 2: the server never trusts `X-Forwarded-*` today. |

### Retention

| Setting | Planned in | State | Secret | Scope | Layer | Note |
| --- | --- | --- | --- | --- | --- | --- |
| Audit retention | ADR 60 decision 21, RFC 22 | open | no | installation | API, live | PCI DSS 10.5.1: 12 months. Waits on the owner. |
| Soft-delete retention of units | ADR 47 decision 2 | accepted | no | installation | API, live | Default 30 days. |
| State version history retention | ADR 28 decision 8, RFC 18 | deferred | no | installation | API, live | "A later ADR decides them". |
| Backup schedule and retention | ADR 30, RFC 8 | deferred | no | installation | API, live | |
| Instance row retention | ADR 48 decision 10 | accepted | no | installation | API, live | "The retention time is a setting, and the default is 24 hours." |
| Fair-use storage quota | owner, 2026-10-13 | proposal | no | installation, and each namespace | API, live | See "Fair-use limits". |
| Fair-use history retention | owner, 2026-10-13 | proposal | no | installation, and each namespace | API, live | See "Fair-use limits". It is a value for the state version history retention row above. |
| Fair-use API request rate | owner, 2026-10-13 | proposal | no | installation, for each token and each namespace | API, live | See "Fair-use limits". It is a value for the rate limit row below. |

### Network and security

| Setting | Planned in | State | Secret | Scope | Layer | Note |
| --- | --- | --- | --- | --- | --- | --- |
| IP allowlist feature switch | ADR 55 decision 8 | pending | no | installation | API, live | Off by default (owner, 2026-10-13). |
| Installation IP allowlist | ADR 55 decision 8 | pending | no | installation, with namespace and token lists too | API, live | Can lock everyone out, as the allowed origins can. Same recovery path. |
| Rate limits per token and per namespace | ADR 32 decision 1, RFC 34 | no RFC yet | no | installation default | API, live | Gap: "Rate limits go to a new RFC." |
| Stop all applies | RFC 6, RFC 27 | proposal | no | installation | API, live | Near the installation state (ADR 38). Maybe state, not a setting. |
| Customer-managed key wrapper | RFC 33 | proposal | credentials | installation, per namespace later | static | A bootstrap setting: the server needs it to open the data keys. |
| Clock-skew bound and lease times | ADR 48 decisions 2 and 7, RFC 60 | accepted | no | installation | static | Wrong values can let two instances serve one address. |

### Operations and appearance

| Setting | Planned in | State | Secret | Scope | Layer | Note |
| --- | --- | --- | --- | --- | --- | --- |
| Advertised address (`--advertise-address`) | ADR 48 decision 1 | accepted | no | instance | static | One value for each process. |
| Roles to run (`--role`) | RFC 7 | proposal | no | instance | static | Bootstrap. |
| Migrate at start (`--migrate=never`) | ADR 33 | follow-up | no | instance | static | Bootstrap. |
| Theme: product name, logos, fonts, tokens | RFC 9 | proposal | no | open: "one theme for each server, or one for each namespace?" | API, live | In the console `/config.json` today. RFC 9: "A theme is runtime config". |
| Product name in server text | RFC 9 | proposal | no | installation | API, live | |

### Gaps that no doc covers

This RFC lists them and does not design them:

- **SMTP and email.** ADR 53 (pending) needs it for the token-expiry email,
  and leaves open "where the SMTP settings live". MFA recovery and invites
  can need it too. The password is a secret.
- **The fields of an OIDC, SAML or LDAP provider.** ADR 55 (pending) names
  the methods, not the fields.
- **Rate limits.** ADR 32 sends them to a new RFC, which does not exist.
- **A WebAuthn relying party ID, a session cookie domain, a default role for
  new users.** No doc names them.

## Precedence

**Owner direction, relayed by the product manager on 2026-10-13:** "Config
file values ALWAYS override the UI/DB values." So a value from the file
always wins over the database, and the database wins over the built-in
default. This section keeps that wording and decides nothing more. Only the
points below are open.

The server has no file today, only flags and environment variables. Where do
they go?

- **Option A. One static layer.** A flag, an environment variable and the
  file all count as the static layer. Each of them wins over the database
  and locks the setting. Inside the static layer: flag, then environment,
  then file, as `internal/cli` already ranks a flag over its variable.
- **Option B. Only the file locks.** Flags and environment variables become
  defaults that the database can override. This breaks every installation
  that sets a flag today and expects it to hold.
- **Option C. Drop flags for non-bootstrap settings.** Simple, but it breaks
  each existing install and each container that sets environment variables.

**Recommendation: option A.** It keeps every current install working and it
matches the owner direction: a value that the operator wrote outside the API
always wins.

Two more rules follow from option A:

- When the operator deletes a key from the file and the server reloads, the
  database value becomes the effective value again, and the setting is no
  longer locked.
- An environment variable and a flag never change while a process runs. So
  a setting that comes from one of them changes only at a restart, even
  when the setting is live. Only the file reloads.

## The configuration file

- **Option A. One JSON file.** `--config` (`DUSTPAN_CONFIG`) names it. JSON
  needs only the standard library. A Kubernetes ConfigMap can hold it.
- **Option B. One YAML file.** Operators and Helm charts use YAML more. It
  adds a dependency, which needs the owner's approval (`CLAUDE.md`).
- **Option C. A directory with one file for each key.** `--config-dir`
  names it, and each file name is a setting key, for example
  `log.level`. A ConfigMap or a Secret mounted as a volume makes exactly
  this layout. It needs no parser, and a secret can come from a Secret mount
  next to the plain values.

**Recommendation: A and C, with one key set.** JSON for an operator who
writes one file, the directory for Kubernetes. Both feed the same static
layer. When both set a key, the server refuses to start, and a reload with
that conflict fails. Keep YAML as an open question.

The file can also hold bootstrap settings, so one file can configure the
whole server. A reload never applies a new bootstrap value. It answers
"restart required" for it (see "Live or restart").

## Reload trigger

Facts about Kubernetes that the design must meet. These come from the
Kubernetes documentation, and the RFC must check them again before an ADR:

- The kubelet updates a ConfigMap or Secret volume through an atomic swap of
  a symbolic link (`..data`). A watch on the file itself can miss it,
  because the file is not written. The name points to a new file.
- The update can arrive a minute or more after the ConfigMap changes, from
  the kubelet sync period and its cache.
- A volume mounted with `subPath` and an environment variable from a
  ConfigMap never update. Only a restart picks up the change.

Options:

- **Option A. SIGHUP only.** The operator, or a sidecar, sends the signal.
  No dependency. Kubernetes sends no signal on a ConfigMap change, so it
  needs a sidecar or a restart there.
- **Option B. fsnotify.** It reacts in milliseconds. It adds a dependency
  (owner approval). It must watch the directory, not the file, because of
  the symbolic link swap. It does not work on some network file systems.
- **Option C. Poll the content.** Every N seconds (proposal: 10), the
  server reads the file or directory and compares a SHA-256 of its content
  with the last one. It needs only the standard library. It sees the symbolic
  link swap, because it reads through the link. The delay is at most N
  seconds after the file changes, and the kubelet delay comes first.
- **Option D. SIGHUP and poll.** The signal reloads at once. The poll
  catches what no one signals.

**Recommendation: option D, with no fsnotify.** A reload reads the whole
file or directory, never one key, so a half-written change cannot apply.
ADR 60 decision 12 already uses SIGHUP to open the `file` log sink again.
One SIGHUP does both. The settings reference documents the `subPath` trap.

## Validation

- A reload parses the whole static layer and checks each key against the
  declared settings: the type, the range, and the rules between settings,
  for example that `--audit-checkpoints` needs `--audit-chain`.
- An unknown key fails the reload, so a typo never passes in silence.
- **An invalid reload keeps the old configuration.** The server writes one
  error log line that names the key and the rule. It does not write the
  value, because the value can be a secret (ADR 32 decision 7).
- Metrics: `dustpan_config_reloads_total` with the label `result`
  (`success` or `failure`), and the gauge `dustpan_config_last_reload_success`
  (1 or 0), so an operator can alert on it. ADR 32 decision 2 counts metric
  families. Two more is in that budget.
- At start, an invalid file stops the start. There is no old
  configuration to keep.
- The API checks a database write with the same rules before it stores
  anything. The answer is 400 with the key and the rule.
- A database value that the binary cannot read, for example after a
  downgrade, does not stop the start. The server uses the default, logs a
  warning, and the API marks the setting `invalid`.

## Live or restart

Each declared setting says whether it is live or a restart setting. The
inventory tables give a first proposal.

- A live setting takes effect on each instance after the change reaches it
  (see "Instances").
- A restart setting never changes in silence. When its stored or file value
  differs from the value that the process runs with, the API answers
  `restartRequired: true` for it, and the console says so. The server also
  writes a warning log line at each reload.
- A live setting takes effect for new work only. A running update keeps the
  delta cutoff and the journaling mode that it started with. A request
  already in flight keeps its size limit.

## API shape

ADR 17 and the owner direction on RFC 59 apply: the console uses the same
endpoints as every other client. RFC 59 leaves the prefix open
(`/api/dustpan/v1` in the owner direction). The paths below use
`/api/server/` because the other installation routes are there today. The
paths follow RFC 59 when it is decided.

- `GET /api/server/settings`: every declared setting, bootstrap settings
  included, with its effective value and source. So an operator sees the
  whole effective configuration on one screen.
- `GET /api/server/settings/{key}`: one setting.
- `PATCH /api/server/settings`: changes one or more database values in one
  transaction. All of them change, or none do.
- `DELETE /api/server/settings/{key}`: deletes the database value. The
  effective value goes back to the default, or to the static value.

One setting in an answer:

```json
{
  "key": "log.level",
  "value": "debug",
  "default": "info",
  "source": "file",
  "locked": true,
  "lockedBy": "file key log.level in /etc/opendustpan/config.json",
  "apply": "live",
  "restartRequired": false,
  "scope": "installation",
  "secret": false
}
```

- `source` is `flag`, `env`, `file`, `db` or `default`. The brief names
  three values (`file`, `db`, `default`). The two more tell an operator
  which flag or variable to change. Option: report all static sources as
  `file`, and give the detail only in `lockedBy`.
- `locked` is true when `source` is `flag`, `env` or `file`. A `PATCH` or
  `DELETE` of a locked key answers 409 with a message that names the key
  and its static source. It changes no key.
- `lockedBy` is the text that the console shows next to the control. The
  console rules need it: a setting that a user cannot change stays visible
  with the reason (RFC 52), and color never carries meaning alone (ADR 58
  decision 9, pending in #469). So grey alone is not enough. The console
  shows the text too.
- A bootstrap setting is always `locked: true` and `apply: "restart"`.
- A change carries `If-Match` with the `ETag` of the settings revision, so
  two administrators cannot overwrite each other. A stale revision answers
  412.
- The JSON and error shapes follow RFC 59 when it is decided. Until then,
  they follow the other routes of `api/server`.

### Actions

ADR 17 asks for one action for each operation, on an object. The object is
`@server`, as for the other installation routes.

- **Option A.** `server:read-settings` and `server:write-settings`. They
  match `server:backup`, `server:observe` and `server:read-audit`.
- **Option B.** Names in the style of the other products that the owner
  asked RFC 59 to follow, for example `settings:read` and
  `settings:update`.

**Recommendation: option A** until RFC 59 decides the action names. Then
rename both together with the other `server:` actions. A change passes the
policy check of ADR 17. ADR 56 (pending in #467) asks for a fresh MFA check
for some actions. An open question asks which settings need it.

### Secrets in settings

Some planned settings hold a secret: the SMTP password, the OIDC client
secret, the HMAC key of a webhook sink, an OTLP header.

- The API never answers a secret value. It answers `"secret": true`,
  `"set": true` or `false`, and the source. The log, the metrics and the
  audit entry never hold it (ADR 32 decision 7, ADR 60 decision 16).
- In the database, the server seals a secret value with a data key that the
  key-encryption key wraps (ADR 23), under the scope `@server`. The backup
  holds only the sealed value. A restore needs the same key (ADR 30
  decision 7).
- The static layer can set a secret: from an environment variable, or from
  a file that a `..._FILE` key names, never from a flag (ADR 23 decision 1).
  In the directory form of the file, the secret is one file in a mounted
  Kubernetes Secret.
- A `PATCH` can set or replace a secret. It can never read it back.

### Audit

- Each database change writes one audit entry of the kind `change` in the
  scope `@server` (ADR 60 decisions 4 and 7). One `PATCH` of three keys
  writes one entry.
- **Open: what the entry holds.** The fields of ADR 60 decision 5 have no
  place for a value.
  - Option A: the names of the changed keys only.
  - Option B: the names and, for a setting that is not a secret, the old
    and the new value, in a new optional field.

  Recommendation: option B. An auditor asks what changed, not only that
  something changed. A secret key gets only its name.
- A reload of the file is not a request, so ADR 60 decision 3 does not
  cover it. Proposal: a reload that changes an effective value writes an
  entry in `@server` with the channel `config-file`, the instance ID as the
  subject, and the changed key names. Values follow the rule above.

## Lock-out and recovery

Two settings can lock every user out when a change through the API is
wrong:

- **Allowed origins.** The console then cannot call the API.
- **Public URL.** The CLI then gets upload URLs and discovery documents for
  a host that does not answer.

Recovery path: the operator sets the key in the file, or in a flag or an
environment variable. The static value wins over the database (option A of
"Precedence"). A reload of the file fixes it at once. A flag or an
environment variable fixes it after a restart. The API stays reachable with
a bootstrap admin token and `curl` during all this, because neither setting
blocks a same-origin request with a bearer token. The settings reference
documents this path next to both settings.

Options to make the mistake less likely:

- **Option A.** Only the recovery path above.
- **Option B.** The API refuses a change of `allowedOrigins` that deletes
  the origin of the request that makes it.
- **Option C.** Both settings are file-only, never in the database.

Recommendation: A and B. Option C goes against the owner ask for these two
settings.

## The backup target

ADR 30 decision 9 is a security decision: a token must not be able to send
the data of the installation to another place. A database value set through
the API reopens that path.

- **Option A. Static only (the default of this RFC).** The flag, the
  environment variable or the file sets it. The file can reload it. The API
  shows it read-only.
- **Option B. API-settable with a guard.** The file sets an allowlist of
  targets. The API can choose only a target in that list. The change needs
  its own action, for example `server:set-backup-target`, and a fresh MFA
  check (ADR 56, pending). The risk stays: a stolen administrator session
  can point backups at any target in the list.

**Recommendation: option A.** Option B only when an operator needs it, and
then as a new ADR that changes ADR 30 decision 9.

The same risk applies to an audit sink or a log sink that the API can set
(ADR 60 decision 15 allows a later ADR to do that): a webhook sink can send
the audit log to any URL. The same guard applies: an allowlist from the file
and its own action.

## TLS files

ADR 27 reads the certificate and the key once at start. cert-manager and
ACME clients replace the files on disk.

- **Option A.** No change. A new certificate needs a restart.
- **Option B.** The paths stay static. A reload (SIGHUP or the poll) reads
  the files again and uses the new pair for new connections, through
  `tls.Config.GetCertificate`. A pair that does not load keeps the old pair,
  logs an error and counts a failure, as for the configuration file.

Recommendation: option B. It needs only the standard library and changes
ADR 27 decision 1 only in when the server reads the files.

## Instances

ADR 48 runs more than one instance on one database. Each instance must pick
up a database change.

- **Option A. Poll a revision.** One row holds a revision number that each
  change increments in the same transaction. Each instance reads it every
  2 seconds, as `pkg/maintenance` reads the installation state, and loads
  the settings when it changed. The delay is at most the poll interval plus
  the time of one read. It works on SQLite and PostgreSQL.
- **Option B. PostgreSQL `LISTEN`/`NOTIFY`.** Faster, with no polls. SQLite
  has no such feature, so option A is still needed. A dropped connection
  loses notifications, so a poll is needed as a safety net too.
- **Option C. Ask each instance (ADR 48).** The instance that writes calls
  every registered instance. A live instance that does not answer delays
  the change, or the change applies only in part.

**Recommendation: option A, with a bound of 2 seconds,** the same as the
installation state. A change answers the new revision. Each instance writes
the revision that it applied, and the hash of its static layer, in its
registry row (ADR 48). Then:

- `GET /api/server/settings` can say how many instances run an older
  revision.
- The API can warn when two instances have different static layers. With
  option A of "Precedence", a file on instance A and no file on instance B
  give two effective values. The precedence is per instance. The warning
  makes the difference visible.

A setting with the scope _instance_ (large bodies in flight) is never in the
database layer, because one row cannot hold a value for each process. It
stays static and reports its source per instance. An open question asks
whether the database should hold a value for each instance.

## Backup and restore

- The settings table goes into `backup.Tables`. The coverage check of ADR 42
  decision 5 fails an export when the column list and the schema differ.
- A secret value is in the backup only as a sealed value (see "Secrets in
  settings").
- A restore replaces the database layer, as it replaces every table. The
  static layer still wins after the restore. The revision changes, so each
  instance loads the restored values.
- **Open:** a restore into another installation brings the public URL and
  the allowed origins of the old one. Options: restore every setting
  (simple, and the static layer can fix it), or skip a fixed list of keys
  that name the place of the installation.

## Fair-use limits

Owner decision 4 of "The hosted service": fair-use limits on the stored
bytes, the history kept and the API request rate. They are installation
settings like every other row of this RFC. The open-source server has them
too, so an operator of any installation can set them (owner direction point
3). The proposed default for each one is "no limit", so an installation
that sets nothing works as it does today.

One rule applies to all three. **A limit never stops work that is in
progress.** A Pulumi update or an OpenTofu run that started must be able to
save its state. When the server refuses a checkpoint or a state write after
the tool changed real infrastructure, the state no longer matches the
infrastructure. So a limit refuses the start of new work, never its end.

### Storage quota

- **Settings.** `quota.storageBytes` for the installation, and the same key
  for each namespace. A namespace value can only be lower than the
  installation value, as a unit switch narrows the namespace switch (ADR 44
  decision 6). `0` means no limit.
- **What the server counts.** The stored bytes of the blobs that the rows of
  a namespace name: state versions, checkpoints, journal entries and their
  history. The rows already keep a size for each blob (the `size` and
  `blob_size` columns). A soft-deleted unit counts until its final delete
  (ADR 47). Backups do not count, because they are in the backup target,
  not in the blob store. Open: does the server keep a running total in a
  row, or sum the sizes when it checks?
- **At 80 percent.** The server warns: a log line, a metric
  `dustpan_quota_used_ratio` with no namespace label (ADR 32 decision 5),
  and a field in the namespace answer. Open: an event (RFC 23) or an email
  (a gap: SMTP).
- **At the limit.** The server refuses the start of new work in that
  namespace: the start of a Pulumi update, an OpenTofu lock, and a state
  upload with no lock. A request that belongs to work that is running (a
  checkpoint, a journal entry, the final state write) always passes. A read
  always passes. A delete always passes, because it frees space after the
  retention period.
- **Status.** Open. Options:
  - 403 with a message that names the quota and the command that helps.
  - 409, as a conflict with the namespace state.
  - 507 Insufficient Storage. It names the cause, but some clients may not
    show its message.

  Recommendation: decide after `make e2e` shows what the Pulumi CLI and
  OpenTofu print for each status. The CLAUDE.md rule applies: any endpoint
  change is checked against the real CLI.

### History retention

- **Settings.** `retention.stateVersions.keep` (the number of versions to
  keep for each unit) and `retention.stateVersions.maxAgeDays`, for the
  installation and for each namespace. `0` means keep every version, as ADR
  28 decision 8 does today. The same rule covers Pulumi checkpoints and
  update history. Open: one pair of keys for both tools, or one pair for
  each tool.
- **What the server counts.** The versions of each unit, oldest first.
- **At the limit.** The server prunes. A scheduled job deletes the versions
  over the count and older than the age. It never deletes:
  - the current version of a unit,
  - a version that a lock, a running update or a running restore names,
  - a blob that a running backup can still copy (ADR 28 decision 8, ADR
    30).

  The rows go first. The blobs become orphans for the orphan sweep. Each
  prune writes one audit entry for each unit in the scope `@server` or the
  namespace (ADR 60). The job runs on one instance at a time. Open: how it
  chooses that instance (RFC 36).
- **No refusal.** History retention never refuses a write.
- **Open.** A version that a person pinned or tagged. ADR 28 decision 8 asks
  a later ADR to decide the prune "with measured sizes". This RFC gives the
  setting, not that ADR.

### API request rate

- **Settings.** `rateLimit.requestsPerSecond` and `rateLimit.burst`, for
  each token and for each namespace, with installation values. `0` means no
  limit. ADR 32 decision 1 sends rate limits to a new RFC, which does not
  exist yet. This RFC gives only the settings and the answer at the limit.
- **What the server counts.** Requests in a token bucket, one bucket for
  each token and one for each namespace. Each instance keeps its own
  buckets in memory. So with N instances, the real rate can be up to N
  times the setting. Open options: divide the setting by the count of live
  instances in the registry (ADR 48), or share a counter in the database
  (one write for each request, which costs too much on the hot path).
  Recommendation: divide by the count of live instances.
- **At the limit.** 429 with `Retry-After`. The Pulumi CLI and OpenTofu
  may retry a 429 on their own. `make e2e` must show what each one does
  before this ships.
- **Exempt.** The calls of an update or a run that is in progress (lease
  renewal, checkpoints, events, the final state write) use a separate
  bucket that the namespace limit does not empty, by the rule above. The
  probes and the metrics route are exempt.
- **Open.** A limit for each IP address before sign-in, for sign-in
  attempts (research for RFC 21 lists an account lockout).

### The no-gate check for these limits

Each of the three passes "No setting gates a feature":

- None of them reads a license, an edition, a plan or a "hosted" flag.
- Each one exists in every build, with the default "no limit".
- Each one bounds an amount (bytes, versions, days, requests). None of them
  turns a feature off. At the limit, every feature still works for work in
  progress, reads and deletes.

## No setting gates a feature

Owner direction point 3 and point 4: settings set limits and defaults only.
This section checks each inventory row against that rule.

**The check.** A setting fails the check when it does one of these:

- it reads a license, an edition, a plan or a "hosted" flag,
- it makes a feature exist in one build or one deployment and not in
  another,
- its only purpose is to keep a feature away from the administrator of an
  installation.

**Result for the inventory: no row fails.**

- No row reads a license, an edition, a plan or a "hosted" flag. No such
  value exists in the code or in a plan.
- These rows are switches: MFA on or off, require MFA, passkey sign-in,
  trusted-header sign-in, the IP allowlist feature, revoke on removal, the
  audit log, the hash chain, signed checkpoints, Pulumi journaling off, and
  an empty backup target. Each one is a choice of the administrator of the
  installation, with a default. Each one exists in every build. So each
  one is a default, not a gate.
- Size limits, lifetimes, retention times and rate limits are limits.

**Rules that keep the check true later:**

- No setting has a name or a value that names an edition, a plan or the
  hosted service.
- A hosted limit is a value of an ordinary setting, set in the static layer
  of the hosted installation, for example the largest OpenTofu state.
- The static layer of a hosted installation can lock a limit or a default.
  It must not lock a feature off for the customer, for example the audit
  log, MFA or SSO. Open question: is this a rule for the hosted service
  only, or does the server enforce it with a list of switches that a lock
  can set only to on?
- The test of a later ADR can check the first rule: it fails when a declared
  setting key holds a word from a short list (`edition`, `plan`, `hosted`,
  `license`, `tier`).

## Multi-tenancy

The aim is to keep a hosted service with many tenants possible. This RFC
adds no tenant model. A separate RFC must define the boundary between the
installation and a tenant, and name the term. `GLOSSARY.md` lists "tenant"
under _Avoid_ for a namespace today, because the namespace is the unit of
tenancy. A hosted tenant (one customer with many namespaces) is a different
concept that does not exist yet.

Owner direction point 5 gives two options for the separate RFC:

- **Option A. One installation for each customer.** Each customer gets its
  own small installation: its own database, blob store and key-encryption
  key. It needs no tenant model in the server. State, secrets and the key
  stay apart for each customer. The settings of this RFC carry the hosted
  limits and defaults (point 4). The cost is one set of processes and one
  database for each customer, and an operations layer outside the server
  that creates and upgrades installations.
- **Option B. One shared installation with a tenant layer above
  namespaces.** Lower cost for each customer. It needs a tenant model, a
  tenant scope for settings, secrets and the audit log, and a tenant check
  on every route. A bug in one of them can let one customer read the data
  of another. The key-encryption key is shared, unless RFC 33 gives each
  tenant its own wrapper.

**Lead recommendation: option A first.** It needs no tenant model. It keeps
state, secrets and the key-encryption key apart for each customer. It fits
owner direction points 1 to 4: the hosted service runs the same binary, and
its limits are settings. The shared tenant layer of option B stays a later
RFC. The project lead decides (point 5).

With option A, this RFC needs nothing more. The rest of this section keeps
option B open. This design must not block these:

- **A scope in the key.** Each database row holds a scope and a key. The
  scope is `@server` now. A later tenant scope, and later namespace
  settings, can use the same table with no new table. Installation
  settings and namespace settings never share a key.
- **Layers in a list.** Precedence is a list of layers: default, database,
  static. A tenant layer can go between the installation and the
  namespace later, with the same `locked` rule: the operator of a hosted
  service locks a value for every tenant.
- **No global variables.** Each service reads a setting through a small
  port with a `context.Context`, never through a package variable. A later
  tenant ID in the context can then choose the layer.
- **Actions on an object.** The actions take an object (`@server`). A tenant
  object can get the same actions later (ADR 17).
- **Secrets sealed per scope.** The data key of `@server` is one scope. A
  tenant can get its own data key and, later, its own wrapper (RFC 33).
- **An audit scope.** `@server` is a scope of its own (ADR 60 decision 7).
  A tenant scope fits the same model.
- **Limits that a tenant can narrow.** Size limits and rate limits are the
  first settings that a hosted service needs per tenant. A tenant layer
  narrows the installation value, as a unit switch narrows the namespace
  switch (ADR 44 decision 6).

## Where the code goes

A proposal, so that the ports have owners:

- `pkg/settings`: the declared settings (key, type, default, scope, live or
  restart, secret, bootstrap), the precedence, and the validation. No
  `net/http`, no `database/sql` (depguard).
- A `settings.Store` port for the database layer, with a conformance suite
  in `settingstest`, and SQL adapters with a migration in both stores.
- A `settings.Source` port for the static layer. The built-in adapter reads
  the JSON file or the directory. An SDK user can pass another source to
  `dustpan.NewServer` (ADR 35), for example one that reads a secret manager.
- `cmd/opendustpan` keeps the flags. The declaration in
  `internal/cli/settings.go` and the declaration in `pkg/settings` must not
  drift. One test compares them.
- `internal/api/server` serves the routes.

## Roadmap

This RFC adds a row to "Later": it waits on the shape of the installation API
(RFC 59).

Proposal for the owner: move the database layer, the read route and the
change route to milestone 4. The identity ADRs pending in #467 add
installation settings (MFA policy, token lifetimes, OIDC issuers, IP
allowlists) that ADR 17 says need an API. Without this RFC, each of them
gets its own route and its own storage. Moving an RFC to a milestone needs
the owner's approval.

## Open questions

1. Which `source` values does the API answer: three, or five (see "API
   shape")?
2. JSON, a directory, or YAML for the file (YAML needs a new dependency)?
3. Does the audit entry of a change hold the old and the new value of a
   setting that is not a secret?
4. Which setting changes need a fresh MFA check (ADR 56, pending)?
5. Does a restore into another installation skip the keys that name its
   place?
6. Does the database hold a value for each instance, for settings like
   large bodies in flight?
7. What is the poll interval of the file (proposal: 10 seconds) and of the
   revision (proposal: 2 seconds)?
8. Does the API make audit sinks and log sinks settable, with an allowlist
   from the file (ADR 60 decision 15)?
9. Can the API turn the audit log off (`--audit`), or does that switch
   stay static so an attacker with an administrator session cannot hide
   the next actions?
10. Which RFC defines the boundary between the installation and a hosted
    tenant? The lead recommends one installation for each customer first.
11. Does the server enforce that a lock in the static layer never turns a
    feature off (see "No setting gates a feature")?
12. Which status does a namespace over its storage quota answer: 403, 409
    or 507? This waits on `make e2e` with both CLIs.
13. Does history retention use one pair of keys for both tools, or one
    pair for each tool?
14. How does a rate limit hold across instances: divided by the count of
    live instances, or a shared counter?

## Outcome

None yet.
