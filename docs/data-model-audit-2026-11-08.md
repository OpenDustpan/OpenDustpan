# Data model audit

Date: 2026-11-08. Commit audited: `607c981` (`main`).

Scope: the two schema baselines
(`pkg/store/sqlite/migrations/0168_baseline.sql`, cited as **S**, and
`pkg/store/postgres/migrations/0168_baseline.sql`, cited as **P**), both
`queries.sql` files, the domain types in `pkg/*` that map to tables, and the
store code that writes and reads them. It does not repeat the architecture
survey on branch `pm/arch-survey`
(`docs/architecture-survey-2026-11-08.md`). Where a finding touches a cut of
that survey, it names the cut and adds only what is new.

The owner principles that bind this audit:

- Every stored reference to an entity that a user can rename, or delete and
  create again, holds the stable ID of that entity. A name is for display
  only.
- Fix the architecture. Do not add a safeguard on top of a wrong key. No
  new tooling is proposed as a fix.
- Check every fix against the ADRs.
- SQLite stays a full database for a single-node installation. MySQL comes
  later. Backups are portable only (ADR 42).

Nothing has shipped. Every fix below edits the baseline of each dialect in
place, runs `make generate`, and needs `opendustpan dev reset` (ADR 322,
ADR 324). No fix needs a migration or data conversion.

## Labels

- **Ready**: the fix follows the current ADRs, or carries out an accepted
  ADR that `main` does not follow yet.
- **Needs RFC**: the fix contradicts an accepted ADR decision that is not
  owner-tagged. CLAUDE.md requires an RFC first.
- **Needs owner**: no ADR decides the point, but the fix fixes a stored form
  that ships with the first tag, so the owner should choose.
- **Needs RFC + owner**: the fix contradicts an owner-tagged decision.
- **One-way door**: after the first tag, a change needs an upgrade path
  (ADR 72, ADR 322).

"Owner-tagged" follows the marking of this repository (ADR 241:41): a
decision headline that ends in "(owner)" or "(owner, date)", an ADR whose
title ends in "(owner)", or a status line that says the owner decided it.
A decision "delegated by the owner" (ADR 18, ADR 19) is marked as such.

## The main result

The owner already decided most of what the natural-key principle asks for.
`main` does not follow it yet.

- **ADR 220 (owner title), decision 1** (`0220:45`): "Each row that belongs
  to a namespace holds its namespace ID. This includes the principals,
  groups, custom roles, grants, tokens, sessions, sign-in settings and keys
  whose home is a namespace."
- **ADR 220 decision 2** (`0220:55`): "A row that names another row of a
  namespace names it with the pair (namespace ID, ID), for example
  `FOREIGN KEY (namespace_id, unit_id) REFERENCES units (namespace_id, id)`."
- **ADR 220 decision 3** (`0220:59`): row-level security on PostgreSQL for
  each table with a namespace ID.
- **ADR 166 decision 10** (`0166:400`, not owner-tagged): "The stores key by
  the namespace ID ... never its name. The ports keep `namespace.Name` ...
  Each store resolves the name to the ID ... in the transaction of each
  call." Decision 12: "Nothing carries over to a namespace that a team
  creates again."
- **ADR 317 decision 1 (owner, 2026-10-19)**: "gate rows key on the stable
  unit ID", "a new unit with the same name starts clean".
- **ADR 313 decision 3 (owner, 2026-10-19)**: the profile authorization
  object is "keyed by profile ID: profile/<namespace>/<prf-ID>".

On `main`: 16 columns hold the namespace name (RFC 126 door 2 lists them),
`namespaces.name` is the primary key, there is no `units` table, there is no
composite foreign key of the form of decision 2, and P has no row-level
security policy (0 matches for `ROW LEVEL SECURITY` or `CREATE POLICY`).
RFC 126 (draft) already lists the namespace-name columns as one-way doors.
This audit confirms that list, extends it beyond namespaces, and ranks the
fixes.

Today most of the name-keyed columns are latent: a namespace cannot be
renamed or purged in production, and a collection or blueprint cannot be
renamed. They turn into live bugs on the day a purge, a rename or a
replication ships. The first tag freezes them. Four findings are live today
(DRV-1, NK-12, NK-14, STR-1).

## Ranked list

Rank: severity first (security and data loss, then correctness, then
cost), then cost in pull requests. "Live" means a reachable bug on `main`
today. "Latent" means a bug that a planned feature (purge, rename,
replication, profile delivery) turns live.

| Rank | ID | Finding | Severity | Live | Cost | Label |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | DRV-1 | The plan-staleness check reads `unit_state_versions`, which no production path writes | Data loss | Live (if the plan/apply path runs) | 1 to 2 PRs | Ready |
| 2 | NK-1 | 16 columns and 9 foreign keys key on the namespace name; `namespaces.name` is the primary key | Security | Latent | 4 to 6 PRs | Ready (carries out ADR 220, owner) |
| 3 | INT-1 | No `units` table: about 16 `unit_id` columns have no foreign key; no composite (namespace ID, ID) keys | Security, integrity | Latent | 3 to 4 PRs | Ready (carries out ADR 220, owner) |
| 4 | NK-2 | The associated data of a sealed data key binds the namespace name | Data loss, security | Latent | 1 PR | Ready |
| 5 | NK-3 | Jobs and imports hold the profile by name | Security | Latent (profile delivery) | 1 to 2 PRs | Needs RFC (ADR 202 decision 9) |
| 6 | NK-4 | Cedar policy text names groups by name | Security | Latent; guarded by name today | 2 to 3 PRs | Needs owner; One-way door |
| 7 | INT-2 | No row-level security on PostgreSQL | Security | Latent | 2 to 4 PRs | Ready (ADR 220 decision 3, owner) |
| 8 | NK-5 | Gate scope keys hold the namespace, collection and profile by name; the blueprint key has no namespace | Security, correctness | Latent | 2 PRs | Needs RFC + owner (ADR 224 decision 1) |
| 9 | NK-6 | The audit chain keys on the namespace name; a collection entry has no object ID | Security (audit) | Latent | 1 to 2 PRs | Ready; One-way door |
| 10 | NK-7 | `grants.namespace` and the namespace target hold the name | Security | Latent | 1 PR (inside NK-1) | Ready |
| 11 | NK-8 | Notification scope and subscription filters hold names | Security, correctness | Live (rename) | 2 PRs | Needs RFC (filter form) |
| 12 | NK-12 | A Pulumi stack rename writes no audit entry | Correctness (No magic) | Live | 1 PR | Ready |
| 13 | INT-3 | `job_id`, notification and runner references have no foreign key | Integrity | Latent | 1 to 2 PRs | Ready |
| 14 | NK-9 | Collections key on `(namespace, name_key)`; members and placement rules point at the name | Correctness | Latent | 1 PR | Ready |
| 15 | NK-10 | Blueprints have no ID; versions, context and gates key on the name | Correctness | Latent | 1 to 2 PRs | Needs RFC (ADR 112 decision 1) |
| 16 | NK-11 | Manual-lock holders, break-glass actors and idempotency records hold the subject | Security (low) | Latent | 1 PR | Ready |
| 17 | NK-14 | The job body compares the unit name in `PhaseClock` | Correctness | Live (after a rename) | 1 PR | Ready |
| 18 | STR-1 | ID columns have `DEFAULT ''` in S | Integrity | Live (S only) | 1 PR | Ready |
| 19 | DUP-1 | Two tombstone forms: name mangling and partial unique indexes | Correctness, cost | Live (design) | 1 PR | Ready |
| 20 | DIA-1 | "Not ended" is 0 in S and NULL in P for three control tables | Correctness (portable backups) | Live | 1 PR | Ready |
| 21 | DUP-2 | `secret_keys` and `installation_secret_keys` are one concept; `secret_keys.stack_id` is a tool word in the core | Cost, ADR 12 | Live (design) | 1 PR | Ready |
| 22 | DIA-2 | 21 boolean columns have no 0/1 CHECK in S; 3 are `bigint` in P | Integrity | Live | 1 PR | Ready |
| 23 | DUP-3 | Absence encoded as NULL in some tables and 0 or '' in others | Correctness, cost | Live (design) | 1 to 2 PRs | Ready |
| 24 | NK-13 | Audit sink cursors key on the operator's sink name, which defaults to the sink kind | Correctness (ops) | Live | 1 PR | Ready |
| 25 | NK-15 | `stacks` and `inventory` primary keys are the name, not the ID | Cost, correctness | Latent | 1 PR (inside NK-1) | Ready |
| 26 | DUP-4 | Release fields live in both `workspaces` and `inventory`; stacks only in `inventory` | Cost | Live (design) | 1 PR | Ready |
| 27 | DUP-5 | Job and plan bodies repeat columns, with time as RFC 3339 next to `*_ms` | Cost | Live (design) | 1 PR | Ready |
| 28 | ID-1 | Six ID generators with four formats | Cost | Live | 1 PR | Ready |
| 29 | DIA-3 | Other dialect divergences (FK, default, index, collation, non-STRICT tables) | Integrity (low) | Live | 1 PR | Ready |
| 30 | NK-16 | The installation is named, with no ID | Correctness | Latent | — | Needs RFC + owner (ADR 248 decision 5) |
| 31 | DIA-4 | What MySQL adds | Cost | Future | — | Input to the MySQL RFC |

Counts by category:

| Category | Findings |
| --- | --- |
| 1. Natural keys (NK) | 16 (NK-1 to NK-16) |
| 2. One concept modeled two ways (DUP) | 5 |
| 3. Dialect divergence (DIA) | 4 |
| 4. Integrity (INT) | 3, plus the fence (see INT-1) |
| 5. Identity and IDs (ID) | 1, plus notes |
| 6. Derived or hand-kept data (DRV) | 1, plus notes (survey cut 6 covers the rest) |
| 7. Other structural (STR) | 1 |
| Total | 31 |

Order and dependencies:

```
NK-1 (namespace ID everywhere) ──> INT-1 (units table, composite keys) ──> INT-2 (RLS)
NK-1 ──> NK-6, NK-7, NK-8 (scope), NK-15        (these land as part of NK-1 or right after)
INT-1 ──> DRV-1 (the units table holds the current state version)
NK-2, NK-3, NK-11, NK-12, NK-14, STR-1, DIA-*  independent
NK-4, NK-5, NK-10, NK-16  wait for an owner decision or an RFC
```

---

## Category 1: Natural keys

### NK-1. The namespace name is the key of namespace rows

**Evidence.**

- `namespaces.name` is the primary key; `id` is a unique index with
  `DEFAULT ''` (S:18-25, S:1568).
- Foreign keys `REFERENCES namespaces (name)` from `stacks` (S:28),
  `secret_keys` (S:72), `inventory` (S:81), `workspaces` (S:95),
  `inventory_deleted` (S:261), `collections` (S:339), `inventory_labels`
  (S:677), `manual_locks` (S:864), `placement_rules` (S:1315).
- Name columns with no foreign key: `audit_entries.namespace` (S:238, part
  of the chain key), `audit_sink_cursors.namespace` (S:314),
  `grants.namespace` (S:304), `principals.namespace` (S:188),
  `trust_rules.namespace` (S:1106), `collection_members.namespace` (S:348),
  `triggers.source_namespace` (S:1077), and the `scope` of
  `notifications`, `notification_sequences`, `notification_cursors`,
  `notification_sinks`, `notification_subscriptions`,
  `notification_deliveries`, `notification_sink_secrets`,
  `notification_sink_refs` (S:1183-1271;
  `notify.Notification.Scope namespace.Name`, `pkg/notify/notify.go:254`).
- The other half of the schema already keys on `namespace_id REFERENCES
  namespaces (id)`: `object_context`, `runner_pools`, `jobs`, `saved_views`,
  `registry_*`, `git_host_*`, `item_keys`, `unit_settings`, `triggers`,
  `settings` (`scopeID()`, `pkg/store/sqlite/settings.go:42-59`) and more.
  So one concept, the namespace of a row, is keyed two ways.
- Queries on ID-keyed tables resolve the name in every statement, for
  example `WHERE namespace_id = (SELECT n.id FROM namespaces AS n WHERE
  n.name = ...)` (`pkg/store/sqlite/queries.sql:3904`).
- The port contract already promises the ID form:
  `pkg/namespace/namespace.go:190-192` says "A purge of a namespace deletes
  its row. A later namespace of the same name has a new ID, so it gets no
  row that holds the ID of the purged one." This holds only for the
  ID-keyed tables.

**Users.** Every store method of stacks, workspaces, inventory, collections,
manual locks, placement rules, grants, audit, trust rules, principals and
notifications.

**Bug it allows.** No production path renames or purges a namespace today
(the only `DELETE FROM namespaces` is in test hooks:
`pkg/store/sqlite/inventory_changes_internal_test.go:44`). On the day a
purge ships (ADR 47 purge, ADR 60 decision 6 "lets a namespace go"), a new
namespace with the old name inherits, from the name-keyed tables with no
foreign key: the grants on `namespace:<name>` (NK-7), the audit hash chain
and the sink cursors (NK-6), the namespace service principals
(`principals.namespace`), the trust rules (a token of the old trust rule
signs in as the new namespace), the notification outbox, sequence and
subscriptions. A replica that must refuse a name that it uses for another
namespace ID (ADR 248 "Not decided", `0248:198`) must touch every one of
these tables. A rename touches about 25 tables.

**Fix.**

1. `namespaces`: `id` is the primary key; `name` gets a unique index. Drop
   `DEFAULT ''` from `id`.
2. Every name column above becomes `namespace_id TEXT NOT NULL REFERENCES
   namespaces (id)`. The installation scope is NULL (or a separate table),
   never `''` or `@server` in the same column, so that the foreign key
   holds.
3. Each store resolves `namespace.Name` to `namespace.ID` once at the start
   of its transaction (ADR 166 decision 10), and the fence carries the ID
   (RFC 126 door 3). Ports keep `namespace.Name`.
4. Stored text that is for a person to read (the audit `object`, the grant
   `object`) keeps the name as a snapshot next to the ID (ADR 60
   decision 6).

**Cost.** 4 to 6 PRs, one per area (units and inventory; collections and
placement; grants and principals; audit; notifications; trust rules and
triggers). About 120 queries in each dialect change their `WHERE`.

**ADRs.** Carries out ADR 220 decision 1 (owner) and ADR 166 decisions 10
and 12 (not owner-tagged). RFC 126 door 2 (draft) proposes the same. ADR
215 decision 4 (owner: the namespace name in the route) and ADR 18
(delegated: the organization name is the namespace name) are route rules
and stay. ADR 60 decision 6 (names as text in audit entries) stays: the
name stays as text, the key becomes the ID.

**One-way door.** Yes. Label: **Ready**.

### NK-2. A sealed data key binds the namespace name

**Evidence.** `pkg/secret/secret.go:80-83`: `type Scope struct { Namespace
namespace.Name; ID string }`. `secret.go:114-116`: `aad()` is `aadLabel +
"\x00" + s.Namespace.String() + "\x00" + s.ID`. `secret_keys` primary key
is `(namespace name, stack_id)` (S:71-78).

**Bug it allows.** After a rename, no data key of the namespace opens. Every
Pulumi secret and every sealed value of the namespace is unreadable until
each key is wrapped again. After a purge and a re-create, the old ciphertext
of a backup authenticates against the new namespace.

**Fix.** `Scope` holds `namespace.ID`; `aad()` binds the ID.
`secret_keys` keys on `(namespace_id, scope_id)` (see DUP-2).

**Cost.** 1 PR (plus the secret conformance suite).

**ADRs.** Follows ADR 217 decision 3 (owner title), which binds the
namespace key to "the namespace ID" (`0217:72`); the data key below it
should bind the same value. RFC 126 door 1 (draft) proposes the same.

**One-way door.** Yes: a ciphertext that ships with the name cannot be read
under another name. Label: **Ready**.

### NK-3. Jobs and imports hold the profile by name

**Evidence.**

- `pkg/job/job.go:297-300`: "Profile names the profile that supplies the
  secrets of a runner job. The server fixes it at start". The field is the
  name, and it travels to the runner (`pkg/job/claimer.go:553`,
  `pkg/launcher/run/run.go:110`).
- `imports.profile` (S:618) holds the profile name.
- Profiles are soft-deleted (`pkg/store/sqlite/profile.go:386-420`), and a
  new profile can take the name (`profiles_live_name`, S:1591, unique only
  among live rows).

**Bug it allows.** Today the launcher refuses each tool step that has a
profile (`pkg/launcher/run/step.go:120-131`), so nothing leaks yet. Once
profile delivery ships, a queued job or import whose profile is deleted
and created again under the same name gets the secrets of the new profile.
The comment "fixes it at start" is false: it fixes the name, not the
profile.

**Fix.** `job.Spec.Profile` and `imports.profile` hold the `prf-` ID.
`profile_path` stays as display text. The rename rule of ADR 202 decision 9
("fails with `profile.ErrInUse` while a profile, a blueprint or an
attachment names the profile by its name") goes away for these
references, because they no longer break on a rename.

**Cost.** 1 to 2 PRs.

**ADRs.** Contradicts ADR 202 decision 9 (`0202:316`, not owner-tagged),
which assumes references by name. Supported by ADR 313 decision 3 (owner:
the profile object is keyed by ID) and ADR 207 (`0207:200`: unit outputs
resolve to the unit ID). Label: **Needs RFC**. One-way door: yes (the job
body and the import row ship with the tag).

### NK-4. Cedar policy text names groups by name

**Evidence.** `pkg/gate/cedargo/schema.go:16-17`: "a policy writes
OpenDustpan::Group::\"sre\"". The request carries group names
(`pkg/gate/cedargo/request.go:22-26`). Groups can be renamed
(`pkg/principal/service.go:411-457`). Approval rules already resolve group
names to IDs at save (`pkg/gate/approvers_check.go:13-54`). Grants hold the
group ID (`pkg/access/access.go:334-340`).

**Bug it allows.** The guard `LimitLiftedBy`
(`pkg/gate/limitgroups/limitgroups.go:47-80`) compares names, and refuses a
rename, delete or member change that can lift a limit. That is a safeguard
on a wrong key: every new path that changes a group or its name (identity
provider sync, SCIM, a new group kind) must call it, and survey cut 5 lists
four pull requests that each added one more caller. With an ID in the
policy, a rename cannot lift a limit, and a new group that takes the old
name cannot match it.

**Fix.** The Cedar entity UID of a group is its ID
(`OpenDustpan::Group::"grp-..."`). The API accepts a group name in a
policy, resolves it to the ID at save in the same transaction, and answers
the policy with names for display (the same pattern as approval rules). The
name-matching branch of `LimitLiftedBy` goes away; the delete and member
checks stay as long as a limit can name the group. Survey cut 5 (the index
of group references) still applies and becomes simpler.

**Cost.** 2 to 3 PRs.

**ADRs.** No owner decision fixes the group UID. ADR 223 decision 11 (PM)
uses `principal in OpenDustpan::Group::"platform"` as an example; the name
form comes from ADR 114 decision 7, which ADR 224 and ADR 229 superseded.
ADR 225 decision 9 (owner) shows `"groups": ["netops"]` in an approval
rule, which the store already resolves to IDs. Label: **Needs owner**: the
stored policy form ships with the tag. One-way door: yes.

### NK-5. Gate scope keys hold names

**Evidence.** `pkg/gate/scope.go:58-80`: `namespace/<name>`,
`collection/<namespace>/<name_key>`; `scope.go:175-184`:
`blueprint/<name>/<version>` with no namespace; `scope.go:186-194`:
`profile/<namespace>/<name>`. The unit form is already the ID: S:1480
`CHECK (level <> 'unit' OR scope_key = 'unit/' || unit_id)`. `gate_objects`
and `gate_references` key on `scope_key` (S:1465-1495). A collection delete
purges its gate rows by the name key (`pkg/store/sqlite/gate.go:875-901`).

**Bug it allows.** Today the collection delete purge keeps a new collection
of the same name clean, by code. The blueprint and profile scopes are not
wired yet (`scope.go:414` is the only caller of `BlueprintScope`). When they
are, two namespaces with a blueprint of the same name share a scope key (the
`gate_objects` primary key has `namespace_id`, which limits the damage), and
a profile created again under a deleted profile's name inherits its gates.

**Fix.** Every variable segment of a scope key is a stable ID:
`namespace/<ns-ID>`, `collection/<col-ID>`, `profile/<prf-ID>` (ADR 313
decision 3 already says so), `blueprint/<bpr-ID>/<version>` (needs NK-10).
The `level` and `unit_id` columns of `gate_objects` become `scope_kind` and
`scope_id`, with a foreign key where the parent table exists.

**Cost.** 2 PRs.

**ADRs.** Contradicts ADR 224 decision 1 (owner; `0224:37-48`): "The scope
key is `installation`, `namespace/<namespace>`,
`collection/<namespace>/<collection>`, ... `blueprint/<blueprint>/<version>`
or `profile/<namespace>/<profile>`." ADR 317 (owner) changed the unit form
to the ID; ADR 313 decision 3 (owner) changed the profile form to the ID.
The namespace and collection forms are still names under an owner decision.
RFC 82 (draft) proposes names for blueprints and rejects the ID because "No
other scope key holds an ID", which ADR 317 has since made false. Label:
**Needs RFC + owner**. One-way door: yes (the Cedar policy ID is
`<scope key>/<id>` and is stored and recorded).

### NK-6. The audit chain keys on the namespace name

**Evidence.** `audit_entries` primary key `(namespace, sequence)` (S:257);
`audit_sink_cursors (sink, namespace)` (S:317). `object_id` is set for a
unit (`internal/api/pulumi/audit_entry.go:62`) and is empty for a
collection.

**Bug it allows.** A namespace created again under a purged name continues
the hash chain and the sink cursor of the old one: the new namespace's
audit log starts with the old entries, and an export of the new namespace
holds them. The audit history of a deleted collection and a new one of the
same name merge by object text.

**Fix.** The chain key is `(namespace_id, sequence)`; the name stays in the
entry as text (ADR 60 decision 6). `object_id` holds the stable ID for every
object kind that has one (namespace, collection, unit, profile, pool, group,
principal, token).

**Cost.** 1 to 2 PRs (inside NK-1, plus the object ID of each kind).

**ADRs.** ADR 41 decision 13 (chain per namespace, key form not stated).
ADR 60 decision 6 (`0060:100`: "An entry holds every name that a person
needs to read it, as text ... It holds no foreign key"): kept, the ID is an
added field and no foreign key is added. ADR 60 (`0060:87`) allows a new
optional field in the hash encoding. ADR 220 decision 1 (owner). Label:
**Ready**. One-way door: yes (the hash encoding).

### NK-7. Grants hold the namespace by name

**Evidence.** `grants.namespace` (S:304) is the name. `targetOf`
(`pkg/store/sqlite/access.go:1094-1112`) writes `unit:<unit ID>` and
`collection:<collection ID>`, but `obj.String()` (names) for every other
object, so a namespace grant has the target `namespace:<name>`.
`grants.object` is the wire text with names at grant time
(`pkg/access/access.go:400-405`). Role and holder are IDs already.

**Bug it allows.** A namespace created again under a purged name inherits
every namespace-level grant. This breaks ADR 166 decision 12 ("Nothing
carries over to a namespace that a team creates again") and the glossary
rule that a new object with an old name gets none of its grants.

**Fix.** The target of a namespace grant is `namespace:<ns-ID>`;
`grants.namespace` becomes `namespace_id` (NK-1). Every target kind with a
stable ID uses it; `targetOf` has no `obj.String()` fallback, and an object
kind with no ID fails closed.

**Cost.** 1 PR inside NK-1. **ADRs.** ADR 52 decision 3 (one holder, one
role, one object; form not stated), ADR 220 decision 1 (owner: grants hold
the namespace ID), RFC 19:336 and the glossary entry "Grant". Label:
**Ready**. One-way door: yes.

### NK-8. Notifications: scope and filters by name

**Evidence.** Every notification table keys on `scope` = namespace name or
`@server` (S:1183-1271), with no foreign key. A subscription filter holds
unit references as text, for example `opentofu:shop/*`, and matches them
against `n.Object` (`pkg/notify/route.go:525-561`).

**Bug it allows.** Live today: a Pulumi stack rename silently stops every
subscription that named the stack, and a new stack with the old name gets
them. Latent: a namespace created again inherits the sinks, the
subscriptions and the sequence of the old one. The read check
`notify.Reader.CanRead` could not be traced to a production implementation
(unverified); if it resolves by name, the recreated-name case is also a
read leak.

**Fix.** `scope` becomes `namespace_id` (NULL for the installation). An
exact unit or collection in a filter is stored as its ID and shown by its
current name. A pattern (`shop/*`) stays a pattern over names, like a
placement rule (ADR 61), but is stored in its own field so that a reader
can tell a pattern from a reference.

**Cost.** 2 PRs. **ADRs.** ADR 145 decisions 1 and 9 (a notification
holds "the object and its stable ID"; the filter form is not decided as an
ID). Label: **Needs RFC** for the filter form; the scope part is **Ready**
under NK-1.

### NK-9. Collections key on the name

**Evidence.** `collections` primary key `(namespace, name_key)`, `id` only
`UNIQUE` (S:337-344). `collection_members.collection_key` and
`placement_rules.collection_key` reference `(namespace, name_key)`
(S:351, S:1323).

**Bug it allows.** No rename exists today
(`pkg/store/sqlite/collections.go`), and a delete purges grants, locks,
gates, placement rules and memberships by hand in one transaction
(`collections.go:90-140`). The hand-written purge is the safeguard: each new
table that refers to a collection must be added to it. With an ID key and a
foreign key, a forgotten table fails the delete instead of leaking into a
new collection of the same name. A rename, when it comes, is one row.

**Fix.** Primary key `id`; unique `(namespace_id, name_key)`;
`collection_id REFERENCES collections (id)` in members and placement rules;
`inventory_changes.collection` holds the ID (S:552).

**Cost.** 1 PR. **ADRs.** ADR 51 (a named set), ADR 166 (`0166:101`,
`0166:408`: "A collection reference resolves its name to the collection
ID"). Label: **Ready**. One-way door: yes.

### NK-10. Blueprints have no ID

**Evidence.** `self_service_blueprints` primary key `(namespace_id, name)`;
`self_service_blueprint_versions` keys on `(namespace_id, name, number)`
(S:1118-1139). `object_context` with `object_kind = 'blueprint'` holds the
name as `object_id` (`BlueprintRef`, `pkg/objectcontext`). The gate scope
`blueprint/<name>/<version>` has no namespace (NK-5).

**Bug it allows.** None today: there is no rename and no delete. A delete or
rename later inherits context, gates and version history by name.

**Fix.** A `bpr-` ID as primary key; versions and context key on it.

**Cost.** 1 to 2 PRs. **ADRs.** ADR 112 decision 1 (`0112:38-42`, not
owner-tagged): "The identity package gets the object kind `blueprint`, with
the namespace and the name." Label: **Needs RFC**.

### NK-11. Actors stored as the subject string

**Evidence.**

- `manual_locks.holder` is the subject; `RemoveLock` compares it with
  `p.Subject` (`pkg/manuallock/service.go:154`,
  `pkg/store/sqlite/manual_locks.go:107`).
- `break_glass_reviews.use_subject`, `use_people`, `verdict_reviewer` are
  subjects (`pkg/breakglass/breakglass.go:94-101, 157`).
- `idempotency_records.principal` is `p.Subject`
  (`internal/api/settings/keyed.go:226`).
- `temp_admins.subject` is display only; the lookup joins the live
  principal (`queries.sql:2433-2440`). This one is correct.

**Bug it allows.** For a local user the subject is `local:<record ID>` and
is stable (ADR 285 decision 2). For a federated user the subject is the
identity provider's value. A principal deleted and created again with the
same subject (ADR 266 decision 2 says it gets a new ID) can release the old
principal's manual lock, and counts as the same reviewer of a break-glass.

**Fix.** Store the principal ID; keep the subject as display text where a
person reads it.

**Cost.** 1 PR. **ADRs.** ADR 266 decision 2 and ADR 285 decision 2 (PM
under owner delegation) already chose principal IDs. Label: **Ready**.

### NK-12. A Pulumi stack rename writes no audit entry

**Evidence.** `RenameStack` runs in `inFencedTx`
(`pkg/store/sqlite/sqlite.go:863`), not in `inFencedAuditedTx` as
`DeleteStack` does (`sqlite.go:994`). `stack.Service.Rename`
(`pkg/stack/stack.go:660`) takes no `audit.Entry`. The only record is an
`inventory_changes` row of kind renamed.

**Bug.** Live. "Each automatic action writes a record of who or what did it
and why" (CLAUDE.md, "No magic"). The audit log cannot tell who renamed a
stack, and the object text of earlier entries no longer matches a live
stack. It is in this audit because a rename is the event that every name
key must survive.

**Fix.** Rename takes an `audit.Entry` and runs in the audited transaction,
with the old and the new name in the entry and the unit ID in `object_id`.

**Cost.** 1 PR. **ADRs.** ADR 60. Label: **Ready**.

### NK-13. Audit sink cursors key on the operator's sink name

**Evidence.** `--audit-sink [name=]spec`; the name defaults to the sink kind
(`cmd/opendustpan/auditsinks.go:101-132`). `audit_sink_cursors (sink,
namespace)` and `audit_sink_leases (sink)` key on it; the lease key is
`"audit-sink/" + name` (`pkg/audit/sink_lease.go:31-33`).

**Bug.** Live. An operator who points the default `webhook` sink at a new
destination keeps the old cursor: the new destination never receives the
history. A rename of the sink sends everything again.

**Fix.** The cursor row stores the destination it delivers to (the kind
and a digest of the address), and the server refuses to start a sink whose
destination does not match the stored row until the operator chooses
"continue" or "start from the beginning". Without a database entity for a
sink, this is the stable identity.

**Cost.** 1 PR. **ADRs.** No ADR decides the cursor key. Label: **Ready**.

### NK-14. The job compares the unit name

**Evidence.** The job body holds the unit as `{tool, namespace, name,
unitID}` (`pkg/store/internal/jobstore/body.go:23-26`).
`PhaseClock.PhaseStartedAt` compares `u.UnitName() != unit.Name`
(`pkg/job/phaseclock.go:41`).

**Bug.** Live after a rename: the manual-lock call of a running job for its
own unit fails with `ErrNoPhase`. It fails closed, so it is a correctness
bug, not a security bug.

**Fix.** Compare the unit ID and the namespace ID. The body keeps the names
as a snapshot for display only.

**Cost.** 1 PR. Label: **Ready**.

### NK-15. `stacks` and `inventory` primary keys are names

**Evidence.** `stacks` primary key `(namespace, project, name)`, `id` a
unique index (S:27-35, S:1497). `inventory` primary key `(namespace, tool,
name)` (S:90). A rename deletes and inserts the inventory row
(`pkg/store/sqlite/sqlite.go:886-906`).

**Bug.** Child rows already key on the stack ID (ADR 19), so a rename is
safe for them. The name primary key forces the delete-and-insert of the
inventory row and the name mangling of DUP-1.

**Fix.** `id` (or `unit_id`) is the primary key; the live name is a partial
unique index `WHERE deleted_at_ms IS NULL`. Lands with NK-1 and INT-1.

**ADRs.** ADR 19 (delegated; "A rename keeps the ID"), ADR 13 (the unit
reference stays `<tool>:<namespace>/<name>` on the wire and in
authorization input; this fix does not change the reference, only the
stored key). Label: **Ready**.

### NK-16. The installation is named, with no ID

**Evidence.** `namespaces.primary_installation` holds the installation name
(S:20).

**Bug it allows.** A rename of an installation is a change of every
namespace row it is primary for. This is by decision.

**ADRs.** ADR 248 decision 5 (owner ADR; `0248:56-62`): "The server does not
make a random installation ID, because a restore into a new database must
keep the name that the namespace rows hold." Label: **Needs RFC + owner**.
The audit lists it for completeness and does not propose a change: the
owner's reason (a restore into a new database) holds.

### Not a natural-key bug (checked)

- `grants.role`, `grants.subject_id`: IDs. Reuse of an ended subject or role
  ID is refused (`CountEndedGrantRecords`,
  `pkg/store/sqlite/access.go:101-115`).
- `unit_defaults.pool`, `unit_settings.body.pool`, `job.Spec.Pool`: pool IDs
  (`pkg/unitsettings/validate.go:73-80`).
- `saved_views.owner`: principal ID (ADR 266 decision 2).
- `settings.scope_id`: namespace ID or the server constant.
- `manual_locks.object_id`: resolved to the namespace, collection or unit ID
  (`pkg/store/sqlite/manual_locks.go:290-330`).
- `triggers.source_unit_id`: ID.
- `maintenance_revocations.target`: ID.
- `placement_rules.project`, `.stack`: glob patterns over names, by design
  (ADR 61, ADR 329 owner).
- `trust_rules.principal_id`: explicit ID.
- `PageNamespaces` keyset cursor on the name
  (`internal/api/server/namespaces.go`): a cursor, not a reference.

---

## Category 2: One concept modeled two ways

### DUP-1. Two tombstone forms

**Evidence.** A soft-deleted stack keeps its row with `name =
'deleted:' || id` and `deleted_name = name` (`queries.sql:399-404`);
workspaces do the same (`queries.sql:796-802`), because `workspaces` has a
full `UNIQUE (namespace, name)` (S:106) and `stacks` has the name in the
primary key. Principals, groups, roles and profiles use a partial unique
index on live rows (S:1511, S:1513, S:1531, S:1591) and keep the name.

**Bug.** Every reader of `name` must know the `deleted:` prefix; a name
check that forgets it sees a fake name. Two rules for one concept.

**Fix.** One form: the row keeps its name, `deleted_at_ms` marks it, and a
partial unique index on live rows holds uniqueness (it exists already as
`workspaces_live_name`, S:1656). Drop `deleted_name`.

**Cost.** 1 PR. **ADRs.** ADR 47, ADR 153 ("The name is free at once").
Label: **Ready**.

### DUP-2. Two secret-key tables, and a tool word in the core

**Evidence.** `secret_keys (namespace, stack_id, wrapper_id, ciphertext,
created_at_ms)` (S:71-78) and `installation_secret_keys (scope_id,
wrapper_id, ciphertext, created_at_ms)` (S:420-425). The secret package is
shared and tool-neutral (`Scope{Namespace, ID}`), but the column is
`stack_id`.

**Fix.** One table `secret_keys (namespace_id NULL for the installation,
scope_id, ...)` with a unique key on both. Lands with NK-2.

**Cost.** 1 PR. **ADRs.** ADR 12 ("no field ... may exist only because one
tool needs it"). Label: **Ready**.

### DUP-3. Absence as NULL in some tables and as 0 or '' in others

**Evidence.**

- Deleted: `stacks.deleted_at_ms`, `principals.deleted_at_ms` NULL;
  `profiles.deleted_at_ms` NOT NULL with 0 (S:692); `runners.retired_at_ms`
  0 (S:756); `issuer_keys.deleted_at_ms` 0 (S:1161).
- Revoked: `grants.revoked_at_ms` NULL (S:308); `runner_join_tokens.revoked_at_ms`
  0 (S:737).
- No namespace: `grants.namespace ''`, `principals.namespace ''`,
  `passkey_challenges.principal_id ''`, notification `scope '@server'`.
- Ended: see DIA-1.

**Bug.** A `''` or `@server` sentinel blocks the foreign key that NK-1 adds.
A 0 sentinel lets a `> since` query treat "never" as "long ago". Each table
needs its reader to know its rule.

**Fix.** One rule in both baselines: an absent time or reference is NULL.
A foreign key then holds for every present value.

**Cost.** 1 to 2 PRs. Label: **Ready**.

### DUP-4. Release fields in two tables

**Evidence.** `workspaces.last_release_lock_id`, `_holder`, `_by`, `_kind`,
`_at_ms` (S:104-105) and the same five columns in `inventory` (S:88-89).
`stacks` has none, so for stacks the projection is the only copy, and for
workspaces it is a second copy.

**Fix.** The tool table holds tool facts; the projection copies them
(ADR 13). Either both tools keep the release in their own table, or the
release is a tool-neutral fact and lives only in the unit row (INT-1).

**Cost.** 1 PR. **ADRs.** ADR 13 (projection in the same transaction).
Label: **Ready**.

### DUP-5. JSON bodies that repeat columns

**Evidence.**

- `jobs.body` holds the unit as `{tool, namespace (name), name, unitID}`
  (`body.go:23-26`) next to the `namespace_id` and `unit_id` columns.
- The plan body holds `createdAt time.Time` (RFC 3339 text,
  `body.go:115`) next to `job_plans.created_at_ms` (S:1034). The log body
  does the same (`body.go:79-83`). The schema stores time as Unix
  milliseconds everywhere else (CLAUDE.md).

**Bug.** Two values that can differ, with no rule for which one wins.

**Fix.** A body holds no field that a column holds. Time in a body is Unix
milliseconds.

**Cost.** 1 PR. Label: **Ready**.

### Checked and not duplicate

- `gate_objects.level` and `unit_id` are derived from `scope_key`, and a
  CHECK keeps them equal (S:1478-1480). NK-5 replaces the three with
  `scope_kind`, `scope_id`.
- `inventory` is the projection of ADR 13; its copies of stack and workspace
  facts are by decision.
- Per-tool tables (`updates`, `checkpoints`, `update_journal*`,
  `state_versions`, `workspace_blame_*`) do not leak into shared tables,
  except DUP-2.

---

## Category 3: Dialect divergence

The two baselines have the same 126 tables and the same column names except
one. Primary keys, unique constraints and partial indexes match. Foreign
keys and their `ON DELETE` match except one. No time is stored as
`TIMESTAMPTZ`; every time column is Unix milliseconds in both.

### DIA-1. "Not ended" is 0 in SQLite and NULL in PostgreSQL

`backups.ended_at_ms` (S:132 vs P:2320), `restores.ended_at_ms` (S:149 vs
P:2368), `store_checks.ended_at_ms` (S:1147 vs P:2382): S is `NOT NULL
DEFAULT 0`, P is nullable with no default. A portable backup or a query
that compares with 0 behaves differently by dialect. Fix with DUP-3 (NULL in
both). 1 PR. **Ready**.

### DIA-2. Boolean columns

- 21 boolean columns in S have no `CHECK (x IN (0, 1))`, so S accepts any
  integer: `access_token_policy.disable_*` (S:324-326),
  `checkpoints.invalid` (S:486), `durable_operations.cancel_requested`
  (S:456), `group_members.pinned` (S:205), `namespaces.allow_*` (S:23),
  `principal_groups.closed_to_identity_provider` (S:199),
  `principals.break_glass` (S:188), `runners.draining` (S:755),
  `sign_in_providers.auto_create`, `.enabled` (S:434-435),
  `state_versions.force`, `.sealed`, `.unlocked_write` (S:119, S:126),
  `updates.dry_run`, `.journal_closed`, `.journal_records_changed` (S:41,
  S:49), `workspaces.allow_*` (S:104).
- In P, 40 flags are `boolean`, but `job_log_chunks.confirmed`,
  `self_service_blueprint_versions.retired` and `triggers.enabled` are
  `bigint` with a 0/1 CHECK.

Fix: one rule per dialect (S: `INTEGER` with the CHECK; P: `boolean`).
1 PR. **Ready**.

### DIA-3. Other divergences

- Foreign key only in P: `update_events (stack_id) REFERENCES stacks (id)
  ON DELETE CASCADE` (P:2286). Same effect through `updates`; add it to S or
  drop it from P.
- `checkpoints.features_json TEXT` (S:487) vs `checkpoints.features jsonb`
  (P:162): the only column with another name.
- The namespace ID format: a CHECK in P (P:759) and two triggers in S
  (S:1711-1721). The trigger text "must start with ns- and name the
  namespace" is wrong: the ID is random and does not name the namespace
  (`pkg/namespace/namespace.go:102-106`). Use a CHECK in S too.
- Defaults only in S, left by `ALTER TABLE ... ADD COLUMN` in the history:
  `namespaces.id` (S:23), `stacks.id` (S:33), `blob_purges.namespace_id`
  (S:333), `backups.target` (S:133). See STR-1.
- Indexes only in P: `backups_started_at_ms` (P:2414),
  `restores_started_at_ms` (P:2418). `inventory_changes_unit` has `at_ms`
  as a key column in S (S:1573) and as `INCLUDE` in P (P:1987).
- Two S tables are not `STRICT`: `temp_admins` (S:558-566, with `BIGINT`
  types and a `principal_id TEXT PRIMARY KEY` that accepts NULL in a rowid
  table) and `maintenance_revocations` (S:1088-1096).
- P puts six control tables in `<schema>_control` (P:2426-2438); S keeps
  them in the main schema. By design.
- Collation: four `grants` columns and 12 indexes are `COLLATE "C"` in P
  (P:352-357). Every other P text column uses the database default
  collation, so `ORDER BY` and range cursors on names can differ from S
  (BINARY) when the database is not created with C collation. Fix: `COLLATE
  "C"` on every key and cursor column in P, or a check at start that the
  database collation is C.

1 PR for all. **Ready**.

### DIA-4. What MySQL adds

MySQL 8 has no direct form for these, counted per baseline (S / P):

| Construct | S | P | MySQL |
| --- | --- | --- | --- |
| Partial indexes (`WHERE`) | 29 | 29 | None. 16 are unique and enforce rules (`*_one_running`, `principals_subject`, `roles_name_key`, `workspaces_live_name`, `jobs_one_change`). Needs a generated column that is NULL out of scope, with a unique index on it. |
| Expression indexes | 3 | 3 | `*_one_running` on `(state = 'running')`: generated column. |
| Text columns in primary keys, unique keys, indexes, foreign keys | 117 PKs, 18 UNIQUE, 91 indexes, 66 FKs | same | `TEXT` cannot be a key without a prefix length: every key column must be `VARCHAR(n)`. The baselines have no length on most ID and name columns, so MySQL needs a length rule per column kind. |
| Default collation | BINARY | C on 4 columns | The MySQL default `utf8mb4_0900_ai_ci` ignores case and accents. Every name, digest and ID comparison, unique key and primary key changes meaning. Every key column needs `utf8mb4_bin`. |
| `GLOB` / regex CHECKs | 8 | 10 | `REGEXP_LIKE`, whose case rules follow the collation. |
| JSON checks | 4 | 3 | `JSON_VALID` / `JSON_TYPE`, or a `JSON` column. |
| `length()` in CHECKs | 35 | 30 | MySQL `LENGTH` counts bytes; use `CHAR_LENGTH`. |
| Text defaults | 87 | 83 | `DEFAULT ('')` expression syntax. |
| Triggers | 2 | 0 | Replace with a CHECK (DIA-3). |
| Control schema | 0 | 1 | A second database. |

The natural-key fixes reduce the MySQL work: fewer text keys, and IDs of a
fixed alphabet and length (ID-1) can be `VARCHAR(n) ... utf8mb4_bin` by one
rule. The partial unique indexes are the largest MySQL cost; DUP-1 and DUP-3
remove some (a NULL-means-absent rule lets a plain unique index on a
nullable column do the work of some partial indexes, because MySQL allows
many NULLs in a unique index). Input to the MySQL RFC (ADR 3, ADR 274
decision 6).

---

## Category 4: Integrity

### INT-1. No `units` table, so no unit reference has a foreign key

**Evidence.** A unit ID is either `stacks.id` or `workspaces.id`. No table
holds all units, so these columns hold a unit ID with no foreign key:
`jobs.unit_id` (S:892), `triggers.unit_id`, `.source_unit_id`
(S:1073, S:1079), `unit_settings.unit_id` (S:1299), `drift_results.unit_id`
(S:1059), `unit_state_versions.unit_id` (S:917),
`unit_final_versions.unit_id` (S:924), `collection_members.unit_id`
(S:347), `inventory_labels.unit_id` (S:678), `gate_objects.unit_id`
(S:1471), `manual_locks.object_id` (S:863), `blob_purges.unit_id` (S:332),
`inventory_changes.unit_id` (S:548), `inventory_deleted.unit_id` (S:263),
`import_units.target_unit_id` (S:648), `import_ledger.item_unit_id`
(S:669). The store and the hand-written purge of each table keep them
right.

Also, where a foreign key exists, it is on the ID alone, not on the pair
(namespace ID, ID). For example `runners.pool_id REFERENCES runner_pools
(id)` (S:743) lets a runner row of namespace A point to a pool of namespace
B; the store code is the only check.

**Bug it allows.** An orphan row after a purge or a failed write. A row that
points across namespaces if one query forgets the namespace. A new table
that refers to a unit and is missing from the purge code.

**Fix.**

1. A `units` table: `(namespace_id, id, tool, created_at_ms,
   deleted_at_ms)`, primary key `id`, unique `(namespace_id, id)`. Each tool
   inserts its unit row in the same transaction as its `stacks` or
   `workspaces` row (`stacks.id` and `workspaces.id` reference it). The
   `inventory` row can become the unit row (ADR 13 already makes it
   transactional with each tool write), which removes one table.
2. Each unit reference becomes `FOREIGN KEY (namespace_id, unit_id)
   REFERENCES units (namespace_id, id)`, exactly the form of ADR 220
   decision 2. The same pair form for pools, jobs, profiles, imports,
   collections.
3. `ON DELETE`: units are soft-deleted (ADR 47), so a purge is the only
   hard delete. Each child declares `ON DELETE CASCADE` (a purge deletes
   it) or `RESTRICT` (a purge must handle it first) explicitly. Audit rows
   keep no foreign key (ADR 60 decision 6). Today only 14 foreign keys in S
   declare `ON DELETE`.
4. The fence carries the namespace ID, and the store checks it in the same
   transaction (RFC 126 door 3).

**Cost.** 3 to 4 PRs, after NK-1. **ADRs.** Carries out ADR 220 decision 2
(owner), which names the `units` table. ADR 13 (projection), ADR 19
(delegated: unit IDs), ADR 47 (soft delete, purge). Label: **Ready**.
One-way door: yes.

### INT-2. No row-level security on PostgreSQL

**Evidence.** P has no `ENABLE ROW LEVEL SECURITY` and no policy. ADR 220
decision 3 (owner) requires both, with `SET LOCAL dustpan.namespace_ids` in
each request transaction and `SECURITY DEFINER` lookup functions.

**Fix.** As ADR 220 decision 3 says. It needs NK-1 (a `namespace_id` column
on each namespace table) first; a policy on a name column would key the
security layer on the name. SQLite has no row-level security; decision 5
(each query names the namespace) is the only layer there.

**Cost.** 2 to 4 PRs. Label: **Ready** (owner decision). Note: this is the
second layer of decision 5; the defense-in-depth rule of CLAUDE.md
applies.

### INT-3. References with no foreign key

- `job_id` with no foreign key to `jobs`: `job_plans.job_id` (S:1032),
  `job_logs.job_id` (S:1040), `drift_results.job_id` (S:1062),
  `unit_final_versions.job_id` (S:928), `runner_claims.job_id` (S:766).
- Notification tables: no foreign key at all.
  `notification_subscriptions.sink_id`, `notification_deliveries
  .notification_id`, `.subscription_id`, `.sink_id`,
  `notification_sink_secrets.sink_id` (S:1194-1271).
- Runner protocol: `runner_claims.operation_id`,
  `runner_handoff_tokens.operation_id`, `.runner_id`,
  `runner_sessions.operation_id`, `.runner_id` (S:762-791).
- `member_removals.group_id` (S:230); `temp_admins.principal_id` (S:559,
  nullable in S); `job_log_chunks.log_id` (S:1048).
- `namespace_id` with no foreign key: `gate_records`, `gate_objects`,
  `imports`, `imports_purged`, `import_ledger`, `import_upload_commits`,
  `profiles`, `issuer_keys`, `issuer_settings`, `inventory_change_heads`,
  `inventory_changes`, `blob_purges`.

Some of these may be on purpose (a log table that must outlive its parent).
Where so, the baseline has no comment that says it. **Fix.** Add the
foreign key, or a comment in both baselines that names the reason and the
code that deletes the row. 1 to 2 PRs. **Ready**.

### Uniqueness in code, not in the schema (checked)

- `notification_sinks` has no unique `(scope, name)`. Whether the code
  enforces one was not checked (unverified).
- `trust_rules_name` is a full unique index on `(namespace, name)`
  (S:1663); a deleted rule (if rules can be deleted softly) would hold its
  name. Not checked further (unverified).

---

## Category 5: Identity and IDs

### ID-1. Six ID generators, four formats

`ident.NewID(prefix)` gives the prefix and 20 random lowercase base32
characters (`internal/ident/ident.go:100-102`) and is used for most objects
(ADR 19). Other generators: saved views, 32 hex digits
(`pkg/savedview/service.go:28-35`); local users and reset codes, hex
(`internal/localusersvc/service.go:454-470`); forge connections, hooks and
credential sets, hex (`pkg/forge/connection.go:106-120`); the registry
upload token, base32 of 20 bytes (`internal/registry/service/service.go:240`,
a token, fine); the import upload, `rand.Text()`
(`internal/unitimport/upload/upload.go:641`).

**Bug.** None today. Cost: several validators and several length rules,
and one more rule per ID kind for MySQL key lengths (DIA-4).

**Fix.** Every object ID comes from `ident.NewID` with its prefix. Tokens
(secrets) are not IDs and keep their own generator. 1 PR. **Ready**.

### Notes (no finding)

- IDs are random, do not sort by time, and carry no installation (RFC 126
  door 4 says keep).
- A namespace ID is never reused (`pkg/namespace/namespace.go:95-100`). A
  subject or role ID of an ended grant is not reused
  (`CountEndedGrantRecords`). A collection created again gets a new ID.
- Built-in roles use their fixed name as ID. They cannot be renamed or
  deleted, so this is not a natural-key bug.
- Tombstone tables (`inventory_deleted`, `imports_purged`, `blob_purges`)
  key on the stable ID.
- Tables with no generated ID as key: `namespaces` (NK-1), `stacks` and
  `inventory` (NK-15), `collections` (NK-9), `self_service_blueprints`
  (NK-10), `notification_cursors (name, scope)`, `durable_schedules (name)`
  and `git_host_app_registrations (kind)`. The last three are named in code
  and are not user entities.

---

## Category 6: Derived or hand-kept data

### DRV-1. The plan-staleness check reads a table that no production path writes

**Evidence.**

- `unit_state_versions` (S:916-921) holds "the current state version of
  the unit ..., as the state write of its tool does"
  (`pkg/store/sqlite/jobs.go:41-44`).
- Its only writer is `PutUnitStateVersion` (`pkg/store/sqlite/jobs.go:45`,
  `pkg/store/postgres/jobs.go:48`). Only tests call it: `rg
  PutUnitStateVersion` outside `_test.go` and `internal/db` finds the two
  definitions and nothing else.
- Its readers are the plan gate (`pkg/store/internal/jobstore/record.go:348`),
  the apply check (`rules.go:173`) and the event path (`events.go:57`).
  `job.CheckPutPlan` (`pkg/job/record.go:494-510`) compares the version that
  the plan phase read with the current one. Both are `""`, so the check
  passes.
- `unit_final_versions` has the same shape: its writer `PutFinalVersion`
  runs only from `ObserveStateVersion`
  (`pkg/store/internal/jobstore/apply.go:244`), and no production code calls
  `ObserveStateVersion` (only `pkg/job/jobtest`).
- The current version is already kept by each tool:
  `workspaces.current_state_version_id` (S:102) and the newest `updates`
  row of a stack.

**Bug.** If the plan-then-apply path runs end to end (not verified; the
runner path may not be wired fully), an apply phase runs on a plan made
against an older state after another write changed the state: the
staleness guard is inert. A job that waits for a final version (write-back)
is never closed by a state write. This is the class "derived data that
drifts", in its worst form: a copy with no writer.

**Fix.** No copy table. The current state version is a column of the unit
row (INT-1), written by each tool in the same transaction as its state
write (the `workspaces` and `updates` writes), the same way the inventory
projection is written. The job store reads it from there.
`unit_final_versions` gets its writer in the same transaction, or goes if
the unit row holds it. A conformance case writes state through each tool's
port and checks that a stale plan is refused.

**Cost.** 1 to 2 PRs. **ADRs.** ADR 13 (projection written in the same
transaction as each tool write). Label: **Ready**.

### Notes

- The central table list, the backup classes and the hand-summed
  `restoreModel()` are survey cut 6. This audit found no new case.
- Counters kept next to their rows (`inventory_change_heads.last_seq`,
  `notification_sequences.last_sequence`, `settings_state.revision`,
  `workspace_blame_cursors.ordinal`) are written in the same transaction as
  the rows they count. No drift found.
- `grants.object` and `audit_entries.object` are name snapshots by design
  (ADR 60 decision 6). The grant list rewrites unit names to the current
  name (`pkg/access/service.go:1762`).

---

## Category 7: Other structural

### STR-1. ID columns with `DEFAULT ''`

**Evidence.** The S baseline is regenerated from numbered migrations and
keeps their `ALTER TABLE ... ADD COLUMN` shape: `namespaces.id TEXT NOT NULL
DEFAULT ''` (S:23), `stacks.id TEXT NOT NULL DEFAULT ''` (S:33),
`blob_purges.namespace_id ... DEFAULT ''` (S:333). P has no default.
`schema_migrations` itself is created and then altered (S:9-16).

**Bug.** In S, an insert that forgets the ID succeeds with `''`. The unique
index on `stacks.id` then lets exactly one such stack exist, and child rows
can reference `''`. The namespace trigger rejects `''` for namespaces only.

**Fix.** Write each table of the S baseline as one `CREATE TABLE` with its
final columns, with no default on an ID column. ADR 322 lets the baseline
change in place before the tag.

**Cost.** 1 PR. **ADRs.** ADR 322 (owner). Label: **Ready**.

---

## Natural-key findings (all, for the move to IDs)

Each line: the stored reference, what it should hold, the label.

| ID | Stored reference | Should hold | Label |
| --- | --- | --- | --- |
| NK-1 | `namespaces` primary key `name`; `namespace` in `stacks`, `secret_keys`, `inventory`, `workspaces`, `inventory_deleted`, `collections`, `collection_members`, `inventory_labels`, `manual_locks`, `placement_rules`, `principals`, `trust_rules`, `audit_entries`, `audit_sink_cursors`, `grants`; `triggers.source_namespace` | namespace ID | Ready |
| NK-1 | `scope` of the 8 notification tables | namespace ID (NULL for the installation) | Ready |
| NK-2 | `secret.Scope.Namespace` in the associated data; `secret_keys` primary key | namespace ID | Ready |
| NK-3 | `job.Spec.Profile`, `imports.profile` | profile ID | Needs RFC (ADR 202 decision 9) |
| NK-4 | Group names in Cedar policy text (`OpenDustpan::Group::"<name>"`) | group ID | Needs owner |
| NK-5 | Gate scope keys `namespace/<name>`, `collection/<ns>/<name>`, `profile/<ns>/<name>`, `blueprint/<name>/<v>` | IDs in every segment | Needs RFC + owner (ADR 224 decision 1) |
| NK-6 | Audit chain key `(namespace, sequence)`; empty `object_id` for non-unit objects | namespace ID; object ID for every kind | Ready |
| NK-7 | `grants.namespace`; grant target `namespace:<name>` | namespace ID | Ready |
| NK-8 | Notification filters with unit references as text | unit or collection ID (patterns stay patterns) | Needs RFC (filter form) |
| NK-9 | `collections` primary key `(namespace, name_key)`; `collection_members.collection_key`, `placement_rules.collection_key`, `inventory_changes.collection` | collection ID | Ready |
| NK-10 | `self_service_blueprints` primary key `(namespace_id, name)`; versions; `object_context` blueprint `object_id` | blueprint ID | Needs RFC (ADR 112 decision 1) |
| NK-11 | `manual_locks.holder`, `break_glass_reviews.use_subject`, `use_people`, `verdict_reviewer`, `idempotency_records.principal` | principal ID | Ready |
| NK-13 | `audit_sink_cursors.sink`, `audit_sink_leases.sink` (operator name, defaults to the kind) | sink name plus destination identity | Ready |
| NK-14 | Unit name comparison in `PhaseClock`; names in the job body | unit ID (names as display snapshot) | Ready |
| NK-15 | `stacks` primary key `(namespace, project, name)`; `inventory` primary key `(namespace, tool, name)` | unit ID; live-name partial unique index | Ready |
| NK-16 | `namespaces.primary_installation` (installation name) | unchanged | Needs RFC + owner (ADR 248 decision 5); not proposed |

NK-12 (stack rename has no audit entry) is in the natural-key section
because it is the event that every name key must survive. It is not a
stored reference.

## Items that need an owner decision

1. **NK-5, gate scope keys.** ADR 224 decision 1 (owner) keeps the
   namespace and collection segments as names. Moving them to IDs (as ADR
   317 did for units and ADR 313 did for profiles) needs an RFC and the
   owner. The blueprint form needs NK-10 first.
2. **NK-4, group IDs in Cedar policy text.** No owner decision fixes the
   group UID, but the stored policy form ships with the first tag. The
   owner should confirm "IDs in stored policy text, names at the API".
   Survey cut 5 raised the same question.
3. **NK-16, the installation name.** ADR 248 decision 5 (owner) decides a
   name with no ID, for a stated reason. Listed so that the owner sees every
   natural key. The audit does not propose a change.
4. **Order of NK-1, INT-1 and INT-2 against RFC 103 step 8 and ADR 274
   decision 2 (owner).** These three edit every store area. If the store
   moves to `internal/store` first (survey cut 6 and cut 13), they conflict
   with that move. The owner should choose the order: the key change first
   (smaller diffs per area, no move conflicts after), or the move first.

Items that need an RFC but not the owner: NK-3 (ADR 202 decision 9), NK-8
(filter form, ADR 145), NK-10 (ADR 112 decision 1).

## One-way doors

All of NK-1 to NK-11, NK-15, INT-1 and DUP-1 to DUP-3 change stored forms.
After the first tag each needs a migration and data conversion (ADR 72,
ADR 322). NK-2 (sealed data) and NK-4, NK-5, NK-6 (stored policy text, policy
IDs and the audit hash encoding) cannot be converted without a rewrap or a
re-hash. Before the tag, each is an edit of the baselines and a `dev reset`.

## Method

- Read the S baseline in full. A script diffed S and P table by table
  (columns, types, defaults, CHECKs, keys, foreign keys, indexes), and each
  divergence was read in the source.
- Read `docs/adr/INDEX.md`, ran `rg` over `docs/adr` and `docs/rfc` for
  namespace, unit, grant, group, collection, profile, blueprint, audit, ID
  and backup terms, and followed each "Changed by" chain. The quotes above
  were read in the ADR files: ADR 220 decisions 1 to 3, ADR 224 decision 1,
  ADR 217 decision 3, ADR 202 decision 9, ADR 112 decision 1, ADR 60
  decision 6, ADR 248 decision 5, ADR 313 decision 3, ADR 317 decision 1,
  RFC 126 doors 1 to 3.
- For each name-keyed column: the writer, the readers, whether a rename or a
  delete-and-recreate exists, and the bug, with file and line. Claims that
  were not traced end to end say "unverified".
- `rg` for every caller of `PutUnitStateVersion`, `ObserveStateVersion`,
  `checkGroup` and `targetOf`.
- Counts are from `main` at `607c981`. Line numbers are of that commit.
