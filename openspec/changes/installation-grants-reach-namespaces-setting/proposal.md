---
version: 1
---

Issue: #3289.

## Why

**Persona: operator.** An operator of a hosted installation must be able to stop an installation grant from reaching the namespaces of other tenants. ADR 216 decision 7 puts that choice in the setting `access.installationGrantsReachNamespaces`. The setting does not exist on main, so the operator cannot make the choice, and the housekeeping changes cannot test their namespace grants with the setting `false`.

The user path this change must make work:

1. **API.** The operator reads the setting `access.installationGrantsReachNamespaces` and its effective value (default `true`) in the settings API.
2. **API.** The operator sets it to `false`. A grant on `@server` then gives only the actions of the operator scope, and gives no action inside a namespace.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A caller that holds only a grant on `@server` does an action inside a namespace while the setting is `false`. | 403 that names the action and the namespace. The server changes nothing. | `GET /api/server/access-review` |

Evidence for the claims above:

- The authorizer always adds the installation object to the objects that it checks for a namespace object, and reads no setting. Proof: pkg/access/authorizer.go:967

## What Changes

- Coarse placeholder for the setting `access.installationGrantsReachNamespaces` of ADR 216 decision 7.
- It carries the probe runs with the setting `false` that the change `housekeeping-principals-install` (task 1.6) and the change `housekeeping-principals-sweeps` (task 1.3) leave out.

Out of scope:

- The housekeeping principals and their grants
- The lock of the value in the static layer of OpenDustpan Cloud

Depends on:

- none

Builds on (not a change):

- ADR 216 decision 7

## Usability

Where the path still costs the person time or surprises them:

- No user path changes until the change is refined.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The authorizer adds the installation object for a namespace object and reads no setting. (`pkg/access/authorizer.go:967`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 216: Namespace administration and operator scope: where each installation action goes** (owner-tagged (all decisions)).

- Decisions that apply: 7 (current).
- Changed by: ADR 273 changes decision 8; ADR 393 changes decision 8. Chain to the end: none.
- Current rule: this ADR, with ADR 273, 393.
- Quote: "The installation setting `access.installationGrantsReachNamespaces` is `true` by default."
- Effect: This change adds the setting with the default `true`.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does an accepted ADR already decide the setting? Yes. ADR 216 decision 7 gives the name, the default and the effect of the value `false`. This change stays coarse until it is refined into a full change.

Drafted by the PM from the lead's request for the change that adds the setting.
