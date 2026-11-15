---
version: 5
---

Issue: none. This change is the follow-up of pull request #3251 (change `add-job-service-routes-start-1`) and part of issue #1315, which lists RedactLog among the calls that answered ErrUnsupported.

Depends on: pull request #3251 merged and its change archived. The delta `specs/job-actions` modifies a capability that the archive of #3251 creates. The delta `specs/jobs` and the tasks use the names that #3251 adds (`LogReplacement`, `RedactRequest`, `service_redact.go`, the route `log-redactions`).

## Why

**Persona: namespace administrator.** A namespace administrator finds a secret in the log of a job that ended. The administrator must remove the secret from that log, and the server must prove that the old content is gone and who removed it.

The user path this change must make work:

1. **API.** Post the text to remove and a reason to `POST /api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}/log-redactions`. The answer holds the new log version and the number of matches.
2. **API.** Read the log of the step. It holds the marker `[redacted]` where the text was. The old version answers `secret.ErrShredded` to every read, and the metadata of both versions stays.
3. **API.** Read the audit log. One entry with the action `unit:redact` holds the job, the two versions, the number of matches and the reason. It never holds the text.
4. **Docs page.** Follow the redact section of `docs/guides/jobs.md` and run each command.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| The version is not a log version of this job. | 404 with code `job.not-found`. | `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}` |
| The step still runs, or the version is redacted already. | 409 with code `job.wrong-state`. The text of the answer names the cause. | `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}` |
| The caller holds `job:read` but not `unit:redact`. | 403 with code `access.denied` names `unit:redact`. | `GET /api/dustpan/v1/namespaces/{namespace}/access/grants` (the grant list of the namespace; a namespace administrator adds the grant) |
| The server has no job artifact store. | 501 with code `job.unsupported`. After this change, only a server with no blob store answers this. | `opendustpan doctor` |

The two scenarios "Redact a secret" and "Unknown version" of #3251 cannot pass on a real server today. Evidence, each with the command that proves it:

- The job service on the head of #3251 gets `Artifacts: nil`. Proof: `git show origin/exec/m5/add-job-service-routes-start-1:pkg/dustpan/jobservice.go | rg -n "Artifacts: nil"` prints line 79, with the comment "No adapter of job.ArtifactStore exists yet".
- No type implements `job.ArtifactStore`. Proof: `rg -n "ArtifactStore" --glob '*.go' pkg internal | rg -v "^pkg/job/artifact.go"` prints three lines, all comments (`pkg/job/store.go:494`, `pkg/job/artifactreader.go:19`, `pkg/job/jobtest/jobtest.go:13`).
- No SQL store implements `history.Redactor` or `history.RedactionLog`. Proof: `rg -n "func \(.*\) (Redact|AppendRedaction)\(" --glob '*.go' pkg internal` prints only the reference adapters in memory (`Redact` at `pkg/job/jobtest/redactor_mem_test.go:90` and `internal/history/historytest/mem_test.go:469`, `AppendRedaction` at `mem_test.go:204` and `redactor_mem_test.go:372`) and `rg -il redaction pkg/store --glob '*.sql'` prints nothing: no table holds a redaction.
- The job record holds one log version for each step record. Proof: `pkg/job/step.go:108` declares `Log *LogRef` and `pkg/store/internal/jobstore/record.go:740` (`logOfRow`) finds a version only as `r.Log.ID == row.ID`. After a redaction writes the next version, the older version has no place in the record, and a retry cannot find it.
- The action `unit:redact` has no family, so only a principal with the `Admin` flag passes the authorizer. Proof: `pkg/access/access.go:115` (`families`) has no key `identity.ActionUnitRedact`, `pkg/access/authorizer.go:127` (`FamilyOf`, then `deny`) denies an action that has no family, and `pkg/access/access_test.go` (the list that ends with `identity.ActionUnitRedact`) asserts "want none until its routes land".

## What Changes

- Add the SQL adapter of `history.RedactionLog` for SQLite and PostgreSQL (two new tables for each dialect), with the suite `historytest.RunRedactions` that main already has.
- Add the job record rows and store methods that write the next version of a log and that mark a plan or a log version redacted, in one fenced transaction with the shred of its item key, the redaction record and the audit entry.
- Add the adapter `job.ArtifactStores` of `job.ArtifactStore` over the job store, the blob store (`job.ArtifactBlobs`) and the item sealer, with the suites `historytest.RunRedactor` for plans and log versions and a new suite for `ReplaceLog`.
- Keep every earlier log version of a step in the step record, and add `LogRef.Replaces`, so that a retry after a failed redaction only redacts the older version, as `Service.RedactLog` documents.
- Wire the adapter in `pkg/dustpan/jobservice.go` in place of `Artifacts: nil`.
- Put `unit:redact` in a new family `redaction` of `pkg/access/access.go`, and give the family to the built-in role `admin` only. No other built-in role holds it, `account-administrator` and `site-administrator` included. A person who needs it gets it through a custom role or a grant of `admin`.
- Name the code actions and the code error in the spec of #3251: `job:cancel`, `job:approve`, the start action of the job for a confirmation, `unit:redact`, and `job.not-found`.
- Add the e2e step and the docs section.

Out of scope, each its own change:

- The shred operation of the history model, which deletes the content blobs and writes the ledger objects after the commit. This change leaves each shred target `pending`. Until that change lands, a redacted version is unreadable because its item key is shredded, and the blobs stay in the store.
- The retention expiry that redacts items with the cause `retention`.
- The redaction of a plan by a route (`POST .../plan/redact` of ADR 102 decision 9). The adapter implements `Redact` for plans, so that route needs no new store code.
- The redaction records route of ADR 151 decision 28.
- The classification of `unit:run-apply`, `unit:run-destroy` and `unit:write-back` in a family. Proof of the gap: `pkg/access/access_test.go:521-523` lists them as "no family until their routes land". It stops a grant from reaching a confirmation of an apply job. The start route starts plan jobs only, so no such job exists yet.

## Usability

Where the path still costs the person time or surprises them:

- The person must know the version ID of the log. `GET .../jobs/{job_id}` shows it in the step record. The docs section shows that command first.
- The redaction replaces literal text only. A secret that a tool printed in two forms (plain and base64) needs two matches. The docs section says so.
- The old content is unreadable at once, and the blobs are deleted later (see "Out of scope"). The docs section says exactly that, and it never says "deleted" for the blobs.

## What main already does

Each fact names its file, line and function on main (commit 23b5c35d4).

- The port `job.ArtifactStore` embeds `history.Redactor` and `ArtifactReader` and has `ReplaceLog`. (`pkg/job/artifact.go:328`)
- `ArtifactBlobs` reads, writes and deletes the sealed plan parts and log chunks in the blob store, with the scope of the namespace. (`pkg/job/artifactblobs.go:65`)
- `ArtifactReads` implements `ArtifactReader` over `RecordStore`, `ArtifactBlobs` and `secret.ItemSealer`, and the server wires it. (`pkg/job/artifactreader.go:38`, `newJobArtifactReader` in `pkg/dustpan/jobs.go`)
- The rules of a redaction are `CheckPlanRedaction` and `CheckLogRedaction`. (`pkg/job/artifact.go:220`, `pkg/job/artifact.go:237`)
- `secret.ItemKeyIndex.ShredItemKey` marks an item key shredded under a fence. (`pkg/secret/itemkey.go:235`)
- The conformance suites `historytest.RunRedactor`, `JobArtifactSituations`, `JobLogSituations` and `RunRedactions` exist, and run only against reference adapters in memory. (`internal/history/historytest/redactor.go:68`, `:74`, `:106`; `redactions.go:25`)
- `item_keys` is in the portable backup tables. (`pkg/backup/portable_item_keys.go`)

## ADR check

Each quote is copied from the file in `docs/adr/`. ADR 102, 103, 151 and 152 record decisions of the project owner of 2026-10-17 (each says the owner decided or approved). They carry no "(owner)" tag in a decision line or in the title, so the tag rule of `openspec/project.md` does not mark them. This proposal treats them as owner decisions anyway. ADR 216, 217 and 220 carry "(owner)" in their titles.

| ADR | Decision lines that apply | Owner | Changed-by chain (followed to the end) | Current rule |
| --- | --- | --- | --- | --- |
| ADR 102 (`0102-job-plans-and-logs.md`) | Decision 11: "It then redacts the older version through `history.Redactor`, with the cause `request` and the reason." and "A log of a step that runs cannot be redacted (`history.ErrInProgress`). A version that is already redacted fails with `history.ErrRedacted`. When the redaction of the older version fails after the new version exists, a retry only redacts the older version." and "The job record keeps the number of matches and the hashes of the two versions. Neither ever keeps the text that the principal gave." The route list (line 187): "Both need `unit:redact`." | Records owner decisions | ADR 190 (decisions 2, 7, 8), ADR 194, ADR 210 (route paths and action names), ADR 220 (decisions 3, 4), ADR 299 (decision 8), ADR 307, ADR 353, ADR 378. None changes decision 11 or the lines quoted. | ADR 102 with ADR 190, 194, 210, 220, 299, 307, 353, 378 |
| ADR 103 (`0103-job-gates-actions-and-audit.md`) | Lines 161-164: "A redaction of a plan or of a log, also a redaction of a log by substitution (ADR 102 decision 11), needs `unit:redact` of the history model (ADR 151). The job model has no redact action of its own." Audit table, line 181: "Redaction of a plan or a log \| as ADR 151 decision 17 says \| `unit:redact` \| `change`". | Records owner decisions | The chain of its INDEX row (ADR 110, 126, 128, 138, 141, 142 and later). None changes the redaction lines. | The "Current rule" cell of the INDEX row 103 |
| ADR 151 (`0151-redaction-and-an-item-key-for-each-history-item.md`) | Decision 29 (line 440): "`unit:redact` is a new action, and the only action of a redaction for every item kind. The built-in roles of a namespace administrator and of a unit administrator get it (ADR 52). No other built-in role gets it. Each redaction also passes the policy check (ADR 17)." Context (line 24): "The server keeps the metadata and the hashes of a redacted item." The shred operation after the commit is out of scope here. | Records owner decisions of 2026-10-17 | ADR 154, ADR 210, ADR 217 (changes decision 2), ADR 220 (changes decision 2). None changes decision 29 words. | ADR 151 with ADR 154, 210, 217, 220 |
| ADR 152 (`0152-history-policy-for-job-artifacts.md`) | Decision 8: "A person redacts a plan or a log version with `unit:redact` on the unit, with a reason (ADR 151)." Decision 9: "A request to redact the current version of a log answers 409 with `history.ErrLatest`, and an open version answers 409 with `history.ErrInProgress`." "The audit entry of the substitution never holds the text that the person gave." Decision 7: "The rows of expired and redacted plans and log versions stay until the final delete of the unit (ADR 47). They hold sizes, hashes, times and actors, never content." | Records owner decisions | ADR 199 (changes decision 4), ADR 210. Neither changes decisions 7, 8, 9. | ADR 152 with ADR 199, 210 |
| ADR 216 (`0216-namespace-administration-and-operator-scope.md`) | "`site-administrator` holds all families. The role `account-administrator` holds administration, data and delegation on the installation, except the two site-only actions `manage-sign-in` and `restore-access`." | Owner (title) | ADR 273 (changes decision 8). Changes ADR 52 decision 5 and ADR 65. | ADR 216 with ADR 273 |
| ADR 217, ADR 220 | ADR 217 changes ADR 151 decision 2, and ADR 220 changes ADR 102 decisions 3 and 4 and ADR 151 decision 2: the blob and item key names carry the namespace prefix. `ArtifactBlobs` builds each key through the scope of the namespace. This change writes new blobs only through `ArtifactBlobs` and the item key through the sealer, so it keeps both. | Owner (title) | ADR 217: ADR 220 changes decision 18. ADR 220: ADR 273, 286 and later (see INDEX). Neither changes the key naming that this change uses. | ADR 217 with ADR 220; ADR 220 with the ADRs of its INDEX row |
| ADR 210 | The tool-neutral API prefix, paths and action names. The route `log-redactions` of #3251 uses its prefix. | Not tagged | ADR 247, ADR 286 (see INDEX). | The "Current rule" cell of the INDEX row 210 |
| ADR 250 | Defense in depth: boundary check, contain, limit, check authorization at the point of use, fail closed, one hostile test for each layer. `design.md` has the table. | Not tagged | ADR 382 extends decision 3. | ADR 250 with ADR 382 |

No conflict with an owner decision. Evidence for that claim:

- The decision "who may redact" is `unit:redact` on the unit, held by the namespace administrator and the unit administrator (ADR 151 decision 29, ADR 152 decision 8, ADR 102 line 187). This change adds no actor and no role. It classifies the action in its own family, so that the stored grant of the built-in role `admin` takes effect and no other built-in role holds it.
- What stays in history is decided: ADR 151 line 24, ADR 152 decision 7 and ADR 102 decision 11 (quoted above).
- Owner decision, 2026-11-15 (via the lead): `unit:redact` is held only by the namespace administrator and the unit administrator, which is the built-in role `admin`. `account-administrator` and `site-administrator` do not hold it. This is ADR 151 decision 29 word for word ("No other built-in role gets it"). It needs its own family, because the built-in sets of `account-administrator` and `site-administrator` are built from the family `data` (`pkg/access/roles.go`, `accountAdministratorActions` and the case `RoleSiteAdministrator`). The one line of ADR 216, "`site-administrator` holds all families", gets an exception for the family `redaction`. The PM records the exception as an ADR 216 amendment with the tag "(owner, 2026-11-15)"; this change does not edit the ADR.

## Open questions

none

Questions considered and answered:

- Who may redact, and what stays in history? Decided: ADR 151 decision 29, ADR 152 decisions 7 and 8, ADR 102 decision 11 (quoted above). This change implements them and adds no rule.
- Does the new family conflict with ADR 216? Only with its line "`site-administrator` holds all families". The owner decided on 2026-11-15 that `account-administrator` and `site-administrator` do not hold `unit:redact` (decision 29 of ADR 151: "No other built-in role gets it"). The family `redaction` is held by `admin` only. A custom role stays free to hold or not hold the action. The family adds no grant: a stored grant of a role that holds the action is the only way to pass (rule "no magic" of `CLAUDE.md`). A principal with the `Admin` flag keeps its existing pass in the authorizer; this change does not touch that rule.
- Does the shred operation belong here? No. It deletes blobs after the commit and is its own durable operation (ADR 151 decision 16). The redaction is complete for the reader without it, because the shredded item key makes the sealed blobs unreadable at once (`secret.ErrShredded`), also on a second instance (the doc of `ItemKeyIndex` in `pkg/secret/itemkey.go`). The docs page and the audit entry name only what is true: key shredded, blobs pending.
- What happens to a redaction that the server cannot finish? One fenced transaction does the mark, the key shred, the record and the audit entry together (ADR 102 decision 11), so a failure leaves none of them. A failure after `ReplaceLog` and before `Redact` leaves the next version in place and the older version readable. A retry with the older version only redacts it. `LogRef.Replaces` makes that retry exact.
- Does a retry use new substitutions? No. A retry redacts the older version and ignores the substitutions of the retry, because the next version holds the earlier result. The answer carries the matches of the first call (`LogRef.Matches` of the next version). The docs section says it.
- What does the route answer for an unknown version? 404 `job.not-found`. The spec of #3251 says `job.version-not-found`, which no catalog has (`rg -n "version-not-found" internal docs` prints nothing on the head of #3251). The delta `specs/job-actions` fixes the spec.
- Where do the earlier versions live? In the step record, as `StepRecord.Earlier`, oldest first. ADR 102 decision 8 says "`LogRef` on each step record" and decision 11 says the job record keeps the hashes of the two versions. No ADR fixes the Go shape. `job_logs` already holds one row for each version, so no new table is needed.
- Does the redaction of a plan need a route here? No (see "Out of scope"). The adapter implements `Redact` for plans for the suite `JobArtifactSituations` and for the later retention expiry.
- Can the memory limit of a redaction be hit? `MaxRedactLogBytes` is 64 MiB in `service_redact.go` of #3251, and a version over it answers 400 `job.invalid`. `ReplaceLog` writes chunks of at most `MaxLogChunk` (1 MiB), so memory in the adapter is one chunk.
- Does the change need a dependency? No. The adapter uses `blob.Store`, `secret.ItemSealer`, `database/sql` through the existing stores, and the standard library.
- Is a numbered migration needed? No. The two new tables go into the baseline of each dialect in name order (ADR 322, `CLAUDE.md`). No tag shipped the shape.
- Does the second review pass add a failure mode? The adversarial list is in `design.md` "Failure modes" and "Threat model". Each answer is either a task or the out-of-scope list above.

Drafted by the PM session from #3251, the code of main and the ADRs. Every quote, path and line was read in the files at commit 23b5c35d4 and on the head of #3251 (f67fddbed).
