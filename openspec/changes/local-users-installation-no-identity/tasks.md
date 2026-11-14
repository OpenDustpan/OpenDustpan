## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Docs fix

- [ ] 1.1 Correct the first-sign-in section of console-sign-in.md. Check: `rg -n -F 'Give the first roles with the bootstrap admin token' docs/guides/console-sign-in.md` returns no match, and `rg -n 'mfa.fresh-step-required' docs/guides/console-sign-in.md` returns a match.
- [ ] 1.2 Correct local-users.md and host-recovery.md. Check: `rg -n 'mfa.fresh-step-required' docs/guides/local-users.md docs/guides/host-recovery.md` returns a match in each file.
- [ ] 1.3 Find other instructions that name a token for a grant, gate or group-member write. Check: `rg -n -i 'bootstrap admin token' docs/guides` lists no sentence that tells the operator to make a grant, gate or group change with a token.

## 2. Definition of done

- [ ] 2.1 Run docs lint and the usability check: the operator reads the guides and meets no instruction that answers 403. Check: `make docs-lint` and `make check` pass with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).
