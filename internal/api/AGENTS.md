# HTTP adapters: instructions for AI coding agents

These rules add to the rules in the root `AGENTS.md`. They do not replace them.

## The tool-compatible APIs are contracts

The real CLIs depend on the tool-compatible APIs, and we cannot change the
CLIs. Until 1.0, everything else can break (ADR 73).

- The Pulumi Cloud-compatible API, the TFE-compatible API and the OpenTofu
  HTTP backend must match what each supported version of the real CLI sends
  and expects. Never break them. Check each change with `make e2e`.
- Each API keeps the shapes its tool expects. The Pulumi Cloud-compatible API:
  camelCase JSON, errors `{"code": <status>, "message": "..."}`. The
  TFE-compatible API: JSON:API documents and errors.
- Before 1.0, the console and the tool-neutral APIs can change and break.
  Do not deprecate first. When a change breaks an API that the console uses,
  merge the console change before the next server tag. Between the two
  merges, `main` of either repository can be broken.
- Make every endpoint declare its authorization actions and the object of
  each action. Make every change pass the policy check. A route without an
  action fails the route test. Put routes that need no sign-in on the explicit
  public list in that test.
- Until the first server tag, do not change `info.version` in the API
  description or `internal/api/apidesc/openapi.pin`. The release sets both.
  `make pin-frozen` checks this.
