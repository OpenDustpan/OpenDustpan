# 201. The profile document, imports and expressions

Status: accepted · 2026-10-18 · part of ADR 200

## Context

The project owner decided on 18 October 2026 that profiles get complete
feature parity with Pulumi ESC, in our own format, with no compatibility
with the ESC language (owner). A migration tool may come later. The
owner also asked for a design that is easy to use, not clunky (owner).

ESC writes an environment as YAML with two top-level keys, `imports` and
`values`. A function is a map with one key `fn::<name>`. ESC has 14
functions: `secret`, `concat`, `final`, `fromBase64`, `fromJSON`, `join`,
`open`, `rotate`, `split`, `toBase64`, `toJSON`, `toString`, `toYAML` and
`validate`. A reference is `${path}`, and `context` gives facts of the
open. The keys `environmentVariables`, `files`, `pulumiConfig` and
`policyConfig` under `values` say what reaches a command and the Pulumi
CLI. The research note `docs/research/profiles-esc-parity-and-oidc-claims.md`
lists each feature with its source.

OpenDustpan already has one expression language: Expr, pinned and limited
for blueprints (ADR 112 decisions 11 and 12). A second language would
double what a person must learn and what the server must limit.

## Decision

1. **The document.** A profile revision holds one document: a YAML text,
   or JSON, which is also YAML. The server stores the text as the person
   wrote it, with each secret literal replaced by its sealed form (ADR 202
   decision 1), so comments and the order of keys stay. The top-level keys
   are:

   | Key | Holds |
   | --- | --- |
   | `schemaVersion` | `1`. Required. A higher version fails with `profile.ErrSchemaVersion`, which names the highest version that the server reads. |
   | `description` | Text of at most 1,000 characters. |
   | `imports` | The profiles to merge, in order (decision 3). |
   | `providers` | Named provider blocks (decision 7). |
   | `rotators` | Named rotator blocks (ADR 202 decision 8). |
   | `values` | A tree of values: maps, lists, strings, numbers, booleans and `null`. |
   | `exports` | What reaches a run: variables, files, and the config of each tool (decision 9). |
   | `checks` | Rules that each open must pass (decision 11). |

   Any other top-level key fails the save with `profile.ErrInvalid`, which
   names the key. The data lives in `values`, and the delivery in
   `exports`. So a value never reaches a command only because of its name.
2. **A small example.**

   ```yaml
   schemaVersion: 1
   description: Production account of the payments team
   imports:
     - shared/tags
   providers:
     aws:
       use: login/aws
       with:
         roleArn: arn:aws:iam::123456789012:role/payments-${ context.access == "write" ? "deploy" : "read" }
       export: true
   values:
     region: eu-west-1
     db:
       host: db.payments.internal
       password: { $secret: correct-horse-battery-staple }
   exports:
     variables:
       AWS_REGION: ${ values.region }
     pulumi:
       aws:region: ${ values.region }
       payments:dbPassword: ${ values.db.password }
   ```

   A login block with `export: true` adds the default exports of its
   adapter (ADR 206 decision 4): here a shared credentials file of AWS and
   the variable that names it. The role differs for the plan phase and the
   apply phase, from the fact `context.access` (ADR 205 decision 4).
3. **Imports.** `imports` is a list of 0 to 32 items. An item is a profile
   name, or a map:

   ```yaml
   imports:
     - shared/tags                 # the revision with the tag latest
     - aws/prod@stable             # the revision with the tag stable
     - aws/prod@12                 # revision 12
     - profile: network/prod
       at: stable
       merge: false                # read it as imports["network/prod"], do not merge it
   ```

   - A name refers to a profile of the same namespace. A profile never
     imports from another namespace, because the namespace is the unit of
     tenancy and of the fence (ADR 4).
   - The open merges the values, the providers and the exports of each
     import in list order, then the profile's own document. A later item
     wins. Maps merge key by key, as JSON Merge Patch (RFC 7396) does. A list
     or a scalar replaces the earlier value. A `null` removes the earlier
     value.
   - `merge: false` evaluates the import and keeps it out of the merge. An
     expression reads it as `imports["network/prod"].values.vpcId`.
   - An expression can also read one value of another profile with no
     import: `profiles["network/prod"].values.vpcId`, with an optional
     `@tag` in the name. The open evaluates each profile and revision at
     most once, also when two paths reach it.
   - The open checks `profile:open` on each profile that it evaluates,
     with the rules of ADR 203 decision 4.
   - A cycle of imports or of `profiles[...]` reads fails the save with
     `profile.ErrImportCycle`, which names the cycle. The open checks again,
     because a later revision of an import can make a cycle.
   - The depth of imports is at most 16 levels.
4. **Expressions are Expr.** A string that is exactly `${ <expression> }`
   gives the value of the expression, of any type. A `${ }` inside a longer
   string gives the string form of the value. `$${` writes a literal `${`.
   The expression language is Expr, with the version pin, the compile
   rules and the limits of ADR 112 decisions 11 and 12. A profile revision
   records its Expr version, as a blueprint version does.
   - An expression sees `values` (the merged tree), `providers` (the
     outputs of each provider block), `imports`, `profiles` and `context`
     (decision 5).
   - An expression is early-bound: the open evaluates it in the profile that
     holds it, before the merge into a profile that imports it. A later
     import cannot change what an expression of an earlier profile read.
   - A path that does not exist fails the open with
     `profile.ErrReference`, which names the path and the profile. The save
     checks each path that it can find without a provider call.
5. **The context.** `context` holds the facts of the open, with the names
   of the claims of ADR 205: `context.namespace`, `context.profile`,
   `context.rootProfile`, `context.revision`, `context.access`,
   `context.action`, `context.phase`, `context.tool`, `context.unit`,
   `context.collection`, `context.actorType`, `context.actorId`,
   `context.ref`, `context.refType`, `context.refProtected`,
   `context.repository`, `context.job` and `context.runnerPool`, and
   `context.openDuration`. A fact that the open does not have is `nil`.
   The context of the profile that holds an expression is the context of
   that profile, so `context.profile` names it, and `context.rootProfile`
   names the profile that the caller opened.
6. **Entries mark a value.** A map whose keys start with `$` is an
   **entry**. It describes one value:

   | Key | Meaning |
   | --- | --- |
   | `$value` | The value, as any value of the tree. |
   | `$secret` | A secret in plain text. The save seals it, and the stored document holds `$sealed` in its place (ADR 202 decision 1). |
   | `$sealed` | A sealed secret. Only the server writes it. |
   | `$final` | `true`: a profile that imports this one cannot replace the value. A replace fails the save with `profile.ErrFinal`, which names the path and the profile that set it. |
   | `$type` | The type and the limits of the value (decision 10). |
   | `$description` | Text for the console and the docs of the profile. |
   | `$neverStore` | `true`: no consumer may store the value (decision 12). |

   An entry holds exactly one of `$value`, `$secret` and `$sealed`. A map
   that mixes `$` keys with other keys fails the save. A key of `values`
   that starts with `$` and that is not an entry key fails the save, so a
   user key cannot look like an entry.

   ESC warns and keeps the first value when an import replaces a final
   value. We refuse the save instead, because a warning that nobody reads
   is the same as no rule.
7. **Provider blocks.** `providers` maps a block name to a block:

   ```yaml
   providers:
     vault:
       use: login/vault
       with: { address: https://vault.internal:8200, role: payments }
     db:
       use: secrets/vault
       login: vault
       with:
         read:
           password: { path: secret/data/payments/db, field: password }
       timeout: 15s
   ```

   - `use` names the kind and the adapter (ADR 206 decision 2).
   - `with` holds the inputs. Each input can be an expression. A secret
     input of the adapter spec must come from an entry with `$secret` or
     from an expression that reads a secret. A plain literal fails the save,
     so a static key never sits in a document in plain text.
   - `login` names a login block of this profile or of an import. The
     open gives its result to this block (`Request.Login`).
   - `audience` and `subject` (a subject template, ADR 205 decision 6) are
     optional for a block that mints a token.
   - `export: true` adds the default exports of the adapter.
   - `neverStore: true` marks each output of the block (decision 12).
   - `timeout` is 1 to 30 seconds, 10 seconds by default.
   - `observe: true` names the login block that the observe of Dusty uses
     (ADR 200 decision 5).

   An expression reads an output as `providers.db.password`. The open calls
   only the blocks that the asked paths and exports need, and each block at
   most once (ADR 203 decision 2). So a person who opens one value does not
   start every login of the profile.
8. **Functions.** Expr gives most functions of ESC with no new code. The
   profile adds the rest to its Expr environment:

   | ESC | Profiles |
   | --- | --- |
   | `fn::secret` | The entry key `$secret`, and the function `secret(x)`, which marks a computed value as secret. |
   | `fn::concat` | `concat(a, b)` of Expr. |
   | `fn::final` | The entry key `$final`. |
   | `fn::toBase64`, `fn::fromBase64` | `toBase64(s)`, `fromBase64(s)` of Expr. |
   | `fn::toJSON`, `fn::fromJSON` | `toJSON(x)`, `fromJSON(s)` of Expr. |
   | `fn::toYAML` | `toYAML(x)`, added. |
   | `fn::toString` | `string(x)` of Expr, and `${ }` inside a string. |
   | `fn::join`, `fn::split` | `join(list, sep)`, `split(s, sep)` of Expr. |
   | `fn::open` | A block of `providers`. |
   | `fn::rotate` | A block of `rotators`. |
   | `fn::validate` | `$type` and `checks` (decisions 10 and 11). |

   We add `fromYAML(s)`, which ESC does not have. Each function keeps the
   secret mark: a result that reads a secret is a secret (decision 12).
9. **Exports.** `exports` says what each consumer gets:

   | Key | Consumer | Shape |
   | --- | --- | --- |
   | `variables` | The run command, and the steps of a runner | A map of variable names to strings. |
   | `files` | The run command, and the steps of a runner | A map of variable names to file content. The consumer writes the file and sets the variable to its path. |
   | `pulumi` | The Pulumi CLI (ADR 209) | A map of config keys, for example `aws:region`, to values. |
   | `opentofu` | OpenTofu (ADR 209) | A map of input variable names to values. |

   The keys `pulumi` and `opentofu` are the tool codes of the glossary, so
   each tool keeps its own words inside its own map. A secret in
   `variables` reaches the run command on a workstation. On a runner, the
   job start refuses it with `profile.ErrSecretVariable`, which names the
   variable and says to use `files`, because no credential passes through
   a variable on a runner (ADR 101 decision 10, ADR 134 decision 6).
10. **Types.** `$type` gives the type of a value and its limits:

    ```yaml
    values:
      size:
        $type: { kind: string, enum: [small, medium, large] }
        $value: medium
      replicas:
        $type: { kind: number, integer: true, min: 1, max: 9 }
        $value: 3
    ```

    The kinds are `string`, `number`, `boolean`, `list`, `map` and `any`,
    with `enum`, `pattern` (an RE2 expression), `min`, `max`, `minLength`,
    `maxLength`, `integer`, `items` and `required` (the keys that a map
    must hold). The save checks each value that it knows. The open checks
    each value after the merge, so a type of an imported profile applies to
    a value that a later profile sets. A failure names the path, the profile
    that set the value and the profile that set the type. We use this small
    form instead of JSON Schema. It covers the checks of `fn::validate` that
    the ESC docs show, and it needs no new dependency.
11. **Checks.** `checks` holds 0 to 32 rules:

    ```yaml
    checks:
      - rule: values.region in ["eu-west-1", "eu-central-1"]
        message: The region must be in the EU.
      - rule: context.access != "write" || context.refProtected == "true"
        message: An apply reads this profile only from a protected ref.
    ```

    A rule is an Expr expression that gives a boolean. The save runs each
    rule that needs no provider and no context. The open runs each rule. A
    rule that gives false fails the open with `profile.ErrCheckFailed` and
    the message. A rule that fails to run fails the open too. So a profile
    can refuse an open by its context, which ESC cannot do.
12. **Marks propagate.** Each value has two marks:
    - **secret**: from `$secret`, `$sealed`, a secret output of a provider,
      or `secret(x)`.
    - **never store**: from `$neverStore` or a block with `neverStore: true`.

    A value that an expression computes from a marked value gets the same
    mark. The server finds the paths that an expression reads from its
    syntax tree, before it runs it. So the mark is never lost, also when
    the result does not look like the input. A value marked never store is
    also a secret. ADR 209 decision 3 gives what each consumer does with
    the never-store mark.
13. **Limits.** A document has at most 256 KiB of text, 64 provider blocks,
    16 rotator blocks, 4,096 leaves in `values` and a depth of 32. The
    resolved result of an open has at most 4 MiB. A save or an open above a
    limit fails with `profile.ErrLimit`, which names the limit.
    `profile.Limits` holds the numbers.

## Consequences

- A person who knows the blueprints of the Catalog already knows the
  expressions of a profile.
- A profile is not an ESC environment. A migration tool reads ESC YAML and
  writes this format. RFC 5 keeps it open.
- `pkg/profile` holds `Parse`, `Check`, `Resolve` and the Expr
  environment. Each is pure, and table-driven tests cover each rule of this
  ADR: merges, nulls, finals, entries, marks, types, checks, cycles and
  limits.
- The console editor needs the adapter specs and the context schema for
  completion. The routes of ADR 203 give them.
