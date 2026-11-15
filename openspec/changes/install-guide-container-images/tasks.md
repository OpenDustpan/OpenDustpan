## 1. Move and extend the page

- [x] 1.1 Move `docs/install.md` to the NEW path `docs/guides/install.md` with `git mv`, and change the path in `e2e/install_docs_test.go:23` (TestInstallPageListsBothPullCommands). Check: `go test -tags e2e -count=1 -run TestInstallPageListsBothPullCommands ./e2e/` passes. `make docs-lint` passes on each edited guide.
- [x] 1.2 No route: the test checks the page text and the commands it names only. Add the section "Pull by digest" to `docs/guides/install.md`: the form `ghcr.io/opendustpan/opendustpan@sha256:<digest>` and where the digest of each release is: the summary of the release workflow run (`.github/workflows/release.yml:211`), and `docker buildx imagetools inspect ghcr.io/opendustpan/opendustpan:<tag>`. The page does not name the release notes, which hold no digest. Add to TestInstallPageListsBothPullCommands in `e2e/install_docs_test.go` the checks that the page holds `docker buildx imagetools inspect` and the words "workflow run", and does not hold "release notes". Check: `go test -tags e2e -count=1 -run TestInstallPageListsBothPullCommands ./e2e/` passes, and `make docs-lint` passes on each edited guide.
- [x] 1.3 Add a link to `docs/guides/install.md` in the install section of `README.md`. Check: `grep -n 'docs/guides/install.md' README.md` finds the line. `make docs-lint` passes on each edited guide.

## 2. The docs test

- [x] 2.1 Write the e2e step `TestInstallGuideCommands` in the NEW file `e2e/install_guide_docs_test.go`: it reads `docs/guides/install.md`, takes the `helm install` command of the page, and runs `helm template` of `deploy/helm/opendustpan` with the same `--set` values. No route: the test renders the chart only. Check: `go test -tags e2e -count=1 -run TestInstallGuideCommands ./e2e/` passes. Confirmed by: CI (the agent writes the e2e test). `make docs-lint` passes on each edited guide.

## 3. Definition of done

- [ ] 3.1 Usability check: an operator follows `docs/guides/install.md` alone, pulls the image by tag and by digest, makes the two Secrets and installs the chart on a cluster. Check: the steps in the pull request. A person's step.
- [ ] 3.2 Run the local checks (`Makefile`). Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
