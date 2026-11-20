# QA round 17: functional and robustness testing

Range: 4a78446d11cb..4daa46bceabe2e730390dad333bc98f030786d8a. Binary built from 4daa46bce. Local server only (SQLite, filesystem blobs, local TLS). Pulumi CLI 3.150.0 and OpenTofu 1.9.0 installed and driven against it.

## Issues
- #3814 Restore refuses a backup with a damaged job row; `backup check` says complete.
- #3815 Damaged job: GET answers 500, cancel answer has zero timestamps, jobs guide silent.
- #3816 serve: --public-url, --listen port range and empty sqlite path errors miss the flag or mislead.

## Coverage
| Area | Result |
| --- | --- |
| Serve refusals, flag errors, plain HTTP refusal | Covered. Matches install guide text. #3816 for gaps. |
| SQLite file mode check and opt-out flag | Covered: db, -wal, -shm wide modes, flag warning per file, non-regular file, symlink, new file mode 0600 under umask 0. Matches docs. |
| Log and stderr escaping | Covered for flags, CLI args, request log (Go rejects raw control bytes in HTTP). No finding. |
| Service tokens end when maker loses right | Covered (grant revoke path): token revoked, 401 at use, audit entry as documented. Group, role change, disable paths not covered. |
| IP allowlists | Covered: installation setting (fresh sign-in rule), namespace list set/get, list leave-out, access/objects excluded, bootstrap exemption. Health and lock-out recovery not run. |
| First administrator, local users | Covered end to end. Matches guides. |
| Code rules audit | Last-site-administrator refusals and audit reason checked. Break-glass restore rule not run. Catalog test passes. |
| Cancel of damaged job | Covered, see #3815. |
| Backup, restore, doctor | Covered, see #3814. doctor checks match the page. |
| Quickstart with Pulumi and OpenTofu | Covered (SQLite, TLS). Plugin download blocked by network, program without resources used. |
| Restarts | kill -9 and restart: data and sessions intact. Second server on one database refused with a clear message. |

## Not covered
PostgreSQL, S3, compose and Helm paths; LDAP, OIDC, passkeys; Git host webhooks; runner and job execution; load limits and disk full; most of the 51 changed pages (only the pages for the areas above were checked claim by claim: install, ip-allowlists, first-administrator, local-users, access-tokens, jobs, backup-restore-upgrade, doctor, configuration database flags, code-rules last administrator).

## Notes
- One call started a local server with the AWS variables set in the shell (I had not unset them). The server cleared them at start; the server used only local stores and made no network request.
