# 231. New languages, right-to-left text, digits, mirroring rules and fonts for each script

Status: accepted · 2026-10-18. Supersedes in part ADR 181 decisions 1, 14 and 16. Extends ADR 181 decisions 2, 3, 5, 8 and 22, and ADR 183 decision 3

## Context

ADR 181 gives the console 28 languages, none of them right to left, and
leaves Russian out for now. It makes the CSS logical, so that a
right-to-left language can come later without a change to each component.
On 18 October 2026 the project owner added Arabic and Hebrew, and planned
them now, not when a volunteer comes. The owner also added Afrikaans,
Ukrainian and Russian. The owner reviews English, Dutch and Afrikaans as a
native reviewer (ADR 232). This ADR records these decisions of the owner with the label
"(owner)", and the design decisions that follow from them. The project
manager decided the design decisions under the owner's decision rule:
follow what Google, AWS, Mozilla or GitLab do. Each such decision has a
"Benchmark:" line.

Facts:

- Arabic (`ar`) and Hebrew (`he`) write from right to left. Numbers,
  Latin words, code and paths in their text stay left to right. The Unicode
  Bidirectional Algorithm puts them in order, but it gets punctuation and
  slashes next to the edge of a run wrong without isolation.
- `Intl.Locale` canonicalizes the old tag `iw` to `he`.
- Arabic has six plural categories in CLDR: `zero`, `one`, `two`, `few`,
  `many` and `other`. Hebrew has more than two.
- IBM Plex has families for Arabic, Hebrew, Japanese and Korean under the
  SIL Open Font License 1.1. IBM released no Simplified Chinese family
  before the project wrote ADR 181. Noto Sans SC is under the same license.
- Ukrainian and Russian use the Cyrillic script, as Bulgarian does. Both
  have the plural categories `one`, `few`, `many` and `other`.
- Arabic text can use Western digits (0 to 9) or Arabic-Indic digits
  (٠ to ٩). `Intl.NumberFormat` and `Intl.DateTimeFormat` take a
  `numberingSystem` option, for example `latn` or `arab`. Without it, the
  locale decides. CLDR gives different defaults for different Arabic
  regions.
- Android ships two pseudo-locales: `en-XA` for long and accented text, and
  `ar-XB` for right-to-left text.

## Decision

### Languages

1. **33 languages (owner).** The set of ADR 181 decision 1 gets five more
   languages: Arabic (`ar`), Hebrew (`he`), Afrikaans (`af`), Ukrainian
   (`uk`) and Russian (`ru`). The 24 official languages of the European
   Union stay as they are. The other languages are `zh-Hans`, `ja`, `ko`,
   `pt-BR`, `ar`, `he`, `af`, `uk` and `ru`.
2. **Ukrainian and Russian now (owner).** The owner adds both languages
   now. To leave one of them out and add it later would start a political
   discussion. Adding both at the same time avoids it. Each is a language
   of its own, with its own message file, term list and reviewers.
3. **Modern Standard Arabic (owner).** The `ar` messages are in Modern
   Standard Arabic, the shared written standard for formal and technical
   text. One `ar` file serves every region. Regional formats, for example
   `ar-EG` or `ar-SA`, come from the formatting locale (ADR 181 decision 4),
   through `Intl`.
4. **Matching.** The match of ADR 181 decision 2 applies. A tag matches on
   its language subtag after `Intl.Locale` canonicalizes it. So `iw` and
   `he-IL` give `he`, `ar-EG` and `ar-SA` give `ar`, and `uk-UA` gives
   `uk`.
5. **Digits follow the formatting locale, with an override (owner).** The
   formatting locale decides between Western and Arabic-Indic digits, as
   `Intl` does for it. A person can override it with the new preference
   `digits`:

   | Field | Values | Default |
   | --- | --- | --- |
   | `digits` | `latn` (Western digits) or `arab` (Arabic-Indic digits), or `null` | `null`: the formatting locale decides |

   The list of values can grow when a new language uses other digits, for
   example `deva` for Devanagari digits. A new value needs no new field.

   The console gives the value to `Intl` as the `numberingSystem` option of
   each number, date and time format. The rules of ADR 181 decision 8 apply
   to the field. The live example of ADR 181 decision 6 shows the digits.
   The ISO 8601 date style of ADR 181 decision 5 always uses Western digits.
   Machine-made values keep their own digits in every case: IDs, versions,
   addresses, times in logs and the text of a plan.

   Benchmark: iOS and macOS let a person select Western or Arabic-Indic
   digits in the language and region settings, separate from the language.
6. **The switcher.** Settings and the user menu list "العربية", "עברית",
   "Afrikaans", "Українська" and "Русский" in their own script. Each entry
   has its own `lang` and `dir` attributes, so that a screen reader and the
   layout of the list read it right.

### Direction

7. **The language sets the direction.** The console sets `dir` on the root
   element from the language, not from the formatting locale. It reads the
   direction from `Intl.Locale.prototype.getTextInfo()`, or from the older
   `textInfo` property. If neither is available, a fixed list in the console
   gives `rtl` for `ar` and `he`. The inline script of ADR 181 decision 9
   sets `dir` with `lang`, before the first paint.
8. **Logical CSS is a rule, not a preparation.** ADR 181 decision 14 stays,
   and the console's lint rule for physical CSS properties and physical
   Tailwind classes is a gate before the first right-to-left release. An
   exception needs a comment with a real reason, for example a diff gutter
   (decision 11).
9. **Isolate machine-made values.** An identifier argument (ADR 182
   decision 4) renders in an element with `dir="ltr"` and `translate="no"`.
   Text that a person wrote, for example a title or a comment, renders with
   `dir="auto"`, because its direction is not known. In plain text, for
   example an email subject or a chat message, the server wraps such a value
   in the Unicode isolates U+2068 and U+2069 (FSI and PDI).

   Benchmark: the W3C guide "Inline markup and bidirectional text in HTML"
   asks for isolation of each inserted value of unknown direction, with
   `<bdi>`, `dir` or the Unicode isolates.

### Mirroring

10. **What mirrors.** In a right-to-left language, the layout mirrors: the
    navigation, the order of columns, breadcrumbs, the steps of a flow,
    progress bars and the placement of a dialog's buttons. An icon that
    points in the direction of reading mirrors: back, forward, the chevron
    of a breadcrumb or a "next" link, and an icon for "indent".
11. **What never mirrors.** These keep their form and their order in every
    language:

    | Object | Why |
    | --- | --- |
    | Diff glyphs `+`, `~`, `-` and `+-`, and the diff block | A diff is machine-made text. The block has `dir="ltr"` and keeps its gutter on the left. |
    | Code, state, logs, CLI output, plans and paths | Machine-made text, with `dir="ltr"` (ADR 181 decision 13). |
    | Shortcut hints `[a]`, `‹a›` and inverse (ADR 180 decision 3) | They name a key. A hint renders in an element with `dir="ltr"`, because the bidirectional algorithm mirrors `[`, `]`, `‹` and `›` in right-to-left text. `‹a›` keeps U+2039 before the key and U+203A after it. |
    | The check mark, the cross, the search glass, a clock, a lock | They do not point in a direction. |
    | Media controls: play and pause | They name a direction of tape, not of reading. |
    | Logos and the mark of a tool | A mark of another project keeps its own form. |
    | Numbers and their digits | The formatting locale decides the digits, never the direction of the layout. |

    Benchmark: Material Design "Bidirectionality" mirrors the layout and the
    icons that point in a direction, and keeps media controls, clocks and
    check marks as they are. Apple's Human Interface Guidelines ask to flip
    only the images that show a direction.
12. **Charts with a time axis (owner).** The direction of time in a chart
    in a right-to-left language needs research first: what other products
    do, and what their users find easy to read. RFC 78 keeps the question
    open until that research. Until a decision, a chart keeps time from
    left to right, with `dir="ltr"` on its plot area. Its labels and its
    legend follow the language.

### Fonts

13. **A font family for each script.** The console loads the files of a
    script only for a language that needs it, split with `unicode-range`
    (ADR 181 decision 16). The console self-hosts every file and loads no
    font from a third party.

    | Script | Languages | Family | Fallback |
    | --- | --- | --- | --- |
    | Latin, Cyrillic, Greek | the EU languages, `pt-BR`, `af`, `uk`, `ru` | IBM Plex Sans | `system-ui, sans-serif` |
    | Arabic | `ar` | IBM Plex Sans Arabic | `system-ui, sans-serif` |
    | Hebrew | `he` | IBM Plex Sans Hebrew | `system-ui, sans-serif` |
    | Japanese | `ja` | IBM Plex Sans JP | `system-ui, sans-serif` |
    | Korean | `ko` | IBM Plex Sans KR | `system-ui, sans-serif` |
    | Simplified Chinese | `zh-Hans` | IBM Plex Sans SC if a release has it, else Noto Sans SC | `system-ui, sans-serif` |

    The license of each family is the SIL Open Font License 1.1. The
    license table of the console records it, as ADR 180 decision 10 does
    for color schemes. Machine-made text stays in IBM Plex Mono, with
    `ui-monospace, monospace` after it.

    Benchmark: Google Fonts serves each CJK family in about 100 slices with
    `unicode-range`, so that a page loads only the slices that its text
    uses.
14. **Type rules for each script.** The console sets `font-synthesis: none`,
    so that a browser never makes a false italic or bold of a CJK, Arabic or
    Hebrew font. Emphasis in these scripts uses weight, not italics. Arabic
    text gets a line height of at least 1.6. Japanese text gets
    `word-break: auto-phrase` where the browser supports it, so that a line
    breaks between phrases. Korean text gets `word-break: keep-all`, because
    Korean puts spaces between words. Other line breaks follow the browser.

### Tests

15. **A right-to-left pseudo-locale.** Test builds get the pseudo-locale
    `ar-XB`, next to `en-XA`. It wraps each literal part of a message in
    U+202E (right-to-left override) and U+202C (pop directional
    formatting), so that Latin letters show from right to left as Arabic
    letters do. It does not wrap the arguments, so that each argument keeps
    its order and its direction, as a real value does in Arabic text. The
    page gets `dir="rtl"`. The console
    e2e suite runs its screens in `ar-XB`. It fails on text that a container
    clips, and on a machine-made value without `dir="ltr"`. A reviewer
    checks the screenshots of the run for a layout that does not mirror.
    Release builds do not list `ar-XB`.

    Benchmark: Android ships the pseudo-locales `en-XA` and `ar-XB` for this
    test.
16. **The axe gate runs in both directions.** The axe gate of ADR 183
    decision 3 also runs in `ar-XB`, and in `ar` and `he` when they ship.
    The gate uses `@axe-core/playwright`, which the owner approved
    (ADR 236).

## Consequences

- ADR 181 decision 1 lists 28 languages and says that none writes from
  right to left, and it leaves Russian out. Decision 1 of this ADR replaces
  that list. ADR 181 decision 14 calls logical CSS a preparation, and
  decision 8 makes it a gate. Decision 13 replaces the CJK rule of ADR 181
  decision 16.
- The preferences document of ADR 181 decision 8 gets the field `digits`
  (decision 5). The integrator adds it, and its check, with the other
  fields of ADR 181.
- `GLOSSARY.md` names 33 languages for "Language", and adds the digits
  override and the right-to-left pseudo-locale.
- `OpenDustpan/console` gets these changes. This ADR does not change that
  repository.
  - The message files `ar`, `he`, `af`, `uk` and `ru`, with their term lists
    (ADR 233).
  - The font files and license entries of decision 13.
  - The `ar-XB` builder and its e2e run.
  - A review of each icon against decisions 10 and 11. Each icon component
    declares whether it mirrors.
- The server needs no change for the language list, because it checks only
  the shape of a tag (ADR 181 decision 8). Server text in these languages
  follows ADR 234.
