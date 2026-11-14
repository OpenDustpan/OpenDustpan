---
version: 2
---

Roadmap: `docs/roadmap.md:388` (Upgrade pull requests).

## Why

**Persona: developer.** A Developer keeps units on current providers and modules. Today a stale pin stays stale until a person finds the new release by hand. The change opens a pull request with the version change and a plan, so review stays fast.

The user path this change must make work:

1. **CLI.** Developer pins an old provider version in unit settings and pushes the unit.
2. **API.** Server scans pins, finds a newer private registry release, and opens a pull request on the forge that changes the pin.
3. **API.** Server runs a plan for the changed unit, posts the result to the pull request, and links it to the upgrade request.
4. **Docs page.** Developer reads the upgrade guide and reviews the pull request and the plan output.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Duplicate scan finds same stale pin | The server keeps the single open request and names it in the list. Read the open upgrade request to see the pull request reference. | `GET /api/dustpan/v1/namespaces/{namespace}/upgrade-requests/{id}` |
| Registry release gone before open | The server stores the missing release on the upgrade request and skips open. List provider versions to confirm the release is gone. | `GET /api/dustpan/v1/namespaces/{namespace}/upgrade-requests` |
| Forge connection fails during open | The server stores forge-unreachable on the upgrade request and retries on the next scan. Read the upgrade request to see the failure. | `GET /api/dustpan/v1/namespaces/{namespace}/upgrade-requests/{id}` |
| Unit has no forge link | The server stores no-forgelink and skips open. The namespace administrator links the forge first, then the next scan opens the request. | `GET /api/dustpan/v1/namespaces/{namespace}/upgrade-requests` |

Evidence for the claims above:

- Forge domain code exists in the server. Proof: pkg/forge, pkg/forge/forgetest, internal/forgelinksvc
- The forge link model exists in the store. Proof: pkg/forgelink, pkg/forgelink/forgelinktest, pkg/store/internal/forgelinkstore
- RFC 56 is a draft and the design is still needed. Proof: part: Design needed: RFC 56 is a draft.
- The context pack names no ADR for this item. Proof: adrs: []
- Registry version routes exist for providers and modules. Proof: route GET /api/dustpan/v1/namespaces/{}/registry/providers/{}/versions and GET /api/dustpan/v1/namespaces/{}/registry/modules/{}/{}/versions

## What Changes

- Add daily upgrade scan that compares fixed-version pins in unit settings with private registry releases with one open request per unit stable ID, pin, and new version.
- Open forge pull requests for stale pins through the Forge port with branch lookup idempotency.
- Run a plan per request through the milestone 5 plan path and post the plan status to the forge request.
- New route `GET /api/dustpan/v1/namespaces/{namespace}/upgrade-requests` to list upgrade requests with state filter.
- New route `GET /api/dustpan/v1/namespaces/{namespace}/upgrade-requests/{id}` to read one upgrade request with versions and plan result.

Out of scope:

- Blueprints catalog and create form
- Ephemeral units
- Environment promotion
- Curated upstreams
- Job proxy
- Console catalog and create form
- Pins that track a forge branch instead of a fixed version
- Automatic merge of an upgrade pull request
- Manual open of an upgrade request

Depends on:

- artifact-registry-private-provider
- artifact-registry-private-module

## Usability

Where the path still costs the person time or surprises them:

- The Developer finds the upgrade pull request, the version change, and the plan result from one screen without reading server logs.
- A suppressed reopen names the closed request and the date when a reopen is allowed.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Catalog request body-invalid error code exists (`internal/errcode/catalog_request.go:7 (func body-invalid)`)
- Catalog request body-too-large error code exists (`internal/errcode/catalog_request.go:9 (func body-too-large)`)
- Access request cache test exists (`pkg/access/access_test.go:1247 (func TestRequestCache)`)
- Rolling upgrade test exists (`pkg/dustpan/workerwatch_internal_test.go:240 (func TestRollingUpgradeGivesNoRefusal)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

Not spec-ready: a code check fails: task "Wire the scanner into the server start. Add the routes `GET /api/dustp" runs an acceptance path but names no existing route and credential kind; name them, or write "BLOCKED by <the missing part.

Questions considered and answered:

- Does an accepted ADR already define upgrade pull requests? No. The pack lists adrs as empty and gives no decision text to quote. Scan timing, request scope, forge write path, and merge-state sync are decided below as provisional project choices for this change.
- Does the change need a new forge client? The pack proves only that forge code exists at pkg/forge and internal/forgelinksvc. This change reuses the Forge port read and open paths and adds no second client. Branch lookup by deterministic branch name gives idempotency after a crash.
- Where do pins live and which registry maps to them? Pins live in unit settings as fixed versions. The scanner parses provider and module pins from unit settings only, not from repository files. Only private registry releases map to a pin.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
