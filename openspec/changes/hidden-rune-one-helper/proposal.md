---
version: 1
---

Issue: #3164.

Short form: tech debt. Owner lane: D2.

Waits for: #2584

## Problem

**Persona: contributor.** A contributor who writes a text check must know
that a control-character check alone misses format characters (for example
U+202E) and the line and paragraph separators (U+2028, U+2029). #2584 adds
the one check, `ident.IsHiddenRune`, and a scanner rule that looks for new
copies. The rule matches only the forms it lists, so a copy written as
`unicode.IsControl(r) || r == '\n'` passes, and such copies are still common.
A wider regex would only find more forms; a structure that leaves one call
removes the bug class.

## Evidence

Production call sites that build their own check from `unicode.IsControl`
or from a Unicode category table, on the head of #2584, outside
`internal/ident` and tests
(`git grep -n -E 'unicode\.(IsControl|Is\(unicode\.(Cc|Cf|Zl|Zp))|unicode\.In\(.*unicode\.(Cc|Cf)' -- '*.go' ':!*_test.go' ':!internal/ident/*'`):

- refuse text: `internal/api/pulumi/stack_page.go:117`,
  `internal/ldapauth/config.go:411`, `internal/signinsvc/password.go:241`,
  `internal/signinsvc/service.go:802`, `internal/unitimport/unitimport.go:215`,
  `internal/unitimport/validate.go:27`, `internal/unmanaged/finding.go:301`,
  `pkg/forge/forge.go:1195`, `pkg/forge/github/review.go:704`,
  `pkg/gate/statechange.go:132`, `pkg/inventory/query.go:59`,
  `pkg/localuser/blocklist/blocklist.go:156`,
  `pkg/objectcontext/objectcontext.go:470`, `pkg/principal/groupmapping.go:95`,
  `pkg/dustpan/web.go:59`, `internal/issuersvc/sign.go:168`,
  `internal/errcode/answer.go:202`, `pkg/blob/s3/probe_retry.go:103`;
- refuse text but allow line breaks or tabs: `internal/api/runnerapi/session.go:670`
  (`\n`, `\t`), `pkg/forge/forge.go:971` and `:1214` (`\n`, `\r`, `\t`),
  `pkg/forge/github/review.go:715` (`\n`, `\r`, `\t`),
  `pkg/objectcontext/objectcontext.go:477` (`\n`, `\t`);
- control or space: `pkg/forge/forge.go:1122`, `pkg/forge/github/status.go:185`
  and `:202`, `internal/unmanaged/unmanaged.go:170`, `pkg/forgelink/forgelink.go:161`;
- change or drop a character instead of refusing: `pkg/notify/chat/chat.go:188`,
  `pkg/backup/devsubject.go:22`, `internal/clientgen/conformance/process.go:414`;
- a password: `internal/localusersvc/password.go:44`;
- a second definition: `audit.IsHiddenRune` in `pkg/audit/audit.go` on
  #2584, which only calls `ident.IsHiddenRune`.

The implementer runs the same `git grep` on main at the start and records
each difference.

## Fix boundary

- `internal/ident` keeps `IsHiddenRune` and `HasHidden`, and gets the NEW
  function `IsHiddenRuneExcept(r rune, allowed ...rune) bool` for text that
  may hold line breaks or tabs. Each caller names its allowed runes.
- Every call site in the list calls an `ident` function. A check that also
  refuses spaces calls `unicode.IsSpace(r) || ident.IsHiddenRune(r)`. A
  sanitizer that changes or drops a character tests with the same function.
- The password check is the one exception to a stricter check: a password
  may hold a format character (U+200D joins the parts of an emoji), so it
  calls the NEW function `ident.IsControlRune`, whose comment gives this
  reason. It stays as strict as today, no stricter.
- `audit.IsHiddenRune` is removed; its callers call `ident.IsHiddenRune`.
- The semgrep rule `.semgrep/hidden-rune-copy.yml` is removed. One
  `forbidigo` pattern in `.golangci.yml` refuses `unicode.IsControl` and the
  tables `unicode.Cc`, `unicode.Cf`, `unicode.Zl` and `unicode.Zp` outside
  `internal/ident` and tests, the place for such rules.
- Every check that changes is stricter or the same, never looser.

## Check

`rg -n 'unicode\.(IsControl|Cc|Cf|Zl|Zp)\b' --type go -g '!*_test.go' -g '!internal/ident/**'`
finds no line, and `make check` reports zero findings (Confirmed by: CI).

## ADR check

**ADR 364: Unexported fields and golangci-lint before added checks**
(owner). Quote: "Put a rule that visibility cannot express into the
golangci-lint configuration. Do not add a custom check or a new tool."
Effect: the one helper is the structure; the rule that keeps it moves from a
custom semgrep rule to the golangci-lint configuration. Conflict: none.

**ADR 250: Defense in depth for security code** (owner). Quote: "Check the
input at the boundary. Use an allowlist. Reject an input that is not valid."
Effect: each boundary check stays where it is and refuses at least what it
refused before. Conflict: none.

## Open questions

none. Considered: (1) widen the semgrep pattern (refused: it finds forms,
not the bug class, and ADR 364 prefers the linter configuration); (2) refuse
format characters in passwords too (refused: an emoji password holds U+200D,
and the sign-in rules accept any printable character); (3) one helper with
an options struct (refused: an explicit list of allowed runes is plainer).
