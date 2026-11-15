## 1. Move and extend the page

- [ ] 1.1 Move `docs/install.md` to the NEW path `docs/guides/install.md` with `git mv`, and change the path in `e2e/install_docs_test.go:23` (TestInstallPageListsBothPullCommands). Check: `go test ./e2e/ -run TestInstallPageListsBothPullCommands -count=1` passes. `make docs-lint` passes on each edited guide.
- [ ] 1.2 Add the section "Pull by digest" to `docs/guides/install.md`: the form `ghcr.io/opendustpan/opendustpan@sha256:<digest>` and where the digest of each release is (the release notes, and `docker buildx imagetools inspect`). Add the line to TestInstallPageListsBothPullCommands in `e2e/install_docs_test.go`. Check: the test passes and `make docs-lint` reports nothing.
- [ ] 1.3 Add a link to `docs/guides/install.md` in the install section of `README.md`. Check: `grep -n 'docs/guides/install.md' README.md` finds the line. `make docs-lint` passes on each edited guide.

## 2. The docs test

- [ ] 2.1 Write the e2e step `TestInstallGuideCommands` in the NEW file `e2e/install_guide_docs_test.go`: it reads `docs/guides/install.md`, takes the `helm install` command of the page, and runs `helm template` of `deploy/helm/opendustpan` with the same `--set` values. No route: the test renders the chart only. Check: `go test ./e2e/ -run TestInstallGuideCommands -count=1` passes. Confirmed by: CI (the agent writes the e2e test). `make docs-lint` passes on each edited guide.

## 3. Definition of done

- [ ] 3.1 Usability check: an operator follows `docs/guides/install.md` alone, pulls the image by tag and by digest, makes the two Secrets and installs the chart on a cluster. Check: the steps in the pull request. A person's step.
- [ ] 3.2 Run the local checks (`Makefile`). Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
