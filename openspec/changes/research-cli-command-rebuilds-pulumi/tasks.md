## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. research

- [ ] 1.1 Write the NEW `docs/research/rebuild-state-from-backup.md` note with the mapping, the secret path, the placement, and the limits. No route: the task reads backup docs and writes a document only. Check: ls `NEW docs/research/rebuild-state-from-backup.md`

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. definition of done

- [ ] 3.1 Confirm the note passes docs checks and states the provisional placement and the RFC need. Check: make docs-lint
