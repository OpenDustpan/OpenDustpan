# 253. Dev data does not reach a production installation

Status: accepted · 2026-10-19. The project owner decided it. Applies ADR 250 to the restore of a backup from a dev installation. Extends ADR 252 decision 7.

## Context

`opendustpan dev` signs in through the stored person `dev:admin`, which holds the site-administrator role (ADR 252). A backup leaves the installation table out. A restore keeps the live installation kind. So an operator can restore a backup of a dev installation into a production installation. The restore then brings `dev:admin`, its grant, and maybe its sessions and tokens.

ADR 252 decision 7 closes the sign-in side. A production server refuses to authenticate a principal with the prefix `dev:`. That is one layer. ADR 250 requires more layers, and each layer must stop the attack alone. This ADR adds two layers before the data arrives. One layer is a refusal at the restore. The other is a strip of the staged data.

No OpenDustpan installation exists yet outside CI. So no backup without an installation kind needs handling.

## Decision

1. **A backup records the kind.** The manifest has the key `installationKind`. Its value is `dev` or `production`. The key is required. The backup export reads the kind from its own database through `installation.KindReader`. It does not accept a kind from the caller.
2. **The manifest format number rises** from 2 to 3. The existing format check refuses a manifest of format 2. A manifest of format 3 with no kind, or with an unknown value, is not valid. The restore refuses it with `backup.ErrManifestKindInvalid`. There is no conversion and no compatibility path.
3. **Layer 1: refuse by default.** A production installation refuses to restore a backup of the kind `dev`. The error is `backup.ErrDevBackupRefused`. One exception exists: the server started with the flag in decision 5. Three independent checks apply:
   - The parse of the manifest refuses a missing or unknown kind.
   - The restore service compares the kind of the backup with the kind of the live installation. It does this before it enters maintenance mode and before it takes the backup of the live data.
   - The stage step of the store adapter compares them again (`backup.StageKindChecker`). It uses the live kind that it reads from its own database.

   The check-only restore runs the same checks. So it reports the refusal and changes nothing. A dev installation restores a dev backup as before. This ADR does not change the restore of a production backup into a dev installation.
4. **The refusal text** says what failed in plain words, and then names the flag in code quotes: "This installation is a production installation. The backup comes from a dev installation. Restoring a dev backup into a production installation is not recommended. To allow it, start the server with `--unsafe-allow-restoring-dev-installation-backups-into-production`."
5. **The flag.** The flag is `--unsafe-allow-restoring-dev-installation-backups-into-production` (`backup.DevBackupFlag`). It follows the pattern of the other long override flags (ADR 124). It is a flag of the command line only. It has no environment variable, no entry in the configuration file, and no setting in the API. The settings layers of ADR 63 never set it. The server writes one warning to the log at each start with the flag. The audit entry of a restore records whether the flag was on.
6. **Layer 2: strip at the restore.** When the flag allows the restore, `backup.DevStripper` removes the dev data from the staged data. It runs after the stage step and before the apply step. The apply step is the one atomic swap of the restore. So the live data never holds a dev principal from the backup. A failure at any point discards the staging area and leaves the live data unchanged. The restore fails with `backup.ErrDevStripIncomplete`. The strip removes these classes:
   - principals with a subject that starts with `dev:`, in any letter case (the authentication refuses every case),
   - their grants, group memberships, sessions, access tokens and open sign-in requests,
   - sign-in providers with the reserved provider ID `dev`,
   - dangling data. This is grants, group memberships, sessions and tokens that name a principal that does not exist. It is also rows that name a subject with the prefix and have no principal row.

   The strip does not trust the foreign keys of the staged data. The data may already be wrong in the backup. The strip does not remove audit entries, so the audit chain stays whole. A column in another table can store the subject of a dev principal as text, for example "created by". That text stays as history and grants no authority. A coverage test lists every column that refers to a principal. It says if the strip removes the column or keeps it. It fails when a new column appears. After the strip, a check step proves that the staged data holds no principal with the prefix and no row that names one. A restore by a caller without `server:restore-access` keeps the live access tables. So no dev principal arrives through them. Layer 1 applies to that restore too.
7. **Record each removal.** The strip logs one warning for each class that removed rows, with the count and never a token or a session ID. After the swap, the restore service writes the audit entries that survive the swap. It writes one entry for each removed principal, with the counts of the rows that went with it. It writes one entry for each class that has no principal (providers and dangling data).
8. **Warn the user.** The text is: "This restore removes the dev users and their grants. Things that depend on them may break. Restoring a dev backup into a production installation is not recommended." The answer to `POST /api/server/restores` and to `GET /api/server/restores/{id}` carries a `warnings` list of `{code, text}` entries, with the code `dev-backup-into-production`. The manifest read shows `installationKind`. So a client can warn before the person accepts. The CLI reads the manifest first. It prints the warning before it starts the restore. The record of a restore keeps the removal counts. A check-only restore reports the counts that it would remove.
9. **Layer 3: refuse at sign-in.** The rule of ADR 252 decision 7 stays. A production server refuses to authenticate a session or a token whose principal has a subject that starts with `dev:`. The start logs one warning. This ADR adds a test for it. The test puts a dev principal with a valid session and token into a production store. It acts as if layers 1 and 2 failed.
10. **Layers and tests.** Each layer has its own test. The test sends hostile input to that layer alone (ADR 250 decision 4). The tests of the stage and the strip run on SQLite and on PostgreSQL.

| Layer | Where | Test, with the other layers off |
| --- | --- | --- |
| 1a Manifest | parse of the manifest | A manifest of format 3 with no kind, an empty kind, `DEV`, `staging` or a kind of the wrong JSON type. The parse refuses each with `ErrManifestKindInvalid`. The format check refuses format 2. |
| 1b Service | `RestoreService` | A stub live store of kind production, a manifest of kind dev, and the flag off. The result is `ErrDevBackupRefused`. There is no call to maintenance mode, no pre-backup and no stage. With the flag on, the restore goes on. |
| 1c Stage | store adapter, `StageKindChecker` | The `storetest` suite calls the stage step directly with a dev backup into a production database. The flag is off and the service check is skipped. The stage step refuses and the staging area stays empty. Both adapters run it. |
| 1d Flag | `cmd/opendustpan` | The flag parses. A test sets every environment variable of the server and puts a key of the same name in the configuration file. The server still refuses the backup. The key in the configuration file is an error. |
| 2a Strip | `DevStripper` | Cases in a table, with no service check and no stage check. One case has a dev principal with a grant, a session, a token and a sign-in request. Other cases have the prefix in other letter cases. Other cases have a provider `dev`. Other cases have a grant that names a missing principal. Other cases have a session of a principal that is removed in part. More cases have a token with the prefix and no principal row. More cases have a group membership of a dev principal. The last case has two subjects that differ only in case. After each case, the staged data holds no dev row. The rows of other people do not change. |
| 2b Atomic | restore service | A strip that fails at its last step. The restore discards the staging area. The live data is byte-equal to the data before. The restore fails with `ErrDevStripIncomplete`. |
| 2c Check | check step after the strip | A stub strip that removes nothing. The check step fails the restore before the apply step. |
| 2d Record | restore service | The audit entries after the swap match the report. There is one entry for each principal and one for each class without a principal. No token value or session ID appears in a log line or an entry. |
| 3 Sign-in | authentication | A production store holds `dev:admin` with a valid session and a valid token, and also `DEV:Admin`. The check of the session cookie and the check of the token refuse both. |
| API | restore routes | The answer carries the warning with the code and the text. The refusal carries the text of decision 4 and a status that the route test lists. |
| e2e | `e2e/` | Back up a dev installation. Restore it into production without the flag, and the restore is refused. Restore it with the flag. The dev users are gone, the audit entries exist, and a sign-in by `dev:admin` is refused. |

## Consequences

- The manifest format number changes. Every backup made before this change is invalid for the restore. No installation outside CI exists, so no one loses data.
- A restore of a dev backup into production needs a restart of the server with a long flag. An operator who needs it types the flag at each start, and the log warns at each start.
- The strip may break what depends on the dev people, for example a job that a dev person owns. The warning says so. The project advises against the restore.
- A new table that refers to a principal needs an entry in the coverage test of the strip. The test fails until the author decides to remove or to keep the rows.
- The console shows the warning in its restore flow. A follow-up issue in the console repository tracks it.
