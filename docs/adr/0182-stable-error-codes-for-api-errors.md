# 182. Stable error codes for API errors

Status: accepted · 2026-10-18. Accepts the "Errors from the server" section of RFC 11. Route paths superseded in part by ADR 210

## Context

The API sends error text in English, in STE (ADR 14). The CLIs of the tools
show this text to their users as it is. ADR 181 translates the console. The
console must not show English server text in a page in another language, and
it must not find the meaning of an error from its English text. On 18
October 2026 the project owner decided how the console gets the meaning of
an error. This ADR records that decision with the label "(owner)", and the
design decisions that follow from it.

Facts:

- Each API has its own error shape. The tool-neutral APIs and the Pulumi
  Cloud-compatible API answer `{"code": <status>, "message": "..."}`. In
  these shapes, `code` is the HTTP status, not a machine code. The
  TFE-compatible API answers JSON:API errors with `status`, `title` and
  `detail`.
- JSON:API defines an optional `code` member and an optional `meta` member
  for an error object.
- The tool-compatible APIs must match what each supported version of the
  real CLI expects. Before 1.0, the tool-neutral APIs can change (ADR 73).
- Each HTTP adapter maps domain sentinels to a status and a message in its
  `classify` functions.

## Decision

1. **A stable machine code for each error (owner).** Each error that an API
   answers has a machine code. A code is a contract: a code that a tag
   shipped is never removed or given another meaning. A new kind of error
   gets a new code. A code is 3 to 64 characters and matches the regular
   expression `^[a-z][a-z0-9]*(-[a-z0-9]+)*(\.[a-z][a-z0-9]*(-[a-z0-9]+)*)+$`.
   A code is two or more segments that dots join. It has the form
   `<area>.<reason>`, for example `workspace.locked`, `stack.not-found` or
   `request.field-invalid`.

   The OpenTofu HTTP backend API (`internal/api/tofuhttp`) is out of scope.
   OpenTofu fixes its error shapes: plain text, and a fixed body for the
   lock answer (status 423). This ADR adds no field to them.
2. **The console never parses English text (owner).** The console selects
   its message from the code. It never reads the meaning of an error from
   the `message`, the `title` or the `detail`. A message in the message file
   of ADR 181 for a code can use the arguments of decision 4.
3. **The field in each shape.**

   | API | The code | The arguments |
   | --- | --- | --- |
   | Tool-neutral APIs (`/api/server`, `/api/inventory` and the other tool-neutral routes) | `errorCode` | `errorArgs` |
   | Pulumi Cloud-compatible API | `errorCode` | `errorArgs` |
   | TFE-compatible API | the JSON:API `code` member | `meta.args` |

   The existing fields keep their names and their meaning. `code` in the
   camelCase shapes stays the HTTP status. The new fields are additions. A
   change to a tool-compatible API lands only after `make e2e` shows that
   each supported CLI version accepts the added fields.
4. **Arguments, not text.** When the English text holds a name that this
   decision allows in an argument, the arguments hold it too, as a JSON
   object. An example is the name of a stack or of a field. The registry of
   decision 7 declares the argument keys of each code and the type of each
   key. The keys are camelCase and part of the contract of the code. A test
   rejects an error that has a key that its code does not declare. These
   limits apply:

   - An error has at most 8 keys.
   - A string is at most 256 characters.
   - A number is an integer within ±(2^53 − 1).
   - An ID is a string.

   An argument holds the name of a field, or the name of an object that the
   path names or that the store holds. It never holds a value from the
   request body. When the English text holds a value from the request body,
   the arguments name the field only. Arguments never hold a secret, a
   token or state. The rules of ADR 32 for error text apply to them too.

   An argument has one of two kinds, and the registry declares the kind:

   - An identifier is a string, for example the name of a stack or of a
     field. The console shows it as it is, in monospace, with
     `translate="no"` (ADR 181 decision 13).
   - A quantity is an integer, for example a count or a limit. The console
     formats it with `Intl.NumberFormat`, in the text font. A message can
     use it with ICU `plural` (ADR 181 decision 10).
5. **The server text stays English.** The server never translates. It
   ignores `Accept-Language`. The `message`, the `title` and the `detail`
   stay in English STE, because the CLIs show them and operators search for
   them. Logs stay in English.
6. **An unknown code.** The console shows the English text of the server
   for a code that it does not know, with `lang="en"` on its element. An
   error with no code, from a proxy or from an older server, is the same
   case. A 500 that no `classify` function handles has the code
   `server.internal`. A classified 500 keeps its own code. The text of a 500
   says nothing more than ADR 32 allows.
7. **One registry of codes.** One registry in `internal/` (not in a shared
   domain package) declares each code, its meaning and its arguments with
   their kinds. Each adapter declares the subset of codes that it uses, next
   to its `classify` functions. Tests fail on:

   - a classified error without a code,
   - two meanings for one code, in a test that runs across all APIs of this
     ADR,
   - an argument key that the code does not declare,
   - a code that a tag shipped and the registry does not have, or has with
     another meaning, checked against the frozen list of shipped codes.

   The frozen list holds the codes of each tag, in the way that
   `TestFrozenMigrations` holds the SHA-256 of each migration file. It is in
   `internal/errcode/shipped/`, with one file for each tag. The tests
   cover the tool-neutral APIs, the Pulumi Cloud-compatible API and the
   TFE-compatible API. They do not cover the OpenTofu HTTP backend API
   (decision 1). `docs/reference/` gets the list of codes, with the meaning
   and the arguments of each code.

## Consequences

- The "Outcome" section of RFC 11 names this ADR, with ADR 181.
- The integrator adds the codes to each `classify` function, the arguments
  to the errors that name a value, the test of decision 7 and the reference
  page. The tool-neutral APIs come first, because the console uses them
  most. The Pulumi Cloud-compatible API and the TFE-compatible API follow,
  each with its e2e step.
- `OpenDustpan/console` gets these changes. This ADR does not change that
  repository.
  - The runtime validation of each API response accepts `errorCode` and
    `errorArgs`, or `code` and `meta.args` for JSON:API.
  - The error component selects its message by the code, with the English
    server text as the fallback of decision 6.
  - The message files of ADR 181 get one message for each code that the
    console shows.
- A high-stakes error, for example an error of a security check, follows
  the review rule of ADR 181 decision 20.
