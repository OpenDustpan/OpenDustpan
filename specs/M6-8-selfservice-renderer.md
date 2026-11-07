# M6-8: the self-service renderers for OpenTofu and Pulumi (internal/selfservice/render)

- Class: SECURITY (untrusted answers become files that a tool runs)
- Routing: Sonnet author, Opus review
- Branch: `m6-8-selfservice-render`
- Conflict group: `selfservice-render`
- Persona path: P6 self-service, step `runner_applies` (#2610); milestone 6
  exit test step 1.
- Depends on: none.

## Facts checked at main 9e96ae30c8c3

- `internal/selfservice/ports.go:195-239`: `File`, `RenderInput`
  (`Version`, `Document`, `Unit`), `Rendered` (`Operation`, `Files`, `Keys`),
  and the port `Renderer` (`Tool()`, `Render(ctx, in)`), with its contract:
  - OpenTofu: a root module of one module call and its `.tfvars.json`.
  - Pulumi: a Pulumi YAML program of one component resource and its stack settings.
  - Two renders of one input give the same bytes.
  - No secret value in a render.
  - Answers and platform values are data only: JSON encoder into the
    variables file or the stack settings, never into the program text. The
    program names only keys that `ValidKey` checked at publish.
  - `ErrOneHome` for a key that another home of the unit also sets,
    compared in the form of `NormalizeKey`.
- No implementation of `selfservice.Renderer` exists (search of the module).
- `internal/selfservice/blueprint.go`: `Program{Module, Component}` (67),
  `Module{Source, Version, Connection, Repository, Directory, Revision}` (74),
  `Component{Package, Version, Type}` (85), `ValidKey(tool, key)` (297),
  `NormalizeKey(b, key)` (354), `CheckOneHome(b)` (372), `reservedModuleArgs` (283).
- `internal/selfservice/check.go:247`: `AnswerValues(b, d)`.
- `internal/selfservice/document.go:183`: `Document.Canonical(b)`; `jcs.go` canonical JSON.
- `internal/selfservice/selfservice.go:81-84`: `ErrOneHome`; `Operation` (214).
- depguard `domain` rule covers `**/internal/selfservice/**` (`.golangci.yml:262`),
  so the new package imports no `net/http`, SQL or `internal/api`.
- ADR 112 (blueprint documents), ADR 110 decision 8 (render), ADR 286 (renames).

## Files it may touch

- `internal/selfservice/render/` (new): `render.go` (shared), `tofu.go`,
  `pulumi.go`, tests, `leak_test.go`, `testdata/` golden files
- `internal/selfservice/selfservicetest/renderer.go` (new): `RunRenderer`
  conformance suite, run by both adapters
- `internal/selfservice/ports.go` (doc comment only, if the contract needs a precise word)

## Files it must not touch

- `internal/selfservice/*.go` other than the doc comment above
- Wiring in `pkg/dustpan` (M6-9 items wire it)
- `internal/expr` (the render evaluates no expression)

## Change

1. `render.NewTofu()` and `render.NewPulumi()` return a `selfservice.Renderer`.
2. OpenTofu: `main.tf` with one `module` block. Its `source` and `version`
   come from `Program.Module`; each argument is `var.<key>` for a key of the
   blueprint, with a `variable` block per key. Values go only into
   `terraform.tfvars.json` through `encoding/json`. Escape nothing by hand:
   the program text holds only keys that pass `ValidKey(selfservice.ToolOpenTofu, key)`;
   check each key again before it goes into the text, and refuse with
   `ErrInvalid` (defense in depth, the publish checks it too).
3. Pulumi: `Pulumi.yaml` with one resource of `Component.Type`, the package at
   its exact version, each property `${<key>}` from config; values go into the
   stack settings file through a YAML encoder that writes each value as a JSON
   scalar or object (no anchors, no tags). Keys pass `ValidKey(selfservice.ToolPulumi, key)`
   again.
4. Deterministic: sort keys; fixed file order; no time, no random value.
5. `Keys` lists each config key in `NormalizeKey` form. Return `ErrOneHome`
   with the key and both homes when a key is set twice.
6. Paths in `Files` are relative, clean, with no `..`, and match an allowlist
   of the file names above.

## Acceptance

- `RunRenderer` suite (both tools): golden files; two renders equal byte for
  byte; `Keys` complete; `ErrOneHome`.
- Hostile, one test for each layer, each alone:
  - boundary: a key `a"; resource "x" "y" {}` and `${evil}` given to the
    renderer directly (bypassing publish) is refused with `ErrInvalid`.
  - encoding: an answer `"}\nresource \"null_resource\" \"x\" {}\n` and a YAML
    answer `!!python/object` and `&a [*a]` stay data: the program text is the
    same as with a plain answer, and the value file parses back to the answer.
  - limit: an answer over the document limit, 10,000 keys: refused, bounded memory.
  - containment: each `File.Path` is relative and in the allowlist.
  - fail closed: an unknown tool, a `Program` with both or neither of
    `Module` and `Component`: error, no files.
- A fuzz test of the OpenTofu value path: any string answer gives a
  `.tfvars.json` that decodes back to it.
- No user docs page: no user-visible change until M6-9 wires it.
- `make check` zero findings.
