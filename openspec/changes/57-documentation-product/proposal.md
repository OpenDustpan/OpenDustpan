---
version: 1
---

## Why

**Persona: developer.** A Developer tries to run the first setup from the docs page alone and get it right the first time. The roadmap row for documentation as a product waits on its research, so the scope is not set yet.

The user path this change must make work:

1. **Docs page.** The Developer reads the user documentation to install and run the first stack or workspace.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A docs page names a step that the server does not support, and the command fails. | The page is wrong. Run the docs check to find the page. | `make docs-lint` |

Evidence for the claims above:

- RFC 57 Documentation as a product waits on its research and is not shipped. Proof: docs/roadmap.md:831 row: [RFC 57](docs/rfc/0057-documentation.md) Documentation as a product | its research

## What Changes

- Record 57-documentation-product as the coarse placeholder for RFC 57. Refine it when the research ends.

Out of scope:

- RFC 61 docs site and landing page
- The docs pages of single features

## Usability

Where the path still costs the person time or surprises them:

- Docs gaps cost the Developer time until the research ends.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main refuses a restore of a dev backup into a production installation. (`pkg/backup/restore_service_dev_test.go:100 (TestRestoreRefusesDevBackupIntoProduction)`)
- Main scheduler waits the first delay before its first claim. (`pkg/durable/scheduler_test.go:175 (TestSchedulerWaitsTheFirstDelayBeforeItsFirstClaim)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. What does documentation as a product cover: architecture, versioning, search, quality checks and ownership?
   - Options: Treat docs as a versioned product with architecture, search and task-based checks; Keep docs as separate pages for each feature with no product standard.
   - Provisional decision (owner review 14 Nov): Treat docs as a versioned product with architecture, search and task-based checks.
   - Reason: The roadmap row waits on its research. The pack does not answer the scope. Benchmark: Google: versioned reference docs with search and a documented review rule for each page.
   - Persona impact: A product standard helps the Developer finish setup from the docs page alone. Separate pages cost time and cause retries. Escape hatch: The server works without this standard. Existing pages stay valid.

Questions considered and answered:

- Does any accepted ADR in the pack decide documentation as a product? No. The pack lists ADRs on namespaces, tool models, restore, state reads, soft delete, federation and sign-in. None of their quoted decisions sets documentation structure, versioning, search or quality checks.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
