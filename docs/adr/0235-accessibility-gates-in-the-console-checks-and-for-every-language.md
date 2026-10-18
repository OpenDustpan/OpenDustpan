# 235. Accessibility gates in the console checks and for every language

Status: accepted · 2026-10-18. Extends ADR 183 decisions 1, 2 and 3

## Context

ADR 183 sets the target, "designed against WCAG 2.2 AA", and puts an axe
gate in the console e2e suite. On 18 October 2026 the project owner
kept the target and asked for the axe check as a gate in the console's
checks too, not only in the e2e suite. ADR 230 adds right-to-left
languages, and ADR 233 adds localized emails. This ADR records the decision
of the owner with the label "(owner)", and the design decisions that
follow from it. The project manager decided the design decisions under the
owner's decision rule. Each such decision has a "Benchmark:" line.

Facts:

- The console's checks run its lint, its type check and its unit tests
  without a browser. jsx-a11y in strict mode is part of the lint (ADR 58).
- axe-core runs on a DOM. It can check one rendered component in a test,
  not only a full page in a browser.
- An automated engine finds only part of the failures (ADR 183).

## Decision

1. **The target stays "designed against WCAG 2.2 AA" (owner).** ADR 183
   decision 1 stays. No text of the project says that the console or an
   email conforms to WCAG, or that it is compliant or certified.
2. **An axe gate in the console's checks (owner).** The unit tests of the
   console render each shared component in each of its states, and run
   axe on the result with the rule tags of ADR 183 decision 3. A violation
   fails the console's checks. The exception rule of ADR 183 decision 4
   applies. Rules that need a full page or real layout, for example color
   contrast, stay in the e2e gate.

   Benchmark: the accessibility add-on of Storybook runs axe on each story
   of a component. GitLab runs axe on its pages in its feature tests.
3. **The e2e gate covers every direction and script.** The axe gate of ADR
   183 decision 3 runs in `en`, `en-XA` and `ar-XB` on each change. It runs
   in each shipped language on the main branch every day. A violation that
   only one language has fails the run of that language.
4. **Translated names are part of the gate.** Each accessible name, label,
   description and live-region text comes from a message file. The check of
   ADR 181 decision 22 finds text outside a message file in `en-XA`, so it
   finds an English `aria-label` too.
5. **Emails follow the same target.** The email templates of ADR 233 are
   designed against the WCAG 2.2 AA criteria that apply to a document: the
   language of the page, the order of reading, link text that names its
   target, and contrast. A test renders each email template in `en` and in
   `ar-XB` and runs axe on its HTML part.
6. **Chat text carries meaning in words.** A chat message puts each status
   in a word, never in an emoji or a color alone. The console's voice rule
   bans emoji anyway.

## Consequences

- `OpenDustpan/console` gets these changes. This ADR does not change that
  repository.
  - An axe package for unit tests is a new dev dependency. It needs the
    approval of the owner before it lands. `@axe-core/playwright` of ADR 183
    needs the same approval.
  - The unit tests of decision 2 and the daily runs of decision 3.
- The server gets the email test of decision 5 with the server message
  files of ADR 233. It runs axe-core in a Node step of CI, not in the Go
  binary.
- `scripts/docs-banned-words.py` can get patterns for a claim of WCAG
  conformance, as it has for DORA. That change is code, not part of this
  ADR.
