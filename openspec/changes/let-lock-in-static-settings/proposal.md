---
version: 1
---

Issue: #956, #2181, #2182.

## Problem

An Operator turns a feature off for the whole installation in the settings file and needs no API caller to turn it on again.

## Evidence

- Generic settings resource exists with one revision per scope, merge patch, and manage-settings action. Proof: Path `pkg/setting` exists; path `pkg/setting/service.go` exists; `internal/cli/settings_api.go` declares `setting.Declaration`; routes `ANY/GET/PATCH /api/dustpan/v1/settings` and `ANY/GET/PATCH /api/dustpan/v1/namespaces/{}/settings` are on main.
- Static layer constructor, store sync, and static_digest column exist on main. Proof: Symbols `cmd/opendustpan/settings_resource.go:24 (func serveFlags.staticLayer)`, `internal/cli/settings_api.go:140 (func NewStaticLayer)`, `pkg/store/postgres/settings.go:254 (func SettingStore.Sync)`, and `static_digest` in `pkg/store/sqlite/internal/db/queries.sql.go:23082`.
- Lock error codes and a static-layer lock test exist on main. Proof: Error codes `settings.locked` in `internal/errcode/catalog_settings.go:9` and `settings.static-only` in `internal/errcode/catalog_settings.go:17`; test `TestSettingsPlainConnectionsLockedByTheStaticLayer` in `internal/api/server/settings_test.go:468`.

## Fix boundary

- A lock marker in the static settings layer turns a declared module switch off; the settings API shows locked true with source static and refuses writes naming the static layer.
- The capabilities document shows the locked module as off; its routes answer the NEW error code `module.off` refusal after the sign-in check.
- Get, Patch, and Sync use the stored static snapshot; a divergent static layer fails readiness and refuses writes with 503 settings.unavailable plus Retry-After.
- Docs page for the static off lock in docs/reference/settings-api.md.

Out of scope:

- Locks that turn a feature on.
- Unit-defaults model merge; tracked apart.
- New optional modules or new switches.

## Check

- Add failing service tests for Patch refusal, Get locked flag, and namespace scope in `pkg/setting/service.go`. Check: `go test ./pkg/setting -run TestStaticOffLock -count=1` covers `pkg/setting/service.go`
- Add failing API tests for capabilities off, the NEW error code `module.off` refusal, and divergent digest 503 in `internal/api/server/settings_test.go:468`. Check: `go test ./internal/api/server -run TestStaticOffLockCapabilities -count=1` covers `internal/api/server/settings_test.go:468`
- Wire the stored-snapshot lock through Get, Patch, and Sync in `pkg/setting/service.go` on route `ANY/GET/PATCH /api/dustpan/v1/settings` with action installation:manage-settings. Check: `PATCH /api/dustpan/v1/settings` on a locked switch answers 409 settings.locked with an access token
- Wire the WARN line, @server entry, per-namespace first-apply entry, and admin report row for a security switch lock in `internal/api/server/settings_test.go:468`. Check: `go test ./internal/api/server -run TestStaticOffLockReports -count=1` covers `internal/api/server/settings_test.go:468`
- Write the docs page section on the static off lock in `docs/reference/settings-api.md` and run the usability check. Check: Operator completes the path from `docs/reference/settings-api.md` alone; refusal names the one file fix
- Run the full gate and confirm wiring, routes, e2e step, docs page, and usability in `docs/reference/settings-api.md`. Check: `make check` reports zero findings after `GET /api/dustpan/v1/settings` with an access token passes on route `ANY/GET/PATCH /api/dustpan/v1/settings`

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Static layer is built from flags, env, and file in serveFlags. (`cmd/opendustpan/settings_resource.go:24 (func serveFlags.staticLayer)`)
- Sync writes the static digest for the settings store. (`pkg/store/postgres/settings.go:254 (func SettingStore.Sync)`)
- Settings route test covers plain connections locked by the static layer. (`internal/api/server/settings_test.go:468 (func TestSettingsPlainConnectionsLockedByTheStaticLayer)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 211: Optional modules, one switch each, and the capabilities document** (not owner-tagged).

- Decisions that apply: 2 (current), 3 (current), 5 (extended).
- Changed by: ADR 237 extends decision 5; ADR 249 extends; ADR 286 changes. Chain to the end: ADR 237, ADR 249, ADR 286, ADR 252, ADR 285, ADR 324, ADR 253, ADR 297, ADR 301, ADR 312, ADR 320, ADR 333, ADR 332.
- Current rule: this ADR, with ADR 237, 249, 286.
- Quote: "One declared switch for each optional module. An optional module is a feature outside the core that has its own routes, its own schedules or both, for example the Catalog, Dusty or Sweep."
- Quote: "When a module is off, each of its routes answers 404 with the error code `module.off` (ADR 182)."
- Effect: Module switches stay declared installation settings; an off module keeps the 404 module.off answer after the sign-in check; the capabilities document keeps the public version-only answer.
- Conflict: none.

**ADR 212: The planner role, SMTP settings, fresh MFA for weaker settings and approvals, trusted proxies, restore audit entries, the branding preset and one settings API** (owner-tagged: decision 7).

- Decisions that apply: 7 (current).
- Changed by: ADR 227 changes decision 3; ADR 232 changes decision 6; ADR 235 changes decision 6; ADR 301 extends decision 2; ADR 365 changes decision 5; ADR 373 extends decision 1. Chain to the end: ADR 227, ADR 232, ADR 235, ADR 301, ADR 365, ADR 320, ADR 339, ADR 354, ADR 288, ADR 332.
- Current rule: this ADR, with ADR 227, 232, 235, 301, 365, 373.
- Quote: "One settings API (owner decision 2026-10-18). The installation has one settings resource, and each namespace has one:"
- Effect: The one settings resource per scope keeps one revision and the ETag; a static off lock shows locked true with source static and a PATCH refuses.
- Conflict: none.

**ADR 219: A lock in the static layer can turn a feature off (owner)** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current), 2 (current), 3 (current), 4 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "A lock can set a switch to off (owner). The static layer can lock each declared switch to on or to off, also a security switch."
- Quote: "Lower layers follow the lock. When the static layer locks a switch off, no namespace can turn it on."
- Quote: "A refused request names the switch and says that the operator locked it."
- Quote: "each start writes a WARN log line that names the switch, and an audit entry in the `@server` scope (ADR 63 decision 10)."
- Effect: The change applies the static off lock, lower scopes follow it, refusals name the operator lock, and a security switch lock writes the start reports.
- Conflict: none.

**ADR 63: Installation settings: precedence, a YAML file, and a record of each change** (not owner-tagged).

- Decisions that apply: 1 (changed), 5 (current).
- Changed by: ADR 124 changes decision 1. Chain to the end: ADR 124, ADR 128, ADR 210, ADR 216, ADR 190, ADR 222, ADR 227, ADR 286, ADR 354, ADR 247, ADR 273, ADR 225, ADR 299, ADR 300, ADR 344, ADR 365, ADR 335, ADR 351, ADR 301, ADR 320, ADR 339, ADR 328, ADR 359, ADR 368, ADR 332.
- Current rule: this ADR, with ADR 124.
- Quote: "Precedence: option A of RFC 68. Flag, then environment variable, then the file, then the database, then the built-in default."
- Quote: "Only the file reloads. SIGHUP reloads it, and a poll of its content hash (10 seconds) catches a change that no one signals (RFC 68 option D)."
- Effect: Static values lock the setting and the API refuses the change; a reload keeps the old configuration when the audit write fails.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- May a static lock turn a feature off? Yes. ADR 219 decisions 1 and 2 allow a static lock off and bar lower scopes from turning it on.
- Which instance answers when static layers differ? ADR 212 decision 7 keeps one revision per scope and the ETag; all instances answer and lock from the stored static snapshot in settings_state, and a divergent instance fails readiness.
- What reports a security switch turned off by a lock? ADR 219 decision 4 lists the WARN line, the @server audit entry, the admin access report entry, and the per-namespace entry; the specs cover each one.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
