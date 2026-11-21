## Context

Two gaps stall the auto-regen workflows that regenerate the generated files of a pull request after main moves. The resolve workflow succeeds but the push workflow never starts, so the generated files stay stale. A busy main also cancels the running resolve run of a pull request, so the pull request starves.

This change touches workflows and one dispatch script only. It adds no server code, no route, and no secret.

## Decisions

1. **Dispatch with the App token so the push run starts.** The dispatch job of `.github/workflows/auto-regen.yml` mints a token of the regen GitHub App with `permission-actions: write` only and passes it as `GH_TOKEN` to the dispatch. A run that the App token starts can start the push workflow through `workflow_run`, while a run that the `GITHUB_TOKEN` starts cannot. The push workflow keeps every guard: the run is a `workflow_dispatch` of the resolve workflow in this repository, the pull request is open and its head is the head of the run, main did not move, and the patch changes only generated paths.
2. **Group resolve runs by pull request and head, and never cancel.** The concurrency group of `.github/workflows/auto-regen-resolve.yml` becomes `auto-regen-resolve-<pr>-<head>` with `cancel-in-progress: false`. A newer main no longer cancels a run for the same head. A newer head of the pull request gets its own group, and the push workflow refuses the older result because the head moved.
3. **Skip a dispatch when a run for the head already works.** The dispatch step lists resolve runs in state `queued` or `in_progress` for the pull request head and starts none when one exists. It logs one line with the pull request number and the head. Every existing check of the dispatch stays: the number, the full SHA of the head, and the refusal of `main` and `gh-readonly-queue/*`.

## Structure

No new server structure and no new workflow. The bug class is a missing workflow trigger and a too-wide cancel group. The group name and the skip check are the structure; the App permission is checked once in the first task and recorded in the pull request body. If the App lacks Actions write, the work stops and a person grants it in the settings of the App.

No custom analyzer and no new tool. The workflow edits travel as a full diff in the pull request description under the heading `Workflow patch (apply locally)`, because the agent cannot push workflow files.

## Risks

- The push workflow still refuses a patch outside the generated paths. That refusal is intended and stays.
- A newer head during a resolve run leaves the older result unpushed. The push log says the head does not match, and the new head gets its own resolve run.

## Failure modes

| Step | Failure | Check after |
| --- | --- | --- |
| Dispatch for a head with a running resolve run | Starts a second run for the same head | The run list shows one resolve run for that head and the skip line in the log. |
| Push to main during a resolve run | Cancels the run | The run ends `success`, not `cancelled`. |
| New head during a resolve run | Pushes the older result | The push log says the head does not match and pushes nothing. |
| Patch outside the generated paths | Pushes an untrusted change | The push log refuses the patch and pushes nothing. |
