## Context

A developer works in their own language and locale. The console needs readable text, dates, and numbers before translation can help.

This change records the language and locale scope for RFC 11. Console core views (RFC 52) do not exist yet, so there is nothing to translate. It changes no server code, no route, and no schema now.

## Decisions

1. **No code now.** Language and locale support ships after console core views exist. No locale plumbing lands before there are views that use it.
2. **Readable base text first, translation later.** Dates and numbers render in the active locale with a documented fallback when the locale is unknown. Translation of strings follows in a later step on the same fallback.
3. **One fallback rule.** An unsupported language or locale falls back to the default with no partial rendering: each view renders fully in one language. The docs page for supported languages names the default and the fallback.
4. **Themes stay out.** Themes and white-labeling (RFC 9) refine on their own path and share only the console views.

## Structure

No new structure now. The bug class is a view that mixes two languages when a string is missing; one fallback rule with full-view rendering is the structure that removes it. When console views exist, locale handling is an unexported formatter built only through its constructor, which refuses an unknown locale instead of rendering half a view.

No custom analyzer and no new tool. A translation dependency needs the owner.

## Risks

- A developer reads the console in an unsupported locale and sees the default language. The fallback is documented, so the behavior surprises no one.
- A date or number renders in a form the developer misreads. Locale-aware formatting with one fallback keeps each view consistent.
