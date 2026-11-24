---
version: 1
---

Issue: #3289.

## Why

**Persona: operator of a hosted installation.** A tenant must be able to trust that the operator staff who hold a grant on `@server` cannot read or change the data in the tenant's namespace. ADR 216 decision 7 gives the operator one setting for this choice: `access.installationGrantsReachNamespaces`. The setting does not exist on main. The authorizer always lets a grant on `@server` reach each namespace, so the operator cannot make the choice, and the two housekeeping changes cannot run their namespace probes with the setting `false`.

The user path this change makes work:

1. **API.** The operator runs `GET /api/dustpan/v1/settings` and finds the entry `access.installationGrantsReachNamespaces` with the value `true`, the source `default` and a text that says what `false` does.
2. **API.** The operator runs `PATCH /api/dustpan/v1/settings` with `{"access.installationGrantsReachNamespaces": false}`, an `Idempotency-Key` and the current revision. The answer shows the value `false` and the source `database`. The server writes one audit entry with the old and the new value.
3. **API.** A person who holds only a grant on `@server` now gets 403 for each action on an object inside a namespace. Operator actions on `@server` still work.
4. **API.** `GET /api/server/access-review` shows the grant on `@server` as a grant that does not reach any namespace, so the operator sees why the 403 happens.
5. **Docs.** A guide page shows these steps with commands that a test runs.
6. **Static lock.** An operator who sets the key in the flag, the environment or the file sees the key as locked and sees which layer locks it. A PATCH answers 409.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A caller that holds only a grant on `@server` does an action on an object inside a namespace while the setting is `false`. | 403 that names the action and the object. The server changes nothing. | `GET /api/server/access-review` |
| A PATCH sets the value to `true` without a fresh step. | 403 `mfa.fresh-step-required`. | Sign in again, then repeat the PATCH. |
| A PATCH sets a value that is not `true` or `false`. | 400 that names the key and the allowed values. | `GET /api/dustpan/v1/settings` |
| A PATCH changes a key that the flag, the variable or the file locks. | 409 that names the layer that locks the key. | `GET /api/dustpan/v1/settings` |
| The server cannot read the setting. | The server treats the value as `false`, and denies. The log names the key. | `GET /api/dustpan/v1/settings` |

Evidence for the claims above:

- The authorizer adds the installation object to the chain of each object that is not the installation, and reads no setting. Proof: pkg/access/authorizer.go:966
- A grant on `@server` becomes the scope of every object. Proof: pkg/access/authorizer.go:296 (`scopeFor`) with `addToScope`.
- `AuthorizerConfig` has no reader of settings. Proof: pkg/access/authorizer.go:49
- No declaration of the key exists. Proof: pkg/access/settings.go (`SettingDeclarations` holds only `accessReview.collectionUnitCap`).

## What Changes

- Add the installation setting `access.installationGrantsReachNamespaces`: boolean, default `true`, held in the database, live, lockable in the static layer. A change to `true` counts as a change that reduces security.
- Make the authorizer read the effective value. With `false`, a grant on `@server` stops at the operator scope in `Authorize`, `Explain`, `ListObjects` and the reach of the access review.
- Add the flag `--access-installation-grants-reach-namespaces`, the variable `DUSTPAN_ACCESS_INSTALLATION_GRANTS_REACH_NAMESPACES` and the file key `access.installationGrantsReachNamespaces`, so the static layer can lock the value.
- Show the effect in the access review: a grant on `@server` carries a flag that says it reaches no namespace.
- Carry the probe runs with the setting `false` that `housekeeping-principals-install` (task 1.6) and `housekeeping-principals-sweeps` (task 1.3) leave out.
- Add one e2e step, a guide page and the settings reference row.

Out of scope:

- The housekeeping principals and their grants.
- The lock of the value in the static layer of OpenDustpan Cloud. This change gives the static key. The Cloud configuration that sets it is not in this repository.
- ADR 216 decision 10 (the wildcard read `namespaces/-`) and decision 11 (support grants of an installation principal, 72 hours at most). See the open questions.
- Any change to `identity.principalHome` (ADR 215).

Depends on:

- none

Builds on (not a change):

- ADR 216 decision 7
- ADR 52 decision 5 (a grant on `@server` reaches each namespace)
- ADR 63 (the static layer)

## Usability

- The default is `true`, so no existing installation changes behavior.
- The setting shows its effective value and source in the settings API (ADR 74 "no magic": each default shows its effective value).
- The 403 names the action and the object, and the access review names the reason.
- The guide page says that the setting `false` also cuts the namespace-level IP allowlist and the token routes of a namespace principal for the holders of a grant on `@server`, if the PM accepts the recommended answer to question 1. The page must say what the operator can still do (see `design.md`).

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- `chainObjects` adds `identity.ServerObject()` for each kind that is not the installation. (`pkg/access/authorizer.go:966`, function `chainObjects`)
- The same function adds it for a principal of a namespace and for a principal with no namespace. (`pkg/access/authorizer.go:945`, `:948`)
- `Authorizer.chain` and `Reach` both call `chainObjects`. (`pkg/access/authorizer.go:907`, `pkg/access/reach.go`)
- `scopeFor` and `reachGrants` turn a grant on `@server` into the scope of every object. (`pkg/access/authorizer.go:296`, `:315`)
- `AuthorizerConfig` has the fields Store, Callers, Collections, Units and Principals, and no reader of settings. (`pkg/access/authorizer.go:49`)
- `Settings` is an existing interface of the package, with `Effective`. (`pkg/access/service.go:71`)
- `SettingDeclarations` holds one key. (`pkg/access/settings.go`)
- `NewAuthorizer` has two call sites in the server. (`pkg/dustpan/dustpan.go:3369`, `:3477`)
- Other uses of the installation object in the access package: `pkg/access/check.go:174`, `pkg/access/service.go:216`, `:957`, `:1159`, `:1233`, `:1303`, `pkg/access/review_service.go:423`, `:735`, `pkg/access/discovery.go:102`.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`.

**ADR 216: Namespace administration and operator scope: where each installation action goes** (owner-tagged (all decisions)).

- Decisions that apply: 7 (current), 2, 3, 5 (current), 10 and 11 (see the open questions).
- Changed by: ADR 273 changes decision 8; ADR 393 changes decision 8. Chain to the end: none. Neither ADR touches decision 7.
- Current rule: this ADR, with ADR 273, 393.
- Quote (decision 7): "The installation setting `access.installationGrantsReachNamespaces` is `true` by default. With `true`, a grant on `@server` reaches each namespace, as ADR 52 decision 5 says. With `false`, a grant on `@server` gives only the actions of the operator scope. It never gives an action inside a namespace. OpenDustpan Cloud locks the value `false` in the static layer (ADR 63)."
- Quote (Consequences): "A `site-administrator` with `access.installationGrantsReachNamespaces: false` is refused each data action in a namespace."
- Effect: This change adds the setting with the default `true` and the effect of `false` that decision 7 states. It does not change the words of decision 7.
- Conflict: none with the words. Question 1 asks how to read "an action inside a namespace" for the actions that are not in the data family.

**ADR 215: Identity home** (owner-tagged). Decision 11 says that an operator setting `false` ends the access of installation persons in a namespace, except the support grants of ADR 216 decision 11. Decision 20 says that a self-hosted installation can set `principalHome: namespace` and `false`. This change adds the setting that both decisions name. Conflict: none.

**ADR 394: Housekeeping grants.** Its text says that a grant on `@server` reaches a namespace only when the setting is `true`, and that Cloud locks it `false`. This change makes that sentence true. Conflict: none.

## Open questions

The PM must answer questions 1 to 4 before an agent starts. Question 1 touches the meaning of the owner-tagged decision 7.

1. **Owner-tagged semantics. What is "an action inside a namespace"?** Decision 7 says that with `false` a grant on `@server` "never gives an action inside a namespace".
   - Option A (by object, recommended): no action on an object inside a namespace. The chain of such an object drops `@server`. This is the literal reading of "never". It also cuts `namespace:manage-ip-allowlist` (a namespace object, administration family) and `principal:manage-tokens` on a principal of a namespace (delegation family) for the holders of a grant on `@server`. Cost: with Option A, a namespace allowlist can be changed only by a namespace grant that carries the action, and today `givenOn` (`pkg/access/authorizer.go:733`) lets a namespace grant give the data family only, so nobody but the admin token can set it. A follow-up decides whether a namespace role holds that action.
   - Option B (by action): drop only the actions of the data family from a grant on `@server`. Cost: an operator with the setting `false` still manages the IP allowlist and the tokens of a tenant, which a reader of "never gives an action inside a namespace" may not expect.
   - The tasks and the spec delta below use Option A. A different answer changes tasks 2.2 and 2.3 and the scenarios of the delta.
2. **Do decisions 10 and 11 of ADR 216 belong here?** Decision 10 (`namespaces/-` answers only granted namespaces) and decision 11 (the server refuses a grant to an installation principal with no end time or an end later than 72 hours, and the policy check ignores it on a namespace) both depend on the setting. Recommendation: no. Decision 10 follows from task 2.2 through `ListObjects`, and this change tests that. Decision 11 is a separate change, because it adds a write-time rule and a list state. The PM names that change.
3. **What does the authorizer do when it cannot read the setting?** Recommendation: treat the value as `false` and deny, and log the key (decision 7 does not say; ADR 250 says fail closed). Option: use the default `true`, as `accessReview.collectionUnitCap` fails toward its default. Fail closed means a storage fault removes the operator reach to namespaces, not the tenant data.
4. **Does a restore keep the live value?** Recommendation: yes, add the key to `KeptSettingKeys`, so a backup cannot turn the reach on (the same rule as `restore.replace-access-tables`). Option: no, and the value comes from the backup.

Questions considered and answered:

- Does an accepted ADR already decide the setting? Yes. ADR 216 decision 7 gives the name, the default and the effect of the value `false`.
- Is a new dependency needed? No.
- Is the default handled by the existing serve settings? No. `boolSetting` has the default `false`. Task 2.6 adds a constructor for a default `true`.

Drafted by the PM from the lead's request for the change that adds the setting. Refined by an agent, with the questions above left for the PM.
