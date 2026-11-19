# QA round 16

Range: a39ce4470d33..4a78446d (tested at 4a78446d). Main had moved to bebc30fa
(34 commits). Before filing, the doc diffs on main were checked: none of the
findings was fixed there.

Method: local only. The server was built to a temporary path and run on
loopback ports with SQLite and with a local PostgreSQL 18 cluster. Five
parallel testers followed the guides step by step. No pulumi, tofu, helm,
MinIO or Garage was installed, so those steps were run with curl against the
documented routes, or were not run. No security testing was done.

## Issues filed (38): #3698 to #3735

Label `needs-triage` on each.

| Issue | Topic |
| --- | --- |
| 3698 | README quick start: first stack or workspace create fails with `placement.no-match` |
| 3699 | Guides disagree on how the first administrator gets a console session on `serve` |
| 3700 | Local password sign-in turned off with a token cannot be turned on again |
| 3701 | Helm minimal values give no admin token, but `serve` requires one |
| 3702 | Docs claim a cloud role works for S3; the adapter has no cloud identity |
| 3703 | Database down: 403 `access.ip-not-allowed` and logs with `"error":"error"` |
| 3704 | ldap-sign-in.md: nested YAML rejected; PATCH example lacks `Idempotency-Key` |
| 3705 | ldap-sign-in.md: loopback LDAP contradiction; TLS error reads as retry |
| 3706 | `--ldap-subject` and `--oidc-issuer` accept values the docs refuse |
| 3707 | Sign-in and `serve` start errors do not name flag or fix |
| 3708 | Client command errors: usage dump, broken text, no hint |
| 3709 | Errors name the wrong command (`run opendustpan backup again`) |
| 3710 | Error text carries internal ADR references |
| 3711 | configuration.md: key rule, missing secrets, reload text, help mismatches |
| 3712 | Static providers show empty values in the API |
| 3713 | local-users.md: password rule text, error texts, repeated paragraph |
| 3714 | passkeys docs differ from options, audit actions and dev behavior |
| 3715 | planner grant below the installation is accepted |
| 3716 | No built-in role holds six runner and job actions |
| 3717 | trust-rules-api.md: bootstrap token claim is wrong |
| 3718 | security-model.md sign-in and authorization sections are stale |
| 3719 | Stale "does not exist yet" text in README, install.md, sizing guide |
| 3720 | roles-and-grants.md: alias, revoke path, principal grants route |
| 3721 | settings.md and state-write-gates.md deny shipped features |
| 3722 | moving-units.md: name hold and import hold claims |
| 3723 | Repeated guide text and curl brackets without `-g` |
| 3724 | `opendustpan status` does not exist |
| 3725 | ip-allowlists.md: unknown namespace, stored value |
| 3726 | Runner and proxy-setting 400 answers do not name the field |
| 3727 | jobs.md behavior differs from text |
| 3728 | observe.md readiness body and metric families |
| 3729 | access-tokens.md error codes and revoke answer |
| 3730 | Matrix `accessToken`, context, collections, deleted-units mismatches |
| 3731 | publish-a-blueprint.md first draft |
| 3732 | backup-restore-upgrade.md step order, leftovers, upgrade output |
| 3733 | backup and restore CLI text for partial, unknown and interrupted backups |
| 3734 | `host create-temp-admin` during a restore; restore drops the temporary admin |
| 3735 | SQLite database files created with mode 0644 |

Related existing issues, not duplicated: #3574, #3049, #3424, #2921, #2199,
#2601 and #2604 (closed). Known issues #2276, #1589, #2408, #3541, #3542 and
#3544 were not recognized in any run.

## Coverage by area

### 1. Sign-in paths
Pages: local-users, console-sign-in, passkeys, ldap-sign-in, cas-sign-in,
trusted-header-sign-in, oidc-issuer, workload-identity, passkeys-api, the
sign-in rows of configuration.md.
Run: local user create, redeem, sign in, lock, unlock, password change, sign out;
LDAP and OIDC flags, environment and file settings with correct and wrong values;
allowed origins and CORS; static provider list; passkey option and error paths and
rate limits; issuer discovery, key rotation, trust-rule exchange errors.
Not run: a real LDAP bind, OIDC or CAS exchange, header sign-in; a successful
WebAuthn ceremony; every route that needs a console session with a fresh
sign-in (no way to get one on `serve`, see #3699). CAS and trusted-header
sign-in have no setting or route in the code; the guides say so.

### 2. Restart in the middle of a backup and a restore
SQLite and PostgreSQL. About 1.2 GB of blobs, 30 namespaces, about 140
workspaces with 10 state versions each. SIGTERM and SIGKILL during a backup, and
at four points of a restore; cancel; worker-only processes killed. Every case
recovered by itself, maintenance mode returned to `normal`, SQLite integrity
check passed, no staging schemas were left on PostgreSQL, and data matched the
backup. Findings are in #3732, #3733, #3734.
Not run: S3 target and blobs, `--force` with active runs, rolling deploy of two
instances, the mixed-version matrix.

### 3. Fresh install path
README, quickstart, install, dev-server, doctor, settings, kubernetes (template
reading only), s3-blobs (fake local endpoint and error text), configuration.md
(79 flags against `serve --help`, defaults against code). SQLite and
PostgreSQL 18 through `serve`, and `dev`. About 60 invalid start variants.
PostgreSQL 16 is refused with a clear message.
Not run: image and chart pulls (no network pulls), helm, compose build,
MinIO or Garage, `make demo`.

### 4. Remaining guide and reference pages
All 59 guides and the reference and explanation pages were read. Mechanical
checks over all of them: relative links and anchors, every `--flag` and
`DUSTPAN_*` name, backticked error codes, actions, metric names and setting
keys, and about 330 documented method and path pairs against a live server.
Deep, live checks on about 45 guide pages (access-review, access-tokens,
audit-log, deleted-units, drift-checks, host-recovery, ip-allowlists, jobs
reads, manual-locks, notification-destinations, observe, output-sharing,
principals-and-groups, private-registry, runner-pools, run-triggers,
saved-views, settings, state-versions, several-instances and others).
Pages changed in the range: backup-restore-upgrade, console-sign-in,
ip-allowlists, job-proxy-pool-settings, private-registry, quickstart,
runner-pools, code-rules, runner-api, all checked.
Partly checked (read and route and code check only): gate-config-api,
runner-api protocol sections, triggers-api, drift-api, jobs-api,
git-host-api, git-host-links-api, issuer-api, profiles-api, proxy-rules,
source-filter-api, journaling, launcher, typescript-client; soak, terragrunt,
slack-notifications, connect-github (no outside service or CLI).

### 5. Known issues
None of #2276, #1589, #2408, #3541, #3542, #3544 was recognized by number.

## Process note
One tester ran `serve --blob-store s3://bucket` with no endpoint while the
sandbox held AWS credentials in its environment. The S3 adapter sent a signed
request to the public S3 service, which answered 403. The credentials were not
printed. This broke the local-only rule; later S3 tests used a local fake
endpoint. A client that reads those variables will do the same on any host
that has them set.
