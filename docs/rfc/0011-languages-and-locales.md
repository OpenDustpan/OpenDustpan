---
rfc: 11
title: Languages and locales
status: draft
created: 2026-10-10
---

# RFC 11. Languages and locales

Status: draft.

## Problem

The console is English only, and it formats dates and numbers in English.
Teams in Germany, China, France and everywhere else use infrastructure tools.
Many of their engineers would rather read the console in their own language.
Some prefer English, even when their system is set to another language.

## Proposal

### Three settings: language, formats, time zone

A person's language and the way they write dates and numbers are often not
the same. A typical case: someone who lives in the Netherlands and reads
English. They want English words, but Dutch dates (`10-10-2026`), a 24-hour
clock (`15:33`), Dutch numbers (`1.234,56`) and a week that starts on Monday.
A colleague in the same office may want English with US formats. So there
are three settings, each with its own default:

1. **Language**: which translation the console shows, including the variant
   of a language when the console has one (English (UK), English (US)).
   Default: the first language in the browser's list (`navigator.languages`)
   that the console has, else English.
2. **Formats**: a full locale for dates, times, numbers and plural rules
   (`nl-NL`, `en-US`, `de-CH`), passed to the browser's `Intl` APIs. It is
   independent of the language: English words with `nl-NL` formats is a valid
   choice. Default: the browser's own locale (`navigator.language`).
   Advanced overrides sit on top of it: 12-hour or 24-hour clock, and the
   first day of the week. They map to the Unicode locale extensions
   (`nl-NL-u-hc-h23`, `-u-fw-mon`), so `Intl` applies them with no code of our
   own.
3. **Time zone**: the zone for every time the console shows. Default: the
   browser's zone. UTC is one choice away, because operators often compare
   times with logs in UTC.

The settings page shows a live example next to each choice ("Friday 10
October 2026, 15:33 · 1.234,56"), so a person sees what they pick. Each
language is listed in its own name (Deutsch, Français, 中文, English (UK)),
never with a flag. A flag is a country, not a language. A short language
switcher in the user menu also helps a person who lands in a language they
cannot read and so cannot find the settings page.

Until accounts exist (ADR 6, RFC 2), the console keeps these settings in the
browser. When accounts exist, the server keeps them with the account, and
they follow the person to every browser.

### What is translated, and what never is

- **Translated:** every word the console writes: labels, headings, help text,
  empty states, confirmations and the console's own error messages.
- **Formatted by the formats setting:** dates, times, relative times
  ("3 hours ago"), numbers and durations, with the browser's `Intl` APIs.
  Plural rules for a message follow the language, because a plural is part
  of the words. `Intl.PluralRules` gives them, because many languages have
  more than two plural forms.
- **Never translated:** anything that a machine made. Stack, workspace and
  resource names, URNs, resource addresses, IDs, commands, CLI output and log
  lines stay verbatim, as the design system already requires.

### Errors from the server

The API sends error text in English, because the tools' CLIs show it to
users as it is. The console must not show that English text inside a German
page. So each API error gets a stable machine code next to its message (an
added field, so no client breaks). The console translates the code, and shows
the server's English text only for a code it does not know.

Server logs stay in English. Operators search them, and logs are not for end
users.

### How translations are made and kept

- English is the source language, written in ASD-STE100 (ADR 14), with
  Oxford spelling: British spelling with `-ize` endings ("organization",
  "colour", "licence", "behaviour", "centre"). The docs already write it this
  way, and it is the spelling of many international bodies (the UN, ISO, the
  Oxford University Press). Its `-ize` endings also match the tools' own
  words ("organization" in both the Pulumi and the TFE APIs).
- Identifiers keep their own spelling: CSS `color`, API fields, flags and
  commands are machine-made and stay verbatim. The spelling rule applies to
  prose only.
- English (US) ships as a small variant catalog that lists only the words that
  differ ("color", "license", "behavior", "center"). English (UK) is the
  source itself. STE was
  made in part to make translation easier and more exact.
- Each language is one catalog file in the console repository. A message uses
  named placeholders, and the product name is a placeholder too, so that
  themes (RFC 9) work in every language.
- `npm run check` reports keys that are missing from a catalog. A missing
  translation falls back to English and warns. It never breaks the build,
  so a half-done language can still ship.
- Volunteers translate through a hosted tool that opens pull requests, for
  example a self-hosted Weblate.

### Layout and type

- **Longer words.** German text is often a third longer than English. Labels
  and buttons wrap, and no layout depends on the length of an English word.
- **Fonts.** The console ships IBM Plex Sans with a Latin subset only. Chinese,
  Japanese and Korean need their own font files, which are large. They load
  only for those languages, split by `unicode-range` so a page downloads only
  the characters it shows. IBM Plex has CJK families. Their licence and their
  size need a check. Machine-made text stays in IBM Plex Mono, because it is
  almost always ASCII.
- **Right-to-left.** Arabic and Hebrew need the layout mirrored. The CSS moves
  to logical properties (`margin-inline-start`, not `margin-left`), so that
  right-to-left costs little later, even if it does not ship first.

## Options

1. **Translated console** (above).
2. **English only.** This costs nothing now, and every string that is added
   without a key makes translation more expensive later.
3. **Translate the server's messages on the server.** The tools' CLIs show
   server errors as they are, so this would show German errors to someone
   whose console is in English. The language is a choice of the person, and
   only the console knows the person.

## Open questions

- Which library? `Intl` covers formats and plural rules. A small catalog
  loader might be enough, or ICU MessageFormat through a library (FormatJS,
  Lingui, i18next). A new dependency needs the owner's approval.
- Are the tools' nouns translated? "Stack" and "workspace" are the tools' own
  words, and the CLIs and their docs say them in English. The lean is to keep
  them in English, like other product terms.
- Which languages ship first, and who checks each one?
- Does right-to-left ship in the first release, or only the logical CSS?
- Do emails and notifications, when they exist, use the account's language?

## Outcome

None yet.
