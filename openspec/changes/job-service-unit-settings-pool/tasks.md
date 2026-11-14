## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write failing planner tests for settings resolution, override authorization, manual lock refusal and missing location. Check: TestStartResolvesFromSettings fails on main
- [ ] 1.2 Write failing read-check tests for run-as principal loss and manual-fire intersection. Check: TestTriggerOutputReadDeniesAfterGrantLoss fails on main

## 2. Build

- [ ] 2.1 Add location, pool, profile ID and steps to unit settings with validation. Check: go test ./pkg/unitsettings/...
- [ ] 2.2 Wire the read check into the output read handlers. Check: go test ./pkg/dustpan
- [ ] 2.3 Classify the actions in the built-in roles after the owner answers. Check: go test ./pkg/access -run TestActionsWithoutRoutesAreUnclassified

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Add the e2e step: PUT /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/settings, then a trigger start, with an access token of a namespace administrator against the real binary. Check: make e2e step passes
- [ ] 4.2 Write the docs page and the usability check from the page alone. Check: make docs-lint
- [ ] 4.3 Run the full check. Check: make check with zero findings
