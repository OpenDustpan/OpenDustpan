# 183. Console accessibility and mobile platform behavior

Status: accepted · 2026-10-18 · adds to ADR 58 decision 9 · decisions 1 to 3 extended by ADRs 231 and 236

Changed by:

- ADR 231 extends decision 3: the axe gate also runs right to left.
- ADR 236 extends decisions 1, 2 and 3: an axe gate in the console checks and
  for every language.
- ADR 311 extends: the on-call view at phone width.

## Context

ADR 58 keeps these console rules: jsx-a11y strict, visible labels and
focus, 4.5:1 text contrast, and color never carries meaning alone. ADR 180
adds contrast rules for every color scheme and a way to turn single-key
shortcuts off. The console has no target standard for accessibility, no
automated check of the rendered pages, and no checks with assistive
technology. On 18 October 2026 the project owner decided the target and the
checks. This ADR records the decisions of the owner with the label
"(owner)", and the design decisions that follow from them.

Facts:

- WCAG 2.2 is the current version of the Web Content Accessibility
  Guidelines of the W3C. Level AA includes all criteria of levels A and AA.
- axe-core is an engine that checks a rendered page against rules that map
  to WCAG criteria. It has a package for Playwright. An automated engine
  finds only part of the failures, so it cannot show conformance.
- The console e2e suite drives the console in a browser with Playwright.

## Decision

### The target

1. **Designed against WCAG 2.2 AA (owner).** The project designs the
   console against WCAG 2.2 level AA. Docs, release notes and the console say
   "designed against WCAG 2.2 AA". They never say that the console conforms
   to WCAG, or that it is compliant or certified. A claim of conformance
   needs a full audit, and this ADR does not decide one.
2. **The same rules in every theme, scheme and language.** The target
   applies to both themes, every color scheme and every language of ADR 181.
   The `lang` attribute of the root element is the language of the page,
   and an element with English fallback text has `lang="en"` (ADR 181).

### Automated checks

3. **An axe gate in the console e2e suite (owner).** The e2e suite runs axe
   on each screen in each state that it reaches: empty, with data, with an
   error, with an open dialog. It uses the rule tags `wcag2a`, `wcag2aa`,
   `wcag21a`, `wcag21aa` and `wcag22aa`. A violation fails the suite. The
   gate runs in the default theme in light and dark, in the Terminal theme,
   and in the pseudo-locale `en-XA` of ADR 181.
4. **An exception has a reason.** A test can turn off one axe rule for one
   element only with the rule ID and a comment that gives a real reason, as
   with `//nolint`. Reviewers check each exception. An exception never turns
   off a rule for a full page.

### Manual checks

5. **Screen-reader checks in acceptance (owner).** A screen or a flow that
   changes passes manual checks before its acceptance:

   | Check | With |
   | --- | --- |
   | Screen reader on the desktop | NVDA with Firefox on Windows, VoiceOver with Safari on macOS |
   | Screen reader on a phone | VoiceOver with Safari on iOS, TalkBack with Chrome on Android |
   | Keyboard only | each flow from start to end, with a visible focus at each step |
   | Zoom and reflow | 200% text zoom. Content reflows at a width of 320 CSS pixels (1280 px at 400% zoom) with no horizontal scroll |
   | Text spacing | The text spacing values of WCAG 1.4.12 cause no loss of content or function |
   | System settings | reduced motion, forced colors (Windows contrast themes) |

   Data tables, diffs, code, logs and other machine-made text can scroll
   horizontally in their own region. The page does not scroll horizontally.

   The pull request of the change records each check and its result. A
   person who cannot do a check asks a maintainer to do it.

### Mobile platform behavior

6. **Platform behaviors are acceptance checks (owner).** A screen or a flow
   that changes passes these checks on iOS Safari and on Android Chrome:

   | Behavior | The check |
   | --- | --- |
   | Back gesture | The back gesture and the back button close an open dialog, sheet or menu first. They never discard input without a confirmation. A step of a flow is one history entry. |
   | Safe area | No control or text is under the notch, the rounded corners or the home indicator. The page uses `viewport-fit=cover` and the `env(safe-area-inset-*)` values. |
   | On-screen keyboard | The field with focus and its action stay visible above the keyboard. A text field has a font size of at least 16 CSS pixels, so the browser does not zoom on focus. Fields set `inputmode`, `autocomplete` and `enterkeyhint` where they apply. |
   | `theme-color` | The `theme-color` meta element follows the current color mode and color scheme, so the browser chrome matches the page. |

   These checks are part of acceptance, with the checks of decision 5. They
   are not a separate design for phones (ADR 180 decision 4).

## Consequences

- `OpenDustpan/console` gets these changes. This ADR does not change that
  repository.
  - `@axe-core/playwright` is a new dev dependency. It needs the approval of
    the owner before it lands.
  - The e2e suite gets the axe gate of decision 3, with the exception rule
    of decision 4.
  - The pull request template gets the manual checks of decisions 5 and 6.
  - The console's `CLAUDE.md` and `design/README.md` say "designed against
    WCAG 2.2 AA" and add the rules of this ADR.
- A later change can add patterns to `scripts/docs-banned-words.py` that
  find a claim of WCAG conformance, as the list does for DORA.
- A full audit, a published conformance report or an accessibility
  statement needs a new decision.
