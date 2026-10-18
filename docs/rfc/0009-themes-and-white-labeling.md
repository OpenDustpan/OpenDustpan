---
rfc: 9
title: Themes and white-labeling
status: draft
created: 2026-10-10
---

# RFC 9. Themes and white-labeling

Status: draft.

## Problem

Teams want the console to look like their own internal platform: their name
for it, their logo, their fonts and their colors. Today the product name,
the lockup and the design tokens are fixed at build time.

## Proposal

- **A theme is runtime config.** The console already reads `/config.json`
  when it starts (ADR 5). A theme adds to it: a product name, logo files
  (light and dark), font files, and values for the color tokens. The same
  build serves every theme. An operator never rebuilds the console.
- **Tokens first.** A theme sets values for the existing tokens (`pan`,
  `surface`, `ink` and the others). This is the supported way, and the one the
  docs show.
- **Custom CSS as an escape hatch.** A theme can also add its own stylesheet.
  The docs say clearly that the design system's guarantees (contrast, focus,
  meaning that never rests on color alone) end where custom CSS starts.
- **Contrast is checked, and the operator decides.** The console checks each
  themed text token against its surfaces when it loads the theme. A theme
  that breaks 4.5:1 still loads. The console warns the operator, with the
  token, the surface and the ratio, on the theme settings and once in the
  server log at startup. People who are not operators never see the warning.
- **The product name is everywhere.** The console, server error messages,
  log lines and CLI output use the configured name. Text that names
  OpenDustpan for legal reasons (the license, the source link) stays.
- **Fonts stay self-hosted.** A theme ships its own font files. The console
  never loads fonts from a third party, as the brand book already requires.

## Posture

OpenDustpan is open source, and operators own their deployment. We give them
every choice, including choices we think are bad. A check that finds a
problem warns, says exactly what is wrong, and does not block. A check blocks
only when going on would lose or corrupt data, as in a restore with the wrong
key (RFC 8).

- The "not affiliated" rule applies to themes that we ship. What an operator
  calls the product in its own network is its business.
- The diff colors (`diff-create`, `diff-update`, `diff-delete`) are themable
  too. The glyphs stay, so a plan still reads correctly when the colors do
  not.

## Options

1. **Runtime themes** (above).
2. **Fork and rebuild.** This is possible today and costs every team a fork.
3. **Tokens only, no custom CSS, and reject themes that fail the contrast
   check.** This keeps every guarantee of the design system, and takes
   choices away from operators who own their deployment. Rejected, by the
   posture above.

## Open questions

- Can a theme hide or rename navigation items, or only change the look?
- Is the theme also available to the server's own pages (error pages, the
  sign-in page)?
- Is there one theme for each server, or one for each namespace?
- Does "warn, do not block" become a project-wide principle in its own ADR,
  so that other features follow it too?

## Outcome

Accepted in part as [ADR 212](../adr/0212-planner-role-smtp-settings-fresh-mfa-proxies-restore-audit-and-branding.md)
decision 6 on 2026-10-18: the branding preset is an installation setting,
changed through the settings resource, and the public answer of the
sign-in page holds it. The rest of the white-label mechanism stays open.

Accepted in part as [ADR 234](../adr/0234-operator-branding-the-product-name-logo-colors-and-agent-name.md)
on 2026-10-18: an operator can change the product name, the logo, the
accent colors and the agent's name. Custom CSS, custom fonts and the product
name in server errors, logs and CLI output are not taken. A theme for each
namespace and the server's own error pages stay open.
