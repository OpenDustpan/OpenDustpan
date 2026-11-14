## 1. Failing tests

- [ ] 1.1 Write the table test TestCheckAIPRSpec in the NEW file `scripts/checkaiprspec/checkaiprspec_test.go`, with a NEW `scripts/checkaiprspec/leak_test.go`. It runs the script on fixture bodies, commit files and change folders in a temporary directory: an agent pull request with a spec-ready link passes; with no link it fails and prints the fix line; with a link to a change whose open questions are not "none" it fails and names the change; with a link only inside a fenced code block it fails; an archived change passes; a person's pull request with no link passes; each of the two AI signals alone makes the pull request AI-authored; an `<id>` with `..`, a slash or capital letters is refused. Check: the test fails on main

## 2. Build

- [ ] 2.1 Add the NEW script `scripts/check-ai-pr-spec.sh` (bash, `set -euo pipefail`, no network). It validates `<id>` with an allowlist before it builds a path, and reads only under `openspec/changes`. Check: `go test ./scripts/checkaiprspec/...` passes, and `shellcheck` reports nothing when it is installed
- [ ] 2.2 Add the NEW target `ai-pr-spec-check` to the `Makefile` with `PR_BODY_FILE` and `PR_COMMITS_FILE`, with no defaults; the target fails with a usage line when one is unset. It is not part of `make check`. Check: `make ai-pr-spec-check` with no variables prints the usage line and fails
- [ ] 2.3 Add the step to `.github/workflows/ci.yml` for `pull_request` only, after the checkout (with the history of the pull request): write the body from `github.event.pull_request.body` through an environment variable to a file, write `git log --format='%ae' <base>..<head>` to a file, and run `make ai-pr-spec-check`. Check: `make semgrep` reports nothing for the workflow. Confirmed by: CI (the agent runs the fast checks only).

## 3. Definition of done

- [ ] 3.1 Note in `CONTRIBUTING.md`, section "Use of AI tools", that an AI-authored pull request names its OpenSpec change with the line `Change: openspec/changes/<id>`. Check: make docs-lint passes
- [ ] 3.2 Run the local checks (`Makefile`). Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
