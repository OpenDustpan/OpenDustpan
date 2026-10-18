# 181. Console languages, formats and translation

Status: accepted · 2026-10-18 · accepts RFC 11 · adds to ADR 180

## Context

The console is English only, and it formats dates and numbers in English.
RFC 11 proposes a translated console, with separate settings for the
language and for the formats. On 18 October 2026 the project owner decided
the languages, the settings and the rules for translation. The project lead
gave the design work to an agent, with the decisions of the owner. This ADR
records the decisions of the owner and the design decisions that follow
from them. Each decision of the owner has the label "(owner)".

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

## Decision

### Languages

1. **The languages of the first release (owner).** The console ships 28
   languages. They are the 24 official languages of the European Union, and
   Simplified Chinese, Japanese, Korean and Portuguese (Brazil). Russian is
   not in the set for now. Each language has one BCP 47 tag:

   | Group | Tags |
   | --- | --- |
   | Official languages of the European Union | `bg`, `cs`, `da`, `de`, `el`, `en`, `es`, `et`, `fi`, `fr`, `ga`, `hr`, `hu`, `it`, `lt`, `lv`, `mt`, `nl`, `pl`, `pt-PT`, `ro`, `sk`, `sl`, `sv` |
   | Other languages | `zh-Hans`, `ja`, `ko`, `pt-BR` |

   `en` is the source language, in STE with US spelling. No language in the
   set writes from right to left.
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
     gives `de`.

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
   controls of the console. If `Intl.Locale.prototype.getWeekInfo` is not
   available, the default is Monday. UTC is one choice away in the time
   zone list, because operators often compare times with logs in UTC. If
   `Intl` rejects a stored time zone, the console falls back to the
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
   settings.
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
    `margin-left` or `right`. The first release has no right-to-left
    language. The logical properties make a later right-to-left language
    possible without a change to each component.
15. **Text can grow.** German text can be one third longer than English
    text. Labels and buttons wrap. No layout depends on the length of an
    English word. A table column can truncate a value only when the full
    value is available, for example in a tooltip and in the accessible
    name.
16. **Fonts for each script.** Bulgarian uses Cyrillic, and Greek uses the
    Greek script. Chinese, Japanese and Korean need large font files. The
    console loads the font files of a script only for a language that needs
    it, split with `unicode-range`. Before a CJK language ships, the console
    checks the license and the size of the IBM Plex family for its script.
    If no IBM Plex family fits, the console uses the system font of that
    script. Machine-made values stay in IBM Plex Mono. IBM Plex Mono has no
    CJK glyphs, so the monospace stack names a system monospace font as the
    fallback after it, for example `ui-monospace, monospace`. This adds no
    font dependency.

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

## Consequences

- RFC 11 gets `status: accepted`, and its "Outcome" section names this ADR
  and ADR 182.
- This ADR changes ADR 180 only by the six fields of decision 8. The
  integrator adds them, and their checks, with the routes of ADR 180.
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
  - The e2e suite gets the `en-XA` run of decision 22.
  - The settings page gets the language, the formatting locale and the
    overrides, with the live example of decision 6.
  - The console's `CLAUDE.md` adds the rules for messages: named arguments,
    ICU plurals, no gender selection, machine-made values in arguments.
- Each new language needs its message file, its term list with a sign-off,
  and native review of its high-stakes messages.
- Text that the server sends to people outside the console, for example
  chat messages and notifications, stays in English in this ADR. A later
  decision can use the `language` of the preferences for it.
- English (UK) as a variant (RFC 11) is not in this decision. It can come
  later as a small file with only the words that differ.
