# 365. Wording refinements edit an ADR in place, with a changelog (owner)

Status: accepted · 2026-11-13. Records a decision of the project owner of 13 November 2026, in the owner's words

Changes:

- Changes ADR 1: an accepted ADR can be edited for a wording refinement, with a changelog.
- Changes ADR 296 decision 1: a wording refinement edits the ADR in place. A changed decision still gets a new ADR.
- Changes ADR 190 decision 10: a wording refinement, an apply job only for a unit whose plan has a change.
- Changes ADR 212 decision 5: a wording refinement, the restore entries follow the audit switch.
- Changes ADR 342 decision 3: a wording refinement with the owner's word, a client-encrypted OpenTofu state is not a read failure.

## Context

ADR 1 forbids edits to an accepted ADR. ADR 16 allows spelling corrections.
ADR 296 decision 1 says that a changed decision gets a new ADR and that an
accepted ADR gets no addendum.

Some decisions are sound, but their words, read alone, say something that
their authors did not mean. One example from the past is "no telemetry",
which a reader took as "no observability". The decision meant "no
phone-home" (ADR 74). A reader who follows the words then builds the wrong
thing. A new ADR for each such case adds one more document that each reader
must find and read together with the first one.

On 13 November 2026 a check of every decision against every other decision
found no real conflict. It found four pairs of decisions that agree in
intent, but where one decision, read alone, says something else.

## Decision

1. **A wording refinement edits the ADR in place (owner, 2026-11-13).** The
   owner said:

   ```text
   "I will allow ADR edits for wording refinement, with a changelog in the
   markdown document."
   ```

   A wording refinement changes the words of a decision so that they say
   what the decision already meant: for example, it names a scope that the
   rest of the ADR or another ADR already gives. It does not change what the
   decision decides. A change to what a decision decides still gets a new
   ADR (ADR 296 decision 1).
2. **Each edit has a changelog entry.** The edited ADR gets a section
   `## Changelog` at its end, with one item for each edit: the date, the
   words "wording refinement", the ADR that records the edit, the old words
   and the new words, and the decision or text that shows the intent. The
   ADR that records the edit has a "Changes" item, and the edited ADR has
   the reverse "Changed by" item (ADR 296).
3. **An owner decision keeps its owner.** The words of an owner decision
   change only with the owner's word, also for a wording refinement.
4. **The first three refinements.**
   - ADR 190 decision 10 said that the apply of a change order "starts one
     apply job for each unit". It now says "for each unit whose plan has a
     change". ADR 213 decision 3 already said that "a unit plan with no
     change in a change order needs no signature and starts no apply job".
   - ADR 212 decision 5 said that a restore writes three audit entries, with
     no condition. It now starts "When the audit log is on". ADR 60 decision
     2 already said that when the audit switch is off, "the server writes no
     entry".
   - ADR 342 decision 3 (owner) said that the server refuses a write when it
     cannot read the body. Read alone, that refuses every state that the
     client encrypted, which ADR 150 decision 9 and ADR 255 keep unread on
     purpose. With the owner's word ("A lgtm", 13 November 2026), decision 3
     now says that such a state is not a read failure: `state.available` is
     `false`, the decision record says why, and the write goes on under the
     gates that apply. The owner also asked that the user documentation name
     the drawbacks of client encryption: the security model page lists them.

## Consequences

- The fourth pair that the check found is not in this ADR. The order of the
  checks of a self-service submit (ADR 111 decision 6 and ADR 113 decision 8)
  is a choice, not a wording fix.
- A reader still finds every edit: the "Changed by" list at the top of the
  edited ADR, its changelog at the end, and `docs/adr/INDEX.md`.
- No code changes by this ADR alone.
