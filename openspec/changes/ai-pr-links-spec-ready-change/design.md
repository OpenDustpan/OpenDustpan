## Context

A pull request that an agent opens must link a spec-ready OpenSpec change, but nothing checks this. An agent pull request with no change, or with a change that still has open questions, reaches review and the maintainer finds out by hand.

This change adds a script, a Makefile target, a CI step, and a table test. It changes no server code, no route, and no schema. It is not part of `make check` because it needs a pull request.

## Decisions

1. **Two signals make a pull request AI-authored.** A pull request is AI-authored when a commit has the author email `noreply@anthropic.com`, or when the body has a disclosure line that starts with `Tool:` or `AI tool:`. Any other pull request is a person's pull request and passes with no link.
2. **A link names a spec-ready change.** A link is a line of the body, outside a fenced code block, that names `openspec/changes/<id>`. The link counts when `openspec/changes/<id>/proposal.md` exists on the head of the pull request, or when an archived change exists, and its `Open questions` section reads `none`. At least one link must count. When a named change is not spec-ready, the failure names that change and its open questions.
3. **The script reads from files, never from shell text.** The NEW script `scripts/check-ai-pr-spec.sh` takes the body file and the commit file (one author email per line) and reads the change folders from the checkout. The NEW Makefile target `ai-pr-spec-check` takes `PR_BODY_FILE` and `PR_COMMITS_FILE` with no defaults and fails with a usage line when one is unset. The NEW CI step runs on `pull_request` only, after checkout: it writes the body from the event through an environment variable to a file, writes the commit emails to a file, and runs the target.

## Structure

No new server structure. The bug class is an unchecked pull request process, which no code structure can express, so a script and a CI step carry the check. The table test `TestCheckAIPRSpec` in `scripts/checkaiprspec` runs the script on fixture bodies, commit files, and change folders in a temporary directory.

No custom analyzer and no new tool for the Go code. The script is bash with `set -euo pipefail` and no network.

## Risks

- A body that names the change only inside a fenced code block does not count. The test holds that case.
- An archived change moved to a dated folder still counts. The script accepts the dated archive path.

## Layers (defense in depth)

The `<id>` that the script reads from the body is untrusted input and goes through three layers, each with its own hostile case in the table test:

| Layer | Guard | Hostile test |
| --- | --- | --- |
| length cap | Refuse an ID over 80 bytes before any regex runs. | An ID of 81 bytes is refused with no regex run. |
| allowlist | Check `^[a-z0-9][a-z0-9-]{0,79}$` before any path is built; read only under `openspec/changes`. | `../x`, an upper-case ID, and an ID with a space are refused. |
| log form | Before the script writes a refused ID, remove CR, LF, and ESC and cut the text to 80 bytes, so the log holds one plain line. | An ID with `\r\n` and an ANSI escape writes one plain line. |

Each layer stops its case alone, and each check that looks redundant carries a comment that names the other layers.
