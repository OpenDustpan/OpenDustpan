---
version: 4
---

Issue: #3289.

## Why

**Persona: operator.** An operator of a hosted installation must be able to stop an installation grant from reaching the namespaces of other tenants. ADR 216 decision 7 puts that choice in the setting `access.installationGrantsReachNamespaces`. The setting does not exist on main, so the operator cannot make the choice, and the housekeeping changes cannot test their namespace grants with the setting `false`.

The user path this change must make work:

1. **API.** The operator reads the setting `access.installationGrantsReachNamespaces` and its effective value (default `true`) with `GET /api/dustpan/v1/settings`, a route that main has.
2. **API.** The operator sets it to `false`. A grant on `@server` then gives only the actions of the operator scope, and gives no action inside a namespace.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A caller that holds only a grant on `@server` does an action inside a namespace while the setting is `false`. | 403 that names the action and the kind of the object, not the namespace. The server changes nothing. | `GET /api/server/access-review` |

Evidence for the claims above:

- The authorizer always adds the installation object to the objects that it checks for a namespace object, and reads no setting. Proof: pkg/access/authorizer.go:967

## What Changes

- The setting `access.installationGrantsReachNamespaces` (ADR 216 decision 7): default `true`, database layer, live, lockable in the static layer. It is a new entry of `SettingDeclarations` in `pkg/access/settings.go`. It uses the settings routes `GET /api/dustpan/v1/settings` and `PATCH /api/dustpan/v1/settings`, which main has. This change adds no route.
- With `false`, the authorizer, the reach, the lists of objects and the access review give a grant on `@server` only the actions of the operator scope.
- A failed read of the setting denies the decision.
- The probe tests run with the value `false` that `housekeeping-principals-install` (task 1.6) and `housekeeping-principals-sweeps` (task 1.3) leave out. This change adds its own access probe as a NEW test in `pkg/access/reach_namespaces_setting_test.go`. The probe for the namespace purposes is a task of `housekeeping-principals-sweeps`. This change writes into no test file of the housekeeping changes.

Out of scope:

- The housekeeping principals and their grants
- The lock of the value in the static layer of OpenDustpan Cloud
- The bootstrap administrator token, which is not a grant (design decision 5)

Depends on:

- none

Builds on (not a change):

- ADR 216 decision 7

## Usability

Where the path still costs the person time or surprises them:

- A person who sets `false` sees at once that a namespace action of a grant on `@server` answers 403. The 403 names the action and the kind of the object ("on this namespace"), not the namespace, and the access review shows the grant.
- The bootstrap administrator token is not limited by the setting. The guide says so.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The authorizer adds the installation object for a namespace object and reads no setting. (`pkg/access/authorizer.go:967 (func chainObjects)`)
- The reach and the access review use the same function. (`pkg/access/reach.go:969 (func Reach.chain)`, `pkg/access/review_service.go:586 (func Service.reviewScope)`)
- The only setting of the package is `accessReview.collectionUnitCap`, declared in `SettingDeclarations`. (`pkg/access/settings.go:47 (func SettingDeclarations)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 216: Namespace administration and operator scope: where each installation action goes** (owner-tagged (all decisions)).

- Decisions that apply: 7 (current).
- Changed by: ADR 273 changes decision 8; ADR 393 changes decision 8. Chain to the end: none.
- Current rule: this ADR, with ADR 273, 393.
- Quote: "The installation setting `access.installationGrantsReachNamespaces` is `true` by default."
- Effect: This change adds the setting with the default `true`, and makes `false` give a grant on `@server` only the operator actions.
- Bootstrap administrator: not a grant, so decision 7 does not name it. The change leaves it as it is and says so in the guide (design decision 5).
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does an accepted ADR already decide the setting? Yes. ADR 216 decision 7 gives the name, the default and the effect of the value `false`.

Refined by the PM from the lead's request after the first driver run blocked.
