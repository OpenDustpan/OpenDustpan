# 58. The console uses React, Tailwind CSS and shadcn/ui, designed in Claude Design

Status: accepted · 2026-10-13

## Context

ADR 5 and ADR 8 put the console in its own repository and let it talk to the
server only through the API. They do not name a frontend stack.

The console repository (`OpenDustpan/console`) has React 19, TypeScript and
Vite. It also has its own design system in `src/design-system/`: hand-written
components and CSS variables in `tokens.css`, made from
`design/tokens.json`. Its `CLAUDE.md` tells agents to use only that design
system, and to keep components that Claude Design can import. No ADR or RFC
records a decision for that design system.

On 2026-10-13 the project lead asked the project owner about a QR code
library for TOTP enrollment (ADR 46). The owner answered: "The approved
frontend stack is Claude Design -> TailWind + shadcn". The owner added:
"And React, and https://impeccable.style/". The owner also said that an
agent did not follow the approved stack.

## Decision

1. **Design.** Designers make each screen in Claude Design first. Code follows
   the design.
2. **Stack.** The console is a React application. It uses Tailwind CSS for
   styles and shadcn/ui for components. A component that shadcn/ui has is
   not written again by hand.
3. **Design quality.** Frontend work follows the Impeccable guidelines
   (`impeccable.style`). The `impeccable` agent skill applies them.
4. **Scope.** This ADR applies to the console and to any other web
   frontend of the project.
5. **The hand-written design system stops growing.** No new component goes
   into `src/design-system/`. The move from the current design system to
   Tailwind CSS and shadcn/ui needs an RFC. The RFC must name what
   happens to `design/tokens.json`, the brand rules in `design/README.md`
   (logo, colors, contrast), and the components that Claude Design imports
   today.
6. **Rules that stay.** The console rules on accessibility, monospace for
   machine-made text, runtime validation of API responses, and color that
   never carries meaning alone stay as they are.

## Consequences

- The console's `CLAUDE.md` changes: the rule "use the design system for
  every UI element" becomes "use shadcn/ui components, styled with Tailwind
  CSS, as Claude Design specifies".
- Adding Tailwind CSS and shadcn/ui follows the dependency rules: versions
  at least two weeks old, pinned exactly. shadcn/ui copies component source
  into the repository, so reviewers check each copied component as they
  check our own code.
- A QR code for TOTP enrollment still needs a small QR library, because
  shadcn/ui has an input for one-time codes but no QR code component.
- The prototypes in console pull request #29 are design input only. They
  are not the base for production screens.
