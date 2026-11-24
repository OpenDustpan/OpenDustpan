## Context

ADR 216 decision 7 adds the setting `access.installationGrantsReachNamespaces`. Decision 7 is owner-tagged: this design keeps its words. The change has two parts: the setting (declaration, static key, docs) and the effect on grant evaluation.

## Setting

| Property | Value |
| --- | --- |
| Key | `access.installationGrantsReachNamespaces` (the key pattern of `pkg/setting` allows camelCase) |
| Level | installation |
| Kind | `setting.KindBool` |
| Default | `true` |
| Layers | static (flag, variable, file key), then database, then default (ADR 63) |
| Apply | `setting.ApplyLive` |
| Check | the value is exactly `true` or `false`; any other value is refused with 400 |
| ReducesSecurity | true when the next value is `true` and the old value is not, and when either value does not parse |
| Secret | no |

The declaration lives in `pkg/access/settings.go` in `SettingDeclarations()`, like `accessReview.collectionUnitCap`. `internal/cli/settings_api.go` already appends `access.SettingDeclarations()` to the held list, so the settings API shows the key without more wiring.

### Effective value in the API

The settings API (`GET /api/dustpan/v1/settings`) shows the item with `value`, `source` (`default`, `database` or the static source), `locked`, `lockedBy` and `reducesSecurity`. No new route or field.

### Static key

Add a serve setting with the flag `--access-installation-grants-reach-namespaces`, the variable `DUSTPAN_ACCESS_INSTALLATION_GRANTS_REACH_NAMESPACES` and the file key `access.installationGrantsReachNamespaces`. The pattern is `allow-plain-connections` in `internal/cli/settings.go` (like `boolSetting(...).WithFile("egress.allowPlainConnections")`). That constructor defaults to `false`. This setting defaults to `true`, so task 2.6 adds a constructor whose default is `true`. The static layer skips a value whose source is the default, so a default `true` in the serve setting locks nothing. A test proves that.

### Who may change it

The caller needs `server:manage-settings` (the settings API rule). A change to `true` needs a fresh step: the answer is 403 `mfa.fresh-step-required` without one. A change to `false` needs no fresh step, because it narrows reach. A locked key answers 409. These are the rules of `openspec/specs/settings-api/spec.md`; the change adds no new code for them, and tests each one for this key.

### Audit record on change

The settings API writes one audit entry for each changed key, with the old and the new value. The change adds no audit code. A test proves the entry for this key.

### Restore

Question 3 of the proposal. Under the recommended answer, `KeptSettingKeys()` and `IsKeptSettingKey` in `pkg/backup/restore_access_setting.go:120`, `:127` list the key (today one key), so a restore keeps the live row. The storetest cases `pkg/store/storetest/restore_setting_keep.go` get a row for the key.

## Effect on grant evaluation

Under Option A of question 1, the rule is: while the value is `false`, the chain of an object inside a namespace has no `@server` object. An object inside a namespace is a namespace, a collection, a unit, or a principal that has a namespace. The installation object, and a principal with no namespace, keep `@server`. A grant on `@server` then still gives each action on those objects (the operator scope).

### Where

| Place | Change |
| --- | --- |
| `chainObjects`, `pkg/access/authorizer.go:921` (the lines at `:945`, `:948`, `:967`) | takes a flag `installationReaches`. When it is `false`, it does not append `identity.ServerObject()` for a namespace, collection, unit or a principal of a namespace. `Authorizer.chain` (`:907`) and `Reach` (`pkg/access/reach.go`) pass the flag, so `Authorize`, `Explain` (`:188`) and the reach agree. |
| `Authorizer.Authorize`, `pkg/access/authorizer.go:111` | reads the effective value once for each call, before the chain. The per-authorizer chain cache `a.chains` must not hold a chain across a change of the value: the cache key includes the flag, or the cache holds only the chain without `@server` and the code appends it. The test of task 1.4 proves a change of the value takes effect on the next call. |
| `scopeFor` and `reachGrants`, `pkg/access/authorizer.go:296`, `:315` | when the value is `false`, drop a grant whose object is the installation if the action is one that an object inside a namespace takes. For `ListObjects` this means: for an action of the data family, the scope has no `Every()` from `@server`. For `principal:manage-tokens`, `ListObjects` keeps the installation grant only for the principals with no namespace. `Scope` cannot exclude the principals of namespaces (`identity.Scope`, `pkg/identity/identity.go`: `Every(except...)`), so the code returns an explicit set: the principals with no namespace. Task 2.3 specifies the lookup. |
| `pkg/access/discovery.go:102` | uses `reachGrants`: no change, and a test proves the discovery shows no installation grant for a namespace action. |
| `pkg/access/review_service.go:423`, `:735` and `pkg/access/service.go:216`, `:957`, `:1159`, `:1233`, `:1303` | read each use. A use that lists what a grant on `@server` reaches reads the flag. The flag in the review item is `reachesNamespaces`. A use that only reads the operator scope needs no change. Task 2.4 lists the result of the read in the pull request body. |
| `AuthorizerConfig`, `pkg/access/authorizer.go:49` | new field `Settings Settings` (the interface at `pkg/access/service.go:71`). It is required, like `Principals`, so a new caller cannot skip the read. |
| `pkg/dustpan/dustpan.go:3369`, `:3477` | pass the settings reader. Other callers of `NewAuthorizer`: `pkg/store/storetest/delegation.go:131`, `pkg/access/accesstest/bench.go:74`, `moves.go:86`, `bench_caller.go:128`. They pass a fixed reader. |

### Failure mode

Under the recommended answer of question 3: a reader that fails, a value that does not parse, or a nil reader gives `false`. The authorizer denies the namespace action and logs the key with `slog` (no value, no secret). The reader has no cache longer than the settings service cache, so a PATCH takes effect in the live time of that cache. The test of task 1.4 states the time bound from the cache of the settings service.

### What stays the same

- Grants on a namespace, a collection or a unit still work. Only the path through `@server` closes.
- The admin token (`p.Admin`) has every object. This change does not change it. (`pkg/access/authorizer.go:258`: `ListObjects` returns `Every()` for it.)
- Operator actions on the installation object.
- The data in a database: no row changes. Turning the value back to `true` restores the reach at once.

### Cost of Option A (question 1)

`namespace:manage-ip-allowlist` is authorized on a namespace object (`internal/api/server/ip_allowlist.go:115`). Only a grant on `@server` can give it today, because `givenOn` (`pkg/access/authorizer.go:733`) limits a namespace grant to the data family. With `false` and Option A, only the admin token changes a namespace allowlist. The guide says so. The follow-up that gives a namespace role this action is not in this change.

## Housekeeping probes

- `TestRevokedNamespaceGrantDeniesManageAgentRuns` (NEW `pkg/access/namespace_housekeeping_grants_test.go` of `housekeeping-principals-install`): this change adds the run with the setting `false`. After a revoke, `namespace:manage-agent-runs` is denied, and `server:sweep-agent-reveals` stays allowed on `@server`.
- `TestNamespacePurposesWorkWithoutInstallationReach` (NEW `pkg/job/housekeeping_reach_test.go` of `housekeeping-principals-sweeps`): this change adds the run with the setting `false`, for the purposes retention, saved view cleanup, issuer rotation, reaper and reconcile on a new namespace.

Both files come from those changes. If they are not on main when an agent starts this change, task 2.8 waits for them (see the tasks).
