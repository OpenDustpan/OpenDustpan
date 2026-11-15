## Context

A runner operator runs runners from one docs page. The server already provides pools, join tokens, drain, and retire. This change adds one docs page and one docs test and changes no server behavior and no schema.

## Decisions

1. **One runner operator page.** Add one runner operator docs page that shows each step in order with copy-paste API calls and no new CLI command.
2. **Docs only, no behavior change.** Document only the routes and error codes proven in the pack. Change no request shape, auth, or stored data.

## Structure

No new type and no new field. The page order makes an out-of-order step impossible: create pool, get join token, start runner, scope to namespaces, watch claims and logs, drain and retire.

## Risks

- The page shows a call that does not run as written.
- The page names a route or error code that does not exist on main.

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| docs commands | 100 | percent runnable as written | runner operator docs page |

## Threat model

Data flow and trust boundaries:

```mermaid
flowchart LR
  subgraph operator[Operator host]
    page[Docs page commands]
    tokenfile[Token file]
  end
  subgraph server[Server boundary]
    poolapi[ANY /api/runners/{}/pools]
    protocol[ANY /runner/v1]
  end
  page -->|access token over TLS| poolapi
  tokenfile -->|join token in body| protocol
```

| Boundary | STRIDE | Threat | Layer | Guard | Hostile test |
| --- | --- | --- | --- | --- | --- |
| Operator host to server | I | A join token or access token leaks through shell history or a log. | boundary check | The page names only the token file path and the server accepts only a valid token. | TestDocsRunnerGuideCommands fails when a command holds a token value. |
| Operator host to server | I | A join token or access token leaks through shell history or a log. | encode or contain | The page sends the token in the request body, never in the URL. | TestDocsRunnerGuideCommands fails when a URL holds a token. |
| Operator host to server | I | A join token or access token leaks through shell history or a log. | limit | A join token is single use, so a leaked used token joins no runner. | Join with a used token returns runner.token-used. |
| Operator host to server | I | A join token or access token leaks through shell history or a log. | authorize at point of use | The server checks the grant for each pool route. | A pool call with no grant is refused. |
| Operator host to server | I | A join token or access token leaks through shell history or a log. | fail closed | An expired or revoked token returns runner.invalid and joins no runner. | Join with a revoked token returns runner.invalid. |
