# 182. Stable error codes for API errors

Status: accepted · 2026-10-18 · accepts the "Errors from the server" section of RFC 11

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
- Each HTTP adapter maps domain sentinels to a status and a message in one
  `classify` function.

## Decision

1. **A stable machine code for each error (owner).** Each error that an API
   answers has a machine code. A code is a contract: it never changes its
   meaning, and a release never removes it. A new kind of error gets a new
   code. A code is 3 to 64 characters of `a-z`, `0-9`, `.` and `-`, in the
   form `<area>.<reason>`, for example `workspace.locked`,
   `stack.not-found` or `request.field-invalid`.
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
4. **Arguments, not text.** A value that the English text holds, for
   example the name of a stack or of a field, is also in the arguments, as
   a JSON object of strings and numbers. The keys are camelCase and part of
   the contract of the code. An argument is a machine-made value, so the
   console shows it as it is, in monospace (ADR 181 decision 13). Arguments
   never hold a secret, a token or state. The rules of ADR 32 for error
   text apply to them too.
5. **The server text stays English.** The server never translates. It
   ignores `Accept-Language`. The `message`, the `title` and the `detail`
   stay in English STE, because the CLIs show them and operators search for
   them. Logs stay in English.
6. **An unknown code.** The console shows the English text of the server
   for a code that it does not know, with `lang="en"` on its element. An
   error with no code, from a proxy or from an older server, is the same
   case. A 500 answer has the code `internal`, and its text says nothing
   more than ADR 32 allows.
7. **One list of codes.** Each adapter declares its codes in one place, next
   to its `classify` function. A test fails on a classified error without a
   code, on two meanings for one code, and on a code that the list of a
   released version has and the current list does not have. `docs/reference/`
   gets the list of codes, with the meaning and the arguments of each code.

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
