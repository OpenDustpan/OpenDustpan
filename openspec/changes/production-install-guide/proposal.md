---
version: 2
---

## Why

**Persona: operator.** Operator installs OpenDustpan for production from one docs page and serves developers with TLS, PostgreSQL, and backups on.

The user path this change must make work:

1. **Docs page.** Open production install docs page and prepare host with certificate files and PostgreSQL.
2. **Server start.** Install server bits from the linked install-from-artifacts page, then start server with production settings behind reverse proxy and confirm health.
3. **API.** Check backups on route ANY /api/server/backups with access token.
4. **CLI.** Run pulumi up and tofu apply against installed server through proxy.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| TLS certificate file missing or key unreadable, server does not start. | Server does not start. It reports the missing file path with no key material. Fix paths and permissions, then run the NEW docs start step again. | `the NEW docs start step` |
| PostgreSQL unreachable, state writes refused. | Server reports store unreachable with no secret in the text. Fix the NEW database address, then check ANY /api/server/backups with an access token. | `ANY /api/server/backups` |
| Missing proxy headers or untrusted sender, request refused. | Server refuses the request and names the missing header. Fix the NEW proxy example to send X-Forwarded-Proto and X-Forwarded-Host from an allowlisted address, then send ANY /api/server/backups again. | `ANY /api/server/backups` |
| Backup check fails, scheduled backup missing or corrupt. | Backup check reports failure and names the failing backup with error code `backup.corrupt`. Fix the NEW blob store and schedule, then run ANY /api/server/backups again. | `ANY /api/server/backups` |

Evidence for the claims above:

- Server binary entry lives in cmd/opendustpan. Proof: cmd/opendustpan
- Backup collection route exists on main. Proof: ANY /api/server/backups
- Backup domain exists on main. Proof: pkg/backup

## What Changes

- Production fixtures on one docs page with links to install-from-artifacts: TLS from files, reverse proxy example, PostgreSQL, blob store, backups on, and key file backup note.
- Persona path test that uses the shared runner from docs-commands-run-in-tests through pulumi up and tofu apply.

Out of scope:

- As-written command runner, owned by docs-commands-run-in-tests.
- Install from image and binary archives and the install-from-artifacts page, owned by release-artifacts-image-install-docs.
- Tagging or publishing a release.
- Automatic certificate issue or renewal.
- Certificate reload without restart; restart applies new files.
- High availability or multi process setup.
- Restore runbook, owned by key-encryption-key-rotation, beyond refusal of dev backup into production.

Depends on:

- docs-commands-run-in-tests
- release-artifacts-image-install-docs
- key-encryption-key-rotation
- `docs-commands-run-in-tests` (order of the milestone review)
- `key-encryption-key-rotation` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- Operator copies long shell blocks. Page keeps each command runnable as written and shows expected output.
- Operator may paste secrets into shell history. Page uses file references and warns against echo of secrets.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Real apply step helper exists for the tofu apply end of the persona path. (`pkg/dustpan/jobservice.go:846 (func tofuApplySteps)`)
- Restore of dev backup into production is refused on main. (`pkg/backup/restore_service_dev_test.go:100 (func TestRestoreRefusesDevBackupIntoProduction)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Which ADRs govern production install, TLS, reverse proxy, and backups? Pack lists no ADR text. This change adds no new server behavior beyond failed-start and refused-request guards already implied by the roadmap step. It states TLS from files, proxy trust from an allowlist, and backups on, and leaves decisions to named ADRs when the pack names them.
- How is overlap with docs-commands-run-in-tests and release-artifacts avoided? This change keeps only production fixtures: TLS files, proxy example, PostgreSQL and blob settings, backup schedule on. The shared runner from docs-commands-run-in-tests runs page commands as written. The install-from-artifacts page from release-artifacts-image-install-docs owns install from image and archives. This change links to that page and does not repeat it.
- How are backups and the key file handled across key-encryption-key rotation? Pack lists no ADR text. The page backs up the NEW key file copy together with the database and points to the restore rule owned by key-encryption-key-rotation. Restore detail stays in that change.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
