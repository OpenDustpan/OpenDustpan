# 327. The job proxy

Status: accepted · 2026-11-05. Decided by the project owner on 18 October, 26 October and 1 November 2026. Accepts RFC 16 in part. The other items of the RFC proposal wait for the owner in RFC 16

Changes:

- Extends ADR 134 decision 2: a fifth mode of the launcher, `proxy`.
- Changes ADR 134 decision 6: the steps of the role `tool` of a unit with
  proxy rules get the proxy variables and the CA bundle variables.
- Extends ADR 134 decision 10: the exit code 72, `network-not-isolated`.
- Changes ADR 137 decision 6: for a unit with proxy rules, the only egress
  of the sandbox is the job proxy.

## Context

Many products that teams manage with OpenTofu or the Pulumi CLI have a weak
access model: one admin key or one root token, and no RBAC. A job that
manages one small part of such a product gets that key. Code in the job can
then change all of the product, and can send the key anywhere.

[RFC 16](../rfc/0016-egress-proxy.md) proposes a job proxy between a job and
the network. The job proxy allows only the requests that a rule of the unit
and the operation allows, and adds the credential itself. So the job never
holds the credential. The RFC holds the problem, the prototype findings of
the spike of 26 October 2026, the facts of other proxies and the full
proposal of 30 October 2026. Where this ADR and the RFC differ, this ADR
holds.

The job proxy is an escape hatch, not a default. When a product has native
RBAC and short-lived credentials, teams use those. The job proxy makes a
root token safer. It does not make it safe.

The owner decided these points:

- 18 October 2026: a research spike comes first. The names are "job proxy",
  "outbound guard" and "upstream proxy".
- 26 October 2026: accept the recommendation of the spike (RFC 16,
  "Recommendation", items 1 to 9) as an ADR. The question named three of
  its items: a TLS end with a CA for each job for the hosts with a
  credential or a path rule, a tunnel for the hosts with a host rule only,
  and the endpoint mode as an opt-in.
- 1 November 2026: no scope cut in milestones 4 to 6. All of RFC 16 stays
  in milestone 6.
- 1 November 2026: the new words of milestone 6 are accepted as listed.
  The word for one allow line of the job proxy is "proxy rule".

This ADR holds only the decisions with an owner source. The decisions
below have the numbers of the decisions of the RFC proposal, so that the
work items can name them. The missing numbers (3, 5, 9, 11 to 17 and 19)
are items that the PM proposed. They wait for the owner in RFC 16, section
"Proposed (PM), waiting for the owner". Decisions 18 and 20 are new. The job
proxy is security code. SSRF through a rule host is in scope.

## Decision

### The words and the scope

18. **The words (owner, 1 November 2026).** One allow line of the job proxy
    is a **proxy rule**. The JSON document that holds the proxy rules of
    one scope is a **proxy rule set**. Text always writes "proxy rule",
    never "rule" alone, and never "egress rule". "Egress" names the
    outbound guard and the egress allowlist of model calls.
20. **The scope (owner, 1 November 2026).** All of RFC 16 ships in
    milestone 6, with no scope cut. That includes the decisions of this ADR
    and the items of RFC 16 that the owner accepts later.

### Where the job proxy runs

1. **One binary, a separate process (owner, 26 October 2026, with details by the PM).**
   The job proxy is our own code in Go, with `net/http` and `crypto/tls`,
   and no new dependency. It is the mode `proxy` of `dustpan-launcher`.
   One release carries it, and the skew rules of ADR 132 apply. It always
   runs as its own process, as its own user, outside the sandbox of the
   steps. It shares no file system, no process space and no memory with a
   step or with the launcher of the steps.
2. **Where it runs, for each isolation kind (owner, 26 October 2026:
   next to each job, with details by the PM).**
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
     permission of the coordinator for this NetworkPolicy waits for the
     owner (RFC 16, item P19). The proxy pod runs as a service account with no role, from
     the Helm values. The proxy sends its pod address through its proxy
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
### Proxy rules

4. **Proxy rules are plain data (owner, 26 October 2026, with details by the PM).** A
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
     the unit (the profile export waits for the owner, RFC 16, item P5),
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
6. **The CA of a claim (owner, 26 October 2026, with details by the PM).** At start,
   the job proxy makes an ECDSA P-256 key and a CA certificate in memory.
   The CA has a name constraint to the hosts of the `inspect` proxy rules
   and the lifetime of the claim. Only the job proxy holds the key. The job
   proxy sends the certificate, never the key, to the server through its
   session. The launcher reads the certificate through its own session. It
   writes a bundle (the roots of the image plus the CA) for the tool steps,
   and names it in `SSL_CERT_FILE`, `NODE_EXTRA_CA_CERTS`,
   `REQUESTS_CA_BUNDLE` and `AWS_CA_BUNDLE`. `HTTPS_PROXY` and `HTTP_PROXY`
   name the job proxy, and `NO_PROXY` is empty.

### Each request

7. **Each request (owner, 26 October 2026, with details by the PM).** For a CONNECT,
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
8. **The outbound guard and the upstream proxy (owner, 26 October 2026,
   details PM).** The job proxy dials with the rules of `pkg/egress`. It
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
10. **Records (owner, 26 October 2026, with details by the PM).** The job proxy writes
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

## Consequences

- A unit with no proxy rule set works as before, with no job proxy.
- The package `pkg/jobproxy` holds the rule model, `Parse`, `Render`,
  `CanonicalPath`, `Match` and the fixed reasons.
- Each layer of the job proxy gets its own test with hostile input, as the
  RFC section "Security tests" lists: the boundary, the encoding, the
  limits, the authorization at the point of use, and the fail-closed
  records.
- The e2e suite runs a machine runner with `gvisor` isolation, a fake
  product over TLS with one admin token and no RBAC, and a provider that
  serves both tools. It runs the steps of the RFC section "End-to-end
  steps" that the decisions of this ADR cover, for OpenTofu and for the
  Pulumi CLI. The image of the e2e suite
  gets `runsc`.
- The docs page "Narrow an admin key with the job proxy" says when to use
  the job proxy, and when to use the native RBAC and short-lived
  credentials of a product.
- Code in the job can still make each request that the proxy rules allow,
  with the injected credential. The job proxy narrows what that is. It does
  not judge intent.
- A product that echoes the credential in an encoded form that the scan
  does not know passes the scan. The scan is a second layer, not a
  guarantee.
- Name constraints protect only clients that honor them. Go honored them in
  the spike. Other clients may not.
- With an upstream proxy, a DNS answer that changes between the check of
  the job proxy and the dial of the upstream proxy is outside the reach of
  the job proxy. The upstream proxy is the only layer that sees the final
  address.
- The questions that RFC 16 keeps open stay open there.
