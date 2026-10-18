# 180. Console themes, color schemes and the preferences of a person

Status: accepted · 2026-10-18 · adds to ADR 58

## Context

The console has one look today: the design system of the console
repository, with a light and a dark palette that follow the operating
system. On 18 October 2026 the project owner decided to add themes. The
project lead gave the design work to an agent, and gave the owner's
decisions with it. This ADR records the decisions of the owner and the
design decisions that follow from them. Each decision of the owner has the
label "(owner)".

Facts:

- ADR 58 makes `design/tokens.json` the source of the colors, the type and
  the Tailwind CSS theme of the console. `npm run tokens` generates the CSS
  from it.
- ADR 17 says that every feature has a REST API, that every endpoint
  declares its actions, and that every change passes a policy check.
- The server has no API for the preferences of a person. The tool-neutral
  API already has routes on the caller, for example
  `/api/server/caller/tokens` with the action `token:manage-own`, which
  every person holds with no grant.
- RFC 9 (themes and white-labeling) is a draft. It proposes runtime branding
  for operators. This ADR does not accept RFC 9.
- The Gogh collection (`Gogh-Co/Gogh`) holds more than 1,200 terminal color
  schemes in one format: 16 ANSI colors, a background, a foreground and a
  cursor. Gogh is dual-licensed MIT or Apache-2.0. Each scheme has its own
  upstream project and license.

The console pull request "Design themes and color schemes" holds the token
schema, the mapping, the types and their tests. Its file
`design/themes/README.md` holds the full mapping table and the license
table.

## Decision

### Themes

1. **Two themes (owner).** The default theme is quiet and serious. It uses
   IBM Plex Sans for the interface and IBM Plex Mono for machine-made
   values. Its surfaces are near-black or white. One yellow accent,
   `#f5c400`, marks primary actions and focus. Every status has a glyph and
   a word. It follows the color scheme of the operating system, light or
   dark. The opt-in theme is "Terminal". It uses IBM Plex Mono for all
   text, it is dense and keyboard first, and buttons show their shortcut as
   `label [key]`. "Terminal" is the only name of this theme. No other
   product's name names it.
2. **Keyboard shortcuts (owner).** The selection of the Terminal theme
   turns keyboard shortcuts on, also single-key shortcuts. The person can
   turn shortcut hints and single-key shortcuts off in settings, for
   example for a screen reader or speech input.
3. **Three independent settings.** A theme sets the font of the interface
   and the default of the keyboard settings. A color mode is `system`,
   `light` or `dark`. A color scheme sets the colors. The console selects
   them with the attributes `data-theme`, `data-mode` and `data-scheme` on
   the root element. The keyboard settings are `theme`, `on` or `off`.
   `theme` takes the default of the theme: on in Terminal, off in the
   default theme. So the selection of Terminal turns shortcuts on, and a
   setting of `off` stays off in every theme.
4. **No work only for phones (owner).** The themes get no design for small
   screens of their own. No meaning in the default theme depends on hover.

### Color schemes

5. **Color schemes from Gogh (owner).** The Terminal theme offers color
   schemes from the Gogh collection. The owner asked for at least these:
   Solarized (dark and light), Rose Pine, Gruvbox, Dracula, Catppuccin,
   Tokyo Night, Nord, Apprentice, Tomorrow Night, One Dark, Kanagawa and
   Monokai.
6. **Schemes for Terminal only.** The default theme keeps its own
   hand-tuned light and dark palettes. A computed scheme gets its accent
   from its own yellow, so it loses the brand look of the default theme:
   the yellow fill with an ink edge on light surfaces. The mapping does not
   make schemes free for the default theme. The built-in scheme
   `opendustpan` is the palette of the default theme. It is the first
   choice in Terminal.
7. **One mapping from a palette to the tokens.** A scheme sets 16 color
   tokens: `surface`, `surface-raised`, `line`, `line-strong`, `ink`,
   `ink-muted`, `pan`, `pan-soft`, `pan-edge`, `pan-strong`, `on-pan`,
   `diff-create`, `diff-update`, `diff-delete`, `diff-replace` and
   `focus-ring`, and the dialog backdrop. The other color tokens are
   aliases. `status-success`, `status-warning` and `status-danger` are new
   aliases of the three diff colors. `diff-replace` is new. The default
   theme gives it the `diff-update` color, and a scheme gives it its
   magenta. The surface comes from the background, the text from the
   foreground, the accent from the yellow, and add, change, delete and
   replace from green, yellow, red and magenta. For each hue, the mapping
   takes the normal or the bright ANSI slot. It takes a slot that reads as
   a color, and of two such slots the one with more contrast. One
   TypeScript file holds the mapping. The token generator and the unit
   tests run the same file.
8. **Contrast is a rule for every shipped scheme.** Text and muted text
   need 4.5:1 on every surface that holds text. Focus rings and other
   marks that are not text need 3:1. Text gets 7:1, so that muted text at
   4.5:1 still reads as a second level. A color that misses its minimum
   moves toward white (on a dark scheme) or black (on a light scheme) in
   steps of 2% until it passes. This computed adjustment is the default. A
   scheme can set a token by hand when the computed value passes but looks
   wrong. The contrast check applies to that value too. The generator fails
   on a scheme that breaks a rule, and a unit test checks every shipped
   scheme and the default palettes.
9. **Meaning never rests on color alone.** Diffs keep the glyphs `+`, `~`,
   `-` and `+-` in every theme and scheme. Every status keeps a shape and a
   word.
10. **Licenses.** The console takes a scheme only when the license of Gogh
    and of the scheme's upstream project are clear. A table in the console
    repository records the license and the source of each scheme, and a
    license notice ships in the build of the console. A scheme with a
    license that is not clear stays out, and the table lists it. The name
    of a scheme is its label, with credit to its authors. It is never our
    name. The first set has 15 schemes from 10 projects:

    | Schemes | Upstream | License |
    | --- | --- | --- |
    | Solarized Dark, Solarized Light | `altercation/solarized` | MIT |
    | Rosé Pine, Rosé Pine Dawn | `rose-pine/rose-pine-theme` | MIT |
    | Gruvbox Dark, Gruvbox Light | `morhetz/gruvbox` | MIT, stated in the README |
    | Dracula | `dracula/dracula-theme` | MIT |
    | Catppuccin Mocha, Catppuccin Latte | `catppuccin/catppuccin` | MIT |
    | Tokyo Night, Tokyo Night Day | `folke/tokyonight.nvim` | Apache-2.0 |
    | Apprentice | `romainl/Apprentice` | MIT |
    | Tomorrow Night | `chriskempson/tomorrow-theme` | MIT |
    | One Dark | `atom/one-dark-syntax` | MIT |
    | Kanagawa | `rebelot/kanagawa.nvim` | MIT |

    Two schemes of the owner's list stay out for now. Nord lists two
    licenses, Apache-2.0 and CC BY-SA 4.0, and does not say which part
    each covers. For Monokai, we found no license for the original scheme,
    and Monokai Pro is a commercial product.

### Branding

11. **A preset is not a user scheme (owner).** The palette of the hosted
    distribution (warm charcoal, with a thin yellow rule at the top) is a
    preset of the white-label mechanism. An operator sets a preset for the
    installation. A person never selects a preset, and the preferences of a
    person never hold one. The theme and the scheme of a person apply on
    top of the preset. RFC 9 stays open for the rest of the mechanism.

### The preferences of a person

12. **Saved to the account, on every device.** The server keeps the
    preferences of each person. The console reads them after the sign-in
    and keeps a copy in the browser's local storage. A small script in the
    page reads the copy before the first paint, so the page does not show
    the wrong theme first. The answer of the server replaces the copy.
13. **The API.** Two routes on the caller, in the tool-neutral API:

    | Method and path | Action | Object |
    | --- | --- | --- |
    | `GET /api/server/caller/preferences` | `caller:read` | the caller |
    | `PATCH /api/server/caller/preferences` | `caller:manage-preferences` | the caller |

    `GET` answers 200 with the full document, with the defaults for the
    fields that the person never set. `PATCH` takes a JSON merge patch
    (`application/merge-patch+json`). A field that the patch leaves out
    keeps its value, and `null` sets a field back to its default. The
    answer is 200 with the full document. A request from a service
    principal answers 403, because preferences belong to a person. The
    document:

    ```json
    {
      "theme": "terminal",
      "colorMode": "system",
      "terminalSchemes": { "light": "solarized-light", "dark": "tokyo-night" },
      "shortcutHints": "theme",
      "singleKeyShortcuts": "theme",
      "updatedAt": 1760745600000
    }
    ```

    | Field | Values | Default |
    | --- | --- | --- |
    | `theme` | `default`, `terminal` | `default` |
    | `colorMode` | `system`, `light`, `dark` | `system` |
    | `terminalSchemes.light`, `terminalSchemes.dark` | a scheme ID: 1 to 64 characters of `a-z`, `0-9` and `-` | `opendustpan` |
    | `shortcutHints`, `singleKeyShortcuts` | `theme`, `on`, `off` | `theme` |
    | `updatedAt` | Unix milliseconds of the last change, or `null` | `null` |

    The server checks the shape of a scheme ID, not a list of schemes. The
    console owns the list, so a new scheme needs no server release. A
    console that gets an ID that it does not know uses `opendustpan` and
    says so in settings. A field that the server does not know answers 400.
    A body over 4 KiB answers 413.
14. **The action.** `caller:manage-preferences` allows a person to change
    its own preferences. Its object is the caller. Every authenticated
    person holds it with no grant, as with `token:manage-own`. The change
    passes the policy check of ADR 17, and with no policy the check allows
    it.
15. **Audit and storage.** A change writes one audit entry,
    `preferences-changed`, with the names of the changed fields. The values
    are not secret, but they tell nothing to an auditor. The store keeps one
    row for each person, with the document and the time of the change.
    Backups hold the rows. A row goes away with its person. Preferences
    belong to the installation, not to a namespace, so a change needs no
    fence.

## Consequences

- The console pull request holds the token schema, the mapping, the
  types, the tests and the license notice. It changes no screen. The theme
  switcher, the inline script and the Terminal type scale come in later
  console pull requests, each with its design in Claude Design.
- The integrator adds `caller:manage-preferences` to the `identity`
  package and to the self actions, and the two routes to the route test.
  The routes land with their e2e step. The console uses the routes only
  after a server release has them.
- The root attribute for light and dark in the console changes from
  `data-theme` to `data-mode`. Before 1.0, this change needs no
  deprecation (ADR 73).
- Each new scheme needs its license checked and recorded. A scheme from
  outside Gogh needs a new decision.
- The generator of the console imports TypeScript, so it needs Node 22.18
  or later.

## Open questions

- Nord: can the owner ask the Nord project which license covers the
  palette, or accept Apache-2.0 for it? Until then, Nord stays out.
- Monokai: is a scheme in the Monokai family with a clear license
  acceptable, for example "Monokai Remastered" or "Monokai Soda" in Gogh,
  each with its upstream license checked?
- The owner calls the preset of decision 11 "OpenDustpan Cloud". The
  glossary names the service "hosted distribution" and lists "cloud
  edition" under _Avoid_. Which name does the preset use?
