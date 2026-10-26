---
rfc: 16
title: Job proxy, with request rules and credential injection
status: draft
created: 2026-10-10
---

# RFC 16. Job proxy, with request rules and credential injection

Status: draft. The owner decided two points on 2026-10-18: a research spike
comes first, and the names are "job proxy", "outbound guard" and "upstream
proxy" (see `GLOSSARY.md`). Where the proxy runs is a proposal. How people
write rules, and how policies (RFC 13) apply to them, is to be decided. The
section "Prototype findings" gives the result of the spike.

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

Two names stay proposals:

**Egress rule**:
A rule that allows requests from a job, for some units and operations, by
host, method and path, and can name the credential to add.
_Avoid_: ACL, network policy (which is Kubernetes' word), permission

The word "egress" now names the server's own outbound rules (the outbound
guard, `pkg/egress`) and the egress allowlist of model calls. A rule of the
job proxy with the same word can cause confusion. "Proxy rule" is one
option (see "Open questions").

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

## Open questions

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

None yet.
