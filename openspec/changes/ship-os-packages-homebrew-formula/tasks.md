## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write the failing tests of the spec scenarios in the NEW file packaging/packaging_test.go: a package that holds the binary and the license files, and a hash mismatch refusal. No route: the tests read files and build packages only. Check: go test ./packaging/... fails on main for the new test names

## 2. Gate

- [ ] 2.1 Write the NEW file docs/rfc/os-packages.md, get the owner decision, then wait for the first tag. Re-check the release workflow files for any package step. No route: this task writes a document only. Check: Owner confirms the revisit after the first tag.

## 3. Definition of done

- [ ] 3.1 Add the package build, the formula, the PKGBUILD, the CI install step in a container and the install docs page. NEW files: packaging/nfpm.yaml, packaging/render.go, .github/workflows/packages.yml, docs/guides/install-packages.md. No route: the steps build and install packages only. Check: CI runs `opendustpan --version` in a container for deb, rpm and apk. The cloud agent cannot push .github/workflows. Write the full diff of the workflow file in the pull request description under the heading 'Workflow patch (apply locally)', and leave the PR as a draft. Confirmed by: A person's step.
- [ ] 3.2 Run `brew install` and an AUR build from the release. No route: the commands install the CLI only. Check: The commands exit 0 and print the version
- [ ] 3.3 Usability check from the NEW page docs/guides/install-packages.md alone, then run the full check. Check: make check with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).
