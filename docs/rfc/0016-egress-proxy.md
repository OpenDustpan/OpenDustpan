---
rfc: 16
title: Job proxy, with request rules and credential injection
status: discussing
adr: 327
created: 2026-10-10
---

# RFC 16. Job proxy, with request rules and credential injection

Status: discussing, accepted in part (ADR 327). The owner decided on 2026-10-18 that a research
spike comes first, and that the names are "job proxy", "outbound guard" and
"upstream proxy" (see `GLOSSARY.md`). The owner accepted the recommendation
of the spike on 2026-10-26, kept all of this RFC in milestone 6 on
2026-11-01, and chose the word "proxy rule" on 2026-11-01 (D4). ADR 327
records the parts of the decisions of the section "Proposal (2026-10-30)"
that have an owner source. The section "Proposed (PM), waiting for the
owner" holds the rest, D5 included. The section "Open questions" lists the
questions that stay open. The example rules below say "egress rule", the
word of the first draft.

**This is an escape hatch, not a default.** When a product has native RBAC or
ABAC and short-lived credentials (OIDC, dynamic credentials), use those first:
they are safer than any proxy. The job proxy is for the products that do
not have them. Teams will configure those products with IaC anyway, even if
it means pasting a root token somewhere. The proxy makes that safer. It does
not make it safe.

## Problem

Many products that teams manage with OpenTofu or Pulumi have a weak access
model. The community or open-source edition of a product often has no RBAC: one
API key with admin access, or one root token. A job (RFC 6) that manages one
small part of such a product gets that key, and with it the power to change
everything in the product.

Two things follow:

- **No least privilege.** A unit for team A's dashboards can delete team B's
  dashboards. A preview can write. The provider cannot help, because the
  product gives it nothing narrower than the admin key.
- **The key spreads.** The runner passes the key to the tool. Any code in the
  job (a Pulumi program, a provider, an `external` data source, a job step)
  can read it, print it, or send it anywhere, and use it after the job ends.

A third case is transport security that the job cannot carry well. A
self-hosted service that accepts only mTLS clients, for example clients that
prove a SPIFFE identity with an X.509 certificate, needs a client certificate
and key in the job. On a shared CI runner that is hard to
set up and unsafe: the key gives access to the admin endpoints, and many
providers cannot verify a server certificate that carries a SPIFFE ID instead
of a DNS name.

Clouds and products with a strong access model (AWS IAM, Google Cloud IAM,
Azure RBAC, any product with scoped tokens through OIDC) do not need this RFC.
Teams use narrow roles there, and a role per phase for read-only previews
(RFC 5). This RFC does not try to rebuild their access models, and the docs
and the console should say so where a team turns the proxy on.

## When to use it

1. The product has native RBAC or ABAC with short-lived credentials: use that.
   Do not use the proxy.
2. The product has scoped, long-lived tokens: use the narrowest token, in a
   profile (RFC 5). The proxy is optional.
3. The product has one admin key or root token, or needs mTLS that the
   provider cannot do: use the proxy for the units that touch it.

## Proposal

A **job proxy** is an opt-in feature for a unit. It sits between a job and the network. The job can reach only
the proxy, and trusts the proxy's CA for that job. The proxy:

1. allows a request only when an **egress rule** for this unit and this
   operation matches it, and denies everything else,
2. adds the real credential to allowed requests, so the job never holds it,
3. can open mTLS, or trust a private CA, towards the endpoint.

In effect, the proxy puts an access model in front of a product that has none,
for the requests that a job makes.

### The smallest useful version

To keep this from growing into a product of its own, the first version is
only:

- a proxy next to each job, on units that have egress rules,
- rules on host, method and path, with templates, deny by default,
- header credentials released to the proxy only,
- a client certificate towards the endpoint,
- an audit record per request.

Everything else in this RFC (body matching, SPIFFE, the endpoint mode, a
shared gateway, learn mode and the rule tester) is a later step, each only
when someone needs it.

### Egress rules

A rule says: for these units, during these operations, these requests are
allowed, with this credential. The example below only shows what a rule must
be able to say. It is not a proposed format (see "How people write rules").

```yaml
egress:
  - units: ["opentofu:acme/tenants/*"]
    operations: [plan]
    allow:
      - host: grafana.acme.internal
        methods: [GET]
        paths:
          - /api/folders/{{ inputs.team }}
          - /api/folders/{{ inputs.team }}/**
          - /api/search
    credential: grafana-admin

  - units: ["opentofu:acme/tenants/*"]
    operations: [apply]
    allow:
      - host: grafana.acme.internal
        methods: [GET, POST, PUT, DELETE]
        paths:
          - /api/folders/{{ inputs.team }}
          - /api/folders/{{ inputs.team }}/**
    credential: grafana-admin
```

- **Match:** host, method, path pattern (`*` for one segment, `**` for the
  rest), and optionally query parameters and headers. The first rule that
  matches allows the request. No match denies it.
- **Templates:** a pattern can use a fixed set of variables: the unit
  reference and its parts, the operation, the unit's labels, the inputs of a
  blueprint (RFC 14), and plain values from the unit's profile (RFC 5). With
  blueprints, a platform team writes the rules once, and each team's unit gets
  rules for its own resources only.
- **Read-only previews:** a rule for `plan` and `preview` that allows only
  `GET` and `HEAD`. This works for REST APIs where the method tells a read from
  a write. It does not work for APIs that read with `POST` (GraphQL, search
  endpoints): those need a rule on the path or the body, and the console says
  so.
- **No rules, no change.** A unit without egress rules works as today, with no
  proxy. The feature is off unless a team turns it on for a unit.

### Two ways to reach the proxy

- **Forward proxy.** The tool uses `HTTPS_PROXY` as usual, and the proxy ends
  the tool's TLS with a CA that the job trusts. This works for any host
  without changes to the tool's configuration.
- **Endpoint.** The provider is configured with the proxy as its endpoint
  (for example `localhost:9443`), and the proxy
  forwards to the real service. The tool needs no CA trust when the hop to
  the proxy stays on the local network namespace, and needs no proxy support
  at all. This suits gRPC and other clients that handle `HTTPS_PROXY` poorly.

### gRPC

The proxy speaks HTTP/2, so gRPC works in both ways above. A gRPC call is a
`POST` to `/<package>.<Service>/<Method>`, so a path rule allows single
methods: for example `/example.v1.ReadService/*` is allowed, while
`/example.v1.AdminService/*` stays denied. The arguments are in the protobuf
body. By default the proxy does not read bodies, so a rule cannot limit a call
by its arguments (see "Request bodies"). The service's own authorization can,
using the identity below.

### TLS identities and SPIFFE

Towards the endpoint, the proxy can present a client identity and check the
server's identity:

- **A client certificate from a profile** (RFC 5), released to the proxy only.
- **A SPIFFE identity.** The proxy gets an X.509 SVID from the SPIFFE Workload
  API (for example a SPIRE agent on the runner) and rotates it. Workload
  attestation can give the SVID to the proxy's container and not to the
  tool's. Each unit can get its own SPIFFE ID (for example
  `spiffe://example.org/opendustpan/opentofu/acme/network`), so the
  service's own authorizer can tell units apart.
- **Server checks by SPIFFE ID or a private CA**, not only by DNS name, for
  services whose providers cannot do this.

The tool never holds the key. The egress rules still limit which calls the
identity can make through this job.

### A generic rule engine, no assumptions about providers

The proxy is a rule engine over requests. It knows HTTP, HTTP/2, gRPC framing
and TLS. It knows nothing about any provider, product or cloud API, and
OpenDustpan ships no built-in rules for one. Everything specific to a
product is in the rules that a team writes.

### Request bodies

Bodies are opaque by default. A rule can opt in to match on the body, with
great care and only in generic ways:

- **Generic formats only:** fields of a JSON body (by path), form fields, and
  protobuf fields only when the team supplies the message descriptors. Never
  logic for one product.
- **Limits:** a maximum body size that the proxy buffers; a request over it,
  or a body that does not parse, is denied, never passed unchecked.
  Streaming calls are not matched by body.
- **The same safety rules as paths:** values are normalized before matching,
  and a template value matches one field value exactly.
- **Audit** records which body fields a rule matched, never their values.

### How people write rules (to be decided)

The interface for rules is open. It must say what the example above says,
and it should be pleasant to write, read and debug. Candidates and ideas:

- **HAProxy's model.** Named conditions (`acl`) that match one thing each
  (method, host, path prefix or pattern), combined in actions with `if`, `!`
  and `or`, evaluated in order. For example, conditions `is_read` and
  `team_path`, then `allow if is_read team_path`. It is proven, compact, and
  known to many operators. Its syntax is terse for people who do not know it.
- **Plain data** (YAML or JSON, as in the example). Easy to validate against a
  schema and to show in the console. Harder to express "and", "or" and "not".
- **A policy engine** (RFC 13). Most power, least readable for this narrow
  job. How policies apply to rules (whether a policy can add, narrow or
  override a rule) is also to be decided.
- **UX ideas, whatever the format:**
  - a **learn mode**: run a job with "allow all and record", then propose
    rules from the requests it saw, grouped by host and path,
  - **"Create rule from this"** on a denied request in the job's log,
  - a **rule tester** that replays recorded requests of past jobs against a
    changed rule set and shows what would now be denied, like the policy
    samples in Spacelift's workbench,
  - a **preview of the rendered rules** for one unit, with its template values
    filled in.

### Making path rules safe

A path rule is an access control, so it must not be easy to get around:

- **Refuse a path that is not canonical.** The first draft said "normalize,
  then match, then send the normalized path". The spike recommends a
  refusal instead (see "Prototype findings"). The proxy refuses a dot
  segment (also when encoded), an empty segment, an encoded `/` or `\`,
  `%00`, `%25`, `;`, and a percent-encoding of a character that needs no
  encoding. So each allowed path has one spelling, and the proxy sends the
  path that it checked with no change. Case stays exact: a rule allows
  `/api`, and `/API` matches no rule. Because rules only allow, a case
  that the rule does not name fails closed.
- **Templates fill one segment.** A template value is encoded as exactly one
  path segment. An input like `team-a/../team-b` or `team-a/**` cannot widen
  the pattern.
- **No redirects to other hosts.** The proxy does not follow a redirect. The
  tool follows it, as a new request, and the rules check it again.
- **Bodies are opaque by default.** A request that changes things through its
  body (a bulk endpoint, GraphQL) is allowed or denied as a whole by its
  method and path, unless a rule opts in to body matching (see "Request
  bodies").

### Credentials that never reach the runner

A credential source in a profile (RFC 5) gets a **release mode**:

- **To the job:** as today. The runner passes it to the tool.
- **To the proxy only:** the runner never receives it. The tool gets a
  placeholder, and the proxy puts the real value in each allowed request:
  a header (`Authorization: Bearer …`, `X-API-Key: …`), Basic auth, or a query
  parameter. Envoy's credential injector does the same for headers.

Credentials that the client must compute per request, such as AWS Signature
Version 4, are out of scope. Those clouds have their own access model.

### Audit

The proxy records each request: job, unit reference, operation, method, host,
normalized path, status, the rule that allowed it or "denied". It never
records bodies or credential values. The console shows these next to the job
log, and a denied request says which host and path, and that no rule matched.
A failed plan must say "blocked by the job proxy", not only show a
provider timeout.

## Where the proxy runs

The backend never runs a tool (RFC 6). The proxy does not run a tool either,
but it holds admin keys and carries production traffic.

1. **Next to each job, on the runner** (proposed default). The runner starts
   the proxy in its own container or process, with its own user. The tool's
   network reaches only the proxy (a network namespace, or a Kubernetes
   NetworkPolicy). The proxy, not the tool, gets the credentials and the rules
   from the backend, for this one job.
2. **A shared egress gateway in the customer's network.** One service for many
   runners, as its own role (RFC 7). It is easier to give fixed egress IPs to,
   but all traffic goes through it, and it holds every team's keys.
3. **In the backend.** Rejected. It would make the backend a path into every
   production network.

## Limits

- **Misuse inside the rules.** Code in the job can still make every request
  that the rules allow, with the injected credential. The proxy narrows what
  that is. It does not judge intent.
- **Rules are only as narrow as the API.** If a product puts many tenants'
  objects under numeric IDs with no tenant in the path, a path rule cannot
  tell them apart. Reads that a provider needs for refresh (lists, searches)
  may need wide rules.
- **Clients that ignore the proxy settings.** Go's `net/http` honors
  `HTTPS_PROXY` and `NO_PROXY`, so most OpenTofu providers and bridged Pulumi
  providers do. Other runtimes may not. Because the job can reach only the
  proxy, such a client fails instead of going around it.
- **Trust stores and pinned certificates.** The runner sets the proxy's CA for
  each runtime (`SSL_CERT_FILE`, `NODE_EXTRA_CA_CERTS`, `REQUESTS_CA_BUNDLE`,
  `AWS_CA_BUNDLE`). A client that pins a certificate cannot go through the
  proxy for that host.
- **Local traffic.** The tool's plugin gRPC and its calls to OpenDustpan go in
  `NO_PROXY`. Plugin and module downloads need rules, or the artifact registry
  of RFC 10.
- **Protocols that are not HTTP** (PostgreSQL, SSH): a rule can allow or deny
  the host and port, without credential injection.

## Options

1. **A proxy next to each job, with request rules per unit and operation,
   templates, and header credentials released to the proxy only** (the
   proposal).
2. **Do nothing.** Jobs get the admin key and the network as today.
3. **Hosts only.** Allow and deny by host, with no TLS interception and no
   credential injection. Simple, and it does not solve the admin-key problem.
4. **Generate configuration for an existing proxy** (Envoy, Smokescreen) from
   the rules. Less code of our own, but a large component to ship, and the
   normalization and template rules above must still hold.

## Glossary

The owner decided the names "job proxy", "outbound guard" and "upstream
proxy" on 2026-10-18. `GLOSSARY.md` holds the three entries. Each says
"Avoid: egress proxy". This RFC used "egress proxy" before that decision.

The owner chose "proxy rule" and "proxy rule set" on 2026-11-01 (ADR 327
decision 18). The entry below is the first draft, kept as the record:

**Egress rule**:
A rule that allows requests from a job, for some units and operations, by
host, method and path, and can name the credential to add.
_Avoid_: ACL, network policy (which is Kubernetes' word), permission

The word "egress" now names the server's own outbound rules (the outbound
guard, `pkg/egress`) and the egress allowlist of model calls. A rule of the
job proxy with the same word can cause confusion. "Proxy rule" is one
option (see "Open questions").

One name stays a proposal:

**Release mode**:
Where a credential source in a profile delivers its credential: to the job,
or to the job proxy only.
_Avoid_: Exposure, visibility

## Prototype findings

The spike of 2026-10-26 built a throwaway job proxy in Go with the standard
library only (about 940 lines with its test program), outside the repository. It used fake
credentials only. A test program started a fake product with one admin
token and no RBAC, a fake upstream proxy that accepts CONNECT only and
needs a proxy credential, and a job client that sends a placeholder token.
Each result below comes from one run on loopback. The prototype is not
code to keep. It answers the questions below, and nothing more.

### What the prototype did

- A forward proxy. For a CONNECT to a host that has a rule, it ends the
  TLS of the job with a leaf certificate from a per-job CA. It then reads
  each request, checks it against the rules, deletes `Authorization`,
  `Proxy-Authorization` and `Cookie`, adds the credential and sends the
  request to the real host with its own TLS.
- A CONNECT to a host with no rule gets 403 before any TLS.
- A host-only tunnel for hosts on a list: no TLS end, no request check and
  no credential.
- The endpoint mode: a loopback port that takes plain HTTP and sends each
  request with TLS to one fixed host.
- A chain to an upstream proxy with CONNECT and a proxy credential.
- A stand-in for the outbound guard on the resolved address of each
  connection that the proxy opens.
- An audit record for each request: mode, method, host, path, rule,
  decision, reason and status. It holds no header, no body and no
  credential.

### Results

| Case | Result |
|---|---|
| `GET` of the team's own folder, plan rule | 200. The product got the real token, not the placeholder. |
| `DELETE` of the same folder, plan rule | 403, no rule matched |
| `GET` of the folder of another team | 403 |
| `**` below the team's own folder | 200 |
| CONNECT to a host with no rule | 403 at CONNECT, no TLS |
| `..`, `%2e%2e`, `%2F`, `//`, `;`, `team-%61` in the path | 400, each one refused before the match |
| `/API/...` where the rule says `/api/...` | 403 (fails closed) |
| `Host` header that names another host than CONNECT | 403 |
| SNI that names another host than CONNECT | TLS handshake refused |
| Template value `team-a/../team-b` | refused when the rule renders |
| Redirect to another host, which the client follows | 403 at the new CONNECT |
| Client that does not trust the job CA | TLS error in the client |
| An allowed path that echoes the `Authorization` header in its body | The job read the real token. With a response scan, the proxy cut the stream. |
| Plain `http` to a host with a credential rule | 403, unless the rule opts in |
| Rule host that resolves to a link-local address | 502, the outbound guard refused it |
| Real host with a certificate that the proxy does not trust | 502 |
| Host-only tunnel | The job got the product's own certificate. The product got the placeholder and answered 401. Each method and path passed. |
| Endpoint mode | 200 over plain HTTP on loopback, with no CA in the job |
| HTTP/2 | h2 from the job to the proxy and from the proxy to the product |
| Chain through the upstream proxy | 200, one CONNECT at the upstream proxy |
| Link-local rule host, through the upstream proxy | The upstream proxy opened the connection. Only the certificate check stopped the request. |
| Upstream proxy refuses the proxy credential | 502 |
| Upstream proxy down | 502 |
| A credential in an audit record | none in 1130 records |

Time on loopback (500 sequential requests with keep-alive, one run, not a
benchmark):

| Path | p50 | p99 |
|---|---|---|
| Job to product, direct | 113 µs | 513 µs |
| Job to job proxy to product | 197 µs | 740 µs |
| Job to job proxy to upstream proxy to product | 188 µs | 677 µs |

A leaf certificate (ECDSA P-256) took 0.2 to 0.45 ms to make, once for each
host in a job. A new TLS connection for each request took a p50 of 1.7 ms
direct and 2.1 ms through the proxy. Against a product on a real network,
the proxy adds less than 1 ms to a request in this run.

### Findings on the rule shape

1. **Host, method and path cover the example of this RFC.** The rules
   allowed the team's folder and denied the rest with three fields and
   two wildcards (`*` and `**`). No case of the spike needed "or" or "not"
   inside one rule. A list of allow rules gave "or".
2. **Refuse, do not normalize.** Normalization needs a choice for each
   product (case, `;` parameters, encoded slashes). A refusal of each
   path that is not canonical is shorter, and it sends the path that the
   rule checked with no change. A product that needs one of the refused
   forms needs a rule option for that form. No case of the spike needed
   one.
3. **Render templates when the job starts, not at each request.** A
   template value that is not exactly one path segment fails the render.
   The job then fails before it starts, with the rule and the variable in
   the error. A failure at each request would show only as a denied
   request in the middle of a plan.
4. **Bind the request to the CONNECT host.** The proxy checks three names:
   the CONNECT host, the SNI and the `Host` header (`:authority` in h2).
   If they differ, the proxy refuses. Else a job can open a tunnel to an
   allowed host and send a request for another host on the same address.
5. **A rule that allows a path that echoes headers gives the credential to
   the job.** Rules cannot know which paths of a product do this. A scan of
   the response for the plain value of the credential found the echo in
   the spike. A product that encodes the value (base64, JSON escape)
   passes the scan. The scan is a second layer, not a guarantee.

### Findings on credential injection

The spike compared four ways:

| Way | Injection for HTTPS | CA in the job | Changes to the tool | Notes |
|---|---|---|---|---|
| A. CONNECT and TLS end (forward mode) | Yes | Yes, a per-job CA | `HTTPS_PROXY` and the CA path | Works for each host and each client that honors `HTTPS_PROXY`. Pinned certificates fail. |
| B. Endpoint mode | Yes | No | The provider's endpoint is the loopback port | Plain HTTP only inside the job's network namespace. A product that sends absolute URLs (pagination links, redirects) sends the job to the real host, which then needs way A or fails closed. |
| C. Injection for plain HTTP only | Only for `http` | No | None | The credential crosses the network in clear text. The spike refuses it unless a rule opts in. |
| D. Host-only tunnel | No | No | `HTTPS_PROXY` | No injection is possible: the proxy sees no request. Each method and path passes. It gives an allowlist of hosts, nothing more. |

So "inject only for hosts on an allowlist, with no TLS end" is not
possible for HTTPS. A proxy can add a header only to a request that it can
read. The useful mix is: way A for hosts with a credential or a path rule,
way D for hosts with a host rule only (registries, mirrors), and way B
where a provider handles a proxy poorly.

### Findings on the upstream proxy

1. **The chain works with CONNECT.** The job proxy opens its outbound
   connection through the upstream proxy and sends the proxy credential in
   `Proxy-Authorization`. The upstream proxy sees one CONNECT for each
   host. It sees no request, no path and no credential of the product.
2. **The outbound guard then sees only the upstream proxy.** With a chain,
   the job proxy resolves and checks the address of the upstream proxy,
   not of the real host. In the spike, a rule host that resolved to a
   link-local address passed the job proxy, and the upstream proxy opened
   the connection. The job proxy must therefore also check the resolved
   address of the rule host before it sends the CONNECT, and must say in
   its documentation that the upstream proxy applies its own rules to the
   final address. This check alone does not stop a DNS answer that
   changes between the two lookups. The upstream proxy is the only layer
   that sees the final address.
3. **The proxy credential of the upstream proxy is a secret of the job
   proxy.** It must follow the same rules as the product credential: never
   in a log, never in an error text, never in the job. The `net/http`
   transport of Go puts it in the URL of the proxy, so an error text that
   prints the URL can leak it. The spike classified errors into a fixed
   set of reasons and recorded no error text.
4. **The tool's own `HTTPS_PROXY` is the job proxy, never the upstream
   proxy.** If the job can reach the upstream proxy, the job can go
   around the job proxy. The network of the job must reach the job proxy
   only.

### Failure modes

| Failure | What the job sees | What the record must say |
|---|---|---|
| No rule for the host | 403 at CONNECT | Host, "no rule for host" |
| No rule for the request | 403 with method, host and path | Rule "none", "no rule matched" |
| Path not canonical | 400 with the reason | The reason, not the path |
| Client does not trust the job CA | TLS error in the tool, often shown as a provider timeout or an x509 error | "client TLS handshake failed". The console must name the job proxy here. |
| Client pins a certificate | Same as above | Same as above |
| Real host certificate not trusted by the proxy | 502 | "endpoint certificate not trusted" |
| Outbound guard refuses the address | 502 | "outbound guard refused the address" |
| Upstream proxy refuses the credential, or is down | 502 | A fixed reason, never the URL of the proxy |
| Credential not available (expired, revoked) | 503 | "credential not available" |
| Response holds the credential | The stream breaks | "response holds the credential" |

The spike found one trap of the Go standard library: `net/http` speaks
HTTP/2 only on a `*tls.Conn`. A wrapper around the connection made the
server answer HTTP/1.1 after ALPN chose h2, and every h2 client
failed. A production proxy needs a test for each protocol through the TLS
end.

### Security trade-offs

- **A TLS-ending proxy needs a CA that the job trusts.** The spike used one
  CA for each job, with a lifetime of the job, a name constraint to the
  hosts of the job's rules, and a key that only the proxy held. A stolen
  key then signs certificates only for those hosts and only for that time.
  A CA for each runner or for each namespace is easier to set up but makes
  a stolen key worth much more. The job gets the certificate, never the
  key. Go honored the name constraint in the spike. Other clients may not.
- **The job must reach the job proxy only.** Without that network rule,
  every rule is advice. The runner, not the job, sets it (a network
  namespace, or a NetworkPolicy).
- **Secrets must never reach logs.** The launcher masks each secret that it
  got (ADR 134 decision 8). With the release mode "to the job proxy only",
  the launcher never gets the value, so the launcher cannot mask it. If a
  product echoes the credential, the value goes to the job log unmasked.
  The response scan of the job proxy is the layer that covers this case.
  The audit record holds no header, no body, no credential and no error
  text from a library.
- **The job can still use what the rules allow.** The proxy narrows the
  admin key to the rules. It does not judge intent. This is the limit
  that the section "Limits" already gives.
- **The proxy holds the credentials and the CA key.** It must run as its
  own user, in its own container or process, with no file system or
  process space in common with the steps.
- **Plain HTTP with a credential is clear text.** Only endpoint mode on
  loopback keeps it inside the job's network namespace.

### Recommendation

The RFC recommends the points below. It does not decide them.

1. Build the job proxy in Go with `net/http` and `crypto/tls`, next to each
   job, as the proposal says. The spike needed no dependency. Generating
   configuration for Envoy stays an option for later.
2. Keep plain data rules on host, method and path, with `*`, `**` and
   templates that render when the job starts. Add conditions with "or" and
   "not" only when a real case needs them.
3. Use way A for hosts with a credential or a path rule, way D for hosts
   with a host rule only, and way B as an opt-in for each rule. Refuse
   way C unless a rule opts in, and then only to loopback.
4. Use one CA for each job, with a name constraint and the lifetime of the
   job. The key stays in the job proxy.
5. Refuse a path that is not canonical. Do not normalize.
6. Check the CONNECT host, the SNI and the `Host` header against each
   other.
7. Apply the outbound guard to the resolved address of each rule host,
   also when an upstream proxy carries the connection. Document that the
   upstream proxy must apply its own rules.
8. Scan each response of a rule with a credential for the plain value of
   that credential, and break the stream on a match.
9. Write each failure as a fixed reason in the audit record, and show
   "blocked by the job proxy" in the job log with the host and the path.

## Proposal (2026-10-30)

This section turns the recommendation of the spike into a design that is
ready to accept. It does not change the decisions of the owner of
2026-10-18 (the spike first, and the three names). Two points are owner
decisions (D4 and D5, after decision 17). The job proxy
is security code (ADR 250), and SSRF through a rule host is in scope.

### Facts from other proxies

Each fact has a tag: **[fetched]** means that the page was read on
2026-10-30, **[not fetched]** means a search snippet only.

- **Squid.** `ssl_bump` with `peek`, `stare`, `splice` and `bump` ends TLS,
  and the `ssl_crtd` helper makes a certificate for each site, signed by one
  CA of the operator. The wiki warns that a stolen CA key allows a
  man-in-the-middle attack on each client that trusts the CA.
  `http_access` rules run in order, and the first match wins.
  <https://www.squid-cache.org/Doc/config/ssl_bump/> [fetched],
  <https://wiki.squid-cache.org/Features/DynamicSslCert> [fetched],
  <https://wiki.squid-cache.org/SquidFaq/SquidAcl> [fetched].
  SQUID-2004:1: the `%xx` decoder could insert a NUL and pass a `url_regex`
  rule. <https://www.squid-cache.org/Advisories/SQUID-2004_1.txt> [fetched].
  Squid is under the GNU General Public License, version 2
  (<https://github.com/squid-cache/squid/blob/master/COPYING>, and the
  sentence "licensed under the GNU GPL" on <https://www.squid-cache.org/>)
  [fetched]. `make trivy` refuses a GPL license for a dependency, so we
  would not ship Squid.
- **Envoy.** The credential injector filter injects a fixed credential or an
  OAuth2 client-credentials token. Its docs say: "This extension is
  functional but has not had substantial production burn time", and "has
  an unknown security posture".
  <https://www.envoyproxy.io/docs/envoy/latest/api-v3/extensions/filters/http/credential_injector/v3/credential_injector.proto>
  [fetched]. `normalize_path` and `merge_slashes` default to false, and
  `path_with_escaped_slashes_action` can refuse `%2F` and `%5C`.
  <https://www.envoyproxy.io/docs/envoy/latest/api-v3/extensions/filters/network/http_connection_manager/v3/http_connection_manager.proto>
  [fetched]. A CONNECT that Envoy ends goes on as raw TCP. HTTP inside it
  needs internal listeners, and the docs give no recipe for TLS inside the
  tunnel. <https://www.envoyproxy.io/docs/envoy/latest/intro/arch_overview/http/upgrades>
  [fetched]
- **Smokescreen (Stripe).** A CONNECT proxy against SSRF. It resolves each
  host and checks that each address is publicly routable, with an ACL for
  each role from the client certificate. It does not end TLS.
  <https://github.com/stripe/smokescreen> [fetched]
- **OWASP SSRF cheat sheet.** "Domain allowlisting alone does not prevent
  DNS rebinding." Bind the connection to the address that the check
  validated, check every address of a name, and do not follow redirects.
  <https://cheatsheetseries.owasp.org/cheatsheets/Server_Side_Request_Forgery_Prevention_Cheat_Sheet.html>
  [fetched]
- **Go.** `crypto/x509` enforces name constraints for each name in a
  certificate since Go 1.10. <https://go.dev/doc/go1.10> [fetched].
  `http.ProxyFromEnvironment` reads `HTTPS_PROXY` and `NO_PROXY`, and
  returns no proxy for `localhost`.
  <https://pkg.go.dev/net/http#ProxyFromEnvironment> [fetched]
- **StepSecurity Harden-Runner.** `egress-policy: block` with
  `allowed-endpoints` blocks each other call of a GitHub Actions job.
  <https://docs.stepsecurity.io/harden-runner> [fetched]

So no product gives the three parts of this RFC together: a TLS end with a
CA for each job, rules on the method and the path that refuse a path that
is not canonical, and a credential that the job never holds.

### Answers to the open questions

| Question | Answer |
| --- | --- |
| The name of a rule | **Owner decision D4.** Recommended: "proxy rule". "Egress" names the outbound guard and the egress allowlist. |
| The interface for rules, and how policies apply | Plain data (JSON), as the spike recommends. A gate never decides on one request. A gate decides on the change, and reads the fact group `proxy` (decision 9). |
| Template variables, and secrets in templates | The unit, the operation, the labels, the answers of a Catalog request, and plain values of a profile. Never a secret value: the render refuses it (decision 4). |
| Our own proxy in Go, or Envoy | Our own proxy in Go, with `net/http` and `crypto/tls`, and no new dependency (decision 1). |
| Who reads the CA key | Nobody but the job proxy. One CA for each claim, in memory, with a name constraint and the lifetime of the claim (decision 6). |
| Encodings in the response scan | Yes: the plain value, base64 (standard and URL, with and without padding), the JSON escape form and the percent form (decision 7). |
| The credential of the upstream proxy | From the runner pool, never from the profile of a unit (decision 8). |
| Client certificates and SPIFFE | A client certificate is a new export of a profile (decision 5). A SPIFFE identity comes from files that a SPIFFE helper of the team writes (decision 15). The SPIRE registration decides a SPIFFE ID, never OpenDustpan. |
| Body formats and size | JSON by JSON pointer, form fields, and protobuf by field number, with a buffer of at most 1 MiB (decision 14). |

### Decisions

1. **One binary, a separate process.** The job proxy is the mode `proxy` of
   `dustpan-launcher` (ADR 134 decision 2): pure Go, static, with no new
   dependency. One release carries it, and the release skew rules of ADR 132
   apply. It always runs as its own process, as its own user, outside the
   sandbox of the steps. It shares no file system, no process space and no
   memory with a step or with the launcher of the steps.
2. **Where it runs, for each isolation kind.**
   - `firecracker` and `gvisor` (the machine coordinator, ADR 137): the
     coordinator starts the job proxy on the host for each claim of a unit
     with proxy rules, as a user of its own. The network namespace of the
     sandbox gets one route, to the address of the job proxy, and the
     coordinator drops every other packet of the sandbox, DNS included. The
     tool needs no DNS: it sends the name in the CONNECT.
   - `kubernetes` (ADR 136): the coordinator creates, for each such claim,
     a second Kubernetes Job, the proxy Job, from
     `opendustpan/launcher:<release>`, and a NetworkPolicy for that claim:
     the job pod may reach only the proxy pod, and the proxy pod accepts
     only the job pod. The coordinator already has the permissions on Jobs
     (ADR 136 decision 4). The proxy pod runs as a service account with no
     role, from the Helm values. The proxy sends its pod address through
     its proxy session, and the launcher reads it through its own session.
     The RBAC change is owner decision D5.
   - A NetworkPolicy works only with a network plugin that enforces it, and
     a drop rule of the machine coordinator can be wrong. So the launcher
     probes the isolation **at each claim**, before the first step: it
     tries to open a TCP connection to the address of the OpenDustpan
     server directly, not through the job proxy, with a time limit of 2
     seconds. If the connection opens, the launcher runs no step, reports
     the phase failed with the reason `network-not-isolated`, and ends with
     the new exit code 72. A probe at each claim leaves no window in which a
     change of the network plugin passes unseen. It costs at most 2 seconds
     for each claim.
   - `process`: no kernel boundary (ADR 137 decision 2). A unit with proxy
     rules never runs on it. The start of such a job on a pool that allows
     only `process` fails with `runner.ErrIsolation`, which names the
     unit, and a runner with `process` isolation never claims it.
3. **One exit.** The job proxy is the only exit of the sandbox. The launcher
   and the tool also reach the OpenDustpan server through it, with a
   built-in tunnel rule (way D) for the host of the server only. So the
   launcher keeps its own TLS check of the server, and no other route
   leaves the sandbox.
4. **Proxy rules are plain data.** A **proxy rule set** is a JSON document
   that a namespace, a blueprint version (field `proxy`) or a unit holds.
   The rule sets of the scopes of a unit are joined. A rule only allows. A
   rule has:
   - `id` (1 to 64 bytes, letters, digits and `-`), and `operations`, a
     list of `plan`, `apply`, `destroy` and `refresh`,
   - `host`, an exact DNS name in lowercase, and `port` (default 443),
   - `mode`: `inspect` (TLS end, way A), `tunnel` (host only, way D) or
     `endpoint` (a loopback port, way B),
   - for `inspect` and `endpoint`: `methods`, and `paths` with `*` (one
     segment), `**` (the rest, last only) and `{{ variable }}` (one whole
     segment),
   - `credential` and `clientCertificate`: names of exports of a profile
     of the unit (decision 5),
   - `serverTrust`: `system` (the default) or the name of a CA export of a
     profile.

   The parser refuses an unknown field, a duplicate key, an IP literal
   host (unless the range is allowed, decision 8), a path pattern that is
   not canonical (the rules of "Making path rules safe"), and a document
   over 64 KiB or 256 rules. Variables: `unit.tool`, `unit.namespace`,
   `unit.name`, `unit.labels.<key>`, `operation`, `inputs.<name>` (a string
   answer of a Catalog request) and `profile.<name>.<key>` (a value that is
   not secret). The job start renders the rules, once. A value that is not
   exactly one valid path segment, or a value marked secret, fails the
   start with `jobproxy.ErrRender`, which names the rule and the variable.
   The job spec holds the SHA-256 of the rendered rules, so each phase
   runs the same rules.
5. **Credentials for the job proxy only.** A profile document gets the
   export key `proxy` (ADR 201 decision 9): a map from a name to a
   `header` (name and value), `basic` (user and password), `query` (name and
   value), `client-certificate` (certificate and key) or `ca`
   (certificates). Only the consumer `job-proxy` gets `exports.proxy`. The
   consumer `launcher` never gets it, and a test checks the answer of each
   consumer. A rule that names an export that no attached profile gives
   fails the job start with `jobproxy.ErrCredential`. This is the release
   mode "to the job proxy only" of this RFC. The tool gets a placeholder,
   for example the value `dustpan-placeholder`, in the variable that the
   rule names.
6. **The CA of a claim.** At start, the job proxy makes an ECDSA P-256 key
   and a CA certificate in memory, with a name constraint to the hosts of
   the `inspect` rules and the lifetime of the claim. It sends the
   certificate, never the key, to the server through its session. The
   launcher reads the certificate through its own session and writes a
   bundle (the roots of the image plus the CA) for the tool steps, named by
   `SSL_CERT_FILE`, `NODE_EXTRA_CA_CERTS`, `REQUESTS_CA_BUNDLE` and
   `AWS_CA_BUNDLE`. `HTTPS_PROXY` and `HTTP_PROXY` name the job proxy, and
   `NO_PROXY` is empty.
7. **Each request.** For a CONNECT, the job proxy:
   1. refuses a host that no rule names, with 403, before any TLS,
   2. for `tunnel`, resolves the host, checks each address with the
      outbound guard, and connects to the address that it checked,
   3. for `inspect`, ends the TLS of the client with a leaf certificate of
      the CA, refuses an SNI or a `Host` (`:authority`) that differs from
      the CONNECT host, refuses a path that is not canonical with 400,
      matches the method and the path against the rendered rules, and
      refuses a request that no rule allows with 403 and the body "blocked
      by the job proxy: <method> <host><path>: no rule matched",
   4. deletes `Authorization`, `Proxy-Authorization`, `Cookie` and each
      header that the credential sets, adds the credential, and sends the
      request with its own TLS to the address that the outbound guard
      checked, with HTTP/2 where the server offers it,
   5. never follows a redirect: the tool follows it as a new request,
   6. scans the response of a rule with a credential for each form of the
      credential (the forms in the row "Encodings in the response scan"
      above), with a window as long as the longest form, and breaks the
      stream on a match.
8. **The outbound guard and the upstream proxy.** The job proxy dials with
   `pkg/egress` rules: it never opens the always-denied addresses, and it
   opens a private address only in a range that the runner pool allows
   (`egress.ParseAllowedRanges`). A pool can name an upstream proxy (a URL
   and a credential). The pool seals the credential with the data key of
   the namespace, and only a proxy session gets it. With an upstream proxy,
   the job proxy still resolves and checks each rule host before the
   CONNECT, and the docs say that the upstream proxy must apply its own
   rules to the final address. An error text never holds the URL or the
   credential of the upstream proxy: the job proxy maps each error to a
   fixed reason.
9. **Gates read a fact.** The fact schemas of `change.propose`,
   `change.planned` and `change.apply` get the group `proxy` (a minor
   version, ADR 226 decision 2): `enabled`, `rules` (the count),
   `ruleSetDigest`, `credentialHosts` (a set of hosts) and `plainHttp`. So
   a gate can deny an apply of a unit with the label `vendor=grafana` when
   `proxy.enabled` is false. A gate never matches one request of a job.
10. **Records.** The job proxy writes one record for each request: the
    job, the phase, the unit, the operation, the mode, the method, the
    host, the path when it is canonical, the rule, the decision, a fixed
    reason, the status, the bytes and the time. It never holds a header, a
    body, a query value, a credential or a library error text. The records
    of a phase are a new kind of job log (ADR 152 retention), sealed with
    an item key. The audit log gets one entry for each phase with the
    counts of allowed and denied requests and the rule set digest. If the
    job proxy cannot send its records, it refuses new requests after its
    buffer of 10,000 records is full: no request without a record.
11. **Actions.** `namespace:manage-proxy-rules` on the namespace and
    `unit:manage-proxy-rules` on the unit save a rule set, with a fresh
    authentication (ADR 227) and an audit entry that holds the old and the
    new digest. A blueprint version holds rules through `blueprint:publish`.
    `namespace:manage-runners` sets the allowed ranges and the upstream
    proxy of a pool.
12. **Routes.** `GET`, `PUT .../namespaces/{namespace}/proxy-rules`, `GET`,
    `PUT .../units/{unit_id}/proxy-rules`, `GET .../units/{unit_id}/proxy-rules/rendered?operation=plan`
    (the rules of the unit with their values, for a person who debugs),
    and `GET .../jobs/{id}/proxy-records`. The runner protocol gets the
    proxy session paths under `/runner/v1`.

13. **The rule tester.** `POST .../units/{unit_id}/proxy-rules/tests`
    takes a changed rule set and a list of past jobs of the unit (at most
    50), replays the stored records of those jobs against it, and answers
    for each record: `allowed`, `denied` or `changed` (another rule
    matches). It reads stored records only and sends no request. It needs
    `unit:manage-proxy-rules`. The records hold no query value and no
    body, so the tester matches the method, the host and the path only, and
    marks a rule with a body condition as `not-testable` for each record.
14. **Body matching.** A rule of the mode `inspect` can add `body`
    conditions. Each condition names one field and the value that it must
    equal: a template value fills one whole field value, as for a path
    segment.
    - Formats: `json` (a field by JSON pointer, compared in the canonical
      form of RFC 8785, which `internal/catalog` already writes),
      `form` (an `application/x-www-form-urlencoded` field), and `protobuf`
      (a field by its number path, for example `1.3`, read from the wire
      format by a small reader in `pkg/jobproxy`, with no descriptor and no
      new dependency).
    - The job proxy buffers at most 1 MiB of a body. A larger body, a body
      that does not parse, a duplicate JSON key, a field that is missing or
      appears twice, and a streaming gRPC call each deny the request.
    - The record holds the names or numbers of the fields that matched,
      never their values.
    - The `Content-Type` of the request picks the format. A request whose
      type differs from the format of the rule is denied.
15. **SPIFFE identities.** A rule can name `spiffe` in place of
    `clientCertificate`: the job proxy presents an X.509 SVID to the
    endpoint. The proxy reads the SVID, its key and the trust bundle from a
    directory that a SPIFFE helper of the team writes (for example
    `spiffe-helper` of the SPIRE project), and reads them again when the
    files change. So the proxy needs no Workload API client and no new
    dependency. The machine coordinator mounts the directory into the
    proxy process only, and the Kubernetes coordinator into the proxy pod
    only: the job pod never sees it. The SPIRE registration decides the
    SPIFFE ID. The docs give the form
    `spiffe://<trust domain>/opendustpan/<tool>/<namespace>/<unit>` as an
    example only. `serverTrust` can name `spiffe:<ID>`: the proxy then
    checks the URI SAN of the endpoint certificate against that ID with
    `crypto/x509`, and checks the chain against the trust bundle.
16. **Learn mode.** A person with `unit:manage-proxy-rules`, with a fresh
    authentication, turns on learn mode for a unit for at most 7 days.
    - In learn mode, a job of the operations `plan` and `refresh` only goes
      through the job proxy with a built-in `tunnel` rule for each host
      that the job asks for. The proxy still applies the outbound guard
      and records each CONNECT. It gives no credential in learn mode: the
      job start opens no `exports.proxy`. A job of `apply` or `destroy` on
      a unit in learn mode fails at start with `jobproxy.ErrLearnMode`.
    - For a host that already has an `inspect` rule, the proxy keeps the
      TLS end (the CA keeps its name constraint) and records each method
      and path.
    - `POST .../units/{unit_id}/proxy-rules/proposals` turns the records
      of the learn jobs into a proposed rule set, grouped by host and by
      path prefix. A proposal is never saved by itself. A person reads it
      and saves it through the normal route.
    - The fact `proxy.learnMode` is true for such a job, so a gate can
      deny learn mode on a unit with a label.
17. **A shared gateway.** A team that needs fixed egress addresses can run
    the job proxy as a **shared gateway**: one process of the mode
    `proxy --gateway` for a runner pool, as a service that the team runs.
    - Each claim still gets its own proxy session, its own CA, its own
      rendered rules and its own credentials inside the gateway. No two
      claims share a CA, a rule set or a credential.
    - The gateway gives each claim its own listening port. The coordinator
      allows the job pod or the sandbox to reach only that port (a
      NetworkPolicy port, or a drop rule of the machine coordinator).
    - The gateway also checks the source address of each connection
      against the address that the coordinator reported for the claim. A
      connection from another address is refused. So the port and the
      source address are two layers that each stop a job from using the
      claim of another job.
    - The gateway holds the credentials of many units at one time. The docs
      say this, and the pool shows "shared gateway" as a warning, as for the
      isolation `process`.
    - The per-job proxy stays the default.

### Amendments that this proposal makes

- ADR 134 decision 2: a fifth mode of the launcher, `proxy`, with the
  option `--gateway`.
- ADR 134 decision 10: the exit code 72, `network-not-isolated`, with the
  disposition "lets the claim expire".
- ADR 134 decision 6: the variables `HTTPS_PROXY`, `HTTP_PROXY`, `NO_PROXY`
  and the CA bundle variables for the steps of the role `tool` of a unit
  with proxy rules.
- ADR 201 decision 9: the export key `proxy` in the table of exports, with
  the consumer `job-proxy` only.
- ADR 203 decision 6: the delivery to a runner never gives `exports.proxy`
  to the consumer `launcher`.
- ADR 136 decision 4: the RBAC change of owner decision D5.
- ADR 137 decision 6: for a unit with proxy rules, the egress of the
  sandbox is the job proxy only, and not the network of the host.
- ADR 132: a new assignment version for the proxy fields, and the proxy
  session paths of the runner protocol.
- ADR 226 decision 2: the fact group `proxy` (a minor version).

### Owner decisions

Answered: D4, option 1, by the owner on 2026-11-01 (ADR 327 decision 18).
D5 waits for the owner. The PM proposes option 1 (item P19 of the section
"Proposed (PM), waiting for the owner").

- **D4. The name of a rule.** In short: what do we call one allow line of
  the job proxy? Options:
  1. "proxy rule" and "proxy rule set" (recommended). Short, and it names
     the thing that applies it. Cost: "rule" alone is a common word, so
     the text must always write "proxy rule".
  2. "egress rule". The first word of this RFC. Cost: "egress" already
     names the outbound guard and the egress allowlist of model calls, so
     one word names two controls.
  3. "request rule". It says what the rule matches. Cost: "request" already
     names a Catalog request and a review request.
- **D5. The RBAC of the Kubernetes coordinator.** ADR 136 decision 4 gives
  the coordinator create, get, list, watch and delete on Jobs, get on pods,
  get on its Secret and create on `tokenreviews`, and "no other
  permission". The full change: add `create`, `get`, `list`, `watch` and
  `delete` on `networkpolicies` (API group `networking.k8s.io`), in the
  namespace of the coordinator only. Nothing else: the proxy runs as a
  Job, which the coordinator can already create, and the isolation probe
  runs in the launcher, so no probe pod exists. Options: (1) this change
  (recommended). (2) The team creates one NetworkPolicy for the label
  `dustpan-role=job` by hand, and the coordinator gets no new permission.
  Option 2 cannot limit a job pod to the proxy pod of its own claim, so
  one job could reach the proxy of another job. The isolation probe of
  decision 2 then is the only layer that tests the network.

Earlier versions of this proposal recommended moving body matching, SPIFFE,
learn mode and a shared gateway after Milestone 6. That was a cut of the
"all" scope of the roadmap. Decisions 13 to 17 now put each of them in
Milestone 6, with work items X12 to X16.

### Ports

- `pkg/jobproxy`: the rule set model, `jobproxy.Parse`, `jobproxy.Render`
  (the variables of decision 4), `jobproxy.CanonicalPath`,
  `jobproxy.Match`, the fixed reasons, and the port `jobproxy.RuleStore`
  (get and put of a rule set for a scope, with the fence and the audit
  entry) with its suite `jobproxytest`. Pure functions have table tests
  and fuzz tests.
- `jobproxy.Session`, the port that the proxy mode of the launcher uses:
  `Assignment` (the rendered rules, the credentials, the pool settings),
  `PutCA`, `PutRecords`. The runner protocol implements it.
- The profile consumer `job-proxy` in `pkg/profile`.
- The producer of the fact group `proxy` in `pkg/gate`.

### End-to-end steps (real CLIs)

The suite runs a machine runner with `gvisor` isolation (the item adds
`runsc` to the e2e image), a fake product over TLS with one admin token and
no RBAC, and a provider that the suite builds. The provider talks to the
product over HTTPS and serves both tools (ADR 176). For OpenTofu and for
the Pulumi CLI:

1. Save a profile with `exports.proxy.grafana-admin` and a unit rule set:
   `plan` allows `GET /api/folders/{{ inputs.team }}/**`, and `apply` adds
   `POST` and `DELETE`.
2. Run a plan. It succeeds. The product saw the real token. A hook step that
   reads the whole environment and the files of the sandbox finds no byte
   of the token.
3. Change the program to read the folder of another team, and run a plan.
   It fails, and the job log shows "blocked by the job proxy" with the host
   and the path.
4. Run an apply. It succeeds with `POST`. The records show each request
   with its rule.
5. Point the provider at a host with no rule. The CONNECT fails with 403,
   and the plan fails.
6. Make the product echo the `Authorization` header on one allowed path.
   The job proxy breaks the stream, and the record says "response holds the
   credential".
7. Run the plan again on a pool with only `process` isolation. The start
   fails with `runner.ErrIsolation`.
8. Add a body rule on a `POST` with a JSON field `team`. A request with the
   field of another team is denied, and the record names the field, not
   its value.
9. Test a changed rule set with the rule tester against the jobs of steps 2
   to 4, and check the `denied` and `changed` marks.
10. Turn on learn mode, run a plan, and read the proposed rule set. Check
    that an apply in learn mode fails at start.
11. Write a test SVID and bundle to the SPIFFE directory, and run a plan
    against an endpoint that asks for a client certificate with that ID.
12. Run steps 2 and 3 through a shared gateway, and check that a second
    sandbox on the port of the first claim is refused.
13. Delete the drop rule of the sandbox with a test hook, and check that the
    launcher ends with the exit code 72 before the first step.

### Security tests (ADR 250)

Each test sends hostile input to one layer, with the other layers skipped.

1. **Boundary (the rule set and the request).**
   - Rule sets: an unknown field, a duplicate key, an IP literal host, a
     host with a trailing dot or in uppercase, a `**` in the middle, a
     pattern with `..`, `%2F` or `;`, and 257 rules.
   - Requests to `jobproxy.CanonicalPath`: `..`, `%2e%2e`, `%2F`, `%5C`,
     `//`, `;`, `%00`, `%25`, `team-%61`, an overlong UTF-8 form and a path
     over 8 KiB, each refused with 400.
   - Fuzz `CanonicalPath` and `Match`. No input that `CanonicalPath`
     accepts may give two different paths.
2. **Encode or contain.**
   - A template value `team-a/../team-b`, `team-a/**` and an empty value
     each fail the render.
   - The job proxy sends the path that it checked byte for byte.
   - CR and LF in a header value of the credential are refused when the
     profile is saved, and again by the job proxy.
   - The record holds no query value and no header.
3. **Limit the effect.**
   - The CA certificate has a name constraint, and a test checks that Go
     refuses a leaf for a host outside it.
   - The proxy key never leaves memory: a test reads the files and the
     records of a claim and finds no key.
   - Per-request time limits and a limit on open connections.
   - The always-denied addresses: `169.254.169.254`, `::ffff:169.254.169.254`,
     `::ffff:a9fe:a9fe`, `127.0.0.1`, `0.0.0.0`, `[::]` and a 6to4 address
     of a metadata IP.
   - DNS rebinding: a name that resolves to a public address at the check
     and to `127.0.0.1` at the dial is refused at the dial.
   - The same check through an upstream proxy, before the CONNECT.
4. **Authorize at the point of use.**
   - A CONNECT host with no rule.
   - An SNI and a `Host` header that differ from the CONNECT host.
   - A rule of `apply` used in a `plan`.
   - A proxy session of one claim that asks for the rules of another
     claim.
   - A launcher session that asks for `exports.proxy`.
   - A proxy session after its claim ended.
   - The job start check that each named credential exists.
5. **Fail closed and record.**
   - An unknown mode, an unknown variable, a profile that cannot open, an
     upstream proxy that is down, an endpoint certificate that is not
     trusted, and a full record buffer. Each one denies.
   - Each denial writes a record with a fixed reason.
   - An error of the upstream proxy holds no URL and no credential in any
     record or log.
   - The launcher runs no step when its isolation probe connects.
   - A body that does not parse, a body over 1 MiB, a duplicate JSON key
     and a protobuf field that appears twice each deny the request.
   - A learn-mode job of `apply` fails at start, and a learn-mode job gets
     no credential.
   - A shared gateway refuses a connection from an address that is not the
     address of the claim of the port, and a connection to a port of a
     claim that ended.
   - A SPIFFE file that is missing, expired or not signed by the bundle
     makes the proxy deny each request of that rule.

### Work items

Sizes and conflict groups as in RFC 111.

| ID | Item | Size | Conflict group | Depends on |
| --- | --- | --- | --- | --- |
| X1 | `pkg/jobproxy`: rule model, parser, `CanonicalPath`, `Render`, `Match`, reasons, table and fuzz tests | M | `jobproxy` | owner decision D4 (the name) |
| X2 | `jobproxy.RuleStore`, `jobproxytest`, SQLite and PostgreSQL adapters, routes, the two actions | M | `store-migrations`, `identity-actions` | X1 |
| X3 | The profile export `proxy` and the consumer `job-proxy`, with the consumer tests | M | `profile` | none |
| X4 | The proxy core in the launcher module. CONNECT, the CA of a claim and the name binding. The modes `inspect`, `tunnel` and `endpoint`. Injection, and HTTP/2 through the TLS end. The outbound guard dial, the upstream proxy and the response scan | L | `jobproxy-core` | X1 |
| X5 | The runner protocol: the proxy handoff token, the proxy session, the assignment, the CA and record uploads, the pool settings (allowed ranges, upstream proxy) | M | `runner-protocol` | X3, X4 |
| X6 | The machine coordinator: the job proxy outside the sandbox, the routes and the drop rules for `gvisor` and `firecracker`, the refusal of `process` | M | `runner-machine` | X5 |
| X7 | The Kubernetes coordinator: the proxy Job, the NetworkPolicy of a claim (after owner decision D5) | L | `runner-k8s` | X5 |
| X8 | The launcher: the CA bundle, the proxy variables, the placeholders, the denial line in the step log, the isolation probe and the exit code 72 | M | `launcher` | X5 |
| X9 | The job start: the render of the rules, the digest in the spec, the credential check, the fact group `proxy` | M | `job-start`, `gate-facts` | X2, X3 |
| X10 | The records as a job log kind, their retention, the audit summary and the read route | M | `job-logs`, `store-migrations` | X5 |
| X11 | The e2e steps above, `runsc` in the e2e image, and the docs page "Narrow an admin key with the job proxy" | L | `e2e`, `docs` | X6, X8, X9, X10 |
| X12 | The rule tester (decision 13) | M | `jobproxy` | X10 |
| X13 | Body matching: `json`, `form` and the protobuf wire reader, the 1 MiB buffer, fuzz tests of each reader (decision 14) | M | `jobproxy-core` | X4 |
| X14 | SPIFFE: the SVID files, the reload, the mount into the proxy only for each coordinator, the check of a SPIFFE ID (decision 15) | M | `jobproxy-core`, `runner-machine`, `runner-k8s` | X6, X7 |
| X15 | Learn mode: the switch with its limit of 7 days, the built-in tunnel rules, the start refusal for `apply`, the proposals route, `proxy.learnMode` (decision 16) | M | `jobproxy`, `job-start`, `store-migrations` | X9, X10 |
| X16 | The shared gateway: `proxy --gateway`, one session and one port for each claim, the source address check, the pool warning (decision 17) | L | `jobproxy-core`, `runner-protocol` | X6, X7 |

Order: X1 and X3 in parallel, then X2 and X4, then X5, then X6, X8, X9
and X10 in parallel, then X7, X12 and X13, then X14, X15 and X16, then X11.
X11 covers each step below, also the steps of decisions 13 to 17.

The cost of decisions 13 to 17, in work items: about 5 sessions (X12 to
X16), and no new dependency. The shared gateway (X16) is the largest, and it
adds the risk of one process that holds the credentials of many units.

## Proposed (PM), waiting for the owner

The PM proposed the items below. The owner has not accepted them, so ADR
327 does not hold them. Each item has the number of its decision in the
section "Proposal (2026-10-30)". Items P1, P2, P4, P6, P7, P8 and P10 give
the full text of a proposal decision. ADR 327 accepts only the part of it
that the owner decided on 2026-10-26, and the item waits for the rest. An item that the owner accepts goes into a
new ADR. Item P19 is the PM answer to D5 of 2026-10-31.

- **P1. One binary, a separate process.** The full text of proposal decision 1. ADR 327 decision 1 accepts only the part that the owner decided on 2026-10-26.
   The job proxy is our own code in Go, with `net/http` and `crypto/tls`,
   and no new dependency. It is the mode `proxy` of `dustpan-launcher`.
   One release carries it, and the skew rules of ADR 132 apply. It always
   runs as its own process, as its own user, outside the sandbox of the
   steps. It shares no file system, no process space and no memory with a
   step or with the launcher of the steps.

- **P2. Where it runs, for each isolation kind.** The full text of proposal decision 2. ADR 327 decision 2 accepts only the part that the owner decided on 2026-10-26.
   - `firecracker` and `gvisor`: the machine coordinator starts the job
     proxy on the host for each claim of a unit with proxy rules, as a
     user of its own. The network namespace of the sandbox gets one route,
     to the address of the job proxy. The coordinator drops each other
     packet of the sandbox, DNS included. The tool needs no DNS, because
     it sends the name in the CONNECT.
   - `kubernetes`: for each such claim, the coordinator creates a second
     Kubernetes Job, the proxy Job, from `opendustpan/launcher:<release>`.
     It also creates a NetworkPolicy for that claim: the job pod can reach
     only the proxy pod, and the proxy pod accepts only the job pod. The
     permission of the coordinator for this NetworkPolicy is item P19.
     The proxy pod runs as a service account with no role, from the Helm
     values. The proxy sends its pod address through its proxy
     session, and the launcher reads it through its own session.
   - The isolation probe: a NetworkPolicy works only with a network plugin
     that enforces it, and a drop rule can be wrong. So at each claim,
     before the first step, the launcher tries to open a TCP connection to
     the OpenDustpan server directly, not through the job proxy, with a
     time limit of 2 seconds. If the connection opens, the launcher runs
     no step, reports the phase failed with the reason
     `network-not-isolated`, and ends with the exit code 72. The runner
     then lets the claim expire.
   - `process`: this isolation has no kernel boundary. A unit with proxy
     rules never runs on it. The start of such a job on a pool that allows
     only `process` fails with `runner.ErrIsolation`, which names the
     unit. A runner with `process` isolation never claims it.

- **P3. One exit.** The job proxy is the only exit of the sandbox. The
   launcher and the tool reach the OpenDustpan server through it too, with
   a built-in tunnel rule for the host of the server only. The launcher
   keeps its own TLS check of the server.

- **P4. Proxy rules are plain data.** The full text of proposal decision 4. ADR 327 decision 4 accepts only the part that the owner decided on 2026-10-26. A
   proxy rule set is a JSON document that a namespace, a blueprint version
   (field `proxy`) or a unit holds. The proxy rule sets of the scopes of a
   unit are joined. A proxy rule only allows. A proxy rule has:
   - `id` (1 to 64 bytes: letters, digits and `-`), and `operations`, a
     list of `plan`, `apply`, `destroy` and `refresh`,
   - `host`, an exact DNS name in lowercase, and `port` (default 443),
   - `mode`: `inspect` (a TLS end), `tunnel` (the host only) or `endpoint`
     (a loopback port),
   - for `inspect` and `endpoint`: `methods`, and `paths` with `*` (one
     segment), `**` (the rest, last only) and `{{ variable }}` (one whole
     segment),
   - `credential` and `clientCertificate`: names of exports of a profile of
     the unit (item P5),
   - `serverTrust`: `system` (the default) or the name of a CA export of a
     profile.

   The parser refuses an unknown field, a duplicate key, an IP literal host
   (unless the pool allows the range, decision 8), a path pattern that is
   not canonical, and a document over 64 KiB or over 256 proxy rules.

   The variables are `unit.tool`, `unit.namespace`, `unit.name`,
   `unit.labels.<key>`, `operation`, `inputs.<name>` (a string answer of a
   Catalog request) and `profile.<name>.<key>` (a value that is not
   secret). The job start renders the proxy rules once. A value that is not
   exactly one valid path segment, or a value marked secret, fails the
   start with `jobproxy.ErrRender`. The error names the proxy rule and the
   variable. The job spec holds the SHA-256 of the rendered proxy rules, so
   each phase runs the same proxy rules.

   "Or" comes from a list of proxy rules. A proxy rule gets "or" and "not"
   conditions only when a real case needs them.

- **P5. Credentials for the job proxy only.** A profile document gets the
   export key `proxy`: a map from a name to a `header` (name and value),
   `basic` (user and password), `query` (name and value),
   `client-certificate` (certificate and key) or `ca` (certificates). Only
   the consumer `job-proxy` gets `exports.proxy`. The consumer `launcher`
   never gets it, and a test checks the answer for each consumer. A proxy
   rule that names an export that no attached profile gives fails the job
   start with `jobproxy.ErrCredential`. The tool gets a placeholder, for
   example `dustpan-placeholder`, in the variable that the proxy rule names.
- **P6. The CA of a claim.** The full text of proposal decision 6. ADR 327 decision 6 accepts only the part that the owner decided on 2026-10-26. At start,
   the job proxy makes an ECDSA P-256 key and a CA certificate in memory.
   The CA has a name constraint to the hosts of the `inspect` proxy rules
   and the lifetime of the claim. Only the job proxy holds the key. The job
   proxy sends the certificate, never the key, to the server through its
   session. The launcher reads the certificate through its own session. It
   writes a bundle (the roots of the image plus the CA) for the tool steps,
   and names it in `SSL_CERT_FILE`, `NODE_EXTRA_CA_CERTS`,
   `REQUESTS_CA_BUNDLE` and `AWS_CA_BUNDLE`. `HTTPS_PROXY` and `HTTP_PROXY`
   name the job proxy, and `NO_PROXY` is empty.

- **P7. Each request.** The full text of proposal decision 7. ADR 327 decision 7 accepts only the part that the owner decided on 2026-10-26. For a CONNECT,
   the job proxy does these steps:
   1. It refuses a host that no proxy rule names, with 403, before any TLS.
   2. For `tunnel`, it resolves the host, checks each address with the
      outbound guard, and connects to the address that it checked.
   3. For `inspect`, it ends the TLS of the client with a leaf certificate
      of the CA of the claim. It refuses an SNI or a `Host` (`:authority`)
      that is not the CONNECT host. It refuses a path that is not
      canonical with 400. It does not normalize a path. It matches the
      method and the path against the rendered proxy rules. It refuses a
      request that no proxy rule allows with 403 and the body "blocked by
      the job proxy: <method> <host><path>: no rule matched".
   4. It deletes `Authorization`, `Proxy-Authorization`, `Cookie` and each
      header that the credential sets, and adds the credential. It sends
      the request with its own TLS to the address that the outbound guard
      checked, with HTTP/2 where the server offers it.
   5. It never follows a redirect. The tool follows it as a new request,
      and the proxy rules check it again.
   6. For a proxy rule with a credential, it scans the response for each
      form of the credential: the plain value, base64 (standard and URL,
      with and without padding), the JSON escape form and the percent
      form. The window is as long as the longest form. On a match, it
      breaks the stream.

   The job proxy refuses a credential over plain `http`, unless a proxy
   rule opts in, and then only to loopback.

- **P8. The outbound guard and the upstream proxy.** The full text of proposal decision 8. ADR 327 decision 8 accepts only the part that the owner decided on 2026-10-26. The job proxy dials with the rules of `pkg/egress`. It
   never opens the always-denied addresses. It opens a private address
   only in a range that the runner pool allows
   (`egress.ParseAllowedRanges`). A pool can name an upstream proxy, with a
   URL and a credential. The credential comes from the pool, never from the
   profile of a unit. The pool seals it with the data key of the namespace,
   and only a proxy session gets it. With an upstream proxy, the job proxy
   still resolves and checks each rule host before the CONNECT. The docs
   say that the upstream proxy must apply its own rules to the final
   address. An error text never holds the URL or the credential of the
   upstream proxy: the job proxy maps each error to a fixed reason.

- **P9. Gates read a fact.** The fact schemas of `change.propose`,
   `change.planned` and `change.apply` get the group `proxy`, as a minor
   version: `enabled`, `rules` (the count), `ruleSetDigest`,
   `credentialHosts` (a set of hosts), `plainHttp` and `learnMode`. A gate
   decides on the change. A gate never matches one request of a job.
- **P10. Records.** The full text of proposal decision 10. ADR 327 decision 10 accepts only the part that the owner decided on 2026-10-26. The job proxy writes
    one record for each request: the job, the phase, the unit, the
    operation, the mode, the method, the host, the path when it is
    canonical, the proxy rule, the decision, a fixed reason, the status,
    the bytes and the time. A record never holds a header, a body, a query
    value, a credential or the error text of a library. The records of a
    phase are a new kind of job log, under the retention of job logs, and
    are sealed with an item key. The audit log gets one entry for each
    phase, with the counts of allowed and denied requests and the digest of
    the proxy rule set. If the job proxy cannot send its records, it
    refuses new requests when its buffer of 10,000 records is full. No
    request passes without a record. The job log shows "blocked by the job
    proxy" with the host and the path.

- **P11. Actions.** `namespace:manage-proxy-rules` on the namespace and
    `unit:manage-proxy-rules` on the unit save a proxy rule set. The save
    needs a fresh authentication, and writes an audit entry with the old
    and the new digest. A blueprint version holds proxy rules through
    `blueprint:publish`. `namespace:manage-runners` sets the allowed ranges
    and the upstream proxy of a pool.
- **P12. Routes.** `GET` and `PUT .../namespaces/{namespace}/proxy-rules`,
    `GET` and `PUT .../units/{unit_id}/proxy-rules`,
    `GET .../units/{unit_id}/proxy-rules/rendered?operation=plan` (the
    proxy rules of the unit with their values, for a person who debugs),
    and `GET .../jobs/{id}/proxy-records`. The runner protocol gets the
    proxy session paths under `/runner/v1`. The proxy fields raise the
    assignment version.

- **P13. The rule tester.** `POST .../units/{unit_id}/proxy-rules/tests`
    takes a changed proxy rule set and a list of at most 50 past jobs of the
    unit. It replays the stored records of those jobs against the changed
    set, and answers `allowed`, `denied` or `changed` (another proxy rule
    matches) for each record. It reads stored records only and sends no
    request. It needs `unit:manage-proxy-rules`. The records hold no query
    value and no body, so the tester matches the method, the host and the
    path only. It marks a proxy rule with a body condition as
    `not-testable`.
- **P14. Body matching.** A proxy rule of the mode `inspect` can add
    `body` conditions. Each condition names one field and the value that it
    must equal. A template value fills one whole field value.
    - The formats are `json` (a field by JSON pointer, compared in the
      canonical form of RFC 8785), `form` (an
      `application/x-www-form-urlencoded` field) and `protobuf` (a field by
      its number path, for example `1.3`, read from the wire format with no
      descriptor and no new dependency).
    - The job proxy buffers at most 1 MiB of a body. A larger body, a body
      that does not parse, a duplicate JSON key, a field that is missing or
      that occurs two times, and a streaming gRPC call each deny the
      request.
    - The `Content-Type` of the request selects the format. A request with
      a type of another format than the proxy rule is denied.
    - The record holds the names or numbers of the fields that matched,
      never their values.
- **P15. SPIFFE identities.** A proxy rule can name `spiffe` in place of
    `clientCertificate`. The job proxy then presents an X.509 SVID to the
    endpoint. It reads the SVID, its key and the trust bundle from a
    directory that a SPIFFE helper of the team writes, and reads them again
    when the files change. So the job proxy needs no Workload API client
    and no new dependency. The machine coordinator mounts the directory into
    the proxy process only, and the Kubernetes coordinator into the proxy
    pod only. The SPIRE registration decides the SPIFFE ID, never
    OpenDustpan. `serverTrust` can name `spiffe:<ID>`: the job proxy then
    checks the URI SAN of the endpoint certificate against that ID, and the
    chain against the trust bundle. A SPIFFE file that is missing, expired
    or not signed by the bundle denies each request of that proxy rule.
- **P16. Learn mode.** A person with `unit:manage-proxy-rules`, with a
    fresh authentication, turns on learn mode for a unit for at most 7 days.
    - In learn mode, a job of the operations `plan` and `refresh` goes
      through the job proxy with a built-in `tunnel` proxy rule for each
      host that the job asks for. The outbound guard still applies, and the
      job proxy records each CONNECT. The job start opens no
      `exports.proxy`, so learn mode gives no credential.
    - A job of `apply` or `destroy` on a unit in learn mode fails at start
      with `jobproxy.ErrLearnMode`.
    - For a host that already has an `inspect` proxy rule, the job proxy
      keeps the TLS end and records each method and path.
    - `POST .../units/{unit_id}/proxy-rules/proposals` turns the records of
      the learn jobs into a proposed proxy rule set, grouped by host and by
      path prefix. The server never saves a proposal by itself. A person
      reads it and saves it through the normal route.
    - The fact `proxy.learnMode` is true for such a job, so a gate can deny
      learn mode.
- **P17. A shared gateway.** A team that needs fixed egress addresses can
    run the job proxy as a shared gateway: one process of the mode
    `proxy --gateway` for a runner pool, as a service that the team runs.
    - Each claim still gets its own proxy session, its own CA, its own
      rendered proxy rules and its own credentials inside the gateway. No
      two claims share a CA, a proxy rule set or a credential.
    - The gateway gives each claim its own listening port. The coordinator
      lets the job pod or the sandbox reach only that port.
    - The gateway checks the source address of each connection against the
      address that the coordinator reported for the claim, and refuses a
      connection from another address. The port and the source address are
      two layers. Each one alone stops a job from using the claim of
      another job.
    - The gateway holds the credentials of many units at one time. The docs
      say so, and the pool shows "shared gateway" as a warning.
    - The job proxy next to each job stays the default.

- **P19. The RBAC of the Kubernetes coordinator (D5).** The
    service account of the coordinator gets `create`, `get`, `list`,
    `watch` and `delete` on `networkpolicies` (API group
    `networking.k8s.io`), in the namespace of the coordinator only. It gets
    no other new permission. The Helm chart creates exactly this role. A
    NetworkPolicy that the team creates by hand cannot limit a job pod to
    the proxy pod of its own claim, so the project does not use one.

## Open questions

ADR 327 accepts the decisions of the section "Proposal (2026-10-30)" that
have an owner source, and answers D4. The items of the section "Proposed
(PM), waiting for the owner" are open until the owner accepts them. These
questions stay open too:

- **Open:** the name "release mode". The proposal of decision 5 uses the
  export key `proxy` and the consumer `job-proxy`, and the owner did not
  choose a word for the concept. Is "release mode" a glossary term, or
  does the glossary need no word for it?
- **Open:** the cost of the response scan for large bodies. Decision 7
  sets the forms that the scan looks for. Nobody measured the cost of the
  window on a large response. The proxy core work measures it.
- **Open:** which clients other than Go honor the name constraint of the
  CA of a claim. The spike tested Go only.

The questions below are answered (ADR 327):


- The name of a rule of the job proxy: "egress rule", "proxy rule", or
  another word? "Egress" now names the outbound guard and the egress
  allowlist.
- Which interface for rules: HAProxy-style conditions and actions, plain
  data, a policy engine, or something new? And how do policies apply to
  rules? The spike recommends plain data for the first version.
- Which template variables are allowed, and can a template read profile
  values that are secret? (Probably not: a secret in a path ends up in logs.)
- Our own proxy in Go (`net/http`, `crypto/tls`), or Envoy configured by
  OpenDustpan? The spike recommends our own proxy in Go.
- Who can read the CA key for TLS interception, and is it per job, per runner
  or per namespace? The spike recommends one CA for each job, with a name
  constraint, and a key that only the job proxy holds.
- Does the response scan also look for common encodings of the credential
  (base64, JSON escape), and what does it cost for large bodies?
- How does the job proxy get the proxy credential of the upstream proxy:
  from the runner pool, or from the profile of the unit?
- Are client certificates and SPIFFE new credential sources in RFC 5, and who
  decides the SPIFFE ID of a unit: OpenDustpan, or the SPIRE registration?
- Which body formats come first for body matching, and what is the maximum
  body size?

## Outcome

Accepted in part. [ADR 327](../adr/0327-the-job-proxy.md) records the
decisions with an owner source. The items of the section "Proposed (PM),
waiting for the owner" wait for the owner.
