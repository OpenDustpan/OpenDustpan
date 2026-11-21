## 0. Confirm against the code first

- [x] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [x] 1.1 Write the workflow conformance test for the two-registry push, the digest comparison, the partial-push completion, the token errors, the tag allowlist and the untrusted run in the NEW file e2e/release_workflow_test.go (package e2e, next to e2e/image_test.go). The test reads the NEW file .github/workflows/release.yml as text. No route: the test reads text files only. It names the Docker Hub token secret DOCKERHUB_TOKEN as a text match only. Check: Test TestReleasePushesOneDigestToBothRegistries (NEW) fails on main. The cloud agent cannot push .github/workflows. Write the full diff of the workflow file in the pull request description under the heading 'Workflow patch (apply locally)', and leave the PR as a draft. Confirmed by: A person's step.
- [x] 1.2 Write the chart test for the default registry and the override value in the NEW file e2e/chart_registry_test.go (package e2e). The test reads the existing file deploy/helm/opendustpan/values.yaml (image.repository :18) and runs `helm template`. No route: the test reads files and chart output only. Check: Test TestChartDefaultRegistryAndOverride (NEW) fails on main; when it passes on main from the start (main's chart default is already the forge registry, deploy/helm/opendustpan/values.yaml image.repository :18), the pull request body says so and the task counts as done.
- [x] 1.3 Write the docs test for the two pull commands in the NEW file e2e/install_docs_test.go (package e2e). The test reads the NEW file docs/guides/install.md and the link to it in the existing readme at the repository root. No route: the test reads a text file only. Check: Test TestInstallPageListsBothPullCommands (NEW) fails on main. `make docs-lint` passes on each edited guide.

## 2. Build

- [x] 2.1 Add the two-registry push, the comparison and the re-run rules to the tag release workflow in the NEW file .github/workflows/release.yml. Check: TestReleasePushesOneDigestToBothRegistries passes. The cloud agent cannot push .github/workflows. Write the full diff of the workflow file in the pull request description under the heading 'Workflow patch (apply locally)', and leave the PR as a draft. Confirmed by: A person's step.
- [x] 2.2 Set the chart default, add the override value and push the chart as an OCI artifact to the forge registry in the existing file deploy/helm/opendustpan/values.yaml (image.repository :18) and the chart tests in deploy/helm/charttest/chart_test.go and in the release workflow .github/workflows/release.yml. Check: TestChartDefaultRegistryAndOverride passes and `make chart-lint` (helm lint, helm template, kubeconform) passes in CI. The cloud agent cannot push .github/workflows. Write the full diff of the workflow file in the pull request description under the heading 'Workflow patch (apply locally)', and leave the PR as a draft. Confirmed by: A person's step.
- [x] 2.3 Write the NEW install page docs/guides/install.md with both pull commands, each pinned by tag, and the image digest of the release, and add a link to it in the existing readme at the repository root. Check: TestInstallPageListsBothPullCommands passes. `make docs-lint` passes on each edited guide.

## 3. Side effects (computed from main)

- [x] 3.1 Not applicable: this change adds no ADR, so `docs/adr/INDEX.md` does not change.

## 4. Definition of done
