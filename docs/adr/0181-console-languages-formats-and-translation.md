# 181. Console languages, formats and translation

Status: accepted · 2026-10-18. Accepts RFC 11. Adds to ADR 180. Route paths superseded in part by ADR 210. Decision 1 superseded in part by ADR 231. Decisions 18 and 19 superseded in part by ADR 232. Extended by ADRs 231 to 234

Changed by:

- ADR 210 changes: the route paths and the server: action names.
- ADR 231 changes decision 1: Afrikaans, 33 languages.
- ADR 231 extends decisions 16 and 26: the font and the switcher entry of
  Afrikaans.
- ADR 232 changes decisions 18 and 19: a draft term list and the translation
  agents.
- ADR 232 extends decisions 12, 20 and 21: statuses, the review order and new
  strings in releases.
- ADR 233 extends decisions 7, 10, 11, 13 and 17: the checks of terms,
  arguments, plurals and formats.
- ADR 234 changes: the consequence that email and chat stay in English.
- ADR 234 extends decision 8: the preferences field resolvedLanguage.
- ADR 286 changes: the words catalog and promotion become self-service and
  failover.

## Context

The console is English only, and it formats dates and numbers in English.
RFC 11 proposes a translated console, with separate settings for the
language and for the formats. On 18 October 2026 the project owner decided
the languages, the settings and the rules for translation. The project lead
gave the design work to an agent, with the decisions of the owner. This ADR
records the decisions of the owner and the design decisions that follow
from them. Each decision of the owner has the label "(owner)". The project
manager decided the design decisions for right-to-left text, mirroring and
fonts under the owner's decision rule: follow what Google, AWS, Mozilla or
GitLab do. Each such decision has a "Benchmark:" line.

Facts:

- ADR 14 makes ASD-STE100 the voice of all English text. ADR 16 makes US
  spelling the rule. The English text is the source of every translation.
- ADR 58 keeps the console rule "monospace for machine-made text".
- ADR 180 adds the preferences of a person: one document for each person on
  the server, at `/api/server/caller/preferences`. The console keeps a copy
  in the browser's local storage and reads it before the first paint.
- The browsers that the console supports have the `Intl` APIs:
  `Intl.DateTimeFormat`, `Intl.NumberFormat`, `Intl.RelativeTimeFormat`,
  `Intl.PluralRules`, `Intl.ListFormat`, `Intl.DisplayNames` and
  `Intl.Locale`.
- ADR 182 gives each API error a stable machine code, so that the console
  can show an error in the language of the person.
- In `GLOSSARY.md`, "Catalog" is the self-service gateway. This ADR calls a
  file of translated messages a "message file", so that the two do not mix.
- Arabic (`ar`) and Hebrew (`he`) write from right to left. Numbers,
  Latin words, code and paths in their text stay left to right. The Unicode
  Bidirectional Algorithm puts them in order, but it gets punctuation and
  slashes next to the edge of a run wrong without isolation.
- `Intl.Locale` canonicalizes the old tag `iw` to `he`.
- Arabic has six plural categories in CLDR: `zero`, `one`, `two`, `few`,
  `many` and `other`. Hebrew has more than two.
- IBM Plex has families for Arabic, Hebrew, Japanese and Korean under the
  SIL Open Font License 1.1. IBM released no Simplified Chinese family
  before the project wrote this ADR. Noto Sans SC is under the same license.
- Bulgarian, Russian and Ukrainian use the Cyrillic script, and Greek uses
  the Greek script.
- Arabic text can use Western digits (0 to 9) or Arabic-Indic digits
  (٠ to ٩). `Intl.NumberFormat` and `Intl.DateTimeFormat` take a
  `numberingSystem` option, for example `latn` or `arab`. Without it, the
  locale decides. CLDR gives different defaults for different Arabic
  regions.
- Android ships two pseudo-locales: `en-XA` for long and accented text, and
  `ar-XB` for right-to-left text.

## Decision

### Languages

1. **The languages of the first release (owner).** The console ships 32
   languages. They are the 24 official languages of the European Union, and
   eight other languages. Each language has one BCP 47 tag:

   | Group | Tags |
   | --- | --- |
   | Official languages of the European Union | `bg`, `cs`, `da`, `de`, `el`, `en`, `es`, `et`, `fi`, `fr`, `ga`, `hr`, `hu`, `it`, `lt`, `lv`, `mt`, `nl`, `pl`, `pt-PT`, `ro`, `sk`, `sl`, `sv` |
   | Other languages | `ar`, `he`, `ja`, `ko`, `pt-BR`, `ru`, `uk`, `zh-Hans` |

   `en` is the source language, in STE with US spelling. `ar` and `he` write
   from right to left (decision 27).
2. **The default is the browser (owner).** With no setting, the console
   shows the first language in `navigator.languages` that it has. A match
   uses the language and the script, and the region only for Portuguese.
   The console calls `Intl.Locale.prototype.maximize()` on each tag to get
   the script:

   - `pt-BR` gives `pt-BR`. A bare `pt` follows the CLDR likely subtags and
     gives `pt-BR`. Every other `pt` tag, for example `pt-AO`, gives
     `pt-PT`.
   - A `zh` tag matches when its maximized script is `Hans`: `zh`, `zh-CN`,
     `zh-SG` and `zh-Hans` give `zh-Hans`. A `zh` tag with another script
     gives no match, because the console has no Traditional Chinese. For
     example `zh-TW`, `zh-HK`, `zh-MO` and `zh-Hant` maximize to `Hant`.
     The console then tries the next language in the list.
   - Any other tag matches on its language subtag, for example `de-AT`
     gives `de` (decision 24).

   With no match in the list, the console shows `en`.
3. **A person can select any language (owner).** Settings lists each
   language in its own name and script, for example "Deutsch", "Français",
   "简体中文" and "Português (Brasil)". It never uses a flag, because a flag
   is a country, not a language. The user menu has a short language switcher
   too. A person who gets a language that they cannot read can then find
   the switcher by its place, without the settings page.

### Formats

4. **The language and the formats are separate settings (owner).** The
   language sets the words. The formatting locale sets dates, times,
   numbers and the order of lists. English words with Dutch formats is a
   valid choice. The default of the formatting locale is the browser's
   locale (`navigator.language`). The console never derives the formatting
   locale from the language.
5. **Overrides for each person (owner).** On top of the formatting locale, a
   person can set:

   | Override | Values | With no value |
   | --- | --- | --- |
   | Clock | 12-hour, 24-hour | the formatting locale decides |
   | Date style | the formatting locale, ISO 8601, DMY, MDY | the formatting locale |
   | First day of the week | Monday to Sunday | the formatting locale decides |
   | Time zone | an IANA time zone name, for example `Europe/Amsterdam` or `UTC` | the browser's time zone |

   The clock maps to the `hourCycle` option of `Intl` (`h12`, `h23`), not to
   a Unicode extension in the locale tag. The ISO 8601 style shows
   `2026-10-18` and `2026-10-18 15:33`. It always uses the 24-hour clock,
   whatever the clock override says. DMY and MDY use numeric parts only,
   for example 18/10/2026 or 10/18/2026. They keep the separators of the
   formatting locale and change only the order of the parts. The console
   builds them from `Intl.DateTimeFormat.prototype.formatToParts`, not from
   its own format strings. The first day of the week sets the calendar
   controls of the console. If neither `Intl.Locale.prototype.getWeekInfo()`
   nor the older `weekInfo` property is available, the default is Monday.
   UTC is one choice away in the time zone list, because operators often
   compare times with logs in UTC. If `Intl` rejects a stored time zone, the console falls back to the
   browser's time zone and says so in settings.
6. **A live example.** Settings shows an example next to each choice, for
   example "Sunday 18 October 2026, 15:33 · 1.234,56". The person sees the
   result before they save.
7. **The platform does the formatting.** Dates, times, relative times,
   numbers, durations, lists and plural rules come from the `Intl` APIs. The
   console has no date or number format code of its own, other than the
   date styles of decision 5. It adds no date library for formatting.

### The preferences

8. **Six new fields in the preferences document.** This ADR adds these
   fields to the document of ADR 180:

   | Field | Values | Default |
   | --- | --- | --- |
   | `language` | a BCP 47 tag of 2 to 35 characters, or `null` | `null`: the browser (decision 2) |
   | `formattingLocale` | a BCP 47 tag of 2 to 35 characters with no `-u-` or `-x-` extension, or `null` | `null`: `navigator.language` |
   | `hourCycle` | `h12`, `h23` or `null` | `null`: the formatting locale |
   | `dateStyle` | `locale`, `iso8601`, `dmy`, `mdy` | `locale` |
   | `firstDayOfWeek` | `mon`, `tue`, `wed`, `thu`, `fri`, `sat`, `sun` or `null` | `null`: the formatting locale |
   | `timeZone` | an IANA time zone name of 1 to 64 characters, or `null` | `null`: the browser's time zone |

   The rules of ADR 180 apply: a JSON merge patch, `null` sets a field back
   to its default, and a value that the table does not allow answers 400.
   The server checks only the shape of `language`: a BCP 47 tag of at most
   35 characters, as ADR 180 does for the IDs of schemes. The console owns
   the list of decision 1, so a new language needs no server release. The
   server checks the shape of `formattingLocale`. It rejects a Unicode
   `-u-` extension and a private `-x-` extension, because `hourCycle` and
   `firstDayOfWeek` are the only overrides. The server checks that
   `timeZone` is a zone in its time zone database. The server binary embeds
   that database (`time/tzdata`), so the check does not depend on the host.
   For a `language` that is unknown or not supported, the console falls back
   to the browser match of decision 2, then to `en`, and says so in
   settings. Decision 25 adds a seventh field, `digits`.
9. **Before the first paint.** The inline script of ADR 180 also sets the
   `lang` attribute of the root element from the copy in local storage, and
   loads the message file of that language. With no copy, the console uses
   decision 2. The sign-in pages have no account yet, so they use decision
   2 only.

### Messages

10. **One message file for each language (owner).** The console keeps each
    language in one message file in the console repository. A message is
    in ICU MessageFormat, with named arguments. A message with a number
    uses `plural` with every plural category that `Intl.PluralRules` gives
    for the language. For example, Polish and Lithuanian need more than two
    forms. The product name is an argument, so that the presets of ADR 180
    work in every language.
11. **Text that is neutral to gender (owner).** A message never selects a
    form by the gender of a person. The console does not know the gender of
    a person and never asks. A message about a person uses a form that is
    neutral in its language, for example a passive form or the name of the
    role. `select` is only for a machine-made value, for example the tool of
    a unit.
12. **A missing message falls back to English.** A message that a language
    does not have shows in English, with `lang="en"` on its element. The
    console check reports each missing message. A missing message does not
    fail the build, so a partial language can ship.
13. **Machine-made values are never translated (owner).** Names, URNs,
    resource addresses, IDs, commands, CLI output, log lines, state and diffs
    stay as they are, in monospace. Their elements get `translate="no"`, so
    that the translation function of a browser does not change them. A
    message puts a machine-made value in an argument, never in the text of
    the message.
14. **Logical CSS properties (owner).** The console uses logical properties,
    for example `margin-inline-start` and `inset-inline-end`, not
    `margin-left` or `right`. The console's lint rule for physical CSS
    properties and physical Tailwind classes is a gate before the first
    right-to-left release. An exception needs a comment with a real reason,
    for example a diff gutter (decision 30).
15. **Text can grow.** German text can be one third longer than English
    text. Labels and buttons wrap. No layout depends on the length of an
    English word. A table column can truncate a value only when the full
    value is available, for example in a tooltip and in the accessible
    name.
16. **A font family for each script.** The console loads the files of a
    script only for a language that needs it, split with `unicode-range`.
    The console self-hosts every file and loads no font from a third party.

    | Script | Languages | Family | Fallback |
    | --- | --- | --- | --- |
    | Latin, Cyrillic, Greek | the EU languages, `pt-BR`, `ru`, `uk` | IBM Plex Sans | `system-ui, sans-serif` |
    | Arabic | `ar` | IBM Plex Sans Arabic | `system-ui, sans-serif` |
    | Hebrew | `he` | IBM Plex Sans Hebrew | `system-ui, sans-serif` |
    | Japanese | `ja` | IBM Plex Sans JP | `system-ui, sans-serif` |
    | Korean | `ko` | IBM Plex Sans KR | `system-ui, sans-serif` |
    | Simplified Chinese | `zh-Hans` | IBM Plex Sans SC if a release has it, else Noto Sans SC | `system-ui, sans-serif` |

    The license of each family is the SIL Open Font License 1.1. The
    license table of the console records it, as ADR 180 decision 10 does
    for color schemes. Machine-made text stays in IBM Plex Mono. IBM Plex
    Mono has no CJK glyphs, so the monospace stack names a system monospace
    font as the fallback after it: `ui-monospace, monospace`. This adds no
    font dependency.

    Benchmark: Google Fonts serves each CJK family in about 100 slices with
    `unicode-range`, so that a page loads only the slices that its text
    uses.

### Terms that stay in English

17. **Do not translate too much (owner).** Each language has a list of terms
    that stay in English, for example "stack", "workspace", "plan", "state",
    the names of commands and the words of a CLI. The rule for the list is:
    use the word that does not make a user of that language cringe. A term
    that engineers of a language say in English stays in English. A term
    that they say in their own language gets its translation.
18. **A native speaker signs off the list (owner).** An engineer whose
    first language is that language signs off its list. The pull request
    that adds a language holds its list and names the engineer who signed
    it off. A language does not ship without a signed-off list. A change to
    a list needs a new sign-off.

### How the project makes translations

19. **Machine translation with inputs (owner).** A first translation comes
    from machine translation. Its inputs are the English message, the
    description of the message for translators, `GLOSSARY.md` and the list
    of terms that stay in English for the language. A translator can change
    any result.
20. **Native review for high-stakes text (owner).** A native speaker
    reviews each high-stakes message before it ships. High-stakes messages
    are confirmations of destructive actions, approvals, security text and
    billing text. The message file marks each high-stakes message.
21. **High-stakes text without review shows in English.** Until a native
    speaker reviews a high-stakes message, the console shows its English
    text, with `lang="en"`. A wrong translation of "Delete this stack" can
    do more harm than an English one. The console check reports each
    high-stakes message without review.

### Tests

22. **A pseudo-locale (owner).** Test builds of the console have the
    pseudo-locale `en-XA`. It makes each message about 40% longer, puts
    accents on its letters and puts brackets around it, for example
    "[Ðéļéţé šţåçķ ·····]". The console e2e suite runs its screens in
    `en-XA` and fails on text that a container clips, and on text without
    brackets. Text without brackets is text that is not in a message file.
    Release builds do not list `en-XA`.

### Arabic, matching and digits

23. **Modern Standard Arabic (owner).** The `ar` messages are in Modern
    Standard Arabic, the shared written standard for formal and technical
    text. One `ar` file serves every region. Regional formats, for example
    `ar-EG` or `ar-SA`, come from the formatting locale (decision 4),
    through `Intl`.
24. **Matching.** A tag matches on its language subtag after `Intl.Locale`
    canonicalizes it. So `iw` and `he-IL` give `he`, `ar-EG` and `ar-SA`
    give `ar`, and `uk-UA` gives `uk`.
25. **Digits follow the formatting locale, with an override (owner).** The
    formatting locale decides between Western and Arabic-Indic digits, as
    `Intl` does for it. A person can override it with the preference
    `digits`:

    | Field | Values | Default |
    | --- | --- | --- |
    | `digits` | `latn` (Western digits) or `arab` (Arabic-Indic digits), or `null` | `null`: the formatting locale decides |

    The list of values can grow when a new language uses other digits, for
    example `deva` for Devanagari digits. A new value needs no new field.

    The console gives the value to `Intl` as the `numberingSystem` option of
    each number, date and time format. The rules of decision 8 apply to the
    field. The live example of decision 6 shows the digits. The ISO 8601
    date style of decision 5 always uses Western digits. Machine-made values
    keep their own digits in every case: IDs, versions, addresses, times in
    logs and the text of a plan.

    Benchmark: iOS and macOS let a person select Western or Arabic-Indic
    digits in the language and region settings, separate from the language.
26. **The switcher.** Each entry of the settings list and of the user menu
    (decision 3) has its own `lang` and `dir` attributes, so that a screen
    reader and the layout of the list read it right.

### Direction

27. **The language sets the direction.** The console sets `dir` on the root
    element from the language, not from the formatting locale. It reads the
    direction from `Intl.Locale.prototype.getTextInfo()`, or from the older
    `textInfo` property. If neither is available, a fixed list in the console
    gives `rtl` for `ar` and `he`. The inline script of decision 9 sets `dir`
    with `lang`, before the first paint.
28. **Isolate machine-made values.** An identifier argument (ADR 182
    decision 4) renders in an element with `dir="ltr"` and `translate="no"`.
    Text that a person wrote, for example a title or a comment, renders with
    `dir="auto"`, because its direction is not known. In plain text, for
    example an email subject or a chat message, the server wraps such a value
    in the Unicode isolates U+2068 and U+2069 (FSI and PDI).

    Benchmark: the W3C guide "Inline markup and bidirectional text in HTML"
    asks for isolation of each inserted value of unknown direction, with
    `<bdi>`, `dir` or the Unicode isolates.

### Mirroring

29. **What mirrors.** In a right-to-left language, the layout mirrors: the
    navigation, the order of columns, breadcrumbs, the steps of a flow,
    progress bars and the placement of a dialog's buttons. An icon that
    points in the direction of reading mirrors: back, forward, the chevron
    of a breadcrumb or a "next" link, and an icon for "indent".
30. **What never mirrors.** These keep their form and their order in every
    language:

    | Object | Why |
    | --- | --- |
    | Diff glyphs `+`, `~`, `-` and `+-`, and the diff block | A diff is machine-made text. The block has `dir="ltr"` and keeps its gutter on the left. |
    | Code, state, logs, CLI output, plans and paths | Machine-made text, with `dir="ltr"` (decision 13). |
    | Shortcut hints `[a]`, `‹a›` and inverse (ADR 180 decision 3) | They name a key. A hint renders in an element with `dir="ltr"`, because the bidirectional algorithm mirrors `[`, `]`, `‹` and `›` in right-to-left text. `‹a›` keeps U+2039 before the key and U+203A after it. |
    | The check mark, the cross, the search glass, a clock, a lock | They do not point in a direction. |
    | Media controls: play and pause | They name a direction of tape, not of reading. |
    | Logos and the mark of a tool | A mark of another project keeps its own form. |
    | Numbers and their digits | The formatting locale decides the digits, never the direction of the layout. |

    Benchmark: Material Design "Bidirectionality" mirrors the layout and the
    icons that point in a direction, and keeps media controls, clocks and
    check marks as they are. Apple's Human Interface Guidelines ask to flip
    only the images that show a direction.
31. **Charts with a time axis (owner).** The direction of time in a chart
    in a right-to-left language needs research first: what other products
    do, and what their users find easy to read. Until a decision, a chart
    keeps time from left to right, with `dir="ltr"` on its plot area. Its
    labels and its legend follow the language.

### Type and right-to-left tests

32. **Type rules for each script.** The console sets `font-synthesis: none`,
    so that a browser never makes a false italic or bold of a CJK, Arabic or
    Hebrew font. Emphasis in these scripts uses weight, not italics. Arabic
    text gets a line height of at least 1.6. Japanese text gets
    `word-break: auto-phrase` where the browser supports it, so that a line
    breaks between phrases. Korean text gets `word-break: keep-all`, because
    Korean puts spaces between words. Other line breaks follow the browser.
33. **A right-to-left pseudo-locale.** Test builds get the pseudo-locale
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
34. **The axe gate runs in both directions.** The axe gate of ADR 183
    decision 3 also runs in `ar-XB`, and in `ar` and `he` when they ship.

## Consequences

- RFC 11 gets `status: accepted`, and its "Outcome" section names this ADR
  and ADR 182.
- This ADR changes ADR 180 only by the six fields of decision 8 and the
  field `digits` of decision 25. The integrator adds them, and their checks,
  with the routes of ADR 180.
- `cmd/opendustpan` imports `time/tzdata` (about 450 KB more binary).
- `OpenDustpan/console` gets these changes. This ADR does not change that
  repository.
  - An ICU MessageFormat library, for example FormatJS, is a new
    dependency. It needs the approval of the owner before it lands.
  - Every string of the current screens moves into the `en` message file.
    This is the largest step, and it comes before any other language.
  - A lint rule fails on a physical CSS property or a physical Tailwind
    class, for example `ml-2` or `left-0`. If the rule needs a new dev
    dependency, that needs approval first.
  - The console check reports missing messages, high-stakes messages
    without review, and a language without a signed-off term list.
  - The e2e suite gets the `en-XA` run of decision 22 and the `ar-XB` run of
    decision 33.
  - The settings page gets the language, the formatting locale and the
    overrides, with the live example of decision 6.
  - The font files and license entries of decision 16.
  - A review of each icon against decisions 29 and 30. Each icon component
    declares whether it mirrors.
  - The console's `CLAUDE.md` adds the rules for messages: named arguments,
    ICU plurals, no gender selection, machine-made values in arguments.
- Each new language needs its message file, its term list with a sign-off,
  and native review of its high-stakes messages.
- Text that the server sends to people outside the console, for example
  chat messages and notifications, stays in English in this ADR. A later
  decision can use the `language` of the preferences for it.
- English (UK) as a variant (RFC 11) is not in this decision. It can come
  later as a small file with only the words that differ.
