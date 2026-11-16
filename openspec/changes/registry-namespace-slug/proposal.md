---
version: 1
---

Issue: #1228, #1291.

## Why

**Persona: namespace administrator.** A namespace administrator publishes private providers for teams that run tofu init. A namespace name outside the registry-safe subset (a dot, an underscore, a capital letter) cannot publish, and a name in the registry URL changes when the namespace is renamed. This change gives each namespace a registry slug that never changes, so that the registry URL stays valid.

This change was split from `h1-hash-provider-archive-registry`. It waits for that change, which adds the publish route and the unpack checks.

The user path this change must make work:

1. **API.** Publish a provider release to a namespace with an unsafe name. The release is stored under the slug of the namespace.
2. **CLI.** Install the provider with tofu init against the registry URL under the namespace slug.
3. **Docs page.** Read the registry publish page for the slug rules and the one command that lists the slug.
4. **E2e.** Run the registry e2e step with a namespace name outside the safe subset and with a rename of the namespace.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Install requests an unknown namespace slug. | The registry path uses the namespace slug with code registry.not-found and the requested slug. List releases to copy the slug, then run tofu init to confirm the install path. | `tofu init` |

Evidence for the claims above:

- The registry accepts only the registry-safe subset of a name. Proof: pkg/registry/registry.go:211:func validSlug(s string) bool {
- The registry download routes take the namespace from the path. Proof: internal/api/tfe/registry.go:182:			Path:   registryPrefix + "{namespace}/{type}/versions",
- The namespace store finds a namespace by name only. Proof: pkg/store/sqlite/sqlite.go:607:func (s *Store) GetNamespace(ctx context.Context, name namespace.Name) (namespace.Namespace, error) {

## What Changes

- Derive and serve a registry-safe slug for each namespace for publish and install paths with dual serve of prior paths.
- Store the slug on the namespace record, in the baseline of each dialect, with a lookup from slug to namespace in the namespace store and its conformance suite.

Out of scope:

- Unpack limits, H1 and the protocol check (change `h1-hash-provider-archive-registry`)
- A slug that a namespace administrator sets by hand (the question of RFC 101 stays open)
- Curated upstreams curation rules
- Console catalog and create form

## Usability

Where the path still costs the person time or surprises them:

- A namespace with an unsafe name surprises users when the registry URL differs from the display name; the docs page names the slug rule and the release list shows the slug in the path of each release.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The registry accepts only the registry-safe subset of a name. (`pkg/registry/registry.go:211`)
- The namespace store finds a namespace by name. (`pkg/store/sqlite/sqlite.go:607`)
- The namespace has a stable ID that never changes. (`pkg/namespace/namespace.go:95`)
- The download routes take the namespace from the path. (`internal/api/tfe/registry.go:182`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched. No ADR decides the registry slug. RFC 101 names "a registry slug for each namespace" as a later escape hatch and says "This RFC does not decide it", so this change states the rule. The slug derives from the stable ID, which follows the rule that a stored reference uses the stable ID and a name is a label.

## Open questions

none

Questions considered and answered:

- Derive the slug from the stable ID, or let the administrator set it? The administrator-set slug is a later step in RFC 101 and needs its own decision, so this change derives the slug from the stable ID and sets it once.
- Backfill the slug of earlier namespaces? No tag shipped the earlier shape and no installation holds a namespace, so there is no backfill: the slug is set at the first publish of the namespace.

Split from `h1-hash-provider-archive-registry` by the project lead. Every quote, path and ADR citation was checked by code.
