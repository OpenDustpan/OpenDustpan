# 228. Expr computes values only

Status: accepted · 2026-10-18. Part of ADR 222. Supersedes the builtin list of ADR 112 decision 12, and ADR 114 decision 5. Supersedes ADR 201 decisions 4 and 8 in part. Extends ADR 112 decisions 9 and 11

## Context

Expr runs in two roles. As a value, it computes a profile value in `${ }`
(ADR 201 decision 4) and the `unitName` of a blueprint (ADR 112 decision 9).
As a rule, it decides in 4 places (ADR 222). The two roles have two builtin
lists. ADR 112 decision 12 leaves out `join`, `string` and `toJSON`, because
they can make a string larger than the memory budget counts. ADR 201
decision 8 gives the same functions to profiles. The ADRs name the Go
constant `catalog.ExprBuiltins` and no shared package.

## Decision

1. **Values only (owner).** Expr computes values: a profile value in `${ }`
   and the `unitName` of a blueprint. Expr never decides whether a change
   may go on. Each rule that decided in Expr is a gate (ADR 224 decision
   5).
2. **One package (owner).** One package, `internal/expr`, holds the Expr
   environment, the compile rules, the limits of ADR 112 decision 12 and
   the builtin list. The profile and the Catalog use it, and no other
   package imports `github.com/expr-lang/expr`. depguard holds this rule.
3. **One builtin set (owner).** One list, `expr.Builtins`, holds the
   builtins for every use. It leaves out the builtins that read the time or
   the local time zone, and the builtins whose result order is the order of
   a map, as ADR 112 decision 12 says. It holds `join`, `split`, `string`,
   `concat`, `toJSON`, `fromJSON`, `toYAML`, `fromYAML`, `toBase64`,
   `fromBase64` and `secret`, each through a wrapper that counts the bytes
   of its result against `MaxConcatBytes` and refuses a larger result. The
   functions of ADR 201 decision 8 keep the secret mark.
4. **One version.** The Expr version pin and its record (ADR 112 decision 11)
   hold for both uses. A profile revision and a blueprint version record the
   same field, `exprVersion`.
5. **Errors.** An Expr value that fails to compute fails the save or the open
   with the error of its domain package (`profile.ErrReference`,
   `catalog.ErrUnitName`). It is never an empty value.

Benchmark: the spike evaluated every rule in Cedar and needed no Expr rule.
One builtin list removes the contradiction between ADR 112 and ADR 201.

## Consequences

- Blueprint `validate` and `policy` rules, quota `expr` rules and profile
  `checks` move to Cedar gates in slices 4 and 5 (ADR 222).
- An answer check of a blueprint input that needs no Cedar stays a type
  check of the input schema (ADR 112).
