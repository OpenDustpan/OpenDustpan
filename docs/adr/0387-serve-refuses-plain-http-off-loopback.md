# 387. `serve` refuses plain HTTP on an address that is not loopback

Status: accepted · 2026-11-20. Decided by the project owner (owner, 2026-11-20). The owner reversed the warn-only start of ADR 27 decision 3 for `serve`

Changes:

- Changes ADR 27 decision 3: `serve` refuses plain HTTP off loopback without TLS, an `https` public URL or `--allow-plain-http`. The opt-in warns.
- Changes ADR 238 decision 6: the `doctor` check of plain HTTP off loopback fails, not warns. The address stays `:8080`.
- Changes ADR 238 decision 10: the row of the listen address in the table of `doctor` gives `fail`. A new row gives `warn` for the opt-in.
- Changes ADR 246 decision 21: the chart needs an `https` `server.publicURL`, `server.tls.existingSecret` or `server.allowPlainHTTP`.

## Context

ADR 27 decision 3 says that the server warns at start when it serves plain
HTTP on an address that is not loopback, and does not refuse to start,
because the Pulumi CLI works over plain HTTP. ADR 238 decision 6 keeps the
default listen address `:8080`, because a container and a host behind a
proxy both need an address that is not loopback. Its decision 10 gives the
`doctor` check of that case the result `warn`. ADR 237 decisions 15 and 16
make `dev` listen on loopback by default and serve plain HTTP, with a banner
line and a log line for an address that is not loopback.

So a production `serve` with the default address and no TLS sends the admin
token, access tokens and state over the network with no encryption. A log
line is the only signal. The project wants layers of defense for security
code (ADR 250).

On 19 October 2026 the owner asked for a secure default on issue #1073:
`serve` refuses, `dev` keeps warn-and-serve. On 20 November 2026 the owner
decided the exact rule. Benchmark: Keycloak `start` refuses HTTP without an
explicit option, and `start-dev` allows it.

## Decision

1. **`serve` refuses plain HTTP off loopback (owner, 2026-11-20).** `serve`
   refuses to start when the listen address is not a loopback address and
   none of these holds:
   - The server has its own TLS (`--tls-cert` and `--tls-key`).
   - `--public-url` is an `https` URL. This says that a proxy or a load
     balancer in front ends TLS. It needs no new flag.
   - The operator sets the opt-in `--allow-plain-http`
     (`DUSTPAN_ALLOW_PLAIN_HTTP`, configuration file key `allow-plain-http`).
2. **One message names the three fixes (owner, 2026-11-20).** The refusal is
   one message. It names the flag and the variable of the listen address, and
   the three fixes with their flags and variables.
3. **`dev` keeps warn-and-serve (owner, 2026-11-20).** ADR 237 decisions 15
   and 16 stay. `dev` does not read `--allow-plain-http`.
4. **The opt-in still warns.** With `--allow-plain-http` on an address that
   is not loopback, `serve` logs a warning at each start that names the flag.
5. **A loopback address.** A loopback address is the name `localhost` or a
   loopback IP address. An address with no host, for example `:8080`,
   listens on every interface and is not loopback. Another host name is not
   loopback.
6. **Two checks.** `serve` checks the settings before it opens the
   database, after the checks of the key and the database. So a person with
   no setting still gets the hint for `dev` first. `serve` then binds the
   address and checks the address that the listener holds with the same
   rule, so a name that resolves to an address that is not loopback does not
   pass.
7. **`doctor` agrees with `serve` (owner, 2026-11-20).** `doctor` runs the
   same rule. In the case that `serve` refuses, the check fails with the
   same text. With the opt-in, it warns.
8. **The Helm chart needs a choice (owner, 2026-11-20).** The chart listens
   on the pod address. It refuses an install that sets none of these:
   `server.publicURL` with `https`, `server.tls.existingSecret`, or
   `server.allowPlainHTTP: true`. `server.allowPlainHTTP` sets
   `DUSTPAN_ALLOW_PLAIN_HTTP`, and the schema refuses that variable in
   `server.extraEnv`.

## Consequences

- An operator who runs `serve` on `:8080` with no TLS and no `https` public
  URL must change the configuration. No tag shipped, so no upgrade path is
  necessary (ADR 73).
- The image command keeps `--listen :8080`. A container with plain HTTP
  needs `DUSTPAN_ALLOW_PLAIN_HTTP=true`, TLS or an `https` public URL.
- The check reads `--public-url` for each component, also for a process that
  runs `worker` without `api`, because that process listens too.
- ADR 27, ADR 238 and ADR 246 get the reverse items.
