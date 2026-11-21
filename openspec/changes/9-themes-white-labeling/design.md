## Context

Operators and teams want the console to carry their own theme and branding. Console core views (RFC 52) do not exist yet, so there is nothing to theme.

This change is a coarse placeholder that waits on console core views and a later RFC refinement. It changes no server code, no route, and no schema now.

## Decisions

1. **No code now.** Theme and white-labeling support ships after console core views exist. No theme plumbing lands before there are views that use it.
2. **One theme contract later.** The later refinement names the preset, the branding values, and the fallback to the default theme when a value is missing. A view never renders half-themed.
3. **Languages stay out.** Languages and locales (RFC 11) refine on their own path and share only the console views.

## Structure

No new structure now. The bug class is a view that mixes the default theme with a partial preset; one theme contract with full-view fallback is the structure that removes it. When console views exist, a theme is an unexported preset built only through its constructor, which fills each missing value from the default.

No custom analyzer and no new tool.

## Risks

- A team sets a partial theme and the console looks broken. Full-view fallback keeps each view in one theme.
- A white-label preset hides the product identity the support path needs. The refinement keeps product and version visible in each preset.
