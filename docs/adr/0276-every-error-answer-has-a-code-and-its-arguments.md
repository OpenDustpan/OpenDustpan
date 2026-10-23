# 276. Every error answer has a code and its arguments

Status: accepted · 2026-10-23. Decisions 1 to 6, 9 and 10 are owner decisions. Decisions 7 and 8 are the design of the author, and follow from them. Extends ADR 182. Follows ADR 210 decision 10 for times

## Context

ADR 182 gives each API error a stable machine code and arguments. It names
the fields of each shape, a registry in `internal/` and the tests of the
registry. On 23 October 2026 the project owner accepted a decision for all
error answers. This ADR records that decision with the label "(owner)". It
also records the design of the catalog and of the gate that follow from it.

Facts on `main`:

- `internal/errcode` holds six codes of a 403: `access.denied`,
  `access.ip-not-allowed`, `gate.limited`, `gate.rule-failed`,
  `session.csrf` and `session.other-site`. `errcode.Forbidden` selects the
  code of a 403 from the error.
- The Pulumi Cloud-compatible API and the tool-neutral APIs put these codes
  in the field `errorCode`. The `code` field of these shapes is the HTTP
  status. The Pulumi CLI reads it.
- The installation API also sends `errorArgs` for some errors, for example
  the refusals of the `Idempotency-Key` header.
- The TFE-compatible API puts its codes in the JSON:API `code` member. Its
  codes are constants in its own package, for example
  `workspace.not-locked`.
- Most error answers have no code. A scan of the classify functions and the
  error bodies of the adapters finds 488 error paths without a code of the
  catalog.
- The registry and the frozen list of shipped codes of ADR 182 decision 7
  do not exist yet.
- The owner text of the decision said that times are Unix milliseconds. In
  the same session, the owner chose RFC 3339 strings, the form of ADR 210
  decision 10. Decision 3 records that choice.

## Decision

1. **Every error answer has a code (owner).** Each 4xx and 5xx answer of
   each HTTP adapter has a stable machine code. This includes the Pulumi
   Cloud-compatible API, the TFE-compatible API, the inventory API, the
   installation API and the other tool-neutral APIs. A code is never
   removed and never changes its meaning.

   Two protocols keep their own shapes, as in ADR 182 decision 1:

   | Protocol | Why it has no code of the catalog |
   | --- | --- |
   | The OpenTofu HTTP backend (`internal/api/tofuhttp`) | OpenTofu fixes its error shapes: plain text, and a fixed body for a lock answer. |
   | The runner protocol (`/runner/v1`) | The protocol has its own codes and its own versions (ADR 131, ADR 132). |

   The runner management API is a tool-neutral API. It gets codes of the
   catalog.
2. **The names of the codes (owner).** A code is a dotted string in the
   form of the existing 403 codes. The grammar and the length are those of
   ADR 182 decision 1. A code has the form `<area>.<reason>`:

   - The area is the kind of object or the layer, for example `stack`,
     `update`, `workspace`, `backup`, `namespace`, `request`, `session`,
     `access`, `gate` or `server`.
   - The reason is a state or a fault in kebab-case, for example
     `not-found`, `conflict`, `locked`, `corrupt`, `too-large` or
     `invalid`.

   Examples: `stack.not-found`, `update.conflict`, `backup.corrupt`. One
   meaning has one code in every adapter. A stack that is not found is
   `stack.not-found` in the Pulumi Cloud-compatible API and in the inventory
   API. The six 403 codes keep their text.
3. **The arguments, from the start (owner).** An answer carries the values
   that its text names, as arguments. An example is the name of the stack,
   the ID of the update or a limit. A code declares its arguments when it
   enters the catalog, and each answer with the code carries each of them.
   An argument has one of three kinds:

   | Kind | JSON | Example |
   | --- | --- | --- |
   | Identifier | a string | the name of a stack or of a field |
   | Quantity | an integer | a count or a limit |
   | Time | an RFC 3339 string in UTC | `"2026-10-23T09:30:00.125Z"` |

   The kinds identifier and quantity are those of ADR 182 decision 4. The
   kind time is new. It follows ADR 210 decision 10, so a time has one
   form in each answer of the tool-neutral API.

   An argument never holds pre-formatted text. It is never a sentence, a
   number with separators or a unit, or a date in a local form. The
   limits of ADR 182 decision 4 stay: at most 8 arguments, an identifier
   of at most 256 characters, a quantity within ±(2^53 − 1), and no value
   from the request body, no secret, no token and no state. An identifier
   has no line break and no control character.

   After a tag ships a code, its arguments only grow. A new argument is an
   addition. An argument is never removed or renamed, and its kind never
   changes.
4. **The English text stays (owner).** The `message` of the camelCase
   shapes and the `title` and the `detail` of JSON:API stay as they are.
   The CLIs show this text, and operators search for it.
5. **The fields of each shape (owner).** The fields are those of ADR 182
   decision 3.

   | API | The code | The arguments |
   | --- | --- | --- |
   | Pulumi Cloud-compatible API | `errorCode` | `errorArgs` |
   | Tool-neutral APIs | `errorCode` | `errorArgs` |
   | TFE-compatible API | the JSON:API `code` member | `meta.args` |

   In the camelCase shapes, `code` stays the HTTP status, because the
   Pulumi CLI reads it. A code without arguments has no `errorArgs` and no
   `meta.args`. A change to a tool-compatible API lands only after
   `make e2e` shows that each supported CLI version accepts it.

   A 404 of the Pulumi Cloud-compatible API:

   ```json
   {
     "code": 404,
     "message": "stack not found",
     "errorCode": "stack.not-found",
     "errorArgs": {"namespace": "acme", "project": "web", "stack": "prod"}
   }
   ```

   A 409 of the installation API, with a time:

   ```json
   {
     "code": 409,
     "message": "another restore is in progress",
     "errorCode": "restore.in-progress",
     "errorArgs": {"restore": "rst-01J9Z", "startedAt": "2026-10-23T09:30:00Z"}
   }
   ```

   A 404 of the TFE-compatible API:

   ```json
   {
     "errors": [{
       "status": "404",
       "code": "workspace.not-found",
       "title": "not found",
       "detail": "workspace not found",
       "meta": {"args": {"namespace": "acme", "workspace": "prod"}}
     }]
   }
   ```

   The codes in these examples show the form. The catalog fixes each code
   when an adapter starts to use it.
6. **No translation of error answers in the server (owner).** The server
   never reads `Accept-Language` and holds no translation of an error. A
   client selects its text from the code and formats the arguments. This
   restates ADR 182 decision 5 and ADR 234 decision 1. The emails and chat
   notices of ADR 234 are not error answers, and this decision does not
   change them.
7. **One catalog in `internal/errcode`.** The catalog is the registry of
   ADR 182 decision 7.

   - `errcode.Spec` declares a code: its meaning and its arguments, each
     with a name and a kind. `errcode.Catalog` returns every spec, in the
     order of the codes. A test checks the grammar, the uniqueness, the
     camelCase names and the limits.
   - `errcode.New` makes the `errcode.Answer` of a code from typed values:
     `errcode.Identifier`, `errcode.Quantity` and `errcode.Time`. It fails
     for a code outside the catalog, for an argument that the code does
     not declare, for a missing argument, for the wrong kind and for a
     value outside the limits. It formats a time in UTC.
   - The adapter writes `Answer.Code` and `Answer.Args` to the fields of
     decision 5. When `errcode.New` fails, the adapter answers 500 with
     `server.internal` and logs the code. A failure is a bug in the
     adapter, and the tests of the adapter find it first.

   Domain packages keep their sentinels and stay free of HTTP and of the
   catalog. An error that names a value is a typed error of the domain
   package, with the value in a field. Each classify function of an
   adapter maps a sentinel or a typed error to its status, its message and
   its code. It reads the arguments from the fields of the typed error.
   The order of the cases stays part of the contract, as for
   `errcode.Forbidden`. A 403 case calls `errcode.Forbidden`, which keeps
   its codes and its order. The codes that an adapter holds today in its
   own package move into the catalog.
8. **The gate.** A test in `internal/errcode` fails for an error path that
   answers without a code of the catalog. It works in the same way as the
   route test, which fails for a route without an authorization action.
   The test reads the source of each adapter. An error path is one of
   these:

   - A case of a switch in a classify function, for each error that the
     case names, and the `default` case.
   - An if statement in a classify function that tests an error with
     `errors.Is`, `errors.As` or `errors.AsType`.
   - A literal of an error body of the adapter outside its classify
     functions.

   A path has a code when it names something of the `errcode` package. A
   case that hands its error to another classify function is no path.

   The known list of each adapter holds its uncoded error paths today, in
   `internal/errcode/testdata/uncoded/<adapter>.txt`. The test fails for a
   new uncoded path. It also fails for a path on a known list that has a
   code now, or that the source no longer has. So a list only shrinks, and a new error path gets a code.
   When each list is empty, the known lists go away.

   A second test fails for a directory of `internal/api` that is not
   gated and is not on the ungated list with a reason. The ungated list
   holds the API description, the helpers of the adapters and the OpenTofu
   HTTP backend.
9. **The codes are in the API description (owner).** The schema of
   `errorCode` in the API description lists every code of the catalog.
   The description also declares the arguments of each code, with their
   kinds. The TypeScript client gets the codes as a union type, and the
   arguments of each code as a type.
10. **A code is a contract (owner).** A code never changes its meaning. The
    frozen list of shipped codes of ADR 182 decision 7 holds the codes and
    the arguments of each tag. It starts at the first tag.

## Consequences

- The design pull request adds this ADR, the catalog types, the six 403
  codes and `server.internal` in the catalog, the gate and the known lists.
  It changes no adapter.
- Later pull requests give the codes, one adapter or one group of
  adapters at a time. Each removes the lines of its paths from the known
  list of its adapter. Each change to a tool-compatible API runs
  `make e2e`.
- A pull request for decision 9 changes the API description and the
  client generator. `make clients-check` shows the new TypeScript output.
- `docs/reference/` gets the list of codes, with the meaning and the
  arguments of each code, as ADR 182 decision 7 says.
- `OpenDustpan/console` selects its messages by code and formats the
  arguments by kind. This ADR does not change that repository.
