## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write the workflow conformance test for the two-registry push, the digest comparison, the partial-push completion, the token errors, the tag allowlist and the untrusted run in the NEW file e2e/release_workflow_test.go (package e2e, next to e2e/image_test.go). The test reads the NEW file .github/workflows/release.yml as text. It names no existing route and no existing credential kind: BLOCKED by the missing route and credential kind, because the test reads text files only. It names the Docker Hub token secret DOCKERHUB_TOKEN as a text match only. Check: Test TestReleasePushesOneDigestToBothRegistries (NEW) fails on main.
- [ ] 1.2 Write the chart test for the default registry and the override value in the NEW file e2e/chart_registry_test.go (package e2e). The test reads the NEW file deploy/helm/opendustpan/values.yaml and runs `helm template`. It names no existing route and no existing credential kind: BLOCKED by the missing route and credential kind, because the test reads files and chart output only. Check: Test TestChartDefaultRegistryAndOverride (NEW) fails on main.
- [ ] 1.3 Write the docs test for the two pull commands in the NEW file e2e/install_docs_test.go (package e2e). The test reads the NEW file docs/install.md. It names no existing route and no existing credential kind: BLOCKED by the missing route and credential kind, because the test reads a text file only. Check: Test TestInstallPageListsBothPullCommands (NEW) fails on main.

## 2. Build

- [ ] 2.1 Add the two-registry push, the comparison and the re-run rules to the tag release workflow in the NEW file .github/workflows/release.yml. Check: TestReleasePushesOneDigestToBothRegistries passes.
- [ ] 2.2 Set the chart default, add the override value and push the chart as an OCI artifact to the forge registry in the file deploy/helm/opendustpan/values.yaml (NEW if main has no such file) and in the release workflow .github/workflows/release.yml. Check: TestChartDefaultRegistryAndOverride passes and `helm template` shows the default.
- [ ] 2.3 Add the install page section with both pull commands in the file docs/install.md (NEW if main has no such file). Check: TestInstallPageListsBothPullCommands passes.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Tag a release candidate in CI, pull both references by digest, run `helm install` from the default registry, and check that an Operator completes the install page alone and each error names one command. Check: The CI run passes, the digests match, `make docs-lint` is clean and `make check` reports zero findings.
