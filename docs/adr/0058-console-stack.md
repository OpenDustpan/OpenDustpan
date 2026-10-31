# 58. The console uses React, Tailwind CSS and shadcn/ui, designed in Claude Design

Status: accepted · 2026-10-13

Changed by:

- ADR 180 extends decisions 5 and 9: themes, color schemes and contrast rules
  on top of the tokens.
- ADR 183 extends decision 9: the WCAG 2.2 AA target, the axe gate and manual
  checks.

## Context

ADR 5 and ADR 8 put the console in its own repository and let it talk to the
server only through the API. They do not name a frontend stack.

The console repository (`OpenDustpan/console`) has React 19, TypeScript and
Vite. It also has a hand-written design system in `src/design-system/`, with
CSS variables in `tokens.css` made from `design/tokens.json`. The owner
approved those components when console pull requests #4 to #9 added them, in
the same session where the owner asked to install the shadcn/ui MCP server.
So the owner implied the stack but nobody recorded it, and agents filled the gap. No
ADR or RFC records it.

`npm run claude-design` (`scripts/build-claude-design.mjs`) connects the
code and Claude Design. It bundles the components into one script that uses
the React of the Claude Design page, adds the CSS, the types, the tokens, and
a README and a live preview of each component, and an agent publishes the
result to the Claude Design system artifact. Claude Design builds screens
from those real components. Claude Design runs React 18, so the console's
components must also run on React 18.

On 2026-10-13 the project lead asked the owner about a QR code library for
TOTP enrollment (ADR 46). The owner answered: "The approved frontend stack
is Claude Design -> TailWind + shadcn", and added: "And React, and
https://impeccable.style/". A design review on this pull request found a
conflict with the React 18 rule and a gap during the move. The decisions
below close both.

## Decision

1. **Design first.** The owner or an agent designs each new or changed
   screen in Claude Design, from the published design system. The screen
   design is the specification for the code.
2. **Stack.** The console is a React application. It uses Tailwind CSS for
   styles and shadcn/ui for components. A component that shadcn/ui has is
   not written again by hand.
3. **React 18 copies.** shadcn/ui copies component source into the
   repository, so the copies are our code. Current shadcn/ui source passes
   `ref` as a plain prop, which only React 19 supports. Our copies use
   `forwardRef` instead, so the same component code runs in the console
   (React 19) and in Claude Design (React 18).
4. **One pipeline.** `npm run claude-design` stays the only way from code to
   Claude Design. It bundles the shadcn/ui-based components as it bundles
   the current ones, and `bundle.css` holds the compiled Tailwind CSS. Each
   exported component keeps `design/components/<Name>/README.md` and
   `preview.html`. The build fails without them, as it does today.
5. **Tokens stay the source.** `design/tokens.json` generates the Tailwind
   theme and the shadcn/ui CSS variables. The default Tailwind palette is
   not available. Colors, spacing, radii and type come from tokens only,
   never from arbitrary values such as `bg-[#3b82f6]` or `p-[13px]`.
6. **The move.** New components are shadcn/ui-based from now on, with their
   `design/components/<Name>/` entry, so Claude Design sees them at once.
   The current hand-written components stay until an RFC moves them. The
   RFC names the order, and what happens to each current component.
7. **Design quality.** Frontend work follows the `impeccable` agent skill,
   at the version that `skills-lock.json` pins (ADR 7), not the live
   website.
8. **Proof of design.** A pull request that adds or changes a screen links
   its Claude Design artifact. A pull request that only puts existing
   components together needs no design. A contributor who cannot use Claude
   Design asks a maintainer for the design.
9. **Rules that stay.** These console rules stay as they are:
   accessibility (jsx-a11y strict, visible labels and focus), 4.5:1 text
   contrast in both themes, the logo and brand rules in `design/README.md`,
   color never carries meaning alone, monospace for machine-made text, and
   runtime validation of every API response.
10. **Scope.** This ADR covers application user interfaces: the console
    and any later web application of the project. A documentation site may
    use its own static site generator and theme.

## Consequences

- The console's `CLAUDE.md` changes to match this ADR. An issue in
  `OpenDustpan/console` tracks that change and the RFC of decision 6.
- `docs/design-workflow.md` in the console repository explains the design
  tooling step by step.
- Tailwind CSS, shadcn/ui and their Radix dependencies follow the
  dependency rules: versions at least two weeks old, pinned exactly.
  Reviewers check each copied component as they check our own code.
- A lint rule that fails on arbitrary Tailwind values may need a new dev
  dependency. That needs the owner's approval first.
- A QR code for TOTP enrollment still needs a small QR library, because
  shadcn/ui has no QR code component.
- The prototypes in console pull request #29 are design input only.
