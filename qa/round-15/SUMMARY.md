# QA round 15: functional and robustness testing

Range: ce2265d36..a39ce4470d33. Local server only (built from main, SQLite, TLS, loopback).
Time: about 45 minutes of real paths. No secret, token or host name is in this file.

## Issues

New:
- #3541 ip-allowlists guide: the installation-list PATCH example answers 403 `mfa.fresh-step-required` with an access token.
- #3542 Runner pool create: every invalid field answers "runner request is not valid".
- #3544 Without `--public-url` the local-user policy shows `enabled: true`, but redeem and password sign-in answer 404 "no route at this path".

Seen again, already open (no new issue; comment added on #2408):
- #2276 No documented way to give the first grant (the bootstrap token and a temporary administrator both get `mfa.fresh-step-required`; `console-sign-in.md` line 207 says to use the bootstrap token).
- #1589 Restore of an unknown backup ID says "backup data is corrupt".
- #2408 Disk full: 500 with no cause, readiness stays ok (also #2344 for the `"error":"error"` log text).

## Coverage

| Area | Result |
| --- | --- |
| Build, quickstart (namespace, collection, placement) | Covered. Works as written. |
| Local users: set-up code, password rules, sign-in, lock after 10 failures, locks list, unlock | Covered with `--public-url`. Works as written. |
| Backup, backup check, restore, restore check-only | Covered. A changed table file is found by check and by restore. A namespace made after the backup is gone after restore. |
| Server stop during drain | Covered. The 10 s drain is by design. |
| Full disk (2 MiB tmpfs) | Covered. 500 with no cause, then recovery after more space with no restart. See #2408. |
| Temporary administrator (`host create-temp-admin`) | Covered. Message for a database in use is clear. The token cannot make grants. |
| Grants and access review | Partly. The access-review read works. Grant create could not be tested (#2276). |
| Runner pools and join tokens | Partly. Create, list, duplicate, join token, unknown pool. Runner join not tested (see below). |
| ip-allowlists guide | Partly. The invalid CIDR answer works. The valid PATCH is refused (#3541). Namespace list not tested (needs a console session). |

## Not covered

- Pulumi CLI and OpenTofu: the install was refused by the permission classifier (download and run of an installer script). I did not retry or work around it. No CLI-driven path (stacks, workspaces, locks, state, planner refusal with a real CLI) was run.
- Passkeys, LDAP, OIDC, CAS: no browser, no directory.
- Jobs and runners with a real runner: the runner binary is in another repository.
- Restart in the middle of a running backup or restore.
- PostgreSQL, S3, Kubernetes, high availability.
- Guide claims: about 5 of the 60 changed pages were checked against behavior (local-users, backup-restore-upgrade, runner-pools, ip-allowlists, host-recovery, settings in part). Not checked: the other pages, including ldap-sign-in, state-versions, terragrunt, private-registry, moving-units, principals-and-groups, audit-log and the reference pages.
