---
rfc: 16
title: Egress proxy for jobs, with request rules and credential injection
status: draft
created: 2026-10-10
---

# RFC 16. Egress proxy for jobs, with request rules and credential injection

Status: draft. The names and where the proxy runs are proposals. How people
write rules, and how policies (RFC 13) apply to them, is to be decided.

**This is an escape hatch, not a default.** When a product has native RBAC or
ABAC and short-lived credentials (OIDC, dynamic credentials), use those first:
they are safer than any proxy. The egress proxy is for the products that do
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

An **egress proxy** is an opt-in feature for a unit. It sits between a job and the network. The job can reach only
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

- **Normalize first.** The proxy decodes and normalizes the path before it
  matches: percent-encoding, `.` and `..` segments, repeated slashes, and
  case where the product ignores case. It denies a path that it cannot
  normalize. It then sends the normalized path, not the original, so that the
  product sees what the rule checked.
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
A failed plan must say "blocked by the egress proxy", not only show a
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

## Proposed glossary entries

**Egress proxy**:
The proxy between a job and the network. It applies the job's egress rules
and adds credentials to allowed requests.
_Avoid_: Gateway (alone), firewall, sidecar (as the name of the feature)

**Egress rule**:
A rule that allows requests from a job, for some units and operations, by
host, method and path, and can name the credential to add.
_Avoid_: ACL, network policy (which is Kubernetes' word), permission

**Release mode**:
Where a credential source in a profile delivers its credential: to the job,
or to the egress proxy only.
_Avoid_: Exposure, visibility

## Open questions

- Which interface for rules: HAProxy-style conditions and actions, plain
  data, a policy engine, or something new? And how do policies apply to
  rules?
- Which template variables are allowed, and can a template read profile
  values that are secret? (Probably not: a secret in a path ends up in logs.)
- Our own proxy in Go (`net/http`, `crypto/tls`), or Envoy configured by
  OpenDustpan?
- Who can read the CA key for TLS interception, and is it per job, per runner
  or per namespace?
- Are client certificates and SPIFFE new credential sources in RFC 5, and who
  decides the SPIFFE ID of a unit: OpenDustpan, or the SPIRE registration?
- Which body formats come first for body matching, and what is the maximum
  body size?

## Outcome

None yet.
