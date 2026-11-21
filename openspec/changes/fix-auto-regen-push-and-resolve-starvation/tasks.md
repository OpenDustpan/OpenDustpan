## 1. Fix

- [ ] 1.4 In `scripts/auto-regen.sh` (`cmd_dispatch` at `:433`), skip a pull request whose head already has a resolve run in the state `queued` or `in_progress`. Find it with `gh api repos/$repo/actions/workflows/auto-regen-resolve.yml/runs` and the run name `auto-regen-resolve #<number> <head>` of `.github/workflows/auto-regen-resolve.yml:30`. Log one line: `#<number>: skip: a resolve run for head <head> is already running`. Keep every existing check of `cmd_dispatch` (the number, the full SHA of the head, the refusal of `main` and `gh-readonly-queue/*`). Check: `bash -n scripts/auto-regen.sh` passes, and `shellcheck` reports nothing when it is installed.

## 2. Hostile cases

The workflows have no test harness on main (`rg -l auto-regen --glob '!*.yml' .` finds only `scripts/auto-regen.sh`). Each case below runs once on a test pull request after the merge, and the pull request body of the fix records the run IDs.

- [ ] 2.1 (`scripts/auto-regen.sh`, `cmd_dispatch`) A dispatch for a pull request head that already has a running resolve run logs the skip line of task 1.4 and starts no run. Check: the run list shows one resolve run for that head.
- [ ] 2.4 (`scripts/auto-regen.sh`, `check-patch`) A patch that changes a file outside the generated paths is still refused by `check-patch`. Check: the push run logs the refusal and pushes nothing.

## 3. Definition of done

- [ ] 3.2 Run the full check (`Makefile`). Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
