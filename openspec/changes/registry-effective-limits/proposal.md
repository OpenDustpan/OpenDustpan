---
version: 2
---

Issue: #1228.

## Why

**Persona: namespace administrator.** A namespace administrator publishes private providers. A publish that fails at a limit names the limit only after the failure. The administrator cannot read the limits before the publish, and a tool that builds an archive cannot check its size against them. This change adds a route that shows the effective request and unpack limits of the registry, so that the limit values are visible and no limit is a hidden rule.

This change was split from `h1-hash-provider-archive-registry`, where the requirement "Show limits as effective values" had no task and no route. It waits for that change, which adds the unpack limits.

The user path this change must make work:

1. **API.** Read the registry limits of a namespace with GET .../registry/limits and an access token with the read grant.
2. **CLI.** Run the same read with curl, as the docs page shows, before the first publish.
3. **Docs page.** Read the registry publish page for the limits and the one command that shows them.
4. **E2e.** Run the registry e2e step that reads the limits and checks that each value equals the value that a refused publish names.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A principal without the read grant asks for the limits. | The answer is 403 and names the grant registry:read on the namespace. | `curl -H "Authorization: Bearer $TOKEN" $URL/api/dustpan/v1/namespaces/$NS/registry/limits` |

Evidence for the claims above:

- The upload size limit is one default of the registry service. Proof: internal/registry/service/service.go:40:	DefaultMaxArchiveBytes int64 = 512 << 20
- The registry routes are one table, and a read route takes the read action. Proof: internal/api/registry/routes.go:63:		h.readRoute(versionsPath, read, h.listReleases),
- The handler holds the upload limit as configuration. Proof: internal/api/registry/registry.go:49:	MaxUploadBytes int64

## What Changes

- Add GET /api/dustpan/v1/namespaces/{namespace}/registry/limits. It returns the request limit, the unpacked size limit, the file count limit, the size limit of one file, the expansion limit and the size limit of the manifest file, as numbers with units.
- Make one source for each limit: a value type of `pkg/registry` that the handler, the unpack and the new route all read, so that the route cannot show a value that a check does not use.
- Add the page section, the e2e step and the route to the API description and the clients.

Out of scope:

- A way to change a limit (a limit stays a constant of the build, and the route shows it)
- The unpack checks themselves and the checks of the manifest file (change `h1-hash-provider-archive-registry`); this change shows the manifest size limit, 65536 bytes, as one more effective value
- Console views of the limits

## Usability

Where the path still costs the person time or surprises them:

- The limits are in the error of a failed publish only. The docs page lists the limits and the command that shows the effective values.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The upload limit is a default of the registry service. (`internal/registry/service/service.go:40`)
- The registry routes are one table with an action for each route. (`internal/api/registry/routes.go:52`, `buildRoutes`)
- The handler holds the upload limit. (`internal/api/registry/registry.go:49`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

- ADR 293, decision 2 (owner-tagged): "**A setting with an effective value (owner).** The 10 minutes are a setting. The API shows its effective value and its source. A change writes an audit entry. (owner)". This change follows the rule that the API shows an effective value. A limit of the registry is a constant of the build, so its source is the build and no change exists to audit.
- ADR 347, decision 3 (owner-tagged): "The API shows the effective value of the setting (see "No magic" in `CLAUDE.md`)." This change follows it.

No ADR is contradicted.

## Open questions

none

Questions considered and answered:

- Per namespace or for the installation? The limits are constants of the build and equal for each namespace, so the route sits under the namespace path (the grant is on the namespace) and shows the same values. A per-namespace limit would need its own decision and is out of scope.
- Which grant? registry:read, the action of the other read routes of the registry.

Split from `h1-hash-provider-archive-registry` by the project lead. Every quote, path and ADR citation was checked by code.
