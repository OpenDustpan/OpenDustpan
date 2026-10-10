---
rfc: 16
title: Egress proxy for jobs, with credential injection
status: draft
created: 2026-10-10
---

# RFC 16. Egress proxy for jobs, with credential injection

Status: draft. The names, the scope and where the proxy runs are proposals.

## Problem

A job (RFC 6) runs a tool, and the tool runs code that the team does not
fully control: Pulumi programs, OpenTofu providers, `external` data sources,
job steps. Today that code gets two things:

- **Credentials.** The runner opens the profile (RFC 5) and passes cloud
  credentials to the tool as environment variables or files. Any code in the
  job can read them, print them or send them anywhere, and use them after the
  job ends.
- **The network.** The job can reach any endpoint that the runner's network
  can reach.

Admins also want controls that the providers do not give them:

- **Read-only previews.** A preview of a review request (RFC 12) should not
  be able to change production, even if the program tries.
- **Landing zones.** One provider configuration must reach many accounts or
  tenants with different credentials, and the provider accepts only one.
- **Transport security.** An internal API needs mTLS or a private CA, and the
  provider has no setting for a client certificate.
- **Audit.** Which endpoints did this job call?

## Proposal

An **egress proxy** sits between a job and the network. The job can reach only
the proxy. The proxy applies the job's **egress rules**: it allows or denies
each request, can attach credentials to it, and can open mTLS to the endpoint.

### What the proxy does, in three levels

1. **Allow and deny by host.** The job sends `CONNECT host:443` and the proxy
   checks the host against the rules. No TLS is opened, so this works for every
   protocol that goes through `HTTPS_PROXY`. This is what Stripe's Smokescreen
   does.
2. **Inject credentials.** The proxy ends the job's TLS connection, adds or
   replaces the credentials in the request, and opens a new TLS connection to
   the endpoint. The job must trust the proxy's CA, which the runner installs
   for the job only. The real credential never enters the job.
3. **Open mTLS or a private CA to the endpoint** with a client certificate
   from the profile, for endpoints whose provider has no setting for it.

Level 1 is cheap and safe. Levels 2 and 3 need TLS interception, which is the
cost of this RFC.

### Credentials that never reach the runner

A credential source in a profile (RFC 5) gets a **release mode**:

- **To the job:** as today. The runner passes it to the tool.
- **To the proxy only:** the runner never receives it. The tool gets a
  placeholder (a dummy key, a fake token), and the proxy swaps in the real
  value for requests that match a rule.

How hard the swap is depends on the cloud:

- **Bearer tokens and API keys** (Google Cloud, Azure, GitHub, most SaaS
  APIs, Kubernetes): replace one header. Envoy's credential injector does
  this, also with OAuth2 client credentials.
- **AWS Signature Version 4:** the signature covers the request, so the proxy
  cannot only swap a header. It must check the request, drop the job's
  signature, and sign again with the real key. Streaming uploads with chunked
  signatures (`aws-chunked` for S3) make this harder. The provider also checks
  its credentials at start (`sts:GetCallerIdentity`), and the proxy must
  answer that call with the real identity.
- **Protocols that are not HTTP** (PostgreSQL, MySQL, SSH): no injection at
  first. Level 1 can still allow or deny the host.

### Read-only previews

There are two ways, and the first is much simpler:

- **A credential per phase.** The rules choose a read-only credential for a
  preview or plan job and a read-write credential for an apply job. The cloud
  enforces "read-only", not the proxy. HCP Terraform's dynamic credentials
  already split plan and apply roles this way. The proxy knows the phase from
  the job's token.
- **Filter requests.** The proxy denies requests that write. HTTP methods do
  not tell reads from writes for many APIs: AWS Query APIs send every action
  as `POST` with `Action=Describe…` or `Action=Delete…`, and JSON APIs use the
  `X-Amz-Target` header. A filter needs knowledge of each API. This is a later
  option, for providers whose cloud has no read-only role.

### Landing zones

A rule can choose the credential from the request: the host, a path prefix,
or an account or tenant ID in the URL. One provider configuration then reaches
many accounts, each with its own credential, and the program never knows.

### Audit

The proxy records each request: job, unit reference, method, host, path,
status, rule, which credential it used. It never records bodies or credential
values. The console shows the endpoints of a job next to its log.

## Where the proxy runs

The backend never runs a tool (RFC 6). The proxy does not run a tool either,
but it holds production credentials and carries production traffic, so where
it runs matters most in this RFC.

1. **A sidecar for each job, on the runner** (proposed default). The runner
   starts the proxy next to the tool, in a separate container or process with
   its own user. The tool's network allows only the proxy (a network namespace
   or a Kubernetes NetworkPolicy). The proxy, not the runner's tool process,
   gets the credentials from the backend for this one job. Traffic stays local
   and fast, and no shared service holds every team's credentials.
2. **A shared egress gateway in the customer's network.** One service for
   many runners, deployed as its own role (RFC 7). Easier to audit and to fix
   egress IPs for allowlists. But every byte of every job goes through it
   (image pushes, S3 uploads), it is a single point of failure, and it holds
   credentials for every team.
3. **In the backend.** Rejected. It would make the backend a path into every
   production network, and it puts cloud traffic on the server.

## Limits (what the proxy does not protect)

- **Use during the job.** A program can still make any request that the rules
  allow, with the injected credential, while the job runs. The proxy stops
  theft of the credential for later use and stops calls to endpoints outside
  the rules. It does not stop misuse inside the rules.
- **Clients that ignore the proxy settings.** Go's `net/http` honors
  `HTTPS_PROXY` and `NO_PROXY`, so most OpenTofu providers and bridged Pulumi
  providers do. Other runtimes and SDKs may not. The network rule (only the
  proxy is reachable) makes such clients fail instead of going around the
  proxy. That is the correct failure, but teams will see it.
- **Clients with their own trust store or pinned certificates.** The runner
  must set the CA for each runtime (`SSL_CERT_FILE`, `AWS_CA_BUNDLE`,
  `NODE_EXTRA_CA_CERTS`, `REQUESTS_CA_BUNDLE`). A client that pins a
  certificate cannot use level 2 or 3 for that host.
- **Local traffic.** OpenTofu and Pulumi talk to their plugins over local
  gRPC, and the tool talks to OpenDustpan. Those go in `NO_PROXY`.
- **Plugin downloads.** Providers and modules come from registries, which the
  rules must allow, or from the artifact registry of RFC 10.

## Options

1. **Sidecar proxy, three levels, credentials released to the proxy only**
   (the proposal). Level 1 first, then bearer tokens, then AWS signing.
2. **Do nothing.** Jobs get credentials and the network as today. Short-lived
   credentials (OIDC in RFC 5) already limit how long a stolen credential
   works.
3. **Allow and deny only (level 1).** Cheap, needs no TLS interception, works
   for every protocol. No credential injection, no read-only previews.
4. **Integrate an existing proxy** (Envoy, Smokescreen) and only generate its
   configuration from the egress rules. Less code of our own, but a large
   component to ship, and AWS re-signing still needs code.

## Proposed glossary entries

**Egress proxy**:
The proxy between a job and the network. It applies the job's egress rules.
_Avoid_: Gateway (alone), firewall, sidecar (as the name of the feature)

**Egress rule**:
A rule that allows or denies a job's requests to a host, and can choose a
credential and a TLS setting for them.
_Avoid_: ACL, network policy (which is Kubernetes' word)

**Release mode**:
Where a credential source in a profile delivers its credential: to the job,
or to the egress proxy only.
_Avoid_: Exposure, visibility

## Open questions

- Are egress rules plain data at first, or the policy engines of RFC 13?
- Is AWS re-signing worth its cost, or do AWS users get per-phase roles
  through OIDC and level 1 only?
- Which proxy: our own Go code (standard library `net/http` and `crypto/tls`),
  or Envoy configured by OpenDustpan?
- Does the runner's CA for TLS interception live per job, per runner, or per
  namespace, and who can read its key?
- Does a job without egress rules get "allow all" (today's behavior) or
  "deny all"? Following RFC 9, the default is probably "allow all" with a
  warning, and a namespace setting to require rules.
- How does the console show a denied request, so that a failed plan says
  "blocked by egress rule X" and not only a provider timeout?

## Outcome

None yet.
