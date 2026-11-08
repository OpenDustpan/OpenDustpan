# 286. Self-service, failover and promotion: three renames

Status: accepted · 2026-10-31. Decisions 1 to 3 are owner decisions of 31 October 2026. Decisions 4 to 7 are PM decisions under the owner's delegation, of 31 October 2026, and follow from decisions 1 to 3. Changes the words of ADRs 4, 100, 110 to 117, 128, 144, 160, 166 and 181. Changes the words of ADRs 199 to 201, 203 to 205, 207, 210, 211, 213 and 218. Changes the words of ADRs 220, 222, 224 to 226, 228 to 230, 248, 271, 273 and 280. Changes the words of RFC 55

## Context

Three words in `GLOSSARY.md` do not fit their concepts:

- **Catalog** names the self-service gateway (ADR 110). The code also says
  "catalog" for other fixed lists: the catalog of error codes
  (`internal/errcode`), the tool catalog of the agent (`agent.Catalog`), the
  catalog of built-in roles, the catalog of event types (ADR 145), the
  section catalog of the context API, the action catalog of unmanaged
  resources and the catalog of code rules. A reader cannot see from the word
  which one a sentence names.
- **Promotion** names the operator action that makes a replica installation
  the primary installation of a namespace (ADR 248). The usual word for this
  action is "failover". ADR 248 already says "manual failover" in its title,
  and the number of the action is the **failover version**.
- RFC 55 (environment promotion) needs a word for the ordered path from a
  test unit to a production unit. Because "promotion" had another meaning, its
  proposal of 30 October 2026 used **Lane** and **Advance**.

No tag shipped. There are no users, installations or data (ADR 73).

The owner made decisions 1 to 3. The product manager made decisions 4 to 7
under the owner's delegation. The owner delegates decisions of this kind to
the product manager, who decides by what Google, AWS, PCI DSS, SOC 2 or ISO
27001 do and records the benchmark. The owner may overturn them.

## Decision

1. **Catalog becomes Self-service (owner).** The request gateway is
   **Self-service**. A **Catalog request** is a **self-service request**.
   The private registry (ADR 257, ADR 269) stays its own concept and its own
   menu item, **Registry**.
2. **Promotion of an installation becomes failover (owner).** The action is
   **failover**: a **planned failover** and a **forced failover**. Copy says
   that the server never fails over on its own: an operator starts each
   failover. **Failover version** keeps its name.
3. **Promotion goes to environment promotion (owner).** RFC 55 uses:
   - **Promotion path**: the ordered path of units from dev to prod.
   - **Promote**: the change that moves a change one step along a promotion
     path. One such change is a **promotion**.
   - **Comparison**: the difference between two steps.

   The owner rejects the word "Lane". "Advance" goes with it.
4. **Every layer takes the new words, with no alias (PM decision under the owner's delegation).** Code, API paths, actions, wire
   values, SQL tables, file and package names, docs and the console take the
   new words. No alias, no shim and no data conversion keeps an old word
   (ADR 73). ADR 73 freezes the SQL migration that made the tables of
   blueprints, so a new numbered migration in each dialect renames the tables.
   The collapse before the next tag merges it.
5. **The spellings in code (PM decision under the owner's delegation).** Go identifiers cannot hold a hyphen, so each
   layer has one spelling:

   | Layer | Old | New |
   | --- | --- | --- |
   | Go package and type names | `catalog`, `Catalog` | `selfservice`, `SelfService` |
   | Actions, route segments, wire values, Git directory | `catalog` | `self-service` |
   | SQL tables | `catalog_` | `self_service_` |
   | Go names of the failover | `Promote`, `promotion` | `Failover`, `failover` |
   | Promotion paths (RFC 55) | `lane`, `Lane`, `lan-`, `advance` | `promotion`, `promotion.Path`, `pph-`, `promote` |

6. **Other uses of "catalog" stay (PM decision under the owner's delegation).** "Catalog" stays a common noun for a
   fixed list: the catalog of error codes, the tool catalog, the role
   catalog, the catalog of event types, the section catalog, the action
   catalog and the catalog of code rules. It is no longer the name of a
   feature. The Software Catalog of Backstage (ADR 247) keeps its own name.
7. **Other uses of "promote" in the code change (PM decision under the owner's delegation).** The word now belongs to
   promotion paths, so the code does not use it for other things:
   - The rotation of the signing keys of an issuer (ADR 204) makes a `next`
     key the `active` key. The code calls this "promote" and the rotate
     answer has the field `promoted`. The new word is **activate**, and the
     field is `activated`.
   - The registry service copies a staged file to its content key in a
     function `promote`. The glossary entry **Staged file** says that a
     publish "moves" it. The function takes that word.
   - Field promotion of an embedded Go struct is a term of the Go language.
     It stays.
   - ADR 237 and RFC 75 say "no promotion of dev data" for a move of the
     data of a dev server to a production installation. These texts are
     accepted and stay. New text says "move dev data to a production
     installation", as ADR 253 does.

## Inventory

The counts come from `rg` on `main` at `e6c8c3e6`. A count of lines is the
number of lines with a match, case-insensitive.

### Self-service (decision 1)

The gateway has a domain package and a store. It has no HTTP handler, no
route in `openapi.json`, no metric and no error code yet.

| What | Where | Count |
| --- | --- | --- |
| Domain package and suite | `internal/catalog/`, `internal/catalog/catalogtest/` | 31 files, 911 lines |
| Store code | `pkg/store/internal/catalogstore/`, `pkg/store/{sqlite,postgres}/catalog_blueprints*.go`, `backup_catalog.go`, `pkg/backup/portable_catalog.go` | 25 files in `pkg/store`, 659 lines |
| SQL tables | `catalog_blueprints`, `catalog_blueprint_versions` | 36 and 61 lines |
| Frozen migrations | `pkg/store/{sqlite,postgres}/migrations/0153_catalog_blueprints.sql`, `postgres/migrations/0100_baseline.sql` | 3 files |
| Migration records and hashes | The name record `pkg/store/schema/migrations/0153_catalog_blueprints.sql` and the file `0153_catalog_blueprints.sql.sha256` in each dialect. They stay with the frozen file. The new migration gets its own record and hashes. | 3 files |
| Agent instructions | The layout line of `CLAUDE.md` and `AGENTS.md` that names `catalog/` under `internal/` | 2 lines |
| Actions | `catalog:request`, `catalog:render`, `catalog:read`, `catalog:read-options` | 17, 14, 8 and 3 lines |
| Durable operation kinds | `catalog.start`, `catalog.quota`, `catalog.expire` | 3 |
| Wire values | `job.SourceCatalog` (`catalog`), `gate.SourceCatalog` (`catalog`), `forge.ConsumerCatalog` (`catalog`), `gate.ChangeCatalogRevision` (`catalog-revision`), `policy.KindCatalogRequest` (`catalog.request`), `catalog.GitRoot` (`catalog`) | 6 |
| Other Go files that name the gateway | for example `pkg/job`, `pkg/gate`, `pkg/forge`, `internal/policy`, `internal/quota`, `internal/agent/agenttest`, `e2e/` | 114 files with any "catalog", about 40 of them for the gateway |
| Route paths in ADRs | `/api/dustpan/v1/catalog/...`: blueprints, requests, options, render, check, policy | 14 paths |
| Accepted ADRs from 100 to 199 | 100, 110 to 117, 128, 144, 160, 166, 181, 199 | 15 files |
| Accepted ADRs from 200 | 200, 201, 203, 205, 207, 210, 211, 213, 218, 220, 222, 224 to 226, 228 to 230, 271, 273 | 19 files |
| RFCs | Open: 5, 12, 16, 43, 55, 56, 82, 111. Accepted or decided: 14, 23, 74, 88, 89, 103. | 14 files, 77 lines |
| User docs | `docs/reference/jobs-api.md` (`catalog` source), `docs/reference/git-host-api.md` (`catalog` consumer) | 2 lines |
| Roadmap | `docs/roadmap.md`: the console step that opens the catalog, the console row of blueprints ("catalog and create form") and the row of the shared job model | 3 lines |
| Glossary | **Catalog**, **Catalog request**, **Job source kind**, **Storage limit**, **Author (change order)**, **Message file**, the flagged ambiguities | 9 lines |

These uses stay (decision 6): `internal/errcode` (30 files, 95 lines),
`agent.Catalog`, the role catalog in `pkg/access`, `notify.Types`,
`objectcontext.SectionCatalog`, `unmanaged.ActionCatalog`, and the error
catalog in `internal/api/problem` and `internal/api/apidesc`.

### Failover (decision 2)

The failover is not built yet. Only comments and docs name it.

| What | Where | Count |
| --- | --- | --- |
| Go comments | `pkg/namespace/namespace.go` (2), `pkg/store/postgres/postgres.go` (3), `pkg/store/postgres/audit.go` (1) | 6 lines |
| SQL comments | `pkg/store/postgres/queries.sql` (2), which sqlc copies to `internal/db/queries.sql.go` | 2 lines, then `make generate` |
| Routes, actions, metrics, error codes, API values | none | 0 |
| Accepted ADRs | 4, 204, 218, 248, 271, 280 | 6 files, 38 lines |
| RFCs | Draft: 37. Accepted: 88, 89. Dropped: 79. | 4 files, 64 lines |
| User docs | `docs/guides/connect-github.md` (1), `docs/guides/several-instances.md` (1) | 2 lines |
| Roadmap | `docs/roadmap.md` | 1 line |
| Glossary | **Promotion**, **Primary installation**, **Divergent tail**, **Failover version** | 7 lines |

### Other uses of "promote" (decision 7)

| What | Where | Count |
| --- | --- | --- |
| Issuer key rotation | `pkg/profile/` (46 lines in 4 files), `internal/issuersvc/` (32 lines in 4 files), `internal/api/issuer/` (5 lines), the field `promoted` in `openapi.json` (2) and `clients/typescript/src/` (3) | 88 lines |
| Issuer docs | `docs/guides/oidc-issuer.md` (2), `docs/reference/issuer-api.md` (2), glossary **Copy check** (1) | 5 lines |
| Registry staged files | `internal/registry/service/publish.go`, `module.go` | 6 lines |
| Go field promotion (stays) | `internal/api/apidesc/fields.go`, `pkg/dustpan/restore_ports_internal_test.go` | 3 lines |

### Promotion path (decision 3)

RFC 55 holds all of "Lane" and "Advance". No code has them. This pull
request changes RFC 55 and `GLOSSARY.md`.

## The plan: one pull request for each conflict group

Each later pull request is mechanical. It renames, runs `make check`, and
changes no behavior.

| Item | Pull request | Conflict group | Depends on |
| --- | --- | --- | --- |
| R0 | This ADR, `GLOSSARY.md`, RFC 55 and its line in the RFC index | `glossary` | none |
| R1 | The status lines of the ADRs that this ADR lists | `adr-status` | R0 |
| R2 | Self-service in code. Move `internal/catalog` to `internal/selfservice`. Rename the store files and the backup table list. Add a migration in each dialect that renames the two tables, with its name record and hashes. Rename the four actions, the durable kinds, the six wire values and the Git directory, with the tests and e2e. Change the layout line of `CLAUDE.md` and `AGENTS.md`. Change `docs/reference/jobs-api.md` and `docs/reference/git-host-api.md` for the new values. | `catalog-core`, `store-migrations`, `identity-actions` | R0. Rebase or merge the open pull request of the Expr evaluator in `internal/catalog` first |
| R3 | Failover and the other uses of "promote" in code. Change the comments and `queries.sql`, then run `make generate`. Rename the issuer key rotation and the field `activated`, then run `make clients-generate`. Rename the function of the registry service. Change `docs/reference/issuer-api.md`, `docs/guides/oidc-issuer.md`, `docs/guides/connect-github.md` and `docs/guides/several-instances.md`. | `issuer`, `api-description` | R0 |
| R4 | Docs: the open RFCs and `docs/roadmap.md` | `docs` | R2, R3 |
| R5 | The console (`OpenDustpan/console`): labels, routes, the issuer field `activated` and the menu item "Self-service" next to "Registry" | console | R2, R3 |

R2 and R3 touch different files, so they can run in parallel. Each of them
changes the user docs of the values that it renames, in the same pull
request (ADR 214). R4 comes after them, so that the RFCs name the code that
exists. R1 can merge at any time after R0. R5 needs R2 and R3, because the
console reads the new values of both.

This pull request and the open pull request of ADR 285 both change
`GLOSSARY.md`. After that pull request merges, merge `main` into this
branch and fix the conflict. Do not rebase.

## Consequences

- One word names one feature. "Catalog" is a common noun for a list again.
- An operator reads "failover" for the action that ADR 248 decides, and the
  failover version follows from it by name.
- RFC 55 can be accepted with its words.
- Accepted ADRs keep their old words in the body (ADR 1). Each status line
  points to this ADR. A reader uses this ADR to map the words.
- The open pull requests that touch `internal/catalog` conflict with R2.
