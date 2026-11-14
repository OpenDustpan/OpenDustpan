# 296. Change links between ADRs, and the ADR index

Status: accepted · 2026-10-31. Decided by the project owner

Changed by:

- ADR 365 changes decision 1: a wording refinement edits the ADR in place,
  with a changelog. A changed decision still gets a new ADR.

Changes:

- Extends ADR 1: an accepted ADR gets a "Changed by" list.

## Context

ADR 1 forbids edits to an accepted ADR, except a mark that the ADR is
superseded. A changed decision gets a new ADR. The new ADR often says what
it changes, but the earlier ADR does not point to it.

A count on 31 October 2026 found 236 ADRs. Later ADRs change 134 of them.
About 90 of these 134 ADRs have no pointer to the later ADR. Agents and the
project manager read an earlier ADR, did not find the later ADR, and gave
answers that were not current.

The same check asked 60 questions about decisions. The Repowise index gave
the correct answer to 14 questions. A search with `rg` over `docs/adr` gave
the correct answer to 58 questions.

The project owner decided these points:

- No addenda to an accepted ADR. A change is a new ADR.
- Each change has a link in one form that a script can read.
- A generated index lists all ADRs and their links. CI checks the index.
- For a question about a decision, agents use `rg` first.

## Decision

1. A changed decision gets a new ADR. An accepted ADR gets no addendum.
   (owner)
2. Each ADR from this ADR on has a "Changes:" list below its status line.
   Each item names one earlier ADR that this ADR changes. An ADR that
   changes no ADR has the item "- None.". (owner)
3. An item of a "Changes:" list has this form:
   "- Supersedes ADR 4 decisions 1 and 2: a short note." The verb is one of
   "Supersedes", "Changes" and "Extends". The decisions and the note are
   optional. (owner)
4. The verbs have these meanings:
   - "supersedes": the named decisions do not apply now. When the item names
     no decision, all of the earlier ADR does not apply now.
   - "changes": the named decisions apply only in part. The reader must read
     the later ADR too. This verb also covers "supersedes in part", "amends",
     "refines" and "narrows".
   - "extends": the named decisions apply. The later ADR adds to them.
   (owner)
5. A decision number is the number of an item in the "Decision" section of
   the earlier ADR. A part that has no number, for example "the route
   paths", goes in the note. (owner)
6. The earlier ADR gets the reverse item in a "Changed by:" list below its
   status line, for example "- ADR 296 extends: a short note.". This list is
   the mark that ADR 1 allows. The change adds nothing else to the earlier
   ADR. The prose of its status line stays. (owner)
7. When a later ADR supersedes all of an earlier ADR, the status line of the
   earlier ADR starts with "superseded by ADR N". (owner)
8. The ADRs before this ADR have no "Changes:" list. Only their "Changed
   by:" items record their links. A backfill adds these items from the
   status lines, the "What this ADR replaces" sections, the explicit
   references and a read of each later ADR. (owner)
9. `scripts/adr-index.py` writes `docs/adr/INDEX.md`. The index has one line
   for each ADR: the number, the title, the status, the ADRs that it
   changes, the ADRs that change it and the ADRs that give the current rule.
   It shows each link in both directions. `make adr-index` writes the file.
   Nobody edits it by hand. (owner)
10. `make docs-lint` runs the self-test of the script and its check. The
    check fails in these conditions:
    - An item does not have the form of decision 3.
    - An item names an ADR that does not exist.
    - A link has no reverse item.
    - A link goes to the same ADR or to a later ADR.
    - An ADR from this ADR on has no "Changes:" list.
    - The status line and the whole-ADR "supersedes" item do not agree.
    - `docs/adr/INDEX.md` differs from a new output.
    (owner)
11. For a question about a decision, an agent reads `docs/adr/INDEX.md` and
    runs `rg` over `docs/adr` and `docs/rfc` first. The agent then reads
    each ADR that the "Current rule" column names. Repowise stays the tool
    for code structure, symbols and history. `CLAUDE.md` says so. (owner)

## Consequences

- A reader of an earlier ADR sees each later change below its status line.
  The reader does not have to search the later ADRs.
- The author of a new ADR writes the "Changes:" list and the reverse items
  in the same pull request. `make docs-lint` fails until both copies agree.
- The Makefile gives the number of this ADR to the script as the first ADR
  with a "Changes:" list. The script holds no ADR number.
- A link can only name what the author knows. A silent change that nobody
  finds stays without a link. A reader who finds one adds the link in a new
  pull request.
- A tool that reads each `.md` file in `docs/adr` as an ADR must skip
  `INDEX.md`.
