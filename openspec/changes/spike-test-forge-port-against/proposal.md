---
version: 1
---

Issue: #888, #889, #928.

## Problem

A developer who runs Forgejo wants to connect it as a forge, and today no finding says which port methods small forges can serve or whether a bridge fits the same port.

## Evidence

- Main has a Forge conformance suite named forgetest. Proof: pkg/forge/forgetest/forgetest.go:1:// Package forgetest is the conformance suite for forge.Forge
- Main documents the forgetest harness call shape. Proof: pkg/forge/forgetest/forgetest.go:7://		forgetest.Run(t, func(t *testing.T) forgetest.Harness { return newHarness(t) })

## Fix boundary

- Throwaway prototype of the Forge port against a local Forgejo and Gitea, kept outside `pkg/forge` and deleted after
- Gap list for webhooks, check runs or commit statuses, review comments, app auth, and in-place comment edit
- Sketch of the bridge protocol with same-port or second-port verdict and reason, with the air-gapped bridge input that links #928
- Research note at NEW `docs/research/forge-small-forges-spike.md` with per-method support table and API calls, container pin, API versions, fake credential kind, and link to the air-gapped question

Out of scope:

- Production Forgejo adapter (#889)
- Gitea or other small forge adapters
- Bridge implementation
- Port ADR changes in #886
- Air-gapped research note with primary sources (#928)

## Check

- Probe local Forgejo and Gitea from throwaway code in NEW `/var/tmp/forge-spike/` kept outside `pkg/forge`, record pins and versions, use fake credentials only, then delete the directory and remove containers before close. Check: No route: the check lists the files of NEW `/var/tmp/forge-spike/`, shows pins and versions and fake credentials only, then shows the directory and containers removed.
- Write the file NEW `docs/research/forge-small-forges-spike.md` with per-method Forgejo and Gitea support and API call, the five gaps of webhooks, check runs or commit statuses, review comments, app auth, and in-place comment edit, the bridge sketch with same-port or second-port verdict, pins, versions, fake credential kind, and the air-gapped bridge input that links #928. Check: No route: the check reads the NEW `docs/research/forge-small-forges-spike.md` note and shows every method of `pkg/forge/forge.go` marked supported, partly supported or unsupported with its API call, the five gaps, the bridge sketch with same-port or second-port verdict, and the air-gapped link.
- Write the proposed port changes into the file NEW `docs/research/forge-small-forges-spike.md`, post them on #886 before #886 merges, and record the post link in the file NEW `docs/research/forge-small-forges-spike.md`. Check: No route: the check reads the NEW `docs/research/forge-small-forges-spike.md` note and shows the recorded post link to #886 before it merges.

Depends on:

- #886

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main checks the audit outcome of the Forge service in its service test. (`pkg/forge/service_test.go:503 (TestCheckAuditsItsOutcome)`)
- Main tests the Forge comment marker. (`pkg/forge/comment_test.go:12 (TestCommentMarker)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "A pull request that changes only internal code needs no page."
- Effect: The spike writes a research note only and changes no route, setting, command, flag, console page or user error, so no user docs page is due.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does this change build the Forgejo adapter? No. The issue scope says the output is a throwaway prototype and a research note. #889 stays out of scope.
- Does the research note need a user docs page? No. ADR 214 decision 1 applies only to a feature that a person or a client can see; a research note is internal output.
- Which second small forge does the spike probe? Gitea. It shares the Forgejo API lineage, so one probe covers both. The NEW note records the limit that a non-Gitea-lineage forge is not probed.
- Does this change do the air-gapped research? No. #928 is a separate note. This spike records only the bridge input it needs and links the air-gapped question.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
