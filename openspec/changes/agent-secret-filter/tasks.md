## 0. Confirm against the code first

- [ ] 0.1 Confirm there is no `SecretFilter`: `rg -n 'SecretFilter|RemovedMarker' internal pkg cmd`.
- [ ] 0.2 Read `internal/agent/mask.go` and note what `MaskText` does. Decide in the pull request what stays of it.
- [ ] 0.3 Confirm the change `agent-connection-document-and-secrets` is on main (`ConnectionSpec`). BLOCKED by that change.
- [ ] 0.4 Read ADR 129 decisions 11 and 24 and note any change since the spec.

## 1. Failing tests first

- [ ] 1.1 A corpus of secrets (prefixes, PEM, JWT, `Authorization`, `password=`) that the filter removes, and a corpus of IDs and hashes from state checks that it keeps.
- [ ] 1.2 The table tests of the layers table: canary in text, in tool call arguments, in readable reasoning, in an opaque part, in a prompt, in a tool result.
- [ ] 1.3 The tests of the limits: a text above 4 MiB, a hostile text for the pattern scan, the no-off-switch test.
- [ ] 1.4 `TestCheckNoSecretValue_Table`.
- [ ] 1.5 For each guard of the layers table, a test that fails when the guard is removed. Check: remove the guard, see the test fail, put the guard back.

## 2. Build

- [ ] 2.1 `internal/agent/filter.go`: the type, the pattern list, `FilterModel`, `CheckNoSecretValue`.
- [ ] 2.2 Wire `FilterModel` where the loop gets its model. Keep `MaskText` for the revealed values until the pull request shows it is not needed.
- [ ] 2.3 The count of removals in the run record (the schema file of each dialect).

## 3. Side effects

- [ ] 3.1 `make generate` if the schema changes. Never edit a generated file by hand.
- [ ] 3.2 The docs page `docs/guides/agent-secret-filter.md`: the forms that the filter removes and the marker. A test runs each command of the page.

## 4. Definition of done

- [ ] 4.1 e2e step: a fake model answers a canary secret; the transcript holds the marker and the count.
- [ ] 4.2 Usability check: an administrator sees why a text has the marker, from the docs page only.
- [ ] 4.3 `make check` passes with zero findings. Paste the output into the pull request.
