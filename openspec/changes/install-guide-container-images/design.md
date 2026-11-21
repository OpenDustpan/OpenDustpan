## Context

The container image install page lived outside the user guides, had no link from the README, and named no digest form. An operator could not find the page or pull by digest. The code that the page describes already shipped.

This change moves and extends docs pages and adds an e2e step. It changes no server code, no route, and no option.

## Decisions

1. **The page lives in the user guides.** `docs/install.md` moves to `docs/guides/install.md` with `git mv`. The README links the new path in the install section. The page gains a section "Pull by digest" with the form `ghcr.io/opendustpan/opendustpan@sha256:<digest>` and the two places of each release digest: the summary of the release workflow run and `docker buildx imagetools inspect <image>:<tag>`. The page does not name the release notes, which hold no digest.
2. **The e2e step runs what it can without a registry.** The NEW step `TestInstallGuideCommands` in `e2e/install_guide_docs_test.go` reads the page, takes the `helm install` command, and runs `helm template` of `deploy/helm/opendustpan` with the same `--set` values. Commands that need a registry, a cluster, or repository settings (`docker pull`, `kubectl`, `helm install` against a cluster, `gh secret set`) stay as steps of the usability check. The existing step `TestInstallPageListsBothPullCommands` keeps checking the tag and digest pull commands plus the new digest section.

## Structure

No new server structure. The bug class is a missing docs task for shipped code. The e2e helpers are unexported functions of the new test file; only the tests call them. No lint setting is added.

No custom analyzer and no new tool.

## Risks

- The digest places change when the release workflow changes. The page names the workflow file and the inspect command, so a move is found by the docs route check.
- `helm template` passes while `helm install` on a real cluster fails. The usability check installs the chart on a cluster and pulls the image by tag and by digest.
