## Context

The command is a client of `GET /api/dustpan/v1/namespaces/{namespace}/runner-pools`. It follows the pattern of the existing commands: a `Command` row with `SettingsFor("server", "ca-file", ...)` and a `Run` function.

## Decisions

- The command reads the pool list only. It adds no route, so the route test and the API description do not change.
- It reads the namespace from `--namespace`. A missing flag is a usage error that names the flag.
- It sorts the rows by name for exact, scriptable output.
- The server answer is validated before print: an unknown shape is an error, not partial output.

## Contract

Input: `--namespace <ns>`, optional `--output json`. Output on success: one line for each pool, columns separated by one tab. Exit 0. On a non-2xx answer: the server message on stderr, exit 1.
