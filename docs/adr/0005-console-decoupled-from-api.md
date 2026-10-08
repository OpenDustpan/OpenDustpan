# 5. The web console is built and hosted separately from the API

Status: accepted · 2026-10-08 · repository layout amended by ADR 8 · console URL findings added 2026-10-08

## Context

Embedding the console in the Go binary (`go:embed`) would force one hosting
model on everyone.

## Decision

- The console (`web/`) is a static single-page app with its own build and
  release artifact. Any web server, CDN or bucket can host it.
- It reads `/config.json` at load time (`apiBaseUrl`), so one build works for
  any deployment. Nothing is baked in at build time.
- The console uses the same API as the Pulumi CLI, plus whatever console-only
  endpoints become necessary.
- For one-process installs (the omnibus container image or package),
  `opendustpan serve --console-dir <dir>` serves a built console from disk at
  `/` next to the API at `/api`. Coupling is a packaging concern.
- Manual installs may use separate hostnames; the API allows configured
  console origins through CORS with credentials. Same-site hostnames are
  recommended so a session cookie works once real login exists. The hostnames
  must follow the `api.` / `app.` convention below, so use for example
  `api.dustpan.acme.internal` and `app.dustpan.acme.internal`. (The original
  example, `dustpan.acme.internal` and `dustpan-api.acme.internal`, does not
  work with the CLI; corrected 2026-10-08.)

## Consequences

- The deployment docs must recommend hostnames (or an override) that match how
  the CLI derives the console URL. See "Console URL derivation" below.
- The interim console sign-in stores an access token in sessionStorage. It must
  be replaced by a proper login flow with an HttpOnly session cookie.

## Console URL derivation

Verified 2026-10-08 by reading the CLI source (`CloudConsoleURL` in
`pkg/backend/httpstate/client/console.go`, identical in v3.200.0, v3.250.0 and
v3.268.0) and by running those versions against this server (`make e2e`).

The CLI computes the console URL from the backend URL alone. Nothing in the API
tells it where the console lives, so the server cannot influence the result.
The first matching rule wins:

1. The CLI's own environment sets `PULUMI_CONSOLE_DOMAIN`: the host is replaced
   by that value, port included. The scheme of the backend URL is kept.
2. The backend host starts with `api.`: that prefix becomes `app.`; scheme and
   port are kept.
3. The backend host is exactly `localhost:8080`: it becomes `localhost:3000`.
4. Otherwise there is no console URL, and the CLI leaves links out.

Observed with the e2e suite (all three versions behave the same):

| Backend URL | Environment | `pulumi login` prints |
|---|---|---|
| `http://127.0.0.1:<port>` | none | `Logged in to http://127.0.0.1:<port> as admin ()` |
| `http://api.dustpan.test` | none | `... as admin (http://app.dustpan.test/admin)` |
| `http://127.0.0.1:<port>` | `PULUMI_CONSOLE_DOMAIN=console.example.test:3000` | `... as admin (http://console.example.test:3000/admin)` |

Where the console URL is used (from the source; the update links are not yet
exercised by e2e because there is no update cycle):

- `pulumi login` shows `<console>/<user>`; with no console URL
  the parentheses are empty.
- `pulumi up` and `preview` print a permalink to `<console>/<org>/<project>/<stack>/updates/<n>`
  (previews: `.../previews/<id>`) and omit it when there is no console URL.
- Interactive `pulumi login` without a token offers browser login when a
  console URL exists: it opens `<console>/cli-login?cliSessionPort=...&cliSessionNonce=...`
  and waits for the console to redirect to `http://127.0.0.1:<port>/?accessToken=...&nonce=...`,
  then lands on `<console>/welcome/cli`. It also links `<console>/user/settings/tokens`.
  With no console URL it asks the user to paste a token instead.
  `PULUMI_ACCESS_TOKEN` (what the e2e suite uses) skips all of this.

Consequences for deployment:

- Recommended layout: API at `api.<domain>`, console at `app.<domain>`. Every
  CLI then finds the console with no configuration.
- Any other layout, including the one-process omnibus install on a single
  host, needs `PULUMI_CONSOLE_DOMAIN` set on every developer machine and CI
  runner, or users get no links. The variable is global to the CLI: it also
  applies to any other backend that CLI talks to. It must include the port when
  the console is not on the default one.
- The console must serve these paths for the links to land anywhere:
  `/<org>/<project>/<stack>`, `/<org>/<project>/<stack>/updates/<n>`,
  `/<org>/<project>/<stack>/previews/<id>`, `/<user>`, `/user/settings/tokens`,
  `/welcome/cli` and, for browser login, `/cli-login`. That is a requirement on
  the console repository (ADR 8).
- Browser login needs the identity work in ADR 6 first. Until then the
  supported flows are `PULUMI_ACCESS_TOKEN` and pasting a token.
