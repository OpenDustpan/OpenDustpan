# 362. Owner decisions of 8 November 2026: stable IDs, store order, MySQL, baseline hashes and portable backups

Status: accepted · 2026-11-08. Records five decisions of the project owner of 8 November 2026, in the owner's words

Changes:

- Changes ADR 223 decisions 4 and 11: a Cedar entity for a group uses the stable ID of the group, not its name.
- Extends ADR 274 decisions 2 and 6: RFC 103 step 8 comes before ADR 274 step 7. MySQL waits for the end of the work of ADR 274.
- Changes ADR 322 decision 5: the `.sha256` files of the schema baselines are absent until the first tag, and the release generates them.

## Context

The project manager (PM) asked the owner four questions on 8 November 2026. The owner also gave two directions without a question. This ADR records the five decisions that result. It records the owner's words and nothing else as a decision. Each decision has the tag "(owner, 2026-11-08)".

- **Stable IDs.** The PM asked if Cedar access policies must name groups by ID instead of by name. ADR 223 decision 11 has an example policy with `OpenDustpan::Group::"platform"`. The example names the group by its name. The Cedar adapter builds a group entity from a string of the request (`pkg/gate/cedargo/request.go`). The check of what that string holds is open (see Consequences).
- **Store order.** ADR 274 decision 2 says that the work starts after step 8 of RFC 103. The PM asked if step 7 of ADR 274 (a central table list) comes now, or if RFC 103 step 8 comes first.
- **MySQL.** ADR 3 says "MySQL waits for a user who needs it". ADR 274 makes the store logic dialect-neutral so that a third dialect costs less.
- **Baseline checksums.** ADR 322 decision 5 says that the hash files stay in the tree. A hand edit of the baseline changes its `.sha256` file too, and parallel pull requests then conflict on that file.
- **Backups.** ADR 42 replaced the native backup format with one portable format.

## Decision

1. **Stable IDs (owner, 2026-11-08).** The owner said:

   ```text
   "I literally have no idea why we would ever use natural keys for any
   entity the user can rename or delete and recreate."
   ```

   Every stored reference to an entity uses the stable ID of the entity when a user can rename the entity, or delete and recreate it. A name is a label for people only. A Cedar access policy therefore names a group by its stable ID.
2. **RFC 103 step 8 first (owner, 2026-11-08).** The owner chose "RFC 103 step 8 first". RFC 103 step 8 (the move of the store adapters to `internal/store/`) comes before step 7 of ADR 274 (the central table list). This is the order of ADR 274 decision 2. ADR 274 states no other order.
3. **MySQL waits (owner, 2026-11-08).** The owner chose "MySQL waits". MySQL waits for the end of the "SQL logic once" work of ADR 274. The owner also said:

   ```text
   "We are also not reducing the role of SQLite. It's a fully supported DB
   option for single-node installs and we ARE adding MySQL at a later stage."
   ```

   SQLite stays a fully supported database for single-node installations. MySQL is a planned dialect for a later stage.
4. **Baseline checksums are absent until the tag (owner, 2026-11-08).** The owner chose "Absent until the tag". The `.sha256` files of the schema baselines (ADR 322) are absent until the first tag. The release generates them.
5. **Portable backups only (owner, 2026-11-08).** The owner said:

   ```text
   "I'm rejecting database native backups. We had this before, I had it
   ripped out so they're portable/can be restored regardless of the user's
   chosen database driver."
   ```

   This repeats [ADR 42](0042-a-portable-backup-format.md) decision 1. That decision makes the portable format the one format of the SQL part of a backup. This ADR does not change ADR 42.

## Consequences

These points follow from the decisions. They are not decisions of the owner.

- **Decision 1.** ADR 223 decision 11 gets a new example when the code changes. The group entity in the example then holds a stable ID, not the name `platform`. A rename of a group then changes no policy. A group that someone deletes and creates again does not inherit a policy of the old group. The console can show the name of a group next to its ID.
- **Follow-ups for decision 1.** These accepted ADRs, files and code store a reference by name for an entity that a user can rename. This ADR does not change them. Their decision text does not clearly fall under decision 1. Each needs a check in a new ADR:
  - [ADR 114](0114-the-policy-port-and-its-default-adapters.md) decision 7 gives group parents as `OpenDustpan::Group::"<name>"`. ADRs 224 and 229 supersede most of ADR 114.
  - `docs/guides/state-write-gates.md` shows `OpenDustpan::Group::"sre"` as an example of a group test in a limit.
  - `pkg/gate/cedargo/request.go` builds the group entities of a request from the group strings of the request. Check that these strings are IDs.
  - [ADR 320](0320-one-sign-in-path-for-every-sign-in-method.md) says that the table `principal_groups` has a `name_key`, unique across the installation. A key is fine for a unique check. A reference to a group from another stored object must use the ID.
  - [ADR 52](0052-groups-roles-grants-and-access-review.md) decision 5 says that a grant can name a principal or a group. A check of how a grant stores that reference is open.
  - [ADR 130](0130-runner-pools-runners-and-coordinators.md) decision 2 and [ADR 137](0137-the-machine-coordinator.md) say that a pool lists a runner or a coordinator by name. The check of the stored reference is open.
  - [ADR 19](0019-a-stack-has-a-stable-id.md) and [ADR 25](0025-opentofu-through-the-tfe-compatible-api.md) keep a route by name for the tool-compatible APIs. The real CLIs send names, so those routes stay. Stored references inside the server use the ID.
- **Decision 2.** The central table list (ADR 274 decision 5) waits for RFC 103 step 8. This matches ADR 274, so no step of ADR 274 moves.
- **Decision 3.** [ADR 3](0003-sql-persistence-per-dialect.md) says "MySQL waits for a user who needs it". The owner now gives a time, not a user, as the condition. This ADR adds no change link to ADR 3. A reader takes this ADR for the MySQL timing. ADR 274 decision 6 keeps the dialect code internal to this repository for now.
- **Decision 4.** The files `pkg/store/<dialect>/migrations/0168_baseline.sql.sha256` are in the tree today. A separate pull request deletes them. It also changes the code, tests and scripts that read them. Examples are `scripts/regenerate-baseline.sh` and the checks of ADR 325 that read a shipped migration with its `.sha256` file. This ADR changes no code and deletes no file. The runner still records the SHA-256 of each migration in `schema_migrations` (ADR 322 decision 3).
- **Decision 5.** No new work. A proposal for a native, per-driver backup is out of scope. The portable format restores into any dialect, SQLite and a later MySQL included.

## Not decided here

- The form of the migration from names to IDs in each ADR and each code path listed under Consequences. A new ADR or an RFC must decide it, because it can change a stored shape.
- The date of the MySQL work. The owner gave an order, not a date.
- Who writes the pull request that deletes the `.sha256` files, and when.
