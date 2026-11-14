## Context

Local run-all uses the current TFE-compatible API with no server change. The fixture pre-creates one workspace per unit directory before Terragrunt runs.

## Decisions

1. **No wrapper, pre-created workspaces.** Make no server change. Pre-create each workspace before the run through the current workspace create route. Map each unit directory to one distinct workspace name. Run Terragrunt at parallelism 3 with no lock timeout. Record no change order.

## Structure

Files: `e2e/terragrunt_local_test.go` (NEW) with its fixture in `e2e/testdata/terragrunt-local/` (NEW); `docs/guides/terragrunt.md` (NEW) linked from `docs/guides/quickstart.md`; `docs/explanation/profiles-and-blueprints.md` (NEW) linked from `docs/guides/terragrunt.md` and `docs/guides/publish-a-blueprint.md`. No new type and no new store. Isolation uses the current per-workspace lock and state versions in pkg/workspace through its current constructor.

## Risks

- Two unit directories that name one workspace merge their state into one chain.
- A held lock blocks another unit only when both name one workspace.
