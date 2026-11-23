## Context

The command is a client of `GET /api/dustpan/v1/namespaces/{namespace}/runner-pools`. It follows the pattern of the existing commands: a `Command` row with `SettingsFor("server", "ca-file", ...)` and a `Run` function.

## Decisions

- The command reads the pool list only. It adds no route, so the route test and the API description do not change.
- It reads the namespace from `--namespace`. A missing flag is a usage error that names the flag.
- It sorts the rows by name for exact, scriptable output.
- The server answer is validated before print: an unknown shape is an error, not partial output.
- Client-side refusals (a missing `--namespace`, an invalid flag value, an unknown answer shape) are not audited, by design. The refusal happens in the CLI process before any request leaves it, so no action takes place and no authority is used. The CLI has no audit path, and a client-side record could be skipped by any other client. The server audits every request it refuses, so each denial of an action has a record where the authority sits. Each refusal names the flag or the shape and still has its own test.

## Contract

Input: `--namespace <ns>`, optional `--output json`. Output on success: one line for each pool, columns separated by one tab. Exit 0. On a non-2xx answer: the server message on stderr, exit 1.
