# 276. Every error answer has a code and its arguments

Status: accepted · 2026-10-23. Decisions 1 to 6, 9 and 10 are owner decisions. Decisions 7 and 8 are the design of the author, and follow from them. Extends ADR 182. Supersedes in part ADR 182 decisions 4 and 7. Follows ADR 210 decision 10 for times

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
- The adapters send more codes in `errorCode` today. Each adapter holds
  them as constants or literals in its own package. The console reads them:

  | Area | Codes | Arguments | Sent by |
  | --- | --- | --- | --- |
  | `auth` | `rate-limited`, `unauthenticated` | none | the notifications, profiles and registry APIs |
  | `namespace` | `fenced`, `invalid-name`, `not-found`, `not-home` | none | the notifications, profiles and registry APIs. The jobs and drift APIs send `fenced` too |
  | `maintenance` | `in-maintenance` | none | the notifications, profiles and registry APIs |
  | `request` | `body-invalid`, `content-type-unsupported`, `invalid-path`, `invalid-query`, `page-size-invalid`, `page-token-invalid`, `precondition-failed`, `precondition-required` | none | the tool-neutral APIs |
  | `request` | `idempotency-key-in-progress`, `-invalid`, `-replayed-secret`, `-required`, `-reused`, `-unavailable`, `-unsupported` | `objectID` (identifier) for `idempotency-key-replayed-secret` | the `Idempotency-Key` middleware, through each tool-neutral API |
  | `preferences` | `body-invalid`, `body-too-large`, `field-invalid`, `field-read-only`, `field-unknown`, `if-match-invalid`, `media-type-unsupported`, `person-not-found`, `person-required`, `precondition-failed`, `unavailable` | `field` (identifier) for `field-invalid` and `field-read-only` | the installation API |
  | `profile` | `conflict`, `invalid`, `limit`, `not-found`, `routing-change-needs-open`, `schema-version` | none | the profiles API |
  | `sink` | `conflict`, `invalid`, `no-sender`, `no-url`, `not-found`, `platform-host`, `test-rate-limited` | `retryAfterSeconds` (quantity) for `test-rate-limited` | the notifications API |
  | `job` | `agent-denied`, `frozen`, `gate-enforced`, `invalid`, `limit`, `policy-denied`, `unsupported` | none | the jobs and drift APIs |
  | `drift` | `start-conflict` | none | the drift API |
  | `git-host` | `app-not-registered`, `installation-not-assigned` | none | the installation API |
  | `mfa` | `fresh-step-required` | none | the Pulumi Cloud-compatible API, the TFE-compatible API, the installation API and the profiles API |
  | `registry` | `archive-set-refused`, `invalid`, `key-exists`, `key-invalid`, `key-limit`, `no-keys`, `not-found`, `not-staged`, `signature-invalid`, `signature-untrusted-key`, `too-large`, `version-exists` | none | the registry API |
  | `workspace` | `client-encrypted`, `lock-mismatch`, `not-locked` | none | the TFE-compatible API, in the JSON:API `code` member |
  | `gate` | `denied`, `unavailable`, and `limited` and `rule-failed` of the six | none | the TFE-compatible API, for a state write that a gate refuses |

  Only the installation API sends `objectID` with
  `request.idempotency-key-replayed-secret`. The other APIs send the code
  without its argument today.
- `errcode.Refusal` answers 403 like a missing permission and has no code.
  It has three calls. The installation API uses it for a refused sign-in
  and for a route with no authorization action for its object. The audit
  API uses it for a route with no action for the installation.
- The catalog holds 83 codes: the 80 codes that `main` sends, and the new
  codes `sign-in.refused`, `access.no-action` and `server.internal`.
- Most error answers have no code. A scan of the mapping functions, the
  tables of errors and statuses and the error bodies of the adapters and of
  `pkg/dustpan` finds 551 error paths without a code of the catalog.
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

   The provider registry protocol of the TFE-compatible adapter is not a
   third such protocol. Its error document is `{"errors": ["<text>"]}`, and
   the protocol does not fix other members of that document. So its six
   error answers get codes, in members next to `errors`, like every other
   answer of the TFE-compatible API. Until then, they are on the known list
   of that adapter. `make e2e` must show that OpenTofu accepts the new
   members before that change lands.
2. **The names of the codes (owner).** A code is a dotted string in the
   form of the existing 403 codes. The grammar and the length are those of
   ADR 182 decision 1. A code has the form `<area>.<reason>`:

   - The area is the kind of object or the layer, for example `stack`,
     `update`, `workspace`, `unit`, `backup`, `namespace`, `request`,
     `session`, `access`, `gate` or `server`.
   - The reason is a state or a fault in kebab-case, for example
     `not-found`, `conflict`, `locked`, `corrupt`, `too-large` or
     `invalid`.

   Examples: `stack.not-found`, `update.conflict`, `backup.corrupt`.

   A code names an object with the words of the API that sends it, as the
   text of that API does (ADR 13, ADR 251). The project manager decided this
   rule from those ADRs:

   | API | The object words of its codes |
   | --- | --- |
   | The Pulumi Cloud-compatible API | `stack`, `update` |
   | The TFE-compatible API | `workspace`, `run`, `state-version` |
   | The tool-neutral APIs: the inventory API, the installation API and the others | `unit` |

   So a stack that is not found is `stack.not-found` in the Pulumi
   Cloud-compatible API, a workspace that is not found is
   `workspace.not-found` in the TFE-compatible API, and a unit that is not
   found is `unit.not-found` in the inventory API. Each object word has one
   meaning for each reason: `unit.not-found` means the same in each
   tool-neutral API. A tool-neutral API never sends `stack.*` or
   `workspace.*`, and a tool-compatible API never sends `unit.*`. A code of
   a layer that is not an object, for example `namespace`, `request`,
   `access` or `server`, is the same in every API.

   Every code that `main` sends today enters the catalog with its text and
   its argument keys, as the list in the context gives them. No code is
   renamed: `git-host.*`, `sink.*` and `mfa.*` keep their areas, also where a
   newer word exists. The six 403 codes keep their text.
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
   kind time is new, and it supersedes in part ADR 182 decision 4, which
   allows two kinds only. It follows ADR 210 decision 10, so a time has one
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
   ADR 182 decision 7. It supersedes in part ADR 182 decision 7: the
   catalog declares every code in one place, and an adapter does not
   declare its subset of codes next to its classify functions.

   - `errcode.Spec` declares a code: its meaning and its arguments, each
     with a name and a kind. `errcode.Catalog` returns every spec, in the
     order of the codes. A test checks the grammar, the uniqueness, the
     camelCase names and the limits. Each area of codes has its own list
     in a file `catalog_<area>.go`, with a constant for each code, and its
     own line in the list of areas. So two changes to two areas touch
     different lines.
   - ADR 182 decision 7 asks for a test of "two meanings for one code"
     across all APIs. One catalog meets it by construction: a code is in
     the catalog once, with one meaning, and each adapter takes it from
     there.
   - `errcode.New` is the only way to make an `errcode.Answer` with a code.
     It makes the answer of a code from typed values: `errcode.Identifier`,
     `errcode.Quantity` and `errcode.Time`. It fails for a code outside the
     catalog, for an argument that the code does not declare, for a missing
     argument, for the wrong kind and for a value outside the limits. It
     formats a time in UTC. `Answer` has no exported field and no JSON
     form of its own.
   - The adapter copies `Answer.Code()` and `Answer.Args()` to the fields
     of decision 5. It never marshals or embeds an `Answer`, because the
     names of the fields differ in each shape. When `errcode.New` fails,
     the adapter answers 500 with `server.internal` and logs the code. A
     failure is a bug in the adapter, and the tests of the adapter find it
     first.
   - `errcode.Refusal` takes a code of the catalog. `errcode.Forbidden`
     gives the code of the refusal only when the catalog has it. For an
     empty code or a code that is not in the catalog, it continues to the
     other causes, so the error gets at least `access.denied`. So it never
     gives no code, and never a code outside the catalog, for an error that
     wraps `identity.ErrDenied`. A refused sign-in answers
     `sign-in.refused`. A route with no authorization action for its object
     answers `access.no-action`. These two codes join the six 403 codes.

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
   The test reads the source of each adapter of `internal/api` and of
   `pkg/dustpan`, which answers the 404 of the router, the 404 of a missing
   component, the 503 of maintenance mode and the 405 of the console files.

   A mapping function is a classify function, or any other function that
   tests an error with `errors.Is`, `errors.As` or `errors.AsType` and
   names an HTTP status of 400 or more. An error path is one of these:

   - A case of a switch in a mapping function, for each error that the
     case names, and the `default` case.
   - An if statement in a mapping function that tests an error.
   - An element of a table that holds a sentinel error and an HTTP status
     of 400 or more, for example the table of errors of the registry API.
     Each element of the table carries its code.
   - A literal of an error body of the adapter outside these cases, also in
     the value of a variable.
   - A string literal that holds the JSON member `code` or `errors`, as a
     raw error body.
   - A call of `http.Error` or `http.NotFound`.

   Outside a classify function, a case or an if statement is a path only
   when its body names an HTTP status of 400 or more. A path has a code
   only when it calls `errcode.New` or `errcode.Forbidden`, or names a code
   constant of the catalog. Any other name of the package, for example
   `errcode.Valid` or `errcode.KindIdentifier`, gives no code. A literal in
   the body of an if statement whose condition has a code has a code. A
   literal in its `else` branch does not. A case that hands its error to
   another classify function is no path.

   The known list of each adapter holds its uncoded error paths today, in
   `internal/errcode/testdata/uncoded/<adapter>.txt`. The test fails for a
   new uncoded path. It also fails for a path on a known list that has a
   code now, or that the source no longer has. Each list has a line
   `limit <n>`, the number of its paths. The test fails when the list is
   longer or shorter than its limit. So a change that gives a path a code
   also lowers the limit, and a higher limit is a line of the diff that a
   review sees. The limit is in the file of the list, so that two groups
   of the migration change different files. When each list is empty, the
   known lists go away.

   The scan finds a body literal only when its type is an error body of
   the adapter. So a further test reads each struct type of a gated
   adapter. It fails for a type with a field whose JSON name is `code`,
   `errorCode` or `errors` that is neither an error body of the adapter
   nor on a short list of other types, each with a reason. A request body
   with a field `code` is an example of such another type.

   A second test fails for a directory of `internal/api` that is not
   gated and is not on the ungated list with a reason. The ungated list
   holds the API description, the helpers of the adapters and the OpenTofu
   HTTP backend. A third test fails for a subdirectory of a gated adapter
   that holds Go source other than tests, unless the ungated list names it
   with a reason.
9. **The codes are in the API description (owner).** The schema of
   `errorCode` in the API description lists every code of the catalog.
   The description also declares the arguments of each code, with their
   kinds. The TypeScript client gets the codes as a union type, and the
   arguments of each code as a type.
10. **A code is a contract (owner).** A code never changes its meaning. The
    frozen lists of shipped codes of ADR 182 decision 7 hold the codes and
    the arguments that `main` sent before the catalog, and those that each
    tag sends. Each line holds a code, then the name and the kind of each
    argument.

    - `internal/errcode/testdata/shipped/` holds the frozen lists. The
      first list, `main.txt`, holds the 80 codes that `main` sends before
      the catalog. A test pins the SHA-256 of each file, as the test of the
      frozen migrations does. It fails when a file changes, when a file is
      removed, and when a file has no pinned hash.
    - A second test fails when a code of a frozen list is not in the
      catalog, and when the catalog does not give the code each argument
      of its line with the same kind. The catalog may give a code more
      arguments than its line, because decision 3 lets a code gain
      arguments. So the test enforces that a shipped code is never removed
      or renamed, and that it never loses an argument or changes the kind
      of an argument. It cannot test the meaning of a code: the review
      does that.
    - Each area has a golden file, `internal/errcode/testdata/catalog/<area>.txt`,
      for the codes of the area that no frozen list holds. A file is
      created, empty, together with `catalog_<area>.go`. A third check is two
      tests. One fails for a code that is in neither a frozen list nor the
      golden file of its area, for a line that differs from the catalog, and
      for a line in the file of another area. The other fails for an area
      without its golden file. So two changes to two areas touch different
      files.
    - At each tag, the lines of the golden files go into a new frozen list
      `shipped/<tag>.txt` with its pinned hash, and the golden files become
      empty.

## Consequences

- The design pull request adds this ADR, the catalog types, every code that
  `main` sends today, `sign-in.refused`, `access.no-action` and
  `server.internal` in the catalog: 83 codes. It adds the frozen list of
  the 80 shipped codes, the golden file of each area, the gate and the
  known lists. It changes no adapter, except that the three calls of
  `errcode.Refusal` now give a code.
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
