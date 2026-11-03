# 323. The tool-neutral API stays v1 and can break before 1.0

Status: accepted · 2026-11-02. Decisions 1, 2 and 4 are owner decisions. Decision 3 is a PM decision, taken on the owner's behalf, marked "(PM)"

Changes:

- Changes ADR 264 decision 5: until the first release, the description has one fixed version. A pin of the version does not name one change of the description.
- Changes ADR 275 decision 8: until the first release, `info.version` does not change with each change of the description. The test of the rule is disabled.

## Context

ADR 275 decision 8 makes `info.version` of the API description rise with
each change of the description. `TestVersionFollowsTheDescription` compares
the description with the file `internal/api/apidesc/openapi.pin`. Two pull
requests that each change the description both write `openapi.pin` and
both raise the version. The second must raise the version again after the
first merges.

The project owner gave these directions on 2 November 2026, in this order:

```text
1. "since we don't have even a single user yet we can delete the migrations
and API versioning altogether"
2. "a single DB schema file and API V1 can accept breaking changes"
3. "keep the capability for after the release ... stop building
compatibility and migrations it's only costing us"
4. "just disable them for now/t.Skip with a todo and a github issue"
5. "we don't need to rip it out, we can just stop using them"
6. "shouldn't be annoying to use during development"
7. "shouldn't cause production issues in the future"
8. "remove automated reset altogether and make reset easily accessible for
DEV"
```

ADR 322 records the schema part, and ADR 324 the schema mismatch and the
dev reset. This ADR records the API part.

## Options considered

| Option | Why it was refused |
| --- | --- |
| Remove `openapi.pin` and the version test | The owner wants the checks disabled, not removed (direction 4), and the capability kept for the release (direction 3). |
| A new major version of the path for each break | No client depends on the API yet (direction 1), and the owner said that v1 can break (direction 2). |
| Keep the version rise of each pull request | Each parallel pull request that changes the description collides on `openapi.pin`. |

## Decision

1. **The tool-neutral API stays at `v1` (owner).** The API under
   `/api/dustpan/v1` and the other tool-neutral prefixes can break before
   1.0, with no new version in the path (ADR 73 decision 1, ADR 210
   decision 8). The release notes list each break.
2. **One version of the description until the first release (owner).**
   `DocumentVersion` in `internal/api/apidesc/document.go` stays at one value.
   A pull request that changes the description does not change it.
   `TestVersionFollowsTheDescription` skips with a TODO until the first
   release, and `openapi.pin` stays in the tree. Issue #2305 lists the test.
   At the first release, the release sets the version, writes the pin again
   and turns the test on again.
3. **Generated files (PM).** The description and the generated clients stay.
   `make clients-check` stays the check that the clients match the
   description. The merge session of each train generates the description,
   the clients and the golden files again. A pull request can generate them
   too. To resolve a conflict in a generated file, run its generator again
   on the merged code. Never edit a generated file by hand.
4. **The tool-compatible APIs do not change (owner).** The Pulumi
   Cloud-compatible API, the TFE-compatible API and the OpenTofu HTTP backend
   keep the rule of ADR 73 decision 2. They match what each supported version
   of the real CLI sends and expects, and `make e2e` checks them.

## Consequences

- The client version keeps rule A4 of RFC 99 and ADR 275 decision 3: the
  major and minor parts come from `info.version`. Until the first release
  they do not change, and the patch part of a client is its own.
- A client cannot tell two untagged descriptions apart by the version. A
  consumer such as the console pins a published package version (ADR 275
  decision 5).
