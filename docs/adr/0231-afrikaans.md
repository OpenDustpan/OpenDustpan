# 231. Afrikaans

Status: accepted · 2026-10-18. Supersedes in part ADR 181 decision 1. Extends ADR 181 decisions 16 and 26

## Context

ADR 181 gives the console 32 languages. On 18 October 2026 the project
owner added Afrikaans. The owner reviews English, Dutch and Afrikaans as a
native reviewer (ADR 232). This ADR records this decision of the owner with
the label "(owner)".

Facts:

- Afrikaans (`af`) uses the Latin script and writes from left to right.
- Afrikaans has the plural categories `one` and `other` in CLDR.

## Decision

1. **Afrikaans (owner).** The set of ADR 181 decision 1 gets Afrikaans
   (`af`), and then has 33 languages. The 24 official languages of the
   European Union stay as they are. The other languages are `af`, `ar`,
   `he`, `ja`, `ko`, `pt-BR`, `ru`, `uk` and `zh-Hans`.
2. **Font.** Afrikaans uses IBM Plex Sans, in the Latin row of the font
   table of ADR 181 decision 16.
3. **The switcher.** Settings and the user menu list "Afrikaans", with
   `lang="af"` and `dir="ltr"` (ADR 181 decision 26).

## Consequences

- `GLOSSARY.md` names 33 languages for "Language".
- `OpenDustpan/console` gets the `af` message file, with its term list
  (ADR 233). This ADR does not change that repository.
- The server needs no change for the language list, because it checks only
  the shape of a tag (ADR 181 decision 8).
