## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write the workflow conformance test for the two-registry push, the digest comparison, the partial-push completion, the token errors, the tag allowlist and the untrusted run. Check: Test TestReleasePushesOneDigestToBothRegistries fails on main.
- [ ] 1.2 Write the chart test for the default registry and the override value. Check: Test TestChartDefaultRegistryAndOverride fails on main.
- [ ] 1.3 Write the docs test for the two pull commands. Check: Test TestInstallPageListsBothPullCommands fails on main.

## 2. Build

- [ ] 2.1 Add the two-registry push, the comparison and the re-run rules to the tag release workflow. Check: TestReleasePushesOneDigestToBothRegistries passes.
- [ ] 2.2 Set the chart default, add the override value and push the chart as an OCI artifact to the forge registry. Check: TestChartDefaultRegistryAndOverride passes and `helm template` shows the default.
- [ ] 2.3 Add the install page section with both pull commands. Check: TestInstallPageListsBothPullCommands passes.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Tag a release candidate in CI, pull both references by digest, run `helm install` from the default registry, and check that an Operator completes the install page alone and each error names one command. Check: The CI run passes, the digests match, `make docs-lint` is clean and `make check` reports zero findings.
