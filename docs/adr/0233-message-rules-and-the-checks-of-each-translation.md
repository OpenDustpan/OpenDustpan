# 233. Message rules and the checks of each translation: terms, arguments, plurals and formats

Status: accepted · 2026-10-18. Extends ADR 181 decisions 7, 10, 11, 13 and 17

## Context

ADR 181 decides the rules for messages: ICU MessageFormat with named
arguments, every plural category of a language, no selection by gender,
machine-made values in arguments, and a term list for each language. It
does not say how a check enforces them. Machine translation (ADR 232) makes
many messages that no person reads first, so the checks must find the
errors that a machine makes. On 18 October 2026 the project owner approved
FormatJS for ICU MessageFormat in the console. The project manager decided
the rest under the owner's decision rule. Each such decision has a
"Benchmark:" line.

Facts:

- A machine translation can translate a term that must stay in English, for
  example "stack", "Pulumi" or `tofu plan`. It can also drop or rename an
  argument, or drop a plural category.
- `Intl.PluralRules.prototype.resolvedOptions().pluralCategories` gives the
  plural categories of a language.
- FormatJS formats a message with one locale. ADR 181 decision 4 separates
  the language, which sets the words, from the formatting locale, which
  sets numbers and dates.

## Decision

### Terms that stay in English

1. **A shared list and a list for each language (owner).** One shared term
   list holds the terms that stay in English in every language: the names
   of tools and projects ("Pulumi", "OpenTofu", "OpenDustpan"), command
   names, CLI flags, environment variables, HTTP methods and the names of
   file formats. The term list of each language adds the terms that its
   engineers say in English, for example "stack", "workspace", "plan" and
   "state" (ADR 181 decision 17). A language list can only add terms. It
   never removes a shared term.
2. **The check of a term.** For each message, the check finds each term of
   the two lists in the English text, as a whole word and with no regard to
   case. The translation must hold the same term in the same spelling. A
   term in a list can name its allowed forms in that language, for example
   the plural "Stacks" in German. A term can be marked as a noun only, for
   example "plan", so that the check skips the verb with the same
   spelling. A message can name an exception, with the term and a reason.
   A reviewer checks each exception.

   Benchmark: the glossaries of Google Cloud Translation and of AWS
   Translate hold terms that the machine must not translate. Mozilla's
   `compare-locales` fails a translation that breaks a fixed rule of the
   source.
3. **Machine-made values never reach the text.** ADR 181 decision 13 stays:
   a message puts a machine-made value in an argument. The check fails on
   an English message that holds a backtick, a path with `/`, a URN, an
   ID with a known prefix, or a word in `kebab-case` or `snake_case`
   outside an argument. A message that must show a command shows it as an
   argument with the kind identifier (ADR 182 decision 4).
4. **The product name and the agent's name are arguments.** A message never
   holds "OpenDustpan" or "Dusty" as text when it names the product or the
   agent of this installation. It uses the arguments `productName` and
   `agentName`. An operator can change both (ADR 235). "OpenDustpan" stays
   as text only where it names the project for a legal reason, for example
   in the license notice.

### Arguments, plurals and gender

5. **The same arguments in every language.** The check parses each
   translation with the ICU parser of FormatJS. It fails when a translation
   has an argument that the English message does not have, or lacks one
   that it has. It fails when the type of an argument differs, for example
   `plural` in English and plain text in the translation. It fails on a
   tag that the English message does not have.
6. **Every plural category.** Each `plural` in a translation has each
   category that `Intl.PluralRules` gives for its language, and `other`.
   Arabic needs six. A missing category fails the check. A category that
   the language does not use fails the check too, because a machine adds
   it by mistake. Every language can use exact matches such as `=0`.

   Benchmark: Mozilla's `compare-locales` checks the plural forms of each
   locale against the CLDR rules of that locale.
7. **No selection by gender.** ADR 181 decision 11 stays. The check fails on
   a `select` whose argument the registry of arguments does not declare as
   a machine-made value, for example the tool of a unit. A `selectordinal`
   follows the rule of decision 6.

### Formats

8. **Words from the language, numbers from the formatting locale.**
   FormatJS formats a message with one locale. So one wrapper of FormatJS
   formats each number, date, time and duration first, with `Intl`, the
   formatting locale and the overrides of the person (ADR 181 decision 5).
   It gives the results to the message as plain arguments. The message
   formats only words, with the language. A `plural` gets its count as a
   separate number argument, and selects the form with the plural rules of
   the language. A message never uses `#` in a plural, because `#` formats
   the count with the language and not with the formatting locale. The
   check fails on `#`. A message never formats a date with its own
   pattern. For example, the wrapper formats `{at}` with its named format
   `short`, and the message holds only `{at}`.
9. **Units and relative times.** A unit of measure, for example bytes or
   seconds, goes through `Intl.NumberFormat` with `style: "unit"`. A
   relative time goes through `Intl.RelativeTimeFormat`. Each time that the
   console shows as relative also has its absolute value, in a `<time>`
   element with a `datetime` attribute and in the accessible name. The
   absolute value names its time zone.

   Benchmark: GitLab shows a relative time with the absolute time in a
   tooltip.

### Where the checks run

10. **The console check.** The console's check runs decisions 2 to 7 on
    every message file, and the status check of ADR 232 decision 3. A
    failure of one of them fails the check. For decision 4, the check fails
    on an English message that holds "OpenDustpan" or "Dusty" as text,
    except legal text. The translation step never commits a translation
    that fails the check: the message stays missing, the console shows its
    English text, and the check reports it (ADR 232 decision 9). A missing
    or stale translation is a report, not a failure (ADR 232 decision
    15).
11. **The server check.** The server's message files (ADR 234) get the same
    checks in a Go test. The shared term list is one file. The console
    repository holds it, and the server test reads a copy that a script
    updates. The copy names the commit of the console that it comes from
    and the SHA-256 of its content. The test fails when the content of the
    copy does not match that SHA-256.

## Consequences

- `OpenDustpan/console` gets these changes. This ADR does not change that
  repository.
  - The shared term list and a term list for each language, with the forms
    of decision 2.
  - The message check of decision 10, as a script that runs in the console
    check and in CI.
  - The format wrapper of decision 8.
  - The console's `CLAUDE.md` adds decisions 3, 4 and 8.
- The server gets the copy of the shared term list and the Go test of
  decision 11 with its message files.
- The parser of FormatJS is part of the approved dependency. The check
  needs no other dependency.
