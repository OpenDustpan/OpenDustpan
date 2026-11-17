---
version: 1
---

## Why

**Persona: developer.** A Developer who adopts OpenDustpan tries to ask a question or report a problem and needs to know where the community meets.

The user path this change must make work:

1. **Docs page.** Developer reads the docs support section looking for where to ask for help and finds no community forum or chat link yet
2. **CLI.** Developer reports the problem in GitHub Issues with the gh command

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Developer finds no forum or chat link on the support page | No community forum or chat exists yet. Report the problem in GitHub Issues. | `gh issue create` |

Evidence for the claims above:

- RFC 69 waits on the owner to set up the channels. Proof: docs/roadmap.md:837 row: | [RFC 69](docs/rfc/0069-community-channels.md) Community channels | the owner sets up the channels |

## What Changes

- Record that RFC 69 waits on the owner to set up the channels.

Out of scope:

- Server code, API routes, console views and store changes for channels

## Usability

Where the path still costs the person time or surprises them:

- No user path changes until the owner names the channels.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main normalizes audit channel values to API values in tests. (`pkg/audit/audit_test.go:81 (TestNormalizeChannelMapsUnknownValuesToAPI)`)
- Main tests that the durable scheduler waits the first delay before its first claim. (`pkg/durable/scheduler_test.go:175 (TestSchedulerWaitsTheFirstDelayBeforeItsFirstClaim)`)
- Main tests that the in-memory durable beat prunes stale owners. (`pkg/durable/durabletest/durabletest_test.go:57 (TestMemBeatPrunesStaleOwners)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Which community channels will the owner set up and support?
   - Options: Owner names one forum and one chat, project links only to those; Owner names docs-first support with GitHub Issues and no chat.
   - Provisional decision (owner review 14 Nov): Owner names one forum and one chat, project links only to those
   - Reason: The roadmap row blocks on the owner. The choice sets support load and moderation duty. Benchmark: Google: open source projects name one primary support channel in the docs and point all other questions to it.
   - Persona impact: A Developer learns where to ask for help on the first docs visit. No persona loses access because GitHub Issues stays available. Escape hatch: GitHub Issues stays the interim path.
2. What moderation, code of conduct and security-report path cover the channels?
   - Options: Owner names a private security contact plus a public code of conduct and moderation rule; Owner defers moderation and security-report path to a later RFC.
   - Provisional decision (owner review 14 Nov): Owner names a private security contact plus a public code of conduct and moderation rule
   - Reason: A public channel receives hostile input and public vulnerability reports. The pack and ADRs do not answer this. Benchmark: Google: open source projects publish a private vulnerability report path and a code of conduct.
   - Persona impact: A Developer knows where to send a vulnerability without posting it in public. Escape hatch: Until decided, the docs direct reports to GitHub Issues and ask for no secrets in public posts.

Questions considered and answered:

- Does any accepted ADR already decide community channels? No. The pack lists accepted ADRs about namespaces, tool models, restore, state reads, backups, deletes, scale-out, redaction, API shape, settings, federation and sign-in. No quoted decision line names a forum, chat or community channel. The roadmap row says the item waits on the owner. So adr_check stays empty.
- Where does a Developer report a problem while no channels exist? GitHub Issues through the gh CLI. The project rules say issues live in GitHub Issues and skills use the gh CLI. That path stays until the owner names channels.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
