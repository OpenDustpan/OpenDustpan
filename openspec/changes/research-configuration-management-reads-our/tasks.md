## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Research

- [ ] 1.1 Write the research note at NEW docs/research/config-mgmt-outputs.md covering the six issue points and the relation to the output-sharing actions on main. It is a docs file: it calls no route and uses no credential. Check: scenario research-note-lists-six-points: the file NEW docs/research/config-mgmt-outputs.md exists and lists the six issue points

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Run docs checks for the note Check: make check with zero findings
