---
version: 1
---

Short form: a CI check that an AI-authored pull request links a spec-ready
OpenSpec change. It can build on the PR-body check of the parked PR #2729.

## Problem

**Persona: maintainer.** The maintainer reviews pull requests that agents
open. `CLAUDE.md` says: "Agent work starts only from an OpenSpec change in
`openspec/changes`. The proposal of the change says "Open questions: none".
The pull request of an agent links its change and its issue." Nothing checks
this. An agent pull request with no change, or with a change that still has
open questions, reaches review, and the maintainer finds out by hand.

## Evidence

- No check exists: `rg -n -i "openspec" .github/workflows Makefile scripts`
  finds no check of a pull request body, and `ls scripts` lists no such script.
- ADR 375 (owner, 2026-11-08) records the decision: "For AI issues only".
  CI fails an AI-authored pull request that does not link a spec-ready
  OpenSpec change, and a pull request of a person is exempt.
- How a pull request discloses AI:
  - ADR 76 decision 2: "The pull request names each AI tool and says which
    parts it wrote."
  - `CONTRIBUTING.md:108-109`: "Say that you used AI. In the pull request or
    the issue, name the tool and say which parts it wrote."
  - ADR 317 decision 2: an agent commit has the identity
    `Claude <noreply@anthropic.com>` as author and sign-off.
- The spec-ready gate: `openspec/project.md:83-86`: "A change may be
  implemented only when that section reads "none" (the spec-ready gate)."
- PR #2729 holds `scripts/check-pr-body.sh` and a Go table test of it in
  `scripts/checkprbody/`, with the body read from a file and passed through an
  environment variable in CI.

## Fix boundary

- A pull request is AI-authored when either of these holds:
  1. a commit of the pull request has the author email `noreply@anthropic.com`
     (ADR 317 decision 2);
  2. the body has a disclosure line that starts with `Tool:` or `AI tool:`
     (ADR 76 decision 2).
  Any other pull request is a person's pull request and is exempt.
- A link is a line of the body, outside a fenced code block, that names
  `openspec/changes/<id>` with `<id>` of lowercase letters, digits and
  hyphens. The link counts when `openspec/changes/<id>/proposal.md` exists on
  the head of the pull request, or `openspec/changes/archive/*-<id>/proposal.md`
  exists (an archived change), and its section "## Open questions" reads
  "none" (or the proposal has the line "Open questions: none").
- At least one link must count. Else the check fails with one line that names
  the fix: `the pull request names no spec-ready change: add a line
  "Change: openspec/changes/<id>" for a change whose proposal says "Open
  questions: none"`. When a named change is not spec-ready, the line names
  that change and its open questions.
- The NEW script `scripts/check-ai-pr-spec.sh` takes the body file and the
  commit file (the author email of each commit, from `git log --format`), and reads the change folders from the checkout. The NEW Makefile
  target `ai-pr-spec-check` runs it with `PR_BODY_FILE` and `PR_COMMITS_FILE`.
  The NEW CI step in `.github/workflows/ci.yml` runs on `pull_request` only. It
  passes the body through an environment variable, never through the shell
  text.
- It is not part of `make check`, because it needs a pull request.

## Check

The table test of the script passes, and the CI step fails an agent pull
request with no link and passes a person's pull request.

## ADR check

**ADR 76: AI-assisted contributions** (decision 2; changed by ADR 317 for
decisions 7 and 8).
- Quote: "**Disclosure.** The pull request names each AI tool and says which
  parts it wrote."
- Effect: the disclosure is one of the two signals of an AI-authored pull
  request.

**ADR 317: Gate rows key on the unit ID, and the identity of an agent commit**
(owner-tagged, decision 2; changed by ADR 319 for the trailer, and ADR 375).
- Quote: "agent commit identity becomes `Claude <noreply@anthropic.com>`
  (author and DCO sign-off) in all new briefs".
- Effect: the author email is a signal.

**ADR 375: AI-authored pull requests link a spec-ready OpenSpec change**
(owner-tagged).
- Quote: "For AI issues only".
- Effect: this change implements the decision.

**ADR 364: Unexported fields and golangci-lint before added checks (owner)**
(decision 1).
- Quote: "This decision adds no custom analyzer and no new tool."
- Effect: ADR 364 is about guards of the Go code. This check guards the pull
  request process, and the owner decided it explicitly (ADR 375). It is a script and a CI step, not an analyzer of the code. No code
  structure can express a rule about a pull request body.

`openspec/project.md` (the spec-ready gate and the short form) applies as
quoted above.

## Open questions

none

Questions considered and answered:

- Is a person's pull request checked? No. The owner said "For AI issues only".
- Does an archived change count? Yes. A change that merged and moved to
  `openspec/changes/archive/` was spec-ready when its work started.
- Where does the body come from? From the event of the pull request, through
  an environment variable and a file, never through the shell text.
