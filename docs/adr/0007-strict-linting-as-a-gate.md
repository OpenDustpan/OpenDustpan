# 7. Strict linting is the first gate

Status: accepted · 2026-10-08

## Context

Much of the code is written with AI agents. Linters are cheap, fast and do not
get tired; they catch most low-effort code before a human reviews it.

## Decision

- Go: golangci-lint v2 with `default: all`. A linter is disabled only with a
  written reason in `.golangci.yml` (noise, duplicate of a stricter check, or
  irrelevant). Notably disabled: `noinlineerr` (inline `if err :=` is
  idiomatic), whitespace-style linters, `varnamelen`. `exhaustruct` is on from
  day one. Formatters: gofumpt (extra rules), gci, golines at 120 columns.
  govet `shadow` runs in non-strict mode because strict mode bans the inline
  error idiom.
- TypeScript: the strictest compiler flags, typescript-eslint
  `strictTypeChecked` and `stylisticTypeChecked`, React hooks, jsx-a11y
  (strict) and unicorn, with zero warnings allowed. Prettier formats.
- Suppressions name the rule and give a reason; unused suppressions are errors.
- Dependencies are pinned exactly, and new versions are adopted only once they
  are at least two weeks old.

## Consequences

- Some idioms need small rewrites to satisfy the linters; that is the point.
- If a rule proves to be noise in practice, change the config in a reviewed
  commit with the reason, never with scattered suppressions.
