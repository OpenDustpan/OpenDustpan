## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Definition of done

- [ ] 1.1 Confirm each verdict reason against main and record the result on the issue. Check: rg over docs/adr/INDEX.md and docs/adr for hashing, token storage, and OAuth state shows no conflicting decision; go.mod toolchain noted; each flagged site re-read on main matches the issue table at 96ed862, else dismissal stops.
- [ ] 1.2 Dismiss the 8 CodeQL alerts from the issue table with their false-positive reasons. Check: Each of alerts 2, 3, 8-13 shows dismissed as false positive with the reason from the issue table.
