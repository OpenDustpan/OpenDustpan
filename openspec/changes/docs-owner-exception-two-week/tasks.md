## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing check first

- [ ] 1.1 Write the check: the skill line in CLAUDE.md holds both 'two weeks' and 'unverified'. Check: rg -n 'two weeks.*unverified|unverified.*two weeks' CLAUDE.md finds nothing on main.

## 2. Docs

- [ ] 2.1 Edit the skill line in CLAUDE.md to keep the two-week rule and add the unverified-claim sentence. Check: The rg command above finds one line in CLAUDE.md.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Check that no other doc states a skills exception: run rg -n 'two weeks' CLAUDE.md docs and read each hit. Check: Each hit agrees with the written rule.
- [ ] 4.2 A person reads the skill line alone and states the rule. Check: The person states: two weeks, claim unverified. A person's step.
- [ ] 4.3 Run the full gate. Check: make check reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
