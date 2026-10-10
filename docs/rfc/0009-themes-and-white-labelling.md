---
rfc: 9
title: Themes and white-labelling
status: draft
created: 2026-10-10
---

# RFC 9. Themes and white-labelling

Status: draft.

## Problem

Teams want the console to look like their own internal platform: their name
for it, their logo, their fonts and their colours. Today the product name,
the lockup and the design tokens are fixed at build time.

## Proposal

- **A theme is runtime config.** The console already reads `/config.json`
  when it starts (ADR 5). A theme adds to it: a product name, logo files
  (light and dark), font files, and values for the colour tokens. The same
  build serves every theme. An operator never rebuilds the console.
- **Tokens are the only way in.** A theme sets values for the existing
  tokens (`pan`, `surface`, `ink` and the others), never raw CSS. The
  console's rules then stay true: no hard-coded colours, meaning never rests
  on colour alone.
- **Contrast is checked.** The console checks each themed text token against
  its surfaces when it loads the theme. A theme that breaks 4.5:1 is
  rejected, with a message that names the token and the ratio. The operator
  can see the result before they save it.
- **The product name is everywhere.** The console, server error messages,
  log lines and CLI output use the configured name. Text that names
  OpenDustpan for legal reasons (the licence, the source link) stays.
- **Fonts stay self-hosted.** A theme ships its own font files. The console
  never loads fonts from a third party, as the brand book already requires.

## Limits

- A theme cannot use another vendor's name, logo or colours as the product's
  own. The "not affiliated" rule applies to themes that we ship, not to what
  an operator does in its own network. The docs say so.
- The diff colours (`diff-create`, `diff-update`, `diff-delete`) are themable,
  but each must still work with its glyph, and the contrast check covers
  them.

## Options

1. **Runtime themes** (above).
2. **Fork and rebuild.** This is possible today and costs every team a fork.
3. **Free CSS overrides.** This is flexible, and it breaks contrast,
   accessibility and every guarantee of the design system.

## Open questions

- Can a theme hide or rename navigation items, or only change the look?
- Is the theme also available to the server's own pages (error pages, the
  sign-in page)?
- Is there one theme for each server, or one for each namespace?

## Outcome

None yet.
