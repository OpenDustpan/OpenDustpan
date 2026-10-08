# 5. The web console is built and hosted separately from the API

Status: accepted · 2026-10-08

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
- Manual installs may use separate hostnames (for example
  `dustpan.acme.internal` and `dustpan-api.acme.internal`); the API allows
  configured console origins through CORS with credentials. Same-site
  hostnames are recommended so a session cookie works once real login exists.

## Consequences

- To verify: how the Pulumi CLI derives the console URL for `pulumi login` and
  "view in browser" links when using a custom backend. The deployment docs
  must recommend hostnames (or an override) that match.
- The interim console sign-in stores an access token in sessionStorage. It must
  be replaced by a proper login flow with an HttpOnly session cookie.
