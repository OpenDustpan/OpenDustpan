# 235. Operator branding: the product name, the logo, the colors and the agent's name

Status: accepted · 2026-10-18. Extends ADR 180 decision 11. Supersedes in part ADR 212 decision 6. Accepts part of RFC 9. Changed by [ADR 288](0288-the-sign-in-page-looks-the-same-on-every-installation.md)

Changed by:

- ADR 288 changes decisions 9 and 10: no logo and no accent before sign-in.

## Context

ADR 212 decision 6 makes the branding preset an installation setting. RFC
9 proposes more: a product name, logo files, font files, color tokens and
custom CSS, and a product name in server errors, logs and CLI output. On 18
October 2026 the project owner decided what an operator can change, and
where the product name reaches. The owner also decided that the product
name reaches chat messages, which RFC 78 asked. This ADR records these decisions of the
owner with the label "(owner)", and the design decisions that follow from
them. The project manager decided the design decisions under the owner's
decision rule. Each such decision has a "Benchmark:" line.

Facts:

- ADR 180 decision 8 makes contrast a rule for every color scheme. A color
  that misses its minimum moves toward white or black until it passes.
- ADR 233 decision 4 makes the product name and the agent's name arguments
  of each message.
- An SVG file can hold a script.

## Decision

### What an operator can change

1. **Four things (owner).** An operator can change the application name,
   the logo, the colors and the name of Dusty. Nothing else changes. There
   is no custom CSS, no custom script, no custom font and no change to the
   navigation. RFC 9's custom CSS is not taken.

   Benchmark: the appearance settings of GitLab take a title, a logo and a
   description, and no stylesheet. Atlassian's cloud products let an
   administrator set a logo, colors and a site title, and no stylesheet.
2. **The settings.** Each one is an installation setting in the settings
   resource of ADR 212 decision 7, with the action
   `installation:manage-settings`. Each change writes an audit entry.

   | Setting | Values | Default |
   | --- | --- | --- |
   | `branding.productName` | 1 to 64 characters | `OpenDustpan` |
   | `branding.agentName` | 1 to 32 characters | `Dusty` |
   | `branding.logo.light`, `branding.logo.dark` | the SHA-256 and the type of a PNG or WebP file of at most 256 KiB, or `null` | `null`: the OpenDustpan logo |
   | `branding.accent.light`, `branding.accent.dark` | a color as `#rrggbb`, or `null` | `null`: the accent of the preset |

   A name refuses control characters and the Unicode direction controls
   (U+200E, U+200F, U+202A to U+202E, U+2066 to U+2069), so that a name
   cannot change the order of the text around it. A logo refuses SVG,
   because an SVG file can hold a script. The logo route of decision 10
   sets a logo file. The settings resource shows only its SHA-256 and its
   type, or `null`.
3. **One name in every language.** The product name and the agent's name
   are the same in every language. Messages never translate them. A name
   in a right-to-left script renders with `dir="auto"` (ADR 231 decision 9).

### Where the names reach

4. **The product name reaches the console, emails and chat messages
   (owner).** The console and the emails and chat messages of ADR 234 show
   the product name that the operator set. Server errors, log lines, CLI
   output, the tool-compatible APIs and audit entries never show it. They
   say "OpenDustpan" where they name the server.
5. **The agent's name reaches every text that the agent writes to a
   person.** The console, emails and chat messages call the agent by
   `branding.agentName`. The audit log, logs, the MCP endpoint and the API
   keep the fixed agent ID `dusty`, so that a search in the logs finds the
   agent whatever its name.
6. **Legal text keeps the project name.** The license notice, the source
   link and the "not affiliated" text keep "OpenDustpan". An operator
   cannot change them.

### Colors

7. **Only the accent changes, through the contrast rule.** "The colors"
   of decision 1 are the accent of each color mode, a design decision. The
   surfaces, the text and the diff colors stay those of the preset and the
   scheme, so that no operator color can break the contrast of the text or
   the meaning of a diff. The console maps the
   accent of each color mode to the accent tokens of ADR 180 decision 7:
   `pan`, `pan-soft`, `pan-edge`, `pan-strong` and `on-pan`. The rule of ADR
   180 decision 8 applies. A color that misses its minimum moves toward
   white or black until it passes. The settings page shows the value that
   the operator set, the value that the console uses, and the ratio of
   each. The server checks only the shape `#rrggbb`.

   Benchmark: GitLab's appearance settings and Atlassian's cloud branding
   take a brand or accent color for the header and links, not a full
   palette.
8. **The person's scheme applies on top.** ADR 180 decision 11 stays. A
   person's theme and color scheme apply on top of the operator's accent.
   The Terminal theme takes its accent from its scheme.

### Delivery

9. **The public sign-in answer holds the branding.** The answer of ADR 212
   decision 6 adds `productName`, `agentName`, `accent` and the paths of
   the logos, next to `brandingPreset` and `translationFeedbackLinks` (ADR 232 decision 6):

   ```json
   {
     "providers": [{ "id": "corp-sso", "displayName": "Corp SSO" }],
     "brandingPreset": "default",
     "translationFeedbackLinks": true,
     "productName": "Platform Portal",
     "agentName": "Sparky",
     "accent": { "light": "#0b5fff", "dark": "#7aa7ff" },
     "logo": { "light": "/api/dustpan/v1/branding/logo/light", "dark": null }
   }
   ```

   A logo of `null` means the OpenDustpan logo. This replaces the sentence
   of ADR 212 decision 6 that the answer holds the preset only. The answer
   holds no secret and no setting other than these.
10. **The logo route.** `GET /api/dustpan/v1/branding/logo/{mode}` answers
    the file with its stored type, `X-Content-Type-Options: nosniff`,
    `Content-Security-Policy: default-src 'none'` and
    `Cache-Control: public, max-age=300`. The route is on the public list of
    the route test, as the sign-in answer is. `PUT` on the same path takes
    a new file, with `installation:manage-settings`. The server checks the
    first bytes of the file against its type. `DELETE` on the same path, or
    a `PATCH` of the settings resource that sets the key to `null`, removes
    the file. When the static layer sets the key (ADR 212 decision 7), a
    `PUT` or a `DELETE` answers 409 with the key and its source.
11. **Emails show the name, not the logo.** An email shows the product name
    as text and loads no image. So an email client that blocks images loses
    nothing, and an email asks for nothing from the server when a person
    opens it.

## Consequences

- RFC 9 stays a draft for the rest of its questions: a theme for each
  namespace, and the server's own error pages. Its "Outcome" section names
  this ADR.
- `GLOSSARY.md` adds "Product name" and "Agent name".
- The settings resource gets the settings of decision 2. The sign-in
  answer gets the fields of decision 9. The logo route lands with its e2e
  step and its row in the route test.
- `OpenDustpan/console` gets these changes. This ADR does not change that
  repository.
  - The console reads the branding from the sign-in answer and gives
    `productName` and `agentName` to each message.
  - The branding settings page, with the contrast values of decision 7.
- Backups hold the branding settings and the logo files.
