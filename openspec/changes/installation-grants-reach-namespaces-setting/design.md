## Context

ADR 216 decision 7 (owner) says: with `access.installationGrantsReachNamespaces` set to `false`, a grant on `@server` gives only the actions of the operator scope and never an action inside a namespace. On main the setting does not exist. The authorizer adds `@server` to the chain of each namespace, collection, unit and namespace principal, and reads no setting. This change adds the setting and makes every reader of the grants follow it.

## Decisions

1. **One declaration.** NEW `SettingInstallationGrantsReachNamespaces = "access.installationGrantsReachNamespaces"` in `pkg/access/settings.go`, next to `SettingCollectionUnitCap`. It is added to `SettingDeclarations()` after the existing entry: kind `setting.KindBool`, level installation, default `"true"`, `Database: true` (the static layer can lock it, so OpenDustpan Cloud locks `false`, ADR 63), `Apply: setting.ApplyLive`, no secret, no `Check` (the bool kind validates the text). `ReducesSecurity(old, next)` is true when `old == "false" && next == "true"`: a change that lets a grant on `@server` reach namespaces needs the fresh authentication that the settings API already asks for. The doc text is in strict STE.
2. **One reading of the value.** NEW unexported `installationReachesNamespaces(ctx, settings Settings) (bool, error)` in `pkg/access/settings.go`. A nil `Settings` returns `true`, the declared default (the configs of the conformance suites that do not test the setting have no settings port). A read that fails returns the error, and a value that is not `true` or `false` returns an error. The callers deny on an error (fail closed, step 5 of the rules for security code). The value is read once for each request: the request cache of `pkg/access/cache.go` keeps it, so a decision never reads two different values, and the next request reads the new value. `Authorize` and `ListObjects` write the denial to the audit log as for any other denied decision.
3. **One place decides the chain.** `chainObjects` (`pkg/access/authorizer.go`) gets one new argument, `reachesNamespaces bool`. When it is `false`, the chain of a namespace, a collection, a unit and a principal of a namespace has no `identity.ServerObject()`. The chain of the installation object and of a principal of the installation keeps it, because these objects are in the operator scope. Operator actions are always checked on `@server` or on an installation principal, so they stay allowed through a grant on `@server`. The authorizer (`Authorizer.chain`), the reach (`Reach.chain`), the access review (`review_service.go` through `s.authz.chain`) and the grant checks (`service.go` through `s.authz.chain`) all call this function, so they all follow the setting with no second rule.
4. **The scope lists follow the same value.** `ListObjects` and the grant ID discovery read grants through `reachGrants` and `grantInScope`, not through the chain. `grantInScope` returns `false` for a grant on the installation when the value is `false` and the action is not an action of the operator scope (a role that holds a namespace action and a grant on `@server` then lists no namespace). `addToScope` is not changed: it never sees such a grant. With `false`, the wildcard read `namespaces/-` lists only the namespaces of grants on a namespace, a collection, a unit or a principal of a namespace (ADR 216 decision 10). The operator-scope check is `FamilyOf(action) == FamilyOperator` plus the administration actions that ADR 216 decision 3 puts in the operator scope: the test of task 2.2 compares both lists, so a new action cannot go to the wrong side without a failing test.
5. **The bootstrap administrator is not a grant.** `Authorize` returns before it reads any grant when `p.Admin` is set (ADR 6: the holder of every action on the installation, with no grant). The decision 7 text speaks of a grant on `@server`, so this change does not change the bootstrap administrator and records it as a limit of the setting in `docs/guides/roles-and-grants.md` ("the bootstrap administrator token is not a grant: the setting does not limit it"). No decision of ADR 216 says otherwise, and this change makes none. (Benchmark: the cloud practice of a break-glass account that no tenant setting limits, with an audit entry for each use.)
6. **Support access stays an ordinary grant.** ADR 216 decision 11 gives support access as a grant on the namespace. A grant on a namespace is not a grant on `@server`, so this change needs no code rule for it.
7. **The settings port reaches the authorizer and the reach.** `AuthorizerConfig` and `ReachConfig` get a field `Settings Settings` (the interface that `ServiceConfig` already has). The `Authorizer` and `Reach` keep it. The copy of the authorizer that `Service.within` (`pkg/access/check.go`) builds for the transaction of a write takes the same settings port, with the chain that the Service read before the write. The `Settings` implementation is the one that `pkg/dustpan/dustpan.go` builds for `ServiceConfig`; the construction moves before `access.NewAuthorizer` in that file, because the authorizer is built first on main.
8. **Hosted installations.** The static layer locks `false` in OpenDustpan Cloud (outside this change, per "Out of scope" in the proposal). A self-hosted installation that sets `false` ends the access of each installation person in a namespace, except the grants on that namespace (ADR 215 decision 11). The effective value and its source show in `GET /api/server/settings` like any setting, so a person can ask why a namespace action was denied (No magic).

## Structure

- One function decides the chain, one function reads the value. No second rule in the review, the reach or the service.
- The setting is read through the port that the Service already has. No new dependency and no new tool.

## Layers

Security code: each layer has its own table of hostile cases, and each guard has a test that fails when the guard is removed.

| Layer | Hostile case | Expected |
| --- | --- | --- |
| Port adapter (`pkg/access/accesstest`, both dialects) | A grant on `@server` of a role with a namespace action, setting `false`, `Authorize` on a namespace, a collection, a unit, and a principal of a namespace | denied |
| Port adapter | The same grant, setting `true` | allowed |
| Port adapter | A grant on `@server` of an operator role, setting `false`, `Authorize` on `@server` and on a principal of the installation | allowed |
| Port adapter | A grant on the namespace of the same role, setting `false` | allowed on that namespace, denied on another |
| Service (`pkg/access`) | The settings read fails, or the value is `maybe` | denied, error returned, one audit entry |
| Service | No settings port | the default `true` applies |
| Service | `ListObjects` and discovery, setting `false`, grant on `@server` | the scope has no namespace, so `namespaces/-` lists only the granted namespaces |
| Service | A change of the setting between two requests | the second request reads the new value |
| Service | Access review of a namespace, setting `false` | no row "inherited from `@server`" for a namespace action |
| HTTP handler (`internal/api/server`) | A caller with only a grant on `@server`, setting `false`, any namespace route | 403 that names the action and the namespace |
| HTTP handler | `PATCH` of the setting to `true` with no fresh authentication | refused as for any `ReducesSecurity` change |

## Files that the code touches

Each file is named only when the design needs it.

- `pkg/access/settings.go`: the declaration (decision 1) and the reader (decision 2).
- `pkg/access/authorizer.go`: `AuthorizerConfig.Settings`, `chainObjects` (decision 3), `grantInScope` (decision 4), and the one read of the value for each decision.
- `pkg/access/reach.go`: `ReachConfig.Settings` and `Reach.chain` (decisions 3 and 7). `Reach.Has` and `ActsAs` change through the chain only.
- `pkg/access/check.go`: `Service.within` builds an `Authorizer` literal; it takes the settings port (decision 7). `roleCheck` (the escalation check on `@server`) stays: it compares the roles of a grant on the installation and is not a reach decision.
- `pkg/access/protected_batch.go`: the function builds a `ReachConfig` literal (line 207); it passes `Settings` on, so that the protected flags of a list match `Protected`.
- `pkg/access/authorizer_test.go`, `pkg/access/settings_test.go` and `pkg/access/protected_internal_test.go`: the new cases, and the `ReachConfig` literal of the internal test, which needs the new field.
- `pkg/access/accesstest/reach.go`, `bench.go`, `bench_caller.go`, `bench_protected.go` and `moves.go`: each builds an `AuthorizerConfig` or `ReachConfig` literal with every field named (the linter `exhaustruct` requires it), so each literal gets `Settings`. `reach.go` also gets the new conformance cases (task 1.2). The other files of `accesstest` build no such literal and stay as they are.
- `pkg/dustpan/dustpan.go`: the settings reader is built before the authorizer and given to it (decision 7). `pkg/dustpan/localusers.go` builds a `ReachConfig` literal (line 299) and passes the same reader.
- `cmd/opendustpan/host.go`, `e2e/coverage_server.go` and `e2e/coverage_signin.go`: each builds a `ReachConfig` literal; each gets `Settings` (the same reader, or nil where the file builds a test server with no settings).
- `internal/cli/settings_api_guard_test.go`: the key goes in `databaseKeys`.
- `e2e/installation_grants_reach_test.go` (NEW): the e2e scenario of task 3.1.
- `docs/reference/settings-api.md`, `docs/guides/roles-and-grants.md`, `docs/guides/access-review.md`, `docs/reference/code-rules.md`: the pages of task 3.2.

Files that this change leaves out, and why:

- `pkg/access/review_service.go`: it reads the chain with `s.authz.chain` (lines 586 and 630), so it follows decision 3 with no edit. Line 735 sets the containers of the installation object itself, which stays on `@server` in both modes.
- `pkg/access/service.go`: its chain calls (lines 377 and 1023) go through `s.authz.chain` and its `Authorize` calls on `@server` are operator-scope checks. No edit.
- `pkg/access/discovery.go`: it reads `reachGrants`, which decision 4 changes. No edit.
- `pkg/access/cache.go`: the request cache keeps the value through the existing context key pattern; the key is added in `settings.go` with the reader, so no edit.
