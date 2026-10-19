# 218. What a namespace is for, and when to make a namespace or a collection (owner)

Status: accepted · 2026-10-18 · decides the backup of one namespace that ADR 30 decision 1 left out. The region of decisions 1, 2 and 5 amended by ADR 248 (primary installation)

## Context

The project owner asked on 2026-10-18 what a namespace is for on a
self-hosted installation, and when a team makes a new namespace instead of
a collection. The project manager relayed the owner's answer: a namespace
is the fence for access, audit, backup and restore, blueprints, profiles
with their OIDC issuer, settings and limits. One namespace can be backed up
and restored (RFC 8). A blueprint never serves another namespace (RFC 14).

What the accepted ADRs already give a namespace:

- ADR 4: a home region and a failover version. Each write in a namespace
  carries a `namespace.Fence`, and the store checks it in the same
  transaction.
- ADR 18: each tool calls a namespace its organization.
- ADR 51: a collection is a named set of units in one namespace. Each unit
  is in one collection. Collections do not nest. A collection can be the
  object of a grant.
- ADR 112 decision 1: a blueprint lives in a namespace.
- ADR 201: a profile never imports from another namespace.
- ADR 204: one OIDC issuer and its own signing keys for each namespace.
- ADR 163: a namespace link lets the classification of one namespace read
  the units of another.
- ADR 30 decision 1: a backup holds the whole installation. RFC 8 lists a
  backup of one namespace as a proposal.

ADR 215 makes the namespace the tenant of OpenDustpan Cloud. On the hosted
service, a namespace also holds its own principals, groups and sign-in.

## Decision

### What a namespace is

1. **A namespace is a boundary (owner).** It is the smallest object that
   has each of these of its own:

   | Boundary | What it means | Source |
   | --- | --- | --- |
   | Writes | Each write carries the fence of the namespace. A promotion or a restore ends the old writers of that namespace only. | ADR 4 |
   | Region | One home region for each namespace. | ADR 4 |
   | Access | A grant on the namespace reaches its collections and units. The IP allowlist and the reach of a token have the namespace as object. | ADR 52, ADR 70 |
   | Members and sign-in | With `identity.principalHome: namespace`, its own principals, groups, custom roles and identity provider. | ADR 215, ADR 216 |
   | Tenant | The tenant of OpenDustpan Cloud, shown as "account" in the hosted console. | ADR 215 |
   | Audit | Each namespace is an audit scope, read with `namespace:read-audit`. | ADR 60 |
   | Backup and restore | One namespace can be backed up and restored alone (decision 4). | owner, RFC 8 |
   | Blueprints | A blueprint lives in one namespace and makes units only in that namespace (decision 3). | owner, ADR 112, RFC 14 |
   | Profiles | A profile imports only profiles of its own namespace. | ADR 201 |
   | Cloud trust | One OIDC issuer and its own signing keys. A cloud trust policy that names the issuer of a namespace never matches a token of another namespace. | ADR 204 |
   | Settings and limits | A namespace layer of settings, and its own storage limit and request rate bucket, each only lower than the installation value. | ADR 220 |
   | Keys | One namespace key, and an optional customer key, wrap the data keys and item keys of the namespace. | ADR 217 |
   | Tool name | Each tool calls it an organization. | ADR 18 |

2. **A collection is not a boundary.** A collection groups units inside one
   namespace for access and for views. Units of different collections in
   one namespace share the fence, the region, the issuer, the profiles,
   the blueprints, the backup and the limits of the namespace.
3. **A blueprint never serves another namespace (owner).** A request of the
   catalog (ADR 110) in namespace A can use only the blueprints of A. The
   server answers a blueprint of another namespace as not found, as for a
   name that does not exist. A platform team that serves many namespaces
   publishes the blueprint in each namespace, for example from one git
   repository (ADR 111). RFC 14 asked "can units in other namespaces use
   it?": no.

### The backup and restore of one namespace

4. **One namespace can be backed up and restored (owner).** This adds to
   ADR 30, which backs up the whole installation.
   - A namespace backup holds each row whose namespace ID is the namespace,
     the blobs that the rows name, and the namespace key records, each
     wrapped as ADR 217 says. It uses the portable format of ADR 42, with
     the scope `namespace` in the manifest.
   - It holds the grants on the namespace and on its collections and units.
     It holds the principals, groups, custom roles and sign-in settings
     whose home is the namespace (ADR 215 decision 9). It holds no
     principal and no group of the installation.
   - **Item keys go only to a named backup target.** A namespace backup
     writes item key blobs only to a backup target that the `backups`
     table names, as ADR 151 does. So a shred still reaches each copy. A
     download of a namespace backup through the API holds no item keys. It
     restores only into its own namespace, while the item keys are still
     on the installation.
   - The actions are `namespace:backup` and `namespace:restore`, in the
     data family. The namespace role `admin` holds them. A restore that
     replaces grants also needs `namespace:restore-access` (ADR 216).
   - A restore replaces the namespace in place. The namespace
     refuses new writes while the restore runs, as maintenance mode does
     for the installation (ADR 38 decision 4), and the restore adds one to
     the failover version of the namespace (ADR 4). So a late write of an
     old writer fails at the fence.
   - A restore never replaces the principals, tokens, sessions or sign-in
     settings of the namespace. It keeps the current ones. It restores a
     grant only for a principal that exists now and is not disabled. The
     restore record lists each grant that it skips.
   - A restore applies each redaction and tombstone that the live
     installation holds (ADR 151), and keeps each namespace key version and
     the current customer key setting of the live namespace. It never
     replaces them with the records of the backup.
   - ADR 204 decision 4 applies: a restore into the same installation
     keeps the issuer keys. The sign-in secrets of the namespace stay
     wrapped by the key-encryption key (ADR 217 decision 6), so they
     restore only into the same installation.
   - RFC 76 keeps open: a restore as a copy into a new namespace, schedules
     for each namespace, and an export to a target of the tenant.

### When to make a namespace or a collection

5. **Make a new namespace when the units need a boundary of their own.**
   Make one when at least one of these is true:
   - The units need a cloud trust that the other units must never get, for
     example production and development in cloud accounts that trust
     different issuers.
   - The units need a restore that must not touch other units, or their
     own backup schedule.
   - The units need another home region.
   - Different people read the audit log of the units.
   - Profiles, blueprints or the classification of unmanaged resources must
     never be shared with the other units, also not by mistake.
   - The units need their own storage limit or request rate.
6. **Make a collection when only access or views differ.** Make one when
   the same people govern the units, the units share one cloud trust, and
   the teams want shared profiles, shared blueprints, one dependency graph
   and one backup. A collection gives each team its own grants and its own
   view in the inventory.
7. **The default.** One namespace for each group of people that answers
   for its own production, and one collection for each application or
   service inside it. A new namespace costs more than a collection: each
   boundary of decision 1 is one more thing to set and to back up. A
   namespace can be split later only with a move of units (RFC 66).
8. **The guide.** A page in `docs/explanation/` gives decisions 1, 2, 5, 6
   and 7 with an example: a platform team, two product teams, production
   and development. It ships with the first change that adds a namespace
   backup route (ADR 214).

## Benchmarks

Designed against these, as a comparison only, never a compliance claim:

- ISO/IEC 27001:2022 Annex A 8.13 (information backup): decision 4.
- AWS Organizations, accounts as the boundary of trust and of blast radius,
  and Google Cloud projects, as a comparison for decision 5.

## Not decided here

- The move of units from one namespace to another (RFC 66).
- The open parts of decision 4 (RFC 76).

## Consequences

- `pkg/backup` gets a scope (installation or namespace) and a table list
  for each scope. A coverage test fails for a table that holds a
  namespace ID and is not in the namespace list.
- `GLOSSARY.md` gets **Namespace backup**, and the entries **Namespace** and
  **Collection** say what each one is for.
- The catalog refuses a blueprint of another namespace with a not-found
  error, and `storetest` checks it.
