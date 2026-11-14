---
version: 1
---

Issue: #2692, #2696, #2697, #2698.

## Problem

Missed non-blocking review notes leave limits and permissions undocumented in the guides, and leave one test case and one script check missing. Each row is checked against main first and skipped when main already has it.

## Evidence

- The notification guide exists on main. Proof: docs/guides/notification-destinations.md
- The local-users guide exists on main. Proof: docs/guides/local-users.md
- The passkeys guide exists on main. Proof: docs/guides/passkeys.md
- The connect guide exists on main. Proof: docs/guides/connect-github.md
- The bench script exists on main. Proof: scripts/bench-s3.sh
- The CEF export test exists on main. Proof: internal/api/audit/export_cef_test.go

## Fix boundary

- In docs/guides/notification-destinations.md add the Mattermost 15000-character limit and MaxPostSize note
- In docs/guides/local-users.md add the name_locks gauge and alert note, and say that the limit bounds one run
- In docs/guides/passkeys.md say that a restore brings back an older sign counter
- In docs/guides/connect-github.md add the Commit statuses permission row
- In scripts/bench-s3.sh fail on an empty Garage or PostgreSQL version
- In internal/api/audit/export_cef_test.go add the curlxffx case with a note on the lossy replace

Out of scope:

- The Makefile actionlint step and the AGENTS.md rule (no pack path)
- The text of ADR 114 and ADR 311 (a rule-ID note or an account switcher note is not added here)
- Behavior changes in services, handlers and forge packages (issues 2696, 2697 and 2698 are separate items)
- The GLOSSARY.md switcher entries

## Check

- Add the curlxffx case to internal/api/audit/export_cef_test.go Check: `go test ./internal/api/audit -run Export` fails on main if the case is missing
- Run the rg row checks for the four guides on main Check: `rg -n "MaxPostSize|name_locks|sign counter|Commit statuses" docs/guides` lists the missing rows
- Add the missing notes to the four guides Check: `rg -n "MaxPostSize|name_locks|sign counter|Commit statuses" docs/guides` shows each line
- Fail on an empty version in scripts/bench-s3.sh Check: `sh -n scripts/bench-s3.sh` passes and an empty variable exits non-zero
- Record each row as added or skipped, and run the full check Check: `make check` passes with zero findings

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The audit user-agent test keeps runes when it truncates. (`pkg/audit/user_agent_test.go:14 (func TestTruncateUserAgentKeepsRunes)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 114: The policy port and its four default adapters** (not owner-tagged).

- Decisions that apply: 4 (current).
- Changed by: ADR 192 extends decision 2; ADR 222 changes decision 8; ADR 222 supersedes decisions 1, 2, 3, 5, 6, 7, 9, 10, 11 and 12; ADR 224 changes decision 8; ADR 224 supersedes decisions 1, 2, 3, 5, 6, 7, 9, 10, 11 and 12; ADR 228 supersedes decision 5; ADR 229 supersedes decision 11; ADR 286 changes. Chain to the end: ADR 192, ADR 222, ADR 224, ADR 228, ADR 229, ADR 286, ADR 225, ADR 227, ADR 299, ADR 335, ADR 351, ADR 354, ADR 241, ADR 313, ADR 317, ADR 342, ADR 353, ADR 301, ADR 320, ADR 339, ADR 328, ADR 359, ADR 368, ADR 248, ADR 343, ADR 319, ADR 365, ADR 332.
- Current rule: this ADR, with ADR 192, 222, 224, 228, 229, 286.
- Quote: "A change needs each requirement."
- Effect: The change leaves ADR 114 unchanged and adds no rule.
- Conflict: none.

## Open questions

Not spec-ready: a code check fails: task "Record each row as added or skipped, and run the full check" is not confirmed by the agent alone: end it with one fixed phrase: "Confirmed by: CI (...)", "Confirmed by: the driver's gates." or "; a code check fails: `internal/api/audit` is named by the design or a task but is not in the touches of the stack plan; a code check fails: `docs/guides` is named by the design or a task but is not in the touches of the stack plan; a code check fails: and 1 more.

Questions considered and answered:

- Does the change contradict an accepted ADR? No. It adds prose and one check and leaves ADR 114 decision 4 and ADR 311 unchanged.
- Does the change alter a tool-compatible API? No. It edits guides, one script and one test.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
