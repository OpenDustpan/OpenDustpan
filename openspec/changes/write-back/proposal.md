---
version: 2
---

Roadmap: `docs/roadmap.md:300` (Write-back).

## Why

**Persona: developer.** A Developer keeps unit config in git and edits one value from the API. The server commits the edit with the trailer of the job, so the team sees the change in git and the next plan shows it.

The user path this change must make work:

1. **API.** Developer edits a config value of a stack or workspace through the tool-neutral API.
2. **Server start.** Server validates the edit, starts a write-back job, and commits the edit on the forge with the trailer of the job.
3. **API.** Developer reads the job, the commit, and the next plan that shows the edit.
4. **Docs page.** Developer follows the docs page for API config edits and write-back.
5. **E2e.** E2e drives the real binary over TLS against the fake forge and checks the commit trailer and the next plan.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| API edit names a file outside the editable config files | Edit refused: path is not an editable config file. Fix the path and send the edit again. | `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{job}` |
| Base commit is stale at deliver time | Job failed: key changed after the base commit. Send the edit again from the latest commit. | `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{job}` |
| Forge call fails during the deliver phase | Write-back failed on the forge. Read the job record for the error class, then send the edit again. | `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{job}` |

Evidence for the claims above:

- An e2e file names unit settings. Proof: e2e/unit_settings_test.go
- A guide names the forge connection step. Proof: docs/guides/connect-github.md
- No e2e file in the pack names Forge. Proof: Forge e2e_count 0
- The Forge port and its conformance suite exist in the tree. Proof: pkg/forge and pkg/forge/forgetest

## What Changes

- API config edit of a unit starts a write-back job that commits only the edited keys at the base commit with the trailer of the job.
- Edits outside the editable config files fail before any forge call.
- A key that changed after the base fails the job with the key named.
- A retryable forge error retries, and a refused forge error fails the job.

Out of scope:

- Runner jobs, plan phases, and stored plans
- Pull request automation and apply after merge
- Profile opens and secret handling in jobs

Depends on:

- add-job-service-routes-start-1
- `git-host-webhook-intake-post` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- The Developer learns the editable files only after a refusal; the docs page shows the editable set first.
- The trailer is opaque until the docs page shows one commit with the job trailer.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Write-back branch helper is tested. (`pkg/forge/review_test.go:71 (TestIsWriteBackBranch)`)
- Commit trailer rule is tested. (`pkg/forge/github/commit_test.go:465 (TestCommitNeedsATrailerInTheLastBlock)`)
- Blob retry backoff is tested. (`pkg/blob/retry_test.go:43 (TestDeleteRetriedBacksOff)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 101: Two executors: write-back and runners** (not owner-tagged).

- Decisions that apply: 3 (extended), 4 (current), 5 (current), 6 (current).
- Changed by: ADR 110 extends decision 3; ADR 130 extends decisions 12 and 14; ADR 131 extends decisions 2, 13 and 15; ADR 133 changes decision 10; ADR 133 extends; ADR 134 extends decisions 7, 10 and 13; ADR 164 extends decisions 7, 10, 14 and 15; ADR 300 changes decision 1. Chain to the end: ADR 110, ADR 130, ADR 131, ADR 133, ADR 134, ADR 164, ADR 300, ADR 210, ADR 218, ADR 222, ADR 225, ADR 286, ADR 305, ADR 308, ADR 343, ADR 139, ADR 204, ADR 350, ADR 199, ADR 330, ADR 166, ADR 247, ADR 248, ADR 335, ADR 351, ADR 354, ADR 299, ADR 360, ADR 282, ADR 310, ADR 353, ADR 328, ADR 359, ADR 304.
- Current rule: this ADR, with ADR 110, 130, 131, 133, 134, 164, 300.
- Quote: "Write-back runs in the server."
- Quote: "Write-back edits only the config of the unit."
- Quote: "Write-back does `apply` only."
- Quote: "The team's CI closes a write-back job."
- Effect: This change implements the write-back deliver phase in the server, the config-only edit rule, the apply-only rule, and the CI-close rule for the job.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does an API config edit commit through write-back or run on a runner? ADR 101 decisions 3, 4 and 5 answer it. The API edit starts a write-back job in the server that commits unit config through the Forge port and runs apply only.
- What happens when an edit names a file that does not exist at the base commit? ADR 101 decision 3 with the ADR 110 extension answers it. The deliver phase reads each file at the base commit, creates a missing editable file, and fails the job when a key changed after the base.
- Does write-back run plan or destroy jobs? ADR 101 decision 5 answers it. A write-back job with operation plan, destroy, or refresh fails with ErrUnsupported; only apply commits.
- Does the commit go to the branch head or open a review request? ADR 101 decision 3 answers it. The commit goes as a direct push to the head of the branch of the code location; a protected branch refusal fails the job as refused.
- Which forge errors retry and which fail the write-back job without retry? Decided in the spec. Retryable is transport errors, timeouts, 429 and 5xx with bounded retry; refused is 4xx with no retry.
- What exact trailer and author does the write-back commit carry? Decided in the spec. The commit carries one trailer line Dustpan-Job with the job ID, written by the server forge identity, with the starting principal named in the commit message body.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
