---
version: 2
---

Issue: #945.

Short form: a missing docs task. It finishes task 2.3 of
publish-container-images-forge-registry, whose code merged in #3103.

## Problem

**Persona: operator.** An operator installs a release from the container
image and follows the install page alone. The page of #3103 is
`docs/install.md`, outside `docs/guides/`, the place of the user guides.
README.md does not link it, so an operator does not find it. The page does not
say where the digest of a release is, so the operator cannot pull by digest.
No test runs the commands of the page.

### Evidence

On main before this change:

- `docs/install.md` existed (#3103). `ls docs/guides/install.md` found
  no file.
- `e2e/install_docs_test.go` (TestInstallPageListsBothPullCommands) read
  `docs/install.md` and checked that strings are on the page. It ran no command.
- `README.md` has no link to the install page (`grep -n 'install' README.md`
  found no line that links the page).
- The release workflow writes the image digest of each tag to the summary of
  its run only (`.github/workflows/release.yml:211`,
  `echo "digest=$FORGE_DIGEST" >> "$GITHUB_STEP_SUMMARY"`). The comment at
  `.github/workflows/release.yml:16-18` records why: the job keeps
  `contents:read`, and an edit of the release notes needs `contents:write`
  (a trade-off that the owner confirmed). The release notes hold no digest.

### Prototype output

The check of task 1.3 and the scan of the page for the lines that start a `helm install` command, run on main after the change:

```
$ grep -n 'docs/guides/install.md' README.md
399:- [Install from the container image](docs/guides/install.md): build the
$ grep -n '^helm install' docs/guides/install.md | cut -c1-16
113:helm install
187:helm install
```

The test takes the first command (line 113) and its continued lines, and compares the four `--set` values with the arguments of the `helm template` command.

## Fix boundary

- Move `docs/install.md` to the NEW path `docs/guides/install.md` with
  `git mv`, and change the path in `e2e/install_docs_test.go`.
- Add to the page a section "Pull by digest": the form
  `ghcr.io/opendustpan/opendustpan@sha256:<digest>`, and where the digest of
  each release is: the summary of the release workflow run, and
  `docker buildx imagetools inspect ghcr.io/opendustpan/opendustpan:<tag>`.
- Add a link to `docs/guides/install.md` in `README.md`.
- The e2e step `TestInstallGuideCommands` in the file
  `e2e/install_guide_docs_test.go` runs each command of the page that needs no
  registry, no cluster and no repository setting: `helm template` of the
  chart in `deploy/helm/opendustpan` with the `--set` values of the page. Each
  other command (`docker pull`, `kubectl`, `helm install` against a cluster,
  `gh secret set`) is a step of the usability check.
- No code change.

## Check

`go test -tags e2e -count=1 -run 'TestInstallPageListsBothPullCommands|TestInstallGuideCommands' ./e2e/`
passes, and `make docs-lint` reports nothing.

## ADR check

**ADR 214: A user-facing feature needs its docs page in the same pull request**
(not owner-tagged; Changed by: ADR 315).
- Quote: "A pull request that adds or changes a feature that a person or a
  client can see adds or changes its page in the user documentation."
- Effect: the image install is a feature that a person sees; its page is in
  the user documentation (`docs/guides/`), and this change finishes it.

The production install from the docs is a persona path of the project (the
product principles of the owner): each command of the page that a test can run
runs in a test.

## Open questions

none

Questions considered and answered:

- Why is the digest not written on the page? Before the first tag no digest
  exists, and each release has its own. The page names where the digest is.
- Why does the test not pull the image? The e2e test server has no registry
  access and no cluster. The test runs what it can (`helm template`); the
  usability check runs the rest.
