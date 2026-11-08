# 112. Blueprints: the document, its versions and Expr

Status: accepted · 2026-10-17 · supersedes the "Input schema" and the "Validation" sections of RFC 14. Decisions 6 and 7 superseded by ADR 224. The builtin list of decision 12 superseded by ADR 228

Changed by:

- ADR 222 supersedes decisions 6 and 7: blueprint rules become gates.
- ADR 222 changes decision 12: one builtin set.
- ADR 224 supersedes decisions 6 and 7: blueprint rules become gates.
- ADR 228 changes decision 12: the builtin list becomes one builtin set.
- ADR 228 extends decisions 9 and 11: one Expr package and one version pin for
  both uses.
- ADR 286 changes: the words catalog and promotion become self-service and
  failover.
- ADR 360 extends: a request upgrades by three paths, and a time to live is a
  unit setting.

## Context

[RFC 14](../rfc/0014-blueprints.md) proposes blueprints with an input
schema in JSON Schema, platform values, a naming rule and versions. The
owner decided on 17 October 2026:

- A blueprint adds to a Pulumi component or an OpenTofu module. It never
  replaces it.
- A blueprint names the module or the component, its inputs (with types,
  defaults and validation in Expr), its choices, its policy and quota rules,
  and its delivery.
- Expr (`github.com/expr-lang/expr`) is the expression language inside
  blueprints. The project pins it to a release at least two weeks old, and each
  blueprint records its Expr version.

ADR 160 assumes that a blueprint lives in a namespace and asks this ADR to
decide.

## Decision

1. **A blueprint lives in a namespace.** A blueprint has a name of 1 to 63
   bytes of lowercase letters, digits and `-`, with a letter first, unique
   in its namespace. Its requests and their units are in the same namespace
   (ADR 110 decision 2). The identity package gets the object kind
   `blueprint`, with the namespace and the name.
2. **Drafts and published versions.** A blueprint has one draft and a list
   of published versions.
   - A principal with `blueprint:publish` writes the draft as often as it
     wants. A draft serves no request.
   - A publish checks the draft (decision 11), compiles each expression,
     and stores it as the next version: the integer after the last one,
     from 1. A published version never changes.
   - A retire of a version stops new requests and upgrades to it. A request
     that pins a retired version keeps it, and its updates still work. A
     retire never changes a unit.
   - A request pins a version (ADR 111 decision 1). An upgrade is a new
     revision with a higher version, and its answers must pass the checks of
     the new version.
3. **The document.** `catalog.Blueprint` holds it. This is a blueprint of
   schema version 1, in short:

   ```json
   {
     "schemaVersion": 1,
     "name": "postgres",
     "title": "PostgreSQL database",
     "tool": "opentofu",
     "program": {
       "module": { "source": "registry.example.com/platform/postgres/aws", "version": "2.4.1" }
     },
     "exprVersion": "v1.17.8",
     "unitName": "'databases/' + request.name",
     "sourcePrincipal": "catalog-reader",
     "inputs": [
       { "name": "size", "type": "string", "default": "small",
         "choice": { "values": ["small", "medium", "large"] },
         "maps": "instance_size" },
       { "name": "network", "type": "string", "required": true,
         "choice": { "source": { "blueprint": "network",
           "value": { "output": "network_id" }, "label": { "output": "display_name" } } },
         "maps": "network_id" },
       { "name": "subnetGroup", "type": "string", "required": true,
         "choice": { "source": { "blueprint": "subnet-group",
           "value": { "output": "name" },
           "where": [ { "output": "network_id", "equals": { "input": "network" } } ] } },
         "maps": "subnet_group" },
       { "name": "storageGb", "type": "integer", "default": 20,
         "validate": [ { "rule": "value >= 20 && value <= 2000",
                         "message": "Give a size from 20 to 2000 GB." } ],
         "maps": "storage_gb" }
     ],
     "platform": { "values": { "backup_retention_days": 14 }, "profile": "aws-prod", "pool": "default" },
     "policy": [ { "when": "inputs.size == 'large'", "effect": "approve", "approvals": 1, "groups": ["dba"] } ],
     "quotas": [ { "kind": "units", "scope": "namespace", "max": 50 } ],
     "delivery": { "homes": ["git", "opendustpan"], "default": "git",
                   "git": { "deliveries": ["review-request"] } }
   }
   ```

4. **The program is a module or a component.** A blueprint names exactly one
   of these:
   - `module`: an OpenTofu module, by its source address and an exact
     version, or by a forge connection, a repository, a directory and a tag
     or a commit.
   - `component`: a Pulumi component, by its package, an exact version and
     its type token.

   `tool` is `opentofu` or `pulumi`. A blueprint of OpenTofu names a
   module, and a blueprint of Pulumi names a component.

   The blueprint never holds code of the tool. The render (ADR 110 decision
   8) makes one module call or one component resource from it. The module
   keeps its own variable validation, and the tool runs it at the plan. A
   version range is not allowed: each version of the blueprint names one
   version of the program, so a render of one revision gives the same bytes.
5. **Inputs have six types.** `string`, `integer`, `number`, `boolean`,
   `string-list`, and a `string` or a `string-list` with a `choice`. An
   input has a name of 1 to 64 bytes of letters, digits and `_`, with a
   letter first, a title, a description, `required`, an optional `default`
   of its type, and `maps`: the variable of the module or the config key of
   the component that the answer sets. An input with a default is never
   required. No input is secret (ADR 111 decision 3).

   The publish checks each key of `maps` and of `platform.values` with
   `catalog.ValidKey`, at most 128 bytes:
   - For OpenTofu, an HCL identifier: a letter or `_`, then letters, digits,
     `_` and `-`, and not a name that a module block keeps (`source`,
     `version`, `providers`, `count`, `for_each`, `lifecycle`,
     `depends_on`, `locals`).
   - For Pulumi, a config key: a name, or a namespace, `:` and a name, where
     each part is a letter, then letters, digits, `_` and `-`.

   The render writes answers only as data, in the variables file or the
   stack settings (ADR 110 decision 8). The program text holds only these
   keys, so no answer and no key can change the program.
6. **Validation in Expr.** An input has 0 to 32 `validate` rules. Each rule
   is an Expr expression that gives a boolean, and a message. The
   expression sees `value` (the answer), `inputs` (all answers, with
   defaults) and `request` (the namespace, the name and the requester:
   subject and groups). A rule that gives false fails the answer with its
   message. A rule that fails to run fails the answer with
   `catalog.ErrRuleFailed`, never with a pass. The server runs every rule
   and returns every failure, so a form can show all of them at once.

   The environment, `catalog.Env`, is data only: a struct of fields with no
   method, and no method on any type in it. The namespace is a plain
   string, and each answer is a JSON value. So an expression can read data
   and cannot call code of the server. A test walks the types and fails on
   a method.
7. **Policy and quota rules.** The `policy` list holds 0 to 64 rules: an
   Expr condition `when`, an effect (`deny` or `approve`), a message, and
   for `approve` a number of approvals and the groups of the approvers. The
   built-in Expr adapter of ADR 114 runs them. The `quotas` list holds 0 to
   16 rules of ADR 115. Both lists are part of the version: a change of a
   rule is a new version.

   A `when` condition or a quota `expr` rule that fails to run is not a
   failure of the answer, as a validation rule is. It is a check that
   cannot run: the engine returns an error that wraps
   `policy.ErrRuleFailed` or `quota.ErrRuleFailed`, and gives no decision.
   The recheck stops and changes nothing (ADR 113 decision 8). So a deny
   rule that fails to run never becomes an allow.
8. **Platform values have one home.** `platform` holds the values that the
   platform team sets and the consumer cannot see or change: fixed values of
   variables or config keys, the profile that supplies the secrets of a run
   (RFC 5), and the runner pool. The one-home rule of RFC 12 holds in three
   places:
   - At publish: no key is in `maps` of two inputs, or in `maps` of an input
     and in `platform.values`. `catalog.CheckOneHome` holds the rule. It
     compares keys in the form of `catalog.NormalizeKey`: in lowercase, and,
     for Pulumi, with no namespace when the namespace is the project of the
     render, which is the name of the blueprint. So `Size`, `size` and
     `postgres:size` are one key of the blueprint `postgres`.
   - At publish: no key of `maps` or of `platform.values` is a key that the
     profile of the blueprint sets.
   - At render: the render refuses a key that another home of the unit also
     sets, for example a stack settings file that the team wrote, with
     `catalog.ErrOneHome`, in the same normalized form. It names the key and
     both homes.
9. **The unit name.** `unitName` is an Expr expression that gives the tool's
   own name of the unit from `request` and `inputs`. The server computes it
   at the first submit and keeps it: a later change of an input never
   renames a unit. A name that the tool refuses, or a unit that exists,
   fails the submit with `catalog.ErrUnitName`.
10. **Delivery.** `delivery.homes` lists the homes that the requests of the
    blueprint can use, and `delivery.default` names one of them.
    `delivery.git` limits the deliveries (`review-request`,
    `direct-push`) and can list the repositories that a request can name.
    An empty list allows each repository of the forge connections of the
    namespace. Schema version 1 has no custom steps: the steps of a runner
    job are the default steps of the tool. A later schema version that adds
    custom steps to a blueprint needs `unit:define-steps` on the blueprint
    (ADR 103 decision 8).
11. **The Expr version for each blueprint.** The server links one version of
    Expr, pinned in `go.mod`. A publish records it in `exprVersion`. The
    built-in adapter checks each blueprint version before it runs an
    expression of it:
    - The same major and minor version: the expression runs.
    - Another minor or major version: the server compiles each expression of
      the version again at start. When each one compiles to the same type,
      the server records the new Expr version for the blueprint version, and
      appends an audit entry. When one does not compile, the blueprint version
      cannot serve new revisions, `catalog.ErrExprVersion` names it, and the
      platform team publishes a new version.

    The version 1 pin is `github.com/expr-lang/expr` v1.17.8, released on
    14 February 2026, under the MIT license. It is not in `go.mod`: this
    design adds no dependency. The pull request of the adapter adds it, and
    `make trivy` and `make licenses` then check it.
12. **Limits of Expr.** An expression is at most 4 KiB of source. The
    adapter compiles it with a type check against the environment of
    decision 6, and runs it with a time limit of 50 ms. The constants of
    `pkg/catalog` hold the other limits:

    | Limit | Value |
    | --- | --- |
    | Node limit of the compiler (`MaxExprNodes`) | 128 |
    | Memory budget of the virtual machine for one run (`ExprMemoryBudget`), in items of the lists that the run makes | 1,000 |
    | String that the operator `+` gives (`MaxConcatBytes`) | 1 KiB |
    | Groups of the requester in the environment (`MaxEnvGroups`) | 1,000 |
    | Depth of closures, such as the predicate of `all` (`MaxClosureDepth`) | 1 |

    The adapter refuses at compile:
    - each builtin that is not in `catalog.ExprBuiltins`. The list leaves
      out the builtins that read the time or the local time zone (`now`,
      `date`, `timezone`, `duration`), the builtins whose result order is
      the order of a map (`keys`, `values`, `toPairs`), and the builtins
      that can make a string or a list larger than the memory budget
      counts (`join`, `repeat`, `replace`, `string`, `toJSON`, `reduce`
      and others),
    - the syntax `let`, the range operator `..`, and a closure inside a
      closure.

    It runs `+` on two strings through a function that refuses a result
    above `MaxConcatBytes`, because the memory budget of Expr does not
    count strings.

    We measured these limits with Expr v1.17.8 on one core of a 2.1 GHz
    Xeon, with a set of hostile expressions (nested loops, string growth,
    regular expressions on 4 KiB strings) and of usual rules:
    - Without the limits, an expression of 200 bytes (a `let` chain that
      doubles a string) used all the memory of the machine, and a nested
      `join` took 180 ms.
    - With the limits, the slowest expression took 13 ms, with 1,000 groups
      in the environment. Each usual rule took less than 1 ms.

    So a rule gives the same result for the same inputs at submit, at
    approval and at apply, and a rule cannot hold the server.
13. **The blueprint document has a schema version.** It follows the rules of
    ADR 111 decision 4, with its own counter. `catalog.ParseBlueprint`
    refuses an unknown field and a document over 256 KiB.

## Consequences

- A platform team publishes a version of the module or the component that
  it already has. It writes no code of the tool in the blueprint.
- A rule in Expr runs in the server at each recheck point, with no runner.
  A check that needs a network call is not an Expr rule: it is a policy
  adapter (ADR 114) or a custom step.
- The server and the tool check an answer three times: by the type, by the Expr rules, and by
  the variable validation of the module at the plan.
- An upgrade of the Expr library can stop old blueprint versions from
  serving new revisions. The audit entry and the error name each version.

## Open questions

- Who migrates the answers of existing requests when a new version renames
  or deletes an input, or adds a required one? Version 1 asks the requester
  to submit an upgrade with valid answers.
- Does a blueprint get a time to live for its units (RFC 43)? The field
  would be a later addition to the document.
- Does a rollout of a new version to many requests need a batch (ADR 100
  "Open questions")? Version 1 has no batch: each upgrade is a submit.
- Can the platform team delete a published version when no request pins it?
